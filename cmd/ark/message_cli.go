package main

// The `ark message` command group, migrated to the urfave/cli v3 command
// tree (Stage 2 of the CLI urfave migration). message {new-request,
// new-response, set-tags, get-tags, check, inbox, dm}. Each node declares
// its flags so --help is generated; each Action keeps the existing body.
// set-tags/get-tags/check delegate to the cmdTagSet/cmdTagGet/cmdTagCheck
// bodies kept in main.go (the legacy one-line cmdMessage* wrappers are
// retired). The message-only helpers readStdinBody / writeAtomicNew move
// here with the group.
// (R580-R584, R613, R614, R616, R849-R852, R1952, R2430, R2431, R2484,
// R2485, R708-R723; see crc-CLITree.md.)

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	ucli "github.com/urfave/cli/v3"
	"github.com/zot/ark"
)

// CRC: crc-CLITree.md, crc-CLI.md | Seq: seq-cli-urfave.md#3.3 | R450, R451, R452, R453, R454, R455, R456, R457, R458, R462, R464, R466, R467, R468, R469, R470, R471, R477, R613, R614, R708, R849, R852
func messageCommand() *ucli.Command {
	return &ucli.Command{
		Name:  "message",
		Usage: "cross-project messaging (new-request, new-response, set-tags, get-tags, check, inbox, dm)",
		Commands: []*ucli.Command{
			{
				Name:      "new-request",
				Usage:     "create a new request file",
				ArgsUsage: "FILE",
				Flags: []ucli.Flag{
					&ucli.StringFlag{Name: "from", Usage: "source project name (required)"},
					&ucli.StringFlag{Name: "to", Usage: "target project name (required)"},
					&ucli.StringFlag{Name: "issue", Usage: "one-line issue description (required unless --issue-file)"},
					&ucli.StringFlag{Name: "issue-file", Usage: "read the @issue line verbatim from a file (mutually exclusive with --issue)"},
					&ucli.StringFlag{Name: "content", Usage: "body text (alternative to stdin)"},
					&ucli.StringFlag{Name: "content-file", Usage: "read the body verbatim from a file (mutually exclusive with --content)"},
				},
				Action: messageNewRequestAction,
			},
			{
				Name:      "new-response",
				Usage:     "create a new response file (response = ack)",
				ArgsUsage: "FILE",
				Flags: []ucli.Flag{
					&ucli.StringFlag{Name: "from", Usage: "source project name (required)"},
					&ucli.StringFlag{Name: "to", Usage: "target project name (required)"},
					&ucli.StringFlag{Name: "request", Usage: "request ID being responded to (required)"},
					&ucli.StringFlag{Name: "content", Usage: "body text (alternative to stdin)"},
					&ucli.StringFlag{Name: "content-file", Usage: "read the body verbatim from a file (mutually exclusive with --content)"},
				},
				Action: messageNewResponseAction,
			},
			{
				Name:      "set-tags",
				Usage:     "update or add tags in a file's tag block (alias for tag set)",
				ArgsUsage: "FILE TAG VALUE [TAG VALUE ...]",
				Action:    messageSetTagsAction,
			},
			{
				Name:      "get-tags",
				Usage:     "read tags from a file's tag block (alias for tag get)",
				ArgsUsage: "FILE [TAG ...]",
				Action:    messageGetTagsAction,
			},
			{
				Name:      "check",
				Usage:     "validate file format",
				ArgsUsage: "FILE",
				Action:    messageCheckAction,
			},
			{
				Name:  "inbox",
				Usage: "list non-completed messages",
				Flags: []ucli.Flag{
					&ucli.StringFlag{Name: "project", Usage: "filter by either to-project or from-project"},
					&ucli.StringFlag{Name: "to", Usage: "filter by to-project"},
					&ucli.StringFlag{Name: "from", Usage: "filter by from-project"},
					&ucli.BoolFlag{Name: "all", Usage: "include completed/denied messages"},
					&ucli.BoolFlag{Name: "include-archived", Usage: "include archived messages"},
					&ucli.BoolFlag{Name: "counts", Usage: "output status counts instead of rows"},
					&ucli.BoolFlag{Name: "unmatched", Usage: "show only requests with no matching response"},
				},
				Action: messageInboxAction,
			},
			{
				Name:  "dm",
				Usage: "send a direct message between agents (tmp://)",
				Flags: []ucli.Flag{
					&ucli.StringFlag{Name: "from", Usage: "sender session ID (mutually exclusive with --from-service)"},
					&ucli.StringFlag{Name: "from-service", Usage: "sender service identity, e.g. ARK-RECALL (mutually exclusive with --from)"},
					&ucli.StringSliceFlag{Name: "to", Usage: "recipient (repeatable for multi-recipient DMs)"},
					&ucli.StringFlag{Name: "subject", Usage: "optional @dm subject suffix"},
					&ucli.StringFlag{Name: "ref", Usage: "reference ID (for threading replies)"},
					&ucli.StringFlag{Name: "content", Usage: "message content (markdown)"},
				},
				Action: messageDMAction,
			},
		},
	}
}

