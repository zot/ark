package main

// The `ark connections` command group, migrated to the urfave/cli v3
// command tree. Each node declares its own flags so --help is generated
// from the declarations (single source — no hand-maintained printer), and
// each Action reads flag values from the command context and reuses the
// existing handler helpers. The deepest tree in the CLI (3 levels:
// `connections recall close`) and where the -loc/-chunk help drift lived —
// the migration pilot. (R2916–R2932; see crc-CLITree.md, seq-cli-urfave.md.)

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	ucli "github.com/urfave/cli/v3"
	"github.com/zot/ark"
)

// connectionsCommand builds the `ark connections` node tree.
// CRC: crc-CLITree.md | Seq: seq-cli-urfave.md#3.3 | R2916, R2917, R2919
func connectionsCommand() *ucli.Command {
	return &ucli.Command{
		Name:  "connections",
		Usage: "find-connections substrate + recall verbs + sidecar (requires server)",
		// R2615: the removed turbo sidecar flags map to a migration hint.
		// Hidden so they don't clutter help; detected in the parent Action.
		Flags: []ucli.Flag{
			&ucli.BoolFlag{Name: "wait", Hidden: true},
			&ucli.BoolFlag{Name: "fetch", Hidden: true},
			&ucli.BoolFlag{Name: "result", Hidden: true},
			&ucli.BoolFlag{Name: "error", Hidden: true},
		},
		Action: connParentAction,
		Commands: []*ucli.Command{
			{
				Name:      "find",
				Usage:     "submit a find-connections request for INPUTS...",
				ArgsUsage: "INPUTS...  (NNNNNN chunkID | PATH:N-M | PATH:N | \"text\")",
				Flags: []ucli.Flag{
					&ucli.StringFlag{Name: "mode", Value: "normal", Usage: "mode: normal | turbo"},
					&ucli.IntFlag{Name: "k", Value: 20, Usage: "top-K candidates (clamped to [1,200])"},
					&ucli.StringFlag{Name: "purpose", Value: "curate", Usage: "purpose: curate | recall | ..."},
					&ucli.IntFlag{Name: "timeout", Value: 30, Usage: "timeout seconds (clamped to [5,300])"},
					&ucli.BoolFlag{Name: "wait", Usage: "block until terminal status; print body on stdout"},
					&ucli.BoolFlag{Name: "json", Usage: "with --wait, emit JSON projection instead of markdown"},
					&ucli.StringFlag{Name: "type", Usage: "input type: chunk | text (default: auto-detect each input)"},
				},
				Action: connFindAction,
			},
			recallCommand(),
			{
				Name:      "wait",
				Usage:     "block until the connections doc at PATH reaches a terminal status",
				ArgsUsage: "PATH",
				Flags: []ucli.Flag{
					&ucli.IntFlag{Name: "timeout", Value: 60, Usage: "timeout in seconds before giving up"},
					&ucli.BoolFlag{Name: "json", Usage: "emit JSON projection instead of markdown"},
				},
				Action: connWaitAction,
			},
			{
				Name:      "show",
				Usage:     "project structured fields from the persisted doc (no block)",
				ArgsUsage: "PATH",
				Flags: []ucli.Flag{
					&ucli.BoolFlag{Name: "status", Usage: "print only @connections-status"},
					&ucli.BoolFlag{Name: "tags", Usage: "list tag-name proposals one per line"},
					&ucli.StringFlag{Name: "tag", Usage: "filter to proposals whose @proposal-value equals NAME"},
					&ucli.FloatFlag{Name: "threshold", Usage: "drop proposals below score N (0.0-1.0)"},
					&ucli.BoolFlag{Name: "json", Usage: "emit JSON projection instead of markdown"},
				},
				Action: connShowAction,
			},
			{
				Name:   "list",
				Usage:  "list in-flight connections requests",
				Flags:  []ucli.Flag{&ucli.BoolFlag{Name: "json", Usage: "emit JSON array instead of markdown table"}},
				Action: connListAction,
			},
			{
				Name:  "clean",
				Usage: "wipe recall-substrate state to help with testing",
				Flags: []ucli.Flag{
					&ucli.BoolFlag{Name: "all", Usage: "also wipe RF, RJ, tmp://connections/*, tmp://ARK-RECALL/*"},
					&ucli.StringFlag{Name: "session", Usage: "restrict RD/RM/checkpoint scope: session UUID, or 'project' for cwd-resolved sessions"},
					&ucli.BoolFlag{Name: "checkpoint", Usage: "advance the indexer's FileLength on session JSONLs so the next turn starts from current EOF"},
				},
				Action: connCleanAction,
			},
			{
				Name:   "sidecar-wait",
				Usage:  "lotto tube: drain the turbo request queue (JSON)",
				Action: func(_ context.Context, _ *ucli.Command) error { cmdConnectionsSidecarWait(nil); return nil },
			},
			{
				Name:      "sidecar-fetch",
				Usage:     "print chunk content JSON for a request",
				ArgsUsage: "ID",
				Action: func(_ context.Context, c *ucli.Command) error {
					cmdConnectionsSidecarFetch(c.Args().Slice())
					return nil
				},
			},
			{
				Name:      "sidecar-result",
				Usage:     "post result JSON (read from stdin) for a request",
				ArgsUsage: "ID",
				Action: func(_ context.Context, c *ucli.Command) error {
					cmdConnectionsSidecarResult(c.Args().Slice())
					return nil
				},
			},
			{
				Name:      "sidecar-error",
				Usage:     "post an error message for a request",
				ArgsUsage: "ID MESSAGE",
				Action: func(_ context.Context, c *ucli.Command) error {
					cmdConnectionsSidecarError(c.Args().Slice())
					return nil
				},
			},
		},
	}
}

