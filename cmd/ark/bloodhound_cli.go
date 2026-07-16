package main

// The `ark bloodhound` command group — external-app access to the warm
// bloodhound (bloodhound-cli.md, S3). `search` submits a directed hunt and
// prints the curated findings as JSONL; the whole protocol (request doc, tag
// baton, pool secretary, Luhmann curation) is hidden behind the one command
// (Batteries Included).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	ucli "github.com/urfave/cli/v3"
)

// CRC: crc-CLITree.md | Seq: seq-bloodhound-cli.md#1.1 | R3021, R3022, R3029
func bloodhoundCommand() *ucli.Command {
	return &ucli.Command{
		Name:  "bloodhound",
		Usage: "external directed hunts against the warm bloodhound (requires a running Luhmann)",
		Commands: []*ucli.Command{
			{
				Name:      "search",
				Usage:     "run a directed hunt and print curated findings as JSONL",
				ArgsUsage: "[CLUE...]",
				Flags: []ucli.Flag{
					&ucli.StringFlag{Name: "file", Usage: "read the clue from a file ('-' = stdin, for a heredoc multi-paragraph markdown clue); mutually exclusive with positional CLUE"},
					&ucli.StringFlag{Name: "scope", Value: "all", Usage: "search scope: code | specs | design | notes | chat | all"},
					&ucli.StringFlag{Name: "depth", Value: "lookup", Usage: "depth: lookup | investigate"},
					&ucli.StringFlag{Name: "want", Value: "passages", Usage: "want: answer | passages | ..."},
					&ucli.BoolFlag{Name: "wait", Usage: "block stubbornly on a busy pool / server bounce instead of failing fast"},
					&ucli.IntFlag{Name: "timeout", Value: 300, Usage: "seconds to wait for the result (default 300)"},
					&ucli.BoolFlag{Name: "raw", Usage: "skip Luhmann curation: return the secretary's own findings (markdown, Baby Food for agents) — curate in your own context"},
					&ucli.BoolFlag{Name: "markdown", Usage: "render the curated findings as a markdown locator list instead of JSONL (redundant with --raw)"},
				},
				Action: bloodhoundSearchAction,
			},
			{
				Name:      "add",
				Usage:     "Luhmann's result stencil: append one curated finding to a CLI hunt, or --done to finish",
				ArgsUsage: "--result tmp://BLOODHOUND-CLI/<id> (--loc PATH:RANGE --note TEXT [--chunk TEXT] | --done)",
				Flags: []ucli.Flag{
					&ucli.StringFlag{Name: "result", Usage: "the request-doc `PATH` handed to 'luhmann next' (tmp://BLOODHOUND-CLI/<id>)"},
					&ucli.StringFlag{Name: "loc", Usage: "the curated finding's PATH:RANGE"},
					&ucli.StringFlag{Name: "note", Usage: "one-line curated note: why it answers the query"},
					&ucli.StringFlag{Name: "chunk", Usage: "optional chunk excerpt to carry in the JSONL"},
					&ucli.BoolFlag{Name: "done", Usage: "terminal call: write the result doc and notify the waiting CLI"},
				},
				Action: bloodhoundAddAction,
			},
		},
	}
}

// CRC: crc-CLITree.md | Seq: seq-bloodhound-cli.md#1.5.2 | R3027, R3028
func bloodhoundAddAction(_ context.Context, c *ucli.Command) error {
	result := c.String("result")
	if result == "" {
		fmt.Fprintln(os.Stderr, "ark bloodhound add --result tmp://BLOODHOUND-CLI/<id> (--loc PATH:RANGE --note TEXT [--chunk TEXT] | --done)")
		os.Exit(2)
	}
	done := c.Bool("done")
	loc := c.String("loc")
	if !done && loc == "" {
		fmt.Fprintln(os.Stderr, "ark bloodhound add: --loc required (or --done to finish)")
		os.Exit(2)
	}
	client := requireServer("bloodhound add")
	if err := proxyOK(client, "POST", "/bloodhound/add", map[string]any{
		"result": result, "loc": loc, "note": c.String("note"), "chunk": c.String("chunk"), "done": done,
	}); err != nil {
		fatal(err)
	}
	return nil
}

