package main

// The flat (non-grouped) top-level commands, migrated to the urfave/cli
// v3 command tree (Stage 2 of the CLI urfave migration). Per the
// thin-tree decision (Bill, 2026-06-07): the node declarations supply the
// self-documenting help, while each Action delegates to the existing cmdX
// body kept in main.go — zero transcription risk for the index-building
// core (serve/init/add/scan/rebuild). No-flag commands pass their
// positional args straight through; flag-bearing commands declare their
// flags here and reconstruct the argv the kept handler expects.
//
// (Steady-state requirements stay anchored on the kept cmdX bodies in
// main.go via crc-CLI.md; the nodes are CLITree surface — see crc-CLITree.md.)

import (
	"context"
	"strconv"

	ucli "github.com/urfave/cli/v3"
)

// flatDelegate adapts a legacy `func(args []string)` handler into an
// urfave Action that passes the command's positional args through.
func flatDelegate(fn func([]string)) ucli.ActionFunc {
	return func(_ context.Context, c *ucli.Command) error {
		fn(c.Args().Slice())
		return nil
	}
}

// flatCommands returns the migrated flat top-level command nodes.
// Grows batch by batch through the flat-command rollout.
// CRC: crc-CLITree.md, crc-CLI.md | Seq: seq-cli-urfave.md#3.3
func flatCommands() []*ucli.Command {
	return []*ucli.Command{
		// --- no-flag, positional-only (thin-delegate) ---
		{Name: "version", Usage: "print the ark version", Action: flatDelegate(cmdVersion)},
		{Name: "setup", Usage: "bootstrap ~/.ark/ (extract assets, install skills)", Action: flatDelegate(cmdSetup)},
		{Name: "rebuild", Usage: "drop and rebuild the entire index", Action: flatDelegate(cmdRebuild)},
		{Name: "scan", Usage: "walk source directories, index new files", Action: flatDelegate(cmdScan)},
		{Name: "refresh", Usage: "re-index stale files", ArgsUsage: "[PATTERN...]", Action: flatDelegate(cmdRefresh)},
		{Name: "remove", Usage: "remove files from the index", ArgsUsage: "<path|pattern>...", Action: flatDelegate(cmdRemove)},
		{Name: "resolve", Usage: "dismiss unresolved files by pattern", ArgsUsage: "<pattern>...", Action: flatDelegate(cmdResolve)},
		{Name: "dismiss", Usage: "dismiss missing files", ArgsUsage: "<path|pattern>...", Action: flatDelegate(cmdDismiss)},
		{Name: "stale", Usage: "list stale files", ArgsUsage: "[PATTERN...]", Action: flatDelegate(cmdStale)},
		{Name: "missing", Usage: "list missing files", ArgsUsage: "[PATTERN...]", Action: flatDelegate(cmdMissing)},
		{Name: "unresolved", Usage: "list unresolved files", ArgsUsage: "[PATTERN...]", Action: flatDelegate(cmdUnresolved)},
		{Name: "sources", Usage: "manage source directories", ArgsUsage: "[check]", Action: flatDelegate(cmdSources)},
		{Name: "grams", Usage: "show trigrams for a query (active/inactive, frequency)", ArgsUsage: "<query>", Action: flatDelegate(cmdGrams)},
		{Name: "chunk-chat-jsonl", Usage: "chunk a JSONL file on newline boundaries (microfts2 protocol)", ArgsUsage: "<file>", Action: flatDelegate(cmdChunkJSONL)},
		{Name: "ls", Usage: "list embedded assets", Action: flatDelegate(cmdBundleLs)},
		{Name: "cat", Usage: "print an embedded file to stdout", ArgsUsage: "<file>", Action: flatDelegate(cmdBundleCat)},
		{Name: "cp", Usage: "extract embedded files matching a glob pattern", ArgsUsage: "<pattern> <dest-dir>", Action: flatDelegate(cmdBundleCp)},

		// nano parses -m/-c/-s itself (not via the flag package), so let
		// urfave hand it the raw argv unparsed.
		{Name: "nano", Usage: "embedded shell-agent loop (Ollama-backed; -m model, -c/-s session)", SkipFlagParsing: true, Action: flatDelegate(cmdNano)},

		// sweep: one subcommand today (correlations).
		{
			Name:  "sweep",
			Usage: "run a corpus-wide sweep (correlations: refresh HC top-K cache)",
			Commands: []*ucli.Command{
				{Name: "correlations", Usage: "refresh the hot-correlations top-K cache per tag (requires server)", Action: flatDelegate(cmdSweepCorrelations)},
			},
		},

		// --- batch 2: flag-bearing commands (declare flags, reconstruct argv) ---
		{
			Name: "add", Usage: "add files to the index", ArgsUsage: "PATH...",
			Flags: []ucli.Flag{
				&ucli.StringFlag{Name: "strategy", Usage: "chunking strategy"},
				&ucli.StringFlag{Name: "content", Usage: "inline content for tmp:// documents"},
				&ucli.StringFlag{Name: "from-file", Usage: "read content from file for tmp:// documents"},
				&ucli.BoolFlag{Name: "append", Usage: "append to existing tmp:// document instead of replacing"},
			},
			Action: flatAddAction,
		},
		{
			Name: "bundle", Usage: "graft a directory onto a binary as a zip appendix (build-time)",
			Flags: []ucli.Flag{
				&ucli.StringFlag{Name: "o", Usage: "output path for bundled binary (required)"},
				&ucli.StringFlag{Name: "src", Usage: "source binary to bundle (default: current executable)"},
			},
			Action: flatBundleAction,
		},
		{
			Name: "chunks", Usage: "show chunks around a search hit (context expansion)", ArgsUsage: "PATTERN...",
			Flags: []ucli.Flag{
				&ucli.IntFlag{Name: "before", Usage: "number of chunks before target"},
				&ucli.IntFlag{Name: "after", Usage: "number of chunks after target"},
				&ucli.StringFlag{Name: "wrap", Usage: "wrap output in XML tags"},
				&ucli.BoolFlag{Name: "status", Usage: "show SIZE FILE:LOCATION for all chunks matching patterns"},
				&ucli.BoolFlag{Name: "anchor", Usage: "print the opinionated @ext target (address) for the chunk"},
			},
			Action: flatChunksAction,
		},
		{
			Name: "chats", Usage: "show conversation transcripts from JSONL logs", ArgsUsage: "[PATH...]",
			Flags: []ucli.Flag{
				&ucli.BoolFlag{Name: "with-tools", Usage: "display tool calls and results"},
				&ucli.BoolFlag{Name: "thinking", Usage: "display chain-of-thought (thinking) blocks"},
				&ucli.BoolFlag{Name: "all", Usage: "display everything: tools + thinking + sidechain"},
				&ucli.BoolFlag{Name: "sidechain", Usage: "display sidechain chatter"},
				&ucli.StringFlag{Name: "wrap", Usage: "wrap output with a name tag"},
				&ucli.IntFlag{Name: "line-length", Value: 100, Usage: "word-wrap line length"},
			},
			Action: flatChatsAction,
		},
		{
			Name: "fetch", Usage: "return full contents of an indexed file", ArgsUsage: "PATH",
			Flags:  []ucli.Flag{&ucli.StringFlag{Name: "wrap", Usage: "wrap output in XML tags (e.g. memory, knowledge)"}},
			Action: flatFetchAction,
		},
		{
			Name: "files", Usage: "list indexed files",
			Flags: []ucli.Flag{
				&ucli.BoolFlag{Name: "status", Usage: "show file status, bytes, and chunk count"},
				&ucli.BoolFlag{Name: "detail", Usage: "show per-file chunk size stats (with --status)"},
				&ucli.StringSliceFlag{Name: "filter-files", Usage: "path-based positive filter (repeatable, glob pattern)"},
				&ucli.StringSliceFlag{Name: "exclude-files", Usage: "path-based negative filter (repeatable, glob pattern)"},
			},
			Action: flatFilesAction,
		},
		{
			Name: "init", Usage: "create a new database",
			Flags: []ucli.Flag{
				&ucli.StringFlag{Name: "embed-cmd", Usage: "embedding command (optional, enables vector search)"},
				&ucli.StringFlag{Name: "query-cmd", Usage: "query embedding command (optional)"},
				&ucli.BoolFlag{Name: "case-insensitive", Value: true, Usage: "case-insensitive indexing"},
				&ucli.StringFlag{Name: "aliases", Usage: "byte aliases (from=to,...)"},
				&ucli.BoolFlag{Name: "no-setup", Usage: "skip automatic setup"},
				&ucli.BoolFlag{Name: "if-needed", Usage: "skip if database already exists"},
			},
			Action: flatInitAction,
		},
		{
			Name: "serve", Usage: "start the server",
			Flags: []ucli.Flag{
				&ucli.BoolFlag{Name: "no-scan", Usage: "skip startup reconciliation"},
				&ucli.BoolFlag{Name: "force", Usage: "accept config changes, clear error conditions"},
				&ucli.BoolFlag{Name: "compact", Usage: "compact LMDB via mdb_env_copy2 before opening (overrides ark.toml auto_compact)"},
			},
			Action: flatServeAction,
		},
		{
			Name: "status", Usage: "show database status",
			Flags: []ucli.Flag{
				&ucli.BoolFlag{Name: "db", Usage: "show LMDB record counts by type"},
				&ucli.BoolFlag{Name: "chunks", Usage: "show chunk size statistics"},
				&ucli.BoolFlag{Name: "tokenize", Usage: "measure in tokens (requires tag_model)"},
				&ucli.StringSliceFlag{Name: "filter-files", Usage: "path-based positive filter (repeatable, glob pattern)"},
				&ucli.StringSliceFlag{Name: "exclude-files", Usage: "path-based negative filter (repeatable, glob pattern)"},
			},
			Action: flatStatusAction,
		},
		{
			Name: "stop", Usage: "stop the running server",
			Flags:  []ucli.Flag{&ucli.BoolFlag{Name: "f", Usage: "send SIGKILL instead of SIGTERM"}},
			Action: flatStopAction,
		},
		{Name: "install", Usage: "connect this project to ark (alias for ui install)", Action: flatDelegate(cmdUIInstall)},
	}
}