// recallCommand is the `connections recall` parent+leaf hybrid: 7 result-
// builder subcommands plus a default Action (the substrate recall) that
// runs when arg0 is not a known subcommand.
// CRC: crc-CLITree.md | R2916, R2919
func recallCommand() *ucli.Command {
	return &ucli.Command{
		Name:      "recall",
		Usage:     "substrate recall (INPUTS...) + result-builder verbs",
		ArgsUsage: "INPUTS...  (or a subcommand: reserve-nonce, surface, recommend, close, context, next, listen)",
		Flags: []ucli.Flag{
			&ucli.IntFlag{Name: "k", Value: 20, Usage: "top-K chunks (clamped [1,200])"},
			&ucli.BoolFlag{Name: "no-content", Usage: "set IncludeContent to false"},
			&ucli.BoolFlag{Name: "json", Usage: "emit JSON result"},
			&ucli.BoolFlag{Name: "all", Usage: "keep tagless chunks in results (default drops them)"},
			&ucli.StringFlag{Name: "type", Usage: "input type: chunk | text (default: auto-detect)"},
			&ucli.StringFlag{Name: "session", Usage: "load the session's discussed-tag set into the exclusion set"},
			&ucli.StringFlag{Name: "discussed", Usage: "comma-separated @t[:v] exclusions (unioned with --session set)"},
			&ucli.BoolFlag{Name: "propose", Usage: "run the statistical derivation pass; persist + surface derived-tag candidates"},
		},
		Action: connRecallDefaultAction,
		Commands: []*ucli.Command{
			{
				Name:   "reserve-nonce",
				Usage:  "return the next per-server monotonic nonce",
				Action: connRecallReserveNonceAction,
			},
			{
				Name:      "surface",
				Usage:     "append one ## Surface: item to the result-doc builder for FIRE",
				ArgsUsage: "FIRE",
				Flags: []ucli.Flag{
					&ucli.StringFlag{Name: "loc", Usage: "candidate <path>:<range> (required)"},
					&ucli.StringFlag{Name: "reason", Usage: "one-line justification (required)"},
				},
				Action: connRecallSurfaceAction,
			},
			{
				Name:      "recommend",
				Usage:     "append one ## Recommend: item to the result-doc builder for FIRE",
				ArgsUsage: "FIRE",
				Flags: []ucli.Flag{
					&ucli.StringFlag{Name: "loc", Usage: "target <path>:<range> (required)"},
					&ucli.StringFlag{Name: "tag", Usage: "@t or @t:value (required)"},
					&ucli.StringFlag{Name: "reason", Usage: "one-line justification (required)"},
				},
				Action: connRecallRecommendAction,
			},
			{
				Name:      "close",
				Usage:     "single cleanup verb: finalize the result doc for FIRE",
				ArgsUsage: "FIRE",
				Flags: []ucli.Flag{
					&ucli.UintFlag{Name: "nonce", Usage: "nonce reserved by the assistant (required)"},
					&ucli.BoolFlag{Name: "preserve-curation", Usage: "leave the curation doc in place after close"},
				},
				Action: connRecallCloseAction,
			},
			{
				Name:  "context",
				Usage: "report the calling subagent's context fill in tokens",
				Flags: []ucli.Flag{
					&ucli.UintFlag{Name: "nonce", Usage: "subagent nonce (required)"},
					&ucli.IntFlag{Name: "limit", Usage: "if non-zero, exit 1 when tokens >= limit, else 0"},
					&ucli.BoolFlag{Name: "json", Usage: "emit JSON instead of bare token count"},
				},
				Action: connRecallContextAction,
			},
			{
				Name:      "next",
				Usage:     "daemon loop verb: block for the next curation doc (NONCE)",
				ArgsUsage: "NONCE",
				Flags:     []ucli.Flag{&ucli.StringFlag{Name: "session", Usage: "Claude Code session UUID — value-scope the curate subscription"}},
				Action:    connRecallNextAction,
			},
			{
				Name:   "listen",
				Usage:  "consumer loop verb: block until a recall result arrives for the session",
				Flags:  []ucli.Flag{&ucli.StringFlag{Name: "session", Usage: "Claude Code session UUID (required)"}},
				Action: connRecallListenAction,
			},
		},
	}
}

