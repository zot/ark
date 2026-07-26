package main

// The `ark ext` command group — author @ext routings into a target's
// mirror file from a plain session (the CLI counterpart to the
// Frictionless workshop's mcp.setExtTag/mcp.removeExtTag). set/add/
// remove each proxy to the running server (POST /ext/{set,add,remove})
// or open the index exclusively when stopped, mirroring the `config`
// add/remove dispatch. All three act only on the target's mirror file.
// (R3048; see crc-CLITree.md.)

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	ucli "github.com/urfave/cli/v3"
	"github.com/zot/ark"
)

// Candidate disposition tokens for `ark ext candidate` — external routes
// an accepted tag to the mirror (default); internal writes it into the
// source file body. Mirror of ark's package-internal disposition tokens.
// CRC: crc-CLITree.md | R3094
const (
	dispositionExternal = "external"
	dispositionInternal = "internal"
)

// CRC: crc-CLITree.md | Seq: seq-ext-author.md#3 | R3048, R3056
func extCommand() *ucli.Command {
	return &ucli.Command{
		Name:  "ext",
		Usage: "author @ext routings in a target's mirror file",
		Commands: []*ucli.Command{
			{
				Name:      "set",
				Usage:     "replace all values of <tag> on <target> with <value>",
				ArgsUsage: "<target> <tag> <value>",
				Action:    extSetAction,
			},
			{
				Name:      "add",
				Usage:     "append a <value> for <tag> on <target> (multi-value; exact dup no-ops)",
				ArgsUsage: "<target> <tag> <value>",
				Action:    extAddAction,
			},
			{
				Name:      "remove",
				Usage:     "remove <tag> routings on <target> (all values, or just <value>); --internal deletes the inline line instead",
				ArgsUsage: "<target> <tag> [value]",
				Flags: []ucli.Flag{
					// R3246: the internal-remove cell's CLI face, mirroring candidate's flags
					&ucli.StringFlag{Name: "disposition", Value: dispositionExternal, Usage: "what to remove: external (mirror routing) or internal (inline line in the source file)"},
					&ucli.BoolFlag{Name: "internal", Usage: "shorthand for --disposition internal"},
				},
				Action: extRemoveAction,
			},
			{
				Name:      "candidate",
				Usage:     "propose <tag> on <target> as an @ext-candidate",
				ArgsUsage: "<target> <tag> [value]",
				Flags: []ucli.Flag{
					&ucli.StringFlag{Name: "insight", Usage: "quoted rationale carried with the proposal"},
					&ucli.StringFlag{Name: "disposition", Value: dispositionExternal, Usage: "where an accepted tag is written: external (mirror) or internal (source file)"},
					&ucli.BoolFlag{Name: "internal", Usage: "shorthand for --disposition internal"},
					&ucli.BoolFlag{Name: "replace", Usage: "on accept, collapse the target's values of the tag to this one (default: add)"},
				},
				Action: extCandidateAction,
			},
			{
				Name:      "accept",
				Usage:     "commit matching @ext-candidate(s) on <target> to @ext",
				ArgsUsage: "<target> <tag> [value]",
				Action:    extAcceptAction,
			},
			{
				Name:      "reject",
				Usage:     "reject matching @ext-candidate(s) on <target> (durable @ext-judgment)",
				ArgsUsage: "<target> <tag> [value]",
				Action:    extRejectAction,
			},
			{
				Name:      "amend",
				Usage:     "revise the @ext-candidate line named by <tvid>, in place",
				ArgsUsage: "<tvid> <tag> [value]",
				Flags: []ucli.Flag{
					&ucli.StringFlag{Name: "insight", Usage: "replace the rationale (unset: keep the old one)"},
					&ucli.StringFlag{Name: "disposition", Usage: "where an accepted tag is written: external or internal (unset: keep)"},
					&ucli.BoolFlag{Name: "internal", Usage: "shorthand for --disposition internal"},
					&ucli.BoolFlag{Name: "replace", Usage: "on accept, collapse the target's values to this one (unset: keep)"},
					&ucli.StringFlag{Name: "target", Usage: "retarget the proposal (unset: keep)"},
				},
				Action: extAmendAction,
			},
			{
				Name:  "pending",
				Usage: "list the pending @ext-candidate ledger, ranked",
				Flags: []ucli.Flag{
					&ucli.StringFlag{Name: "chunk", Usage: "only candidates landing on this TARGET"},
					&ucli.IntFlag{Name: "limit", Usage: "keep only the first N rows after ranking (0 = all)"},
					&ucli.BoolFlag{Name: "json", Usage: "emit rows as JSON instead of a markdown table"},
				},
				Action: extPendingAction,
			},
		},
	}
}

