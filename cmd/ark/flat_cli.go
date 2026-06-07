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
	}
}