// --- Actions ---------------------------------------------------------------

// CRC: crc-CLITree.md | R2615
func connParentAction(_ context.Context, c *ucli.Command) error {
	for _, legacy := range []string{"wait", "fetch", "result", "error"} {
		if c.Bool(legacy) {
			fmt.Fprintf(os.Stderr, "ark connections --%s removed; use `ark connections sidecar-%s` instead\n", legacy, legacy)
			os.Exit(2)
		}
	}
	// No matching subcommand: a stray arg is an unknown subcommand; bare
	// `connections` just shows help. Both exit non-zero, matching state A.
	if args := c.Args().Slice(); len(args) > 0 {
		fmt.Fprintf(os.Stderr, "unknown connections subcommand: %s\n", args[0])
	}
	_ = ucli.ShowSubcommandHelp(c)
	os.Exit(1)
	return nil
}

// CRC: crc-CLITree.md | Seq: seq-find-connections-substrate.md | R2604, R2605, R2616, R2928
func connFindAction(_ context.Context, c *ucli.Command) error {
	posArgs := c.Args().Slice()
	if len(posArgs) == 0 {
		fmt.Fprintln(os.Stderr, "usage: ark connections find INPUTS... (see --help)")
		os.Exit(1)
	}
	inputs, err := parseConnectionsInputs(posArgs, c.String("type"))
	if err != nil {
		fatal(err)
	}
	client := serverClient(arkDir)
	if client == nil {
		fatal(fmt.Errorf("server not running — start with: ark serve"))
	}
	body := map[string]any{
		"inputs": inputs,
		"opts": map[string]any{
			"mode":           c.String("mode"),
			"k":              c.Int("k"),
			"purpose":        c.String("purpose"),
			"timeoutSeconds": c.Int("timeout"),
		},
	}
	raw, err := proxyRaw(client, "POST", "/connections/find", body)
	if err != nil {
		fatal(err)
	}
	var resp struct {
		RequestID string `json:"requestID"`
		Path      string `json:"path"`
	}
	if jerr := json.Unmarshal(raw, &resp); jerr != nil {
		fatal(fmt.Errorf("decode find response: %w", jerr))
	}
	if !c.Bool("wait") {
		fmt.Println(resp.Path)
		return nil
	}
	out, werr := waitConnectionsDoc(client, resp.Path, c.Int("timeout"), c.Bool("json"))
	if werr != nil {
		fatal(werr)
	}
	fmt.Print(out)
	return nil
}