// extProxyOrLocal runs an @ext mirror mutation: proxy to the running
// server (POST /ext/<verb>) when one is up, else open the index
// exclusively and call local. Mirrors the config add/remove dispatch.
// insight, disposition, and replace ride along only for candidate (empty /
// false for the other verbs).
// CRC: crc-CLITree.md | R3048, R3056, R3094, R3105
func extProxyOrLocal(verb, target, tag, value, insight, disposition string, replace bool, local func(*ark.DB) error) {
	if client := serverClient(arkDir); client != nil {
		body := map[string]string{"target": target, "tag": tag, "value": value}
		if insight != "" {
			body["insight"] = insight
		}
		if disposition != "" {
			body["disposition"] = disposition
		}
		if replace {
			body["replace"] = "true"
		}
		if err := proxyOK(client, "POST", "/ext/"+verb, body); err != nil {
			fatal(err)
		}
		return
	}
	withExclusiveDB(func(d *ark.DB) {
		if err := local(d); err != nil {
			fatal(err)
		}
	})
}

// CRC: crc-CLITree.md | Seq: seq-ext-author.md#3 | R3048
func extSetAction(_ context.Context, c *ucli.Command) error {
	target, tag, value := extArgs(c, true)
	extProxyOrLocal("set", target, tag, value, "", "", false, func(d *ark.DB) error { return d.SetExtTag(target, tag, value) })
	return nil
}

// CRC: crc-CLITree.md | Seq: seq-ext-author.md#3 | R3048
func extAddAction(_ context.Context, c *ucli.Command) error {
	target, tag, value := extArgs(c, true)
	extProxyOrLocal("add", target, tag, value, "", "", false, func(d *ark.DB) error { return d.AddExtTag(target, tag, value) })
	return nil
}

// CRC: crc-CLITree.md | Seq: seq-ext-author.md#3.4.1 | R3048, R3246
func extRemoveAction(_ context.Context, c *ucli.Command) error {
	target, tag, value := extArgs(c, false)
	disposition := extDisposition(c)
	extProxyOrLocal("remove", target, tag, value, "", disposition, false, func(d *ark.DB) error {
		if disposition == dispositionInternal {
			return d.RemoveInternalTag(target, tag)
		}
		return d.RemoveExtTag(target, tag, value)
	})
	return nil
}

// CRC: crc-CLITree.md | Seq: seq-ext-author.md#4.2 | R3056, R3094, R3105
func extCandidateAction(_ context.Context, c *ucli.Command) error {
	target, tag, value := extArgs(c, false)
	insight := c.String("insight")
	disposition := extDisposition(c)
	replace := c.Bool("replace")
	extProxyOrLocal("candidate", target, tag, value, insight, disposition, replace, func(d *ark.DB) error {
		return d.CandidateExtTag(target, tag, value, insight, disposition, replace)
	})
	return nil
}

// extDisposition resolves the candidate disposition from `--internal`
// (shorthand) or `--disposition`, defaulting to external. An unrecognized
// `--disposition` value is a fatal usage error.
// CRC: crc-CLITree.md | R3094
func extDisposition(c *ucli.Command) string {
	if c.Bool("internal") {
		return dispositionInternal
	}
	switch d := c.String("disposition"); d {
	case dispositionInternal, dispositionExternal:
		return d
	default:
		fatal(fmt.Errorf("--disposition must be %s or %s, got %q", dispositionInternal, dispositionExternal, d))
		return ""
	}
}