// Batch-2 actions: declare flags on the node (self-documenting help), then
// reconstruct the argv the kept cmdX body expects (flags first, then the
// positional args), so the index-core bodies stay untouched in main.go.

func flatAddAction(_ context.Context, c *ucli.Command) error {
	var a []string
	if v := c.String("strategy"); v != "" {
		a = append(a, "--strategy", v)
	}
	if v := c.String("content"); v != "" {
		a = append(a, "--content", v)
	}
	if v := c.String("from-file"); v != "" {
		a = append(a, "--from-file", v)
	}
	if c.Bool("append") {
		a = append(a, "--append")
	}
	cmdAdd(append(a, c.Args().Slice()...))
	return nil
}

func flatBundleAction(_ context.Context, c *ucli.Command) error {
	var a []string
	if v := c.String("o"); v != "" {
		a = append(a, "--o", v)
	}
	if v := c.String("src"); v != "" {
		a = append(a, "--src", v)
	}
	cmdBundle(append(a, c.Args().Slice()...))
	return nil
}

func flatChunksAction(_ context.Context, c *ucli.Command) error {
	a := []string{"--before", strconv.Itoa(c.Int("before")), "--after", strconv.Itoa(c.Int("after"))}
	if v := c.String("wrap"); v != "" {
		a = append(a, "--wrap", v)
	}
	if c.Bool("status") {
		a = append(a, "--status")
	}
	if c.Bool("anchor") {
		a = append(a, "--anchor")
	}
	cmdChunks(append(a, c.Args().Slice()...))
	return nil
}