// CRC: crc-CLITree.md | R2606
func connWaitAction(_ context.Context, c *ucli.Command) error {
	posArgs := c.Args().Slice()
	if len(posArgs) != 1 {
		fmt.Fprintln(os.Stderr, "usage: ark connections wait PATH [--timeout S] [--json]")
		os.Exit(1)
	}
	client := serverClient(arkDir)
	if client == nil {
		fatal(fmt.Errorf("server not running — start with: ark serve"))
	}
	out, err := waitConnectionsDoc(client, posArgs[0], c.Int("timeout"), c.Bool("json"))
	if err != nil {
		fatal(err)
	}
	fmt.Print(out)
	return nil
}

// CRC: crc-CLITree.md | R2607, R2608
func connShowAction(_ context.Context, c *ucli.Command) error {
	posArgs := c.Args().Slice()
	if len(posArgs) != 1 {
		fmt.Fprintln(os.Stderr, "usage: ark connections show PATH [options]")
		os.Exit(1)
	}
	client := serverClient(arkDir)
	if client == nil {
		fatal(fmt.Errorf("server not running — start with: ark serve"))
	}
	body, err := proxyRaw(client, "GET", "/fetch?path="+url.QueryEscape(posArgs[0]), nil)
	if err != nil {
		fatal(err)
	}
	doc := ark.ParseConnectionsDoc(body)
	if c.Bool("status") {
		fmt.Println(doc.Status)
		return nil
	}
	tagFilter := c.String("tag")
	threshold := c.Float("threshold")
	if tagFilter != "" || threshold > 0 {
		filtered := make([]ark.ConnectionsPropo, 0, len(doc.Proposals))
		for _, p := range doc.Proposals {
			if tagFilter != "" && p.Value != tagFilter {
				continue
			}
			if threshold > 0 && p.Score < threshold {
				continue
			}
			filtered = append(filtered, p)
		}
		doc.Proposals = filtered
		doc.ProposalCount = len(filtered)
	}
	if c.Bool("tags") {
		for _, p := range doc.Proposals {
			if p.Kind == "tag-name" {
				fmt.Println(p.Value)
			}
		}
		return nil
	}
	if c.Bool("json") {
		out, _ := json.MarshalIndent(doc, "", "  ")
		fmt.Println(string(out))
		return nil
	}
	renderConnectionsShow(doc)
	return nil
}

// CRC: crc-CLITree.md | R2609
func connListAction(_ context.Context, c *ucli.Command) error {
	client := serverClient(arkDir)
	if client == nil {
		fatal(fmt.Errorf("server not running — start with: ark serve"))
	}
	raw, err := proxyRaw(client, "GET", "/connections/list", nil)
	if err != nil {
		fatal(err)
	}
	if c.Bool("json") {
		fmt.Print(string(raw))
		if !strings.HasSuffix(string(raw), "\n") {
			fmt.Println()
		}
		return nil
	}
	var recs []struct {
		ID            string    `json:"id"`
		Mode          string    `json:"mode"`
		Purpose       string    `json:"purpose"`
		Status        string    `json:"status"`
		Started       time.Time `json:"started"`
		Elapsed       int       `json:"elapsed"`
		Path          string    `json:"path"`
		ProposalCount int       `json:"proposalCount,omitempty"`
		Error         string    `json:"error,omitempty"`
		Warning       string    `json:"warning,omitempty"`
	}
	if jerr := json.Unmarshal(raw, &recs); jerr != nil {
		fatal(fmt.Errorf("decode list response: %w", jerr))
	}
	if len(recs) == 0 {
		fmt.Println("no in-flight connections requests")
		return nil
	}
	fmt.Println("| ID       | Mode    | Status     | Purpose | Elapsed | Path |")
	fmt.Println("|----------|---------|------------|---------|---------|------|")
	for _, r := range recs {
		fmt.Printf("| %-8s | %-7s | %-10s | %-7s | %ds | %s |\n",
			truncStr(r.ID, 8), r.Mode, r.Status, r.Purpose, r.Elapsed, r.Path)
	}
	return nil
}