// CRC: crc-CLITree.md | Seq: seq-bloodhound-cli.md#1.1 | R3020, R3021, R3022, R3029
func bloodhoundSearchAction(_ context.Context, c *ucli.Command) error {
	raw := c.Bool("raw")
	markdown := c.Bool("markdown")
	// R3046: the clue comes from positional CLUE... or --file (--file - = stdin,
	// the heredoc path); the two are mutually exclusive.
	clue, err := resolveClue(c.Args().Slice(), c.String("file"), os.Stdin)
	if err != nil {
		fatal(err)
	}
	if clue == "" {
		fmt.Fprintln(os.Stderr, "ark bloodhound search [CLUE...] [--file PATH|-] [--scope S] [--depth D] [--want W] [--wait] [--timeout SECONDS] [--raw] [--markdown]")
		os.Exit(2)
	}
	// The payload the watcher wraps as the ## Search task the secretary reads:
	// scope/depth/want (and curate:false for --raw) as leading metadata lines, then
	// the clue body last, so the watcher's clueOf splits only the clue (R3044, R3046).
	payload := buildSearchPayload(clue, c.String("scope"), c.String("depth"), c.String("want"), raw)
	wait := c.Bool("wait")
	timeout := c.Int("timeout")

	// Submit. The server gates on a live Luhmann (503, R3020) and — without
	// --wait — a busy pool (429, R3022); either surfaces as a fatal exit and
	// submits nothing.
	client := requireServer("bloodhound search")
	var resp struct {
		ID string `json:"id"`
	}
	if err := proxyDecode(client, "POST", "/bloodhound/search",
		map[string]any{"payload": payload, "wait": wait}, &resp); err != nil {
		fatal(err)
	}

	// Block on the curated result up to --timeout (R3021). With --wait the block
	// is stubborn across a server bounce (redials); without it, a drop fails.
	resultURL := fmt.Sprintf("http://ark/bloodhound/result?id=%s&timeout=%d", resp.ID, timeout)
	deadline := time.Now().Add(time.Duration(timeout)*time.Second + 15*time.Second)
	backoff := recallNextRedialBackoff
	for {
		client = serverClient(arkDir)
		if client == nil {
			if !wait || time.Now().After(deadline) {
				fatal(errors.New("ark server unreachable while awaiting result"))
			}
			time.Sleep(backoff)
			backoff = min(backoff*2, recallNextRedialMaxBackoff)
			continue
		}
		req, err := http.NewRequest("GET", resultURL, nil)
		if err != nil {
			fatal(err)
		}
		httpResp, derr := client.Do(req)
		if derr != nil {
			if !wait || time.Now().After(deadline) {
				fatal(fmt.Errorf("awaiting result: %w", derr))
			}
			time.Sleep(backoff)
			continue
		}
		data, _ := io.ReadAll(httpResp.Body)
		httpResp.Body.Close()
		if httpResp.StatusCode != http.StatusOK {
			fatal(fmt.Errorf("server error (%d): %s", httpResp.StatusCode, strings.TrimSpace(string(data))))
		}
		if httpResp.Header.Get("X-Bloodhound-Timeout") == "1" {
			fmt.Fprintln(os.Stderr, "bloodhound search: timed out awaiting the curated result")
			os.Exit(1)
		}
		// R3037/R3040: print per the flag the caller sent. --raw relays the
		// secretary's own markdown (already Baby Food) verbatim; --markdown renders
		// the curated JSONL as a locator list; default is the JSONL verbatim (R3029
		// — an empty hunt is an empty body, no lines, exit 0).
		if markdown && !raw {
			os.Stdout.WriteString(renderFindingsMarkdown(data))
		} else {
			os.Stdout.Write(data)
		}
		return nil
	}
}

// bloodhoundFinding mirrors the CLI-hunt result JSONL wire shape (R3027) so the
// thin client can render it without importing the server type. path + range +
// curated note, with an optional chunk excerpt.
type bloodhoundFinding struct {
	Path  string `json:"path"`
	Range string `json:"range"`
	Note  string `json:"note"`
	Chunk string `json:"chunk"`
}

// renderFindingsMarkdown turns the curated JSONL into the Baby Food an agent
// reads (R3037): one `- ` + "`path:range`" + ` — note` locator line per finding,
// the chunk excerpt as a blockquote when present, and a "no findings" line for
// the empty result. Defensive — a line that does not parse as a finding is
// skipped, never fatal.
// CRC: crc-CLITree.md | R3037, R3040
func renderFindingsMarkdown(data []byte) string {
	var b strings.Builder
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var f bloodhoundFinding
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			continue // skip a malformed line rather than aborting the render
		}
		loc := f.Path
		if f.Range != "" {
			loc += ":" + f.Range
		}
		fmt.Fprintf(&b, "- `%s`", loc)
		if f.Note != "" {
			fmt.Fprintf(&b, " — %s", f.Note)
		}
		b.WriteByte('\n')
		if f.Chunk != "" {
			for _, cl := range strings.Split(strings.TrimRight(f.Chunk, "\n"), "\n") {
				fmt.Fprintf(&b, "> %s\n", cl)
			}
		}
		n++
	}
	if n == 0 {
		return "no findings\n"
	}
	return b.String()
}

// resolveClue returns the search clue from either positional args (joined into
// one line) or --file (read byte-for-byte; "-" reads stdin, the heredoc path).
// The two are mutually exclusive.
// CRC: crc-CLITree.md | R3046
func resolveClue(args []string, file string, stdin io.Reader) (string, error) {
	positional := strings.TrimSpace(strings.Join(args, " "))
	if file == "" {
		return positional, nil
	}
	if positional != "" {
		return "", errors.New("give the clue as positional args OR --file, not both")
	}
	var (
		data []byte
		err  error
	)
	if file == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(file)
	}
	if err != nil {
		return "", fmt.Errorf("reading clue: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// buildSearchPayload assembles the request payload metadata-first: scope/depth/
// want (and curate:false for --raw) as leading key:value lines, then the clue
// body last, so the watcher's clueOf strips the metadata and splits only the clue
// for the per-paragraph seed.
// CRC: crc-CLITree.md | Seq: seq-bloodhound-cli.md#1.1.2 | R3044, R3046
func buildSearchPayload(clue, scope, depth, want string, raw bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "scope: %s\ndepth: %s\nwant: %s\n", scope, depth, want)
	if raw {
		// R3038: the watcher reads this marker to skip Luhmann curation.
		b.WriteString("curate: false\n")
	}
	b.WriteString("\n")
	b.WriteString(clue)
	b.WriteString("\n")
	return b.String()
}