// CRC: crc-CLITree.md | Seq: seq-ext-author.md#4.3 | R3056
func extAcceptAction(_ context.Context, c *ucli.Command) error {
	target, tag, value := extArgs(c, false)
	extProxyOrLocal("accept", target, tag, value, "", "", false, func(d *ark.DB) error { _, err := d.AcceptExtTag(target, tag, value); return err })
	return nil
}

// CRC: crc-CLITree.md | Seq: seq-ext-author.md#4.4 | R3056
func extRejectAction(_ context.Context, c *ucli.Command) error {
	target, tag, value := extArgs(c, false)
	extProxyOrLocal("reject", target, tag, value, "", "", false, func(d *ark.DB) error { return d.RejectExtTag(target, tag, value) })
	return nil
}

// extAmendAction revises the @ext-candidate line named by <tvid>. Every
// flag is read through c.IsSet before c.String/c.Bool, because an unset
// flag must reach ExtAmendOpts as nil (carry the old line's value forward)
// and c.Bool alone cannot tell an unset --replace from --replace=false.
// CRC: crc-CLITree.md | Seq: seq-ext-author.md#6.1 | R3256
func extAmendAction(_ context.Context, c *ucli.Command) error {
	args := c.Args()
	if args.Len() < 2 {
		fatal(fmt.Errorf("usage: ark ext amend <tvid> <tag> [value]"))
	}
	tvid, err := strconv.ParseUint(args.Get(0), 10, 64)
	if err != nil {
		fatal(fmt.Errorf("<tvid> must be a number, got %q", args.Get(0)))
	}
	tag, value := args.Get(1), args.Get(2)

	var opts ark.ExtAmendOpts
	if c.IsSet("insight") {
		opts.Insight = ptrTo(c.String("insight"))
	}
	// --internal is a shorthand that IS a disposition, so it counts as set.
	if c.IsSet("disposition") || c.Bool("internal") {
		opts.Disposition = ptrTo(extDisposition(c))
	}
	if c.IsSet("replace") {
		opts.Replace = ptrTo(c.Bool("replace"))
	}
	if c.IsSet("target") {
		opts.Target = ptrTo(c.String("target"))
	}

	// The proxy body carries a key only for a flag the user actually set,
	// so the server's pointer fields decode to nil and carry forward — the
	// wire has to preserve absent-vs-empty just as ExtAmendOpts does.
	body := map[string]any{"tvid": tvid, "tag": tag, "value": value}
	putIfSet(body, "insight", opts.Insight)
	putIfSet(body, "disposition", opts.Disposition)
	putIfSet(body, "replace", opts.Replace)
	putIfSet(body, "target", opts.Target)

	proxyOrLocal(
		func(client *http.Client) error { return proxyOK(client, "POST", "/ext/amend", body) },
		func(d *ark.DB) error { return d.AmendExtCandidate(tvid, tag, value, opts) },
	)
	return nil
}

// ptrTo wraps a supplied flag value in the non-nil pointer that says
// "revise this field" rather than "inherit it" (ark.ExtAmendOpts).
// CRC: crc-CLITree.md | R3256
func ptrTo[T any](v T) *T { return &v }

// putIfSet adds a key only when the caller supplied the field, so an
// unset flag sends no key at all and the server decodes nil (inherit).
// CRC: crc-CLITree.md | R3256
func putIfSet[T any](m map[string]any, key string, p *T) {
	if p != nil {
		m[key] = *p
	}
}