// CRC: crc-CLI.md | R580, R581, R582, R583, R584
func readStdinBody() string {
	info, err := os.Stdin.Stat()
	if err != nil {
		return ""
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return "" // terminal, not piped
	}
	var lines []string
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "." {
			break
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// readMessageFile reads a *-file flag's file verbatim, trimming trailing
// newlines so a file saved with a trailing newline matches the shape of
// the inline --issue/--content flags. The point of the *-file flags is
// fidelity: a caller writes the exact text and hands over the path, so a
// relaying agent never retypes the payload.
// CRC: crc-CLI.md | R2956, R2957
func readMessageFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		fatal(fmt.Errorf("read %s: %w", path, err))
	}
	return strings.TrimRight(string(b), "\n")
}

// resolveIssue returns the @issue value from --issue or --issue-file
// (mutually exclusive). --issue-file is read verbatim and must be a single
// line, since @issue runs to end of line.
// CRC: crc-CLI.md | R2956
func resolveIssue(c *ucli.Command) string {
	issue, file := c.String("issue"), c.String("issue-file")
	if issue != "" && file != "" {
		fatal(fmt.Errorf("--issue and --issue-file are mutually exclusive"))
	}
	if file == "" {
		return issue
	}
	v := readMessageFile(file)
	if strings.Contains(v, "\n") {
		fatal(fmt.Errorf("--issue-file must be a single line (the @issue value runs to end of line)"))
	}
	return v
}

// resolveContent returns the body from --content or --content-file
// (mutually exclusive) and whether either flag was set — when set, stdin
// is skipped even if the file is empty.
// CRC: crc-CLI.md | R2957
func resolveContent(c *ucli.Command) (body string, set bool) {
	content, file := c.String("content"), c.String("content-file")
	if content != "" && file != "" {
		fatal(fmt.Errorf("--content and --content-file are mutually exclusive"))
	}
	if file != "" {
		return readMessageFile(file), true
	}
	return content, content != ""
}