// CRC: crc-CLITree.md | R2744, R2887
func connCleanAction(_ context.Context, c *ucli.Command) error {
	all := c.Bool("all")
	checkpoint := c.Bool("checkpoint")

	var sessions []string
	switch c.String("session") {
	case "":
		sessions = nil
	case "project":
		ids, err := projectSessionIDs()
		if err != nil {
			fatal(err)
		}
		if len(ids) == 0 {
			fmt.Fprintln(os.Stderr, "no sessions found for this project; RD would be a no-op")
			os.Exit(1)
		}
		sessions = ids
	default:
		sessions = []string{c.String("session")}
	}

	var resp struct {
		Status          string `json:"status"`
		RC              int    `json:"rc"`
		RD              int    `json:"rd"`
		RM              int    `json:"rm"`
		RF              int    `json:"rf"`
		RJ              int    `json:"rj"`
		TmpConnections  int    `json:"tmpConnections"`
		TmpRecall       int    `json:"tmpRecall"`
		CheckpointFiles int    `json:"checkpointFiles"`
	}
	if client := serverClient(arkDir); client != nil {
		body := map[string]any{"sessions": sessions, "all": all, "checkpoint": checkpoint}
		if err := proxyDecode(client, "POST", "/connections/clean", body, &resp); err != nil {
			fatal(err)
		}
	} else {
		withDB(func(db *ark.DB) {
			rc, err := db.ClearAllDerivedProposals()
			if err != nil {
				fatal(err)
			}
			resp.RC = rc
			if len(sessions) == 0 {
				n, err := db.ClearAllDiscussed()
				if err != nil {
					fatal(err)
				}
				resp.RD = n
			} else {
				for _, sess := range sessions {
					n, err := db.ClearDiscussed(sess)
					if err != nil {
						fatal(err)
					}
					resp.RD += n
				}
			}
			if len(sessions) == 0 {
				n, err := db.ClearAllSurfaceCooldown()
				if err != nil {
					fatal(err)
				}
				resp.RM = n
			} else {
				for _, sess := range sessions {
					n, err := db.ClearSurfaceCooldown(sess)
					if err != nil {
						fatal(err)
					}
					resp.RM += n
				}
			}
			if all {
				rf, err := db.ClearAllDerivedFreshness()
				if err != nil {
					fatal(err)
				}
				resp.RF = rf
				rj, err := db.ClearAllDerivedRejections()
				if err != nil {
					fatal(err)
				}
				resp.RJ = rj
			}
			if checkpoint {
				paths, err := db.SessionJSONLs(sessions)
				if err != nil {
					fatal(err)
				}
				for _, p := range paths {
					if _, err := db.CheckpointFile(p); err != nil {
						fatal(err)
					}
					resp.CheckpointFiles++
				}
			}
		})
	}
	fmt.Fprintf(os.Stderr, "cleaned: RC=%d RD=%d RM=%d", resp.RC, resp.RD, resp.RM)
	if all {
		fmt.Fprintf(os.Stderr, " RF=%d RJ=%d tmp-connections=%d tmp-recall=%d",
			resp.RF, resp.RJ, resp.TmpConnections, resp.TmpRecall)
	}
	if checkpoint {
		fmt.Fprintf(os.Stderr, " checkpoint-files=%d", resp.CheckpointFiles)
	}
	fmt.Fprintln(os.Stderr)
	return nil
}