// extPendingAction lists the pending ledger. The group's first **read**
// verb, so it takes the plain proxyOrLocal dispatch — proxy to
// GET /ext/pending when a server is up, open the index locally when none
// is — rather than its mutating siblings' hand-rolled shape. The `--chunk`
// TARGET is resolved wherever the index actually lives: server-side over
// the wire, locally otherwise. Rendering does no sorting or truncation of
// its own; the ranking and the limit are the query's (R3253).
// CRC: crc-CLITree.md | Seq: seq-ext-author.md#7 | R3258
func extPendingAction(_ context.Context, c *ucli.Command) error {
	target, limit := c.String("chunk"), c.Int("limit")
	var rows []ark.CandidateRow
	proxyOrLocal(
		func(client *http.Client) error {
			q := url.Values{}
			if target != "" {
				q.Set("target", target)
			}
			if limit != 0 {
				q.Set("limit", strconv.Itoa(int(limit)))
			}
			return proxyDecode(client, "GET", "/ext/pending?"+q.Encode(), nil, &rows)
		},
		func(d *ark.DB) error {
			var filter []uint64
			if target != "" {
				filter = d.ResolveExtTarget(target, "")
				if len(filter) == 0 {
					// An unresolvable filter must not silently widen into
					// the corpus-wide scan, which is what an empty filter
					// means to PendingCandidates.
					return fmt.Errorf("--chunk %q resolves to no chunk", target)
				}
			}
			var err error
			rows, err = d.Store().PendingCandidates(filter, int(limit))
			return err
		},
	)
	if c.Bool("json") {
		out, err := json.MarshalIndent(rows, "", "  ")
		if err != nil {
			fatal(err)
		}
		fmt.Println(string(out))
		return nil
	}
	printPendingTable(rows)
	return nil
}

// printPendingTable renders the ledger as baby food: a markdown table the
// reader never has to parse. Unattached rows (no target chunk) lead, and
// their empty chunk cell is the signal that the proposal found nothing —
// a typo'd TARGET, or a corpus change that moved the content.
// CRC: crc-CLITree.md | R3258
func printPendingTable(rows []ark.CandidateRow) {
	if len(rows) == 0 {
		fmt.Println("No pending @ext-candidate proposals.")
		return
	}
	fmt.Println("| tvid | tag | value | target | chunks | tally | date | on accept | insight |")
	fmt.Println("|------|-----|-------|--------|--------|-------|------|-----------|---------|")
	for _, row := range rows {
		chunks := "—"
		if len(row.Chunks) > 0 {
			ids := make([]string, len(row.Chunks))
			for i, c := range row.Chunks {
				ids[i] = strconv.FormatUint(c, 10)
			}
			chunks = strings.Join(ids, ", ")
		}
		accept := row.Disposition
		if row.Replace {
			accept += " replace"
		}
		fmt.Printf("| %d | @%s | %s | %s | %s | %d | %s | %s | %s |\n",
			row.SourceTvid, row.Tagname, cell(row.Value), cell(row.Target),
			chunks, row.Tally, cell(row.Date), accept, cell(row.Insight))
	}
	fmt.Printf("\n%d pending proposal(s).\n", len(rows))
}

// cell keeps a value from breaking the markdown table: pipes are escaped
// and an empty field reads as an em dash rather than a blank cell.
// CRC: crc-CLITree.md | R3258
func cell(s string) string {
	if s = strings.TrimSpace(s); s == "" {
		return "—"
	}
	return strings.ReplaceAll(s, "|", `\|`)
}

// extArgs pulls <target> <tag> [value] positionals. requireValue
// fatals when <value> is absent (set/add need it; remove treats it as
// an optional filter, defaulting to "" = all values).
// CRC: crc-CLITree.md | R3048
func extArgs(c *ucli.Command, requireValue bool) (target, tag, value string) {
	args := c.Args()
	need := "<target> <tag> [value]"
	if requireValue {
		need = "<target> <tag> <value>"
	}
	if args.Len() < 2 || (requireValue && args.Len() < 3) {
		fatal(fmt.Errorf("usage: ark ext %s %s", c.Name, need))
	}
	// Args().Get(n) returns "" when out of range, so value is empty
	// for `remove <target> <tag>` (remove-all).
	return args.Get(0), args.Get(1), args.Get(2)
}