// writeAtomicNew writes data to filePath atomically: write to a sibling
// temp file in the same directory, fsync-close, then rename into place.
// The destination either appears with full content or not at all — no
// 0-byte husks from a partial WriteFile or an interrupted process.
// CRC: crc-CLI.md | R2485
func writeAtomicNew(filePath string, data []byte) error {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(filePath)+".tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, filePath); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R849, R850, R851, R2485, R2956, R2957
func messageNewRequestAction(_ context.Context, c *ucli.Command) error {
	from, to := c.String("from"), c.String("to")
	issue := resolveIssue(c)
	content, contentSet := resolveContent(c)
	if from == "" || to == "" || issue == "" {
		fatal(fmt.Errorf("--from, --to, and --issue (or --issue-file) are required"))
	}
	if c.Args().Len() < 1 {
		fatal(fmt.Errorf("FILE path required"))
	}
	filePath := c.Args().First()

	if _, err := os.Stat(filePath); err == nil {
		fatal(fmt.Errorf("file already exists: %s", filePath))
	}

	// Derive request ID from filename
	base := filepath.Base(filePath)
	id := strings.TrimSuffix(base, filepath.Ext(base))

	tb := ark.ParseTagBlock(nil)
	tb.Set("ark-request", id)
	tb.Set("from-project", from)
	tb.Set("to-project", to)
	tb.Set("status", "open")
	tb.Set("status-date", time.Now().Format("2006-01-02"))
	tb.Set("issue", issue)

	var buf bytes.Buffer
	buf.Write(tb.Render())
	fmt.Fprintf(&buf, "# %s\n\n%s\n", id, issue)
	// R849-R851: --content preferred over stdin. R2957: --content-file is the
	// verbatim alternative; either flag (even an empty file) skips stdin.
	if content != "" {
		fmt.Fprintf(&buf, "\n%s\n", content)
	} else if !contentSet {
		if body := readStdinBody(); body != "" {
			fmt.Fprintf(&buf, "\n%s", body)
		}
	}

	// R2485: atomic create — no 0-byte husk if writing fails.
	if err := writeAtomicNew(filePath, buf.Bytes()); err != nil {
		fatal(err)
	}
	fmt.Fprintf(os.Stderr, "hint: when the response arrives, track your progress with @response-handled:\n")
	fmt.Fprintf(os.Stderr, "  ~/.ark/ark tag set %s response-handled accepted\n", filePath)
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R852, R2485, R2957
func messageNewResponseAction(_ context.Context, c *ucli.Command) error {
	from, to, request := c.String("from"), c.String("to"), c.String("request")
	content, contentSet := resolveContent(c)
	if from == "" || to == "" || request == "" {
		fatal(fmt.Errorf("--from, --to, and --request are required"))
	}
	if c.Args().Len() < 1 {
		fatal(fmt.Errorf("FILE path required"))
	}
	filePath := c.Args().First()

	if _, err := os.Stat(filePath); err == nil {
		fatal(fmt.Errorf("file already exists: %s", filePath))
	}

	tb := ark.ParseTagBlock(nil)
	tb.Set("ark-response", request)
	tb.Set("from-project", from)
	tb.Set("to-project", to)
	tb.Set("status", "accepted")
	tb.Set("status-date", time.Now().Format("2006-01-02"))

	var buf bytes.Buffer
	buf.Write(tb.Render())
	fmt.Fprintf(&buf, "# RESP %s\n\n", request)
	// R852: --content preferred over stdin. R2957: --content-file is the
	// verbatim alternative; either flag (even an empty file) skips stdin.
	if content != "" {
		fmt.Fprintf(&buf, "%s\n", content)
	} else if !contentSet {
		if body := readStdinBody(); body != "" {
			buf.WriteString(body)
		}
	}

	// R2485: atomic create — no 0-byte husk if writing fails.
	if err := writeAtomicNew(filePath, buf.Bytes()); err != nil {
		fatal(err)
	}
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R614, R616
func messageSetTagsAction(_ context.Context, c *ucli.Command) error {
	cmdTagSet(c.Args().Slice())
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R614, R616
func messageGetTagsAction(_ context.Context, c *ucli.Command) error {
	cmdTagGet(c.Args().Slice())
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R613
func messageCheckAction(_ context.Context, c *ucli.Command) error {
	if c.Args().Len() < 1 {
		fmt.Fprintln(os.Stderr, "usage: ark message check FILE")
		return nil
	}
	cmdTagCheck(c.Args().Slice())
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | Seq: seq-message.md | R708, R709, R710, R713, R718
func messageInboxAction(_ context.Context, c *ucli.Command) error {
	// R2430, R2431: --project matches either side; --to is the strict
	// to-project filter (the old --project semantic).
	project := c.String("project")
	to := c.String("to")
	from := c.String("from")
	all := c.Bool("all")
	includeArchived := c.Bool("include-archived")
	counts := c.Bool("counts")
	unmatched := c.Bool("unmatched")

	printEntries := func(entries []ark.InboxEntry) {
		// R714, R723, R2484: pair entries by requestId against the FULL
		// inbox before applying CLI filters, so --unmatched and lag see
		// the counterpart even when a directional filter would hide it.
		type pair struct {
			request  *ark.InboxEntry
			response *ark.InboxEntry
		}
		byID := make(map[string]*pair)
		for i := range entries {
			e := &entries[i]
			id := e.RequestID
			if id == "" {
				id = e.Path
			}
			p, ok := byID[id]
			if !ok {
				p = &pair{}
				byID[id] = p
			}
			if e.Kind == "response" {
				p.response = e
			} else {
				p.request = e
			}
		}

		// CRC: crc-CLI.md | R2430, R2431, R2484
		// CLI-specific post-filters. --all is applied here (not by the
		// fetch) so that completed responses remain visible to byID for
		// pair lookup; the display view still hides them unless asked.
		var filtered []ark.InboxEntry
		for _, e := range entries {
			if !all && (e.Status == "completed" || e.Status == "denied") {
				continue
			}
			if project != "" && e.To != project && e.From != project {
				continue
			}
			if to != "" && e.To != to {
				continue
			}
			if from != "" && e.From != from {
				continue
			}
			filtered = append(filtered, e)
		}

		// R713, R2484: --unmatched keeps only requests whose global pair has no response
		if unmatched {
			var um []ark.InboxEntry
			for _, e := range filtered {
				if e.Kind == "response" {
					continue
				}
				id := e.RequestID
				if id == "" {
					id = e.Path
				}
				if p := byID[id]; p != nil && p.response == nil {
					um = append(um, e)
				}
			}
			filtered = um
		}

		if counts {
			statusCounts := make(map[string]int)
			for _, e := range filtered {
				statusCounts[e.Status]++
			}
			var keys []string
			for k := range statusCounts {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Printf("%s\t%d\n", k, statusCounts[k])
			}
			return
		}

		// R718, R719, R720, R721, R722: compute bookmark lag per entry
		lagFor := func(e ark.InboxEntry) string {
			id := e.RequestID
			if id == "" {
				id = e.Path
			}
			p := byID[id]
			if p == nil {
				return ""
			}
			if e.Kind != "response" && p.response != nil {
				// Request side: is reqResponseHandled behind respStatus?
				if p.response.Status != "" && e.ResponseHandled != p.response.Status {
					return "lag:" + e.From + ":" + p.response.Status
				}
			}
			if e.Kind == "response" && p.request != nil {
				// Response side: is respRequestHandled behind reqStatus?
				if p.request.Status != "" && e.RequestHandled != p.request.Status {
					return "lag:" + e.From + ":" + p.request.Status
				}
			}
			return ""
		}

		// Pre-compute display date: most recent status-date from paired request/response
		type dated struct {
			entry ark.InboxEntry
			date  string
		}
		datedEntries := make([]dated, len(filtered))
		for i, e := range filtered {
			id := e.RequestID
			if id == "" {
				id = e.Path
			}
			best := e.StatusDate
			if p := byID[id]; p != nil {
				if p.request != nil && p.request.StatusDate > best {
					best = p.request.StatusDate
				}
				if p.response != nil && p.response.StatusDate > best {
					best = p.response.StatusDate
				}
			}
			datedEntries[i] = dated{entry: e, date: best}
		}

		// Sort by display date descending (most recent first, empty last)
		sort.SliceStable(datedEntries, func(i, j int) bool {
			di, dj := datedEntries[i].date, datedEntries[j].date
			if di == "" && dj != "" {
				return false
			}
			if di != "" && dj == "" {
				return true
			}
			return di > dj
		})

		fmt.Printf("# inbox %s\n", time.Now().Format("2006-01-02"))
		for _, d := range datedEntries {
			lag := lagFor(d.entry)
			date := d.date
			if date == "" {
				date = "-"
			}
			fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				date, d.entry.Status, d.entry.To, d.entry.From, d.entry.Summary, d.entry.Path, lag)
		}
	}

	// Server-first so tmp:// inbox messages (only in server memory) are
	// visible. Cold-path fallback when no server is running.
	// CRC: crc-CLI.md | R1952, R2484
	// We always fetch with showAll=true so completed responses remain in
	// the entry stream for byID pair lookup; --all is applied as a CLI
	// post-filter in printEntries.
	if client := serverClient(arkDir); client != nil {
		req := struct {
			ShowAll         bool `json:"showAll,omitempty"`
			IncludeArchived bool `json:"includeArchived,omitempty"`
		}{ShowAll: true, IncludeArchived: includeArchived}
		var entries []ark.InboxEntry
		if err := proxyDecode(client, "POST", "/inbox", req, &entries); err == nil {
			printEntries(entries)
			return nil
		}
	}
	withDB(func(d *ark.DB) {
		entries, err := d.Inbox(true, includeArchived)
		if err != nil {
			fatal(err)
		}
		printEntries(entries)
	})
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | Seq: seq-pubsub.md
func messageDMAction(_ context.Context, c *ucli.Command) error {
	content := c.String("content")
	if content == "" {
		fatal(fmt.Errorf("--content is required"))
	}

	sender := ark.DMSender{Session: c.String("from"), Service: c.String("from-service")}
	path, payload, err := ark.ComposeDM(sender, c.StringSlice("to"), c.String("subject"), c.String("ref"), content)
	if err != nil {
		fatal(err)
	}

	client := serverClient(arkDir)
	if client == nil {
		fatal(fmt.Errorf("server not running (dm requires server)"))
	}
	if err := proxyOK(client, "POST", "/tmp/append", map[string]any{
		"path":     path,
		"strategy": "markdown",
		"content":  payload,
	}); err != nil {
		fatal(err)
	}
	return nil
}