// CRC: crc-CLITree.md | R2617, R2618, R2619, R2627, R2641, R2655
func connRecallDefaultAction(_ context.Context, c *ucli.Command) error {
	posArgs := c.Args().Slice()
	if len(posArgs) == 0 {
		fatal(fmt.Errorf("no inputs given"))
	}
	clampedK := c.Int("k")
	if clampedK <= 0 {
		clampedK = 20
	}
	if clampedK > 200 {
		clampedK = 200
	}
	inputs, err := parseConnectionsInputs(posArgs, c.String("type"))
	if err != nil {
		fatal(err)
	}
	discussed, err := parseDiscussedList(c.String("discussed"))
	if err != nil {
		fatal(err)
	}
	opts := ark.RecallOpts{
		K:              clampedK,
		IncludeContent: !c.Bool("no-content"),
		KeepTagless:    c.Bool("all"),
		Session:        c.String("session"),
		Discussed:      discussed,
		Propose:        c.Bool("propose"),
	}
	if err := runConnectionsRecallParsed(inputs, opts, c.Bool("json"), os.Stdout); err != nil {
		fatal(err)
	}
	return nil
}

// CRC: crc-CLITree.md | Seq: seq-recall-agent.md#2 | R2755
func connRecallReserveNonceAction(_ context.Context, c *ucli.Command) error {
	if c.Args().Len() > 0 {
		fmt.Fprintln(os.Stderr, "ark connections recall reserve-nonce: no arguments expected")
		os.Exit(2)
	}
	client := serverClient(arkDir)
	if client == nil {
		fatal(errors.New("server not running; start with `ark serve`"))
	}
	var resp struct {
		Nonce uint32 `json:"nonce"`
	}
	if err := proxyDecode(client, "POST", "/connections/recall/reserve-nonce", struct{}{}, &resp); err != nil {
		fatal(err)
	}
	fmt.Println(resp.Nonce)
	return nil
}

// CRC: crc-CLITree.md | Seq: seq-recall-agent.md#4 | R2900
func connRecallSurfaceAction(_ context.Context, c *ucli.Command) error {
	fire, _, err := popFire(c.Args().Slice(), "surface")
	if err != nil {
		fatal(err)
	}
	loc := c.String("loc")
	reason := c.String("reason")
	if loc == "" || reason == "" {
		fmt.Fprintln(os.Stderr, "ark connections recall surface FIRE -loc PATH:RANGE -reason TEXT")
		os.Exit(2)
	}
	client := serverClient(arkDir)
	if client == nil {
		fatal(errors.New("server not running; start with `ark serve`"))
	}
	if err := proxyOK(client, "POST", "/connections/recall/surface", map[string]any{
		"fire": fire, "loc": loc, "reason": reason,
	}); err != nil {
		fatal(err)
	}
	return nil
}

// CRC: crc-CLITree.md | Seq: seq-recall-agent.md#5 | R2757
func connRecallRecommendAction(_ context.Context, c *ucli.Command) error {
	fire, _, err := popFire(c.Args().Slice(), "recommend")
	if err != nil {
		fatal(err)
	}
	loc := c.String("loc")
	tagSpec := c.String("tag")
	reason := c.String("reason")
	if loc == "" || tagSpec == "" || reason == "" {
		fmt.Fprintln(os.Stderr, "ark connections recall recommend FIRE -loc PATH:RANGE -tag @t[:v] -reason TEXT")
		os.Exit(2)
	}
	client := serverClient(arkDir)
	if client == nil {
		fatal(errors.New("server not running; start with `ark serve`"))
	}
	if err := proxyOK(client, "POST", "/connections/recall/recommend", map[string]any{
		"fire": fire, "loc": loc, "tag": tagSpec, "reason": reason,
	}); err != nil {
		fatal(err)
	}
	return nil
}

// CRC: crc-CLITree.md | Seq: seq-recall-agent.md#7 | R2758
func connRecallCloseAction(_ context.Context, c *ucli.Command) error {
	fire, _, err := popFire(c.Args().Slice(), "close")
	if err != nil {
		fatal(err)
	}
	nonce := c.Uint("nonce")
	if nonce == 0 {
		fmt.Fprintln(os.Stderr, "ark connections recall close FIRE --nonce N [-preserve-curation]")
		os.Exit(2)
	}
	client := serverClient(arkDir)
	if client == nil {
		fatal(errors.New("server not running; start with `ark serve`"))
	}
	if err := proxyOK(client, "POST", "/connections/recall/close", map[string]any{
		"fire": fire, "nonce": nonce, "preserveCuration": c.Bool("preserve-curation"),
	}); err != nil {
		fatal(err)
	}
	return nil
}