func flatChatsAction(_ context.Context, c *ucli.Command) error {
	a := []string{"--line-length", strconv.Itoa(c.Int("line-length"))}
	if c.Bool("with-tools") {
		a = append(a, "--with-tools")
	}
	if c.Bool("thinking") {
		a = append(a, "--thinking")
	}
	if c.Bool("all") {
		a = append(a, "--all")
	}
	if c.Bool("sidechain") {
		a = append(a, "--sidechain")
	}
	if v := c.String("wrap"); v != "" {
		a = append(a, "--wrap", v)
	}
	cmdChats(append(a, c.Args().Slice()...))
	return nil
}

func flatFetchAction(_ context.Context, c *ucli.Command) error {
	var a []string
	if v := c.String("wrap"); v != "" {
		a = append(a, "--wrap", v)
	}
	cmdFetch(append(a, c.Args().Slice()...))
	return nil
}

func flatFilesAction(_ context.Context, c *ucli.Command) error {
	var a []string
	if c.Bool("status") {
		a = append(a, "--status")
	}
	if c.Bool("detail") {
		a = append(a, "--detail")
	}
	for _, v := range c.StringSlice("filter-files") {
		a = append(a, "--filter-files", v)
	}
	for _, v := range c.StringSlice("exclude-files") {
		a = append(a, "--exclude-files", v)
	}
	cmdFiles(append(a, c.Args().Slice()...))
	return nil
}

func flatInitAction(_ context.Context, c *ucli.Command) error {
	var a []string
	if v := c.String("embed-cmd"); v != "" {
		a = append(a, "--embed-cmd", v)
	}
	if v := c.String("query-cmd"); v != "" {
		a = append(a, "--query-cmd", v)
	}
	// case-insensitive defaults true in cmdInit; only pass when disabled.
	if !c.Bool("case-insensitive") {
		a = append(a, "--case-insensitive=false")
	}
	if v := c.String("aliases"); v != "" {
		a = append(a, "--aliases", v)
	}
	if c.Bool("no-setup") {
		a = append(a, "--no-setup")
	}
	if c.Bool("if-needed") {
		a = append(a, "--if-needed")
	}
	cmdInit(append(a, c.Args().Slice()...))
	return nil
}

func flatServeAction(_ context.Context, c *ucli.Command) error {
	var a []string
	if c.Bool("no-scan") {
		a = append(a, "--no-scan")
	}
	if c.Bool("force") {
		a = append(a, "--force")
	}
	if c.Bool("compact") {
		a = append(a, "--compact")
	}
	cmdServe(append(a, c.Args().Slice()...))
	return nil
}

func flatStatusAction(_ context.Context, c *ucli.Command) error {
	var a []string
	if c.Bool("db") {
		a = append(a, "--db")
	}
	if c.Bool("chunks") {
		a = append(a, "--chunks")
	}
	if c.Bool("tokenize") {
		a = append(a, "--tokenize")
	}
	for _, v := range c.StringSlice("filter-files") {
		a = append(a, "--filter-files", v)
	}
	for _, v := range c.StringSlice("exclude-files") {
		a = append(a, "--exclude-files", v)
	}
	cmdStatus(append(a, c.Args().Slice()...))
	return nil
}

func flatStopAction(_ context.Context, c *ucli.Command) error {
	var a []string
	if c.Bool("f") {
		a = append(a, "--f")
	}
	cmdStop(append(a, c.Args().Slice()...))
	return nil
}