// CRC: crc-CLITree.md | R2777
func connRecallContextAction(_ context.Context, c *ucli.Command) error {
	nonce := c.Uint("nonce")
	if nonce == 0 {
		fmt.Fprintln(os.Stderr, "ark connections recall context --nonce N [--limit N] [--json]")
		os.Exit(2)
	}
	client := serverClient(arkDir)
	if client == nil {
		fatal(errors.New("server not running; start with `ark serve`"))
	}
	var resp struct {
		Tokens int  `json:"tokens"`
		Found  bool `json:"found"`
	}
	if err := proxyDecode(client, "POST", "/connections/recall/context", map[string]any{"nonce": nonce}, &resp); err != nil {
		fatal(err)
	}
	if c.Bool("json") {
		enc, _ := json.Marshal(resp)
		fmt.Println(string(enc))
	} else {
		fmt.Println(resp.Tokens)
	}
	if limit := c.Int("limit"); limit > 0 && resp.Tokens >= limit {
		os.Exit(1)
	}
	return nil
}

// CRC: crc-CLITree.md | Seq: seq-recall-agent.md#3 | R2857, R2858, R2903
func connRecallNextAction(_ context.Context, c *ucli.Command) error {
	rest := c.Args().Slice()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "ark connections recall next [--session SID] NONCE")
		os.Exit(2)
	}
	nonce, err := strconv.ParseUint(rest[0], 10, 32)
	if err != nil || nonce == 0 {
		fmt.Fprintln(os.Stderr, "ark connections recall next [--session SID] NONCE (positive integer)")
		os.Exit(2)
	}
	session := c.String("session")
	nextURL := fmt.Sprintf("http://ark/connections/recall/next?nonce=%d", nonce)
	if session != "" {
		nextURL += "&session=" + session
	}
	req, err := http.NewRequest("GET", nextURL, nil)
	if err != nil {
		fatal(err)
	}
	// R2903: stubborn plumbing — a server bounce is a wait condition, not an
	// error. Redial with backoff on a cold dial up to a bounded budget; on a
	// mid-block drop or exhausted budget, hand back a keepalive (exit 0) so
	// the secretary's loop re-invokes next and rides out the bounce.
	deadline := time.Now().Add(recallNextRedialBudget)
	backoff := recallNextRedialBackoff
	for {
		client := serverClient(arkDir)
		if client == nil {
			if time.Now().After(deadline) {
				os.Stdout.WriteString(recallRedialKeepalive(session, nonce))
				return nil
			}
			time.Sleep(backoff)
			backoff = min(backoff*2, recallNextRedialMaxBackoff)
			continue
		}
		resp, derr := client.Do(req)
		if derr != nil {
			time.Sleep(backoff)
			os.Stdout.WriteString(recallRedialKeepalive(session, nonce))
			return nil
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			fatal(fmt.Errorf("server error (%d): %s", resp.StatusCode, strings.TrimSpace(string(data))))
		}
		os.Stdout.Write(data)
		if resp.Header.Get("X-Recall-Exit") == "1" {
			os.Exit(2)
		}
		return nil
	}
}

// CRC: crc-CLITree.md | R2865
func connRecallListenAction(_ context.Context, c *ucli.Command) error {
	session := c.String("session")
	if session == "" {
		fmt.Fprintln(os.Stderr, "ark connections recall listen --session SID")
		os.Exit(2)
	}
	client := serverClient(arkDir)
	if client == nil {
		fatal(errors.New("server not running; start with `ark serve`"))
	}
	req, err := http.NewRequest("GET", fmt.Sprintf("http://ark/connections/recall/listen?session=%s", session), nil)
	if err != nil {
		fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fatal(fmt.Errorf("server error (%d): %s", resp.StatusCode, strings.TrimSpace(string(data))))
	}
	os.Stdout.Write(data)
	return nil
}
