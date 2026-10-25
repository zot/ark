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
	"github.com/zot/ark"
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
		{
			Name:  "setup",
			Usage: "install ark (global), or --local to connect the current directory",
			Description: "Global install into ~/.ark and ~/.claude, idempotent on the\n" +
				"   database marker. --local reads the CURRENT DIRECTORY and links\n" +
				"   ark's skills and agents into ./.claude/, requiring a prior `ark setup`.",
			Flags:  []ucli.Flag{&ucli.BoolFlag{Name: "local", Usage: "connect the current directory to an existing install"}},
			Action: flatSetupAction,
		},
		{
			Name:  "wsl-init",
			Usage: "print a sh script that sets ark up inside WSL2 (eval it there)",
			Description: "Run inside WSL, with the Windows install's ark.exe:\n" +
				"     eval \"$(/mnt/c/Users/<you>/.ark/ark.exe wsl-init)\"\n" +
				"   The script makes ~/.ark/ark a shim that runs ark.exe, links the rest\n" +
				"   of ~/.ark and ark's ~/.claude skills and agents to the Windows install,\n" +
				"   and never overwrites a real file. wsl-init itself creates nothing.\n" +
				"   Windows builds only.",
			Action: flatDelegate(cmdWSLInit), // CRC: crc-WSLBridge.md | R3507
		},
		{Name: "rebuild", Usage: "drop and rebuild the entire index", Action: flatDelegate(cmdRebuild)},
		{Name: "scan", Usage: "walk source directories, index new files", Action: flatDelegate(cmdScan)},
		{
			// R3568, R3569: --force is declared on the node; flatDelegate passes only
			// positionals, so a flag parsed inside the handler would never be reached.
			// CRC: crc-CLI.md | R3568, R3569
			Name:      "refresh",
			Usage:     "re-index stale files (--force: re-chunk matching files even if unchanged)",
			ArgsUsage: "[PATTERN...]",
			Flags: []ucli.Flag{
				&ucli.BoolFlag{Name: "force", Usage: "re-chunk every matching file in place, stale or not, keeping unchanged chunks' embeddings; requires a pattern"},
			},
			Action: func(_ context.Context, c *ucli.Command) error {
				cmdRefreshForce(c.Args().Slice(), c.Bool("force"))
				return nil
			},
		},
		{Name: "remove", Usage: "remove files from the index", ArgsUsage: "<path|pattern>...", Action: flatDelegate(cmdRemove)},
		{Name: "resolve", Usage: "dismiss unresolved files by pattern", ArgsUsage: "<pattern>...", Action: flatDelegate(cmdResolve)},
		{Name: "dismiss", Usage: "dismiss missing files", ArgsUsage: "<path|pattern>...", Action: flatDelegate(cmdDismiss)},
		{Name: "stale", Usage: "list stale files", ArgsUsage: "[PATTERN...]", Action: flatDelegate(cmdStale)},
		{Name: "missing", Usage: "list missing files", ArgsUsage: "[PATTERN...]", Action: flatDelegate(cmdMissing)},
		// `unresolved` declares its flag on the node, not in the legacy
		// FlagSet: flatDelegate passes only positionals, so a flag parsed
		// inside the handler is never reached and urfave rejects it as
		// undefined. ArgsUsage carries no PATTERN because the command has
		// never filtered by one — `stale` and `missing` do, this does not.
		// CRC: crc-CLITree.md, crc-CLI.md | R3366
		{
			Name:  "unresolved",
			Usage: "list unresolved files, with why each was not indexed",
			Flags: []ucli.Flag{
				&ucli.StringFlag{Name: "reason", Usage: "show only this reason: uncovered, unindexable, or symlink"},
			},
			Action: func(_ context.Context, c *ucli.Command) error {
				cmdUnresolved(c.String("reason"))
				return nil
			},
		},
		{
			Name:   "sources",
			Usage:  "manage source directories",
			Action: flatDelegate(cmdSources), // default: `sources` alone → check
			Commands: []*ucli.Command{
				{Name: "check", Usage: "expand globs, add new directories, report orphans", Action: flatDelegate(cmdSources)},
				{
					// R3290: the vendor path-mangling heuristic runs once here,
					// never in the lookup path (R3289).
					Name:  "pair",
					Usage: "propose logs_for for unpaired sources (pairs a chat source with the project it logs)",
					Flags: []ucli.Flag{
						&ucli.BoolFlag{Name: "auto", Usage: "write the proposed pairings into ark.toml"},
						&ucli.BoolFlag{Name: "dry-run", Usage: "print the proposals and write nothing"},
					},
					Action: flatSourcesPairAction,
				},
			},
		},
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
			Name: "chats", Usage: "show conversation transcripts from JSONL logs", ArgsUsage: "PATH-OR-GLOB...",
			Flags: []ucli.Flag{
				&ucli.BoolFlag{Name: "with-tools", Usage: "display tool calls and results"},
				&ucli.BoolFlag{Name: "thinking", Usage: "display chain-of-thought (thinking) blocks"},
				&ucli.BoolFlag{Name: "all", Usage: "display everything: tools + thinking + sidechain"},
				&ucli.BoolFlag{Name: "sidechain", Usage: "display sidechain chatter"},
				&ucli.StringFlag{Name: "wrap", Usage: "wrap output with a name tag"},
				&ucli.IntFlag{Name: "line-length", Value: ark.DefaultChatLineLength, Usage: "word-wrap line length"},
			},
			Commands: []*ucli.Command{
				{
					// R3284: the question that precedes rendering one.
					Name:  "list",
					Usage: "list indexed conversations, newest first, labelled by their first user turn",
					Flags: []ucli.Flag{
						&ucli.StringFlag{Name: "source", Usage: "restrict to one chat source (name or directory)"},
						&ucli.StringFlag{Name: "for", Usage: "restrict to chat sources logging this source (see: ark sources pair)"},
						&ucli.IntFlag{Name: "limit", Usage: "maximum rows, newest first (default 20)"},
						&ucli.BoolFlag{Name: "json", Usage: "emit one JSON object per line"},
					},
					Action: flatChatsListAction,
				},
			},
			Action: flatChatsAction,
		},
		{
			Name: "fetch", Usage: "return full contents of an indexed file", ArgsUsage: "PATH",
			Flags:  []ucli.Flag{&ucli.StringFlag{Name: "wrap", Usage: "wrap output in XML tags (e.g. memory, knowledge)"}},
			Action: flatFetchAction,
		},
		// CRC: crc-CLITree.md | R3379
		// emitsAddresses: `files` answers with paths *as addresses*, so it
		// consults the link-view selector. The wrap is the declaration —
		// content-emitting nodes must not carry it (R3377).
		emitsAddresses(&ucli.Command{
			// R3204: the -files stack is order-sensitive, so this node cannot
			// re-serialize its flags into a legacy argv the way its siblings
			// do — order and polarity would be lost in the rebuild. Raw args
			// go straight to cmdFiles, which runs parsePathFilterStack.
			Name: "files", Usage: "list indexed files",
			ArgsUsage:       "[-files GLOB]... [--status] [--detail] [PATTERN...]",
			SkipFlagParsing: true,
			Description: "List indexed files. Trailing PATTERNs narrow the result and are\n" +
				"anchored just like -files rows.\n\n" +
				"   --status   show file status, bytes, and chunk count\n" +
				"   --detail   show per-file chunk size stats (with --status)\n\n" +
				filterStackHelp,
			Action: filterStackAction(cmdFiles),
		}),
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
			// R3204: same as `files` — the stack cannot survive flag
			// re-serialization, so raw args reach cmdStatus directly.
			Name: "status", Usage: "show database status",
			ArgsUsage:       "[-files GLOB]... [--db] [--chunks] [--tokenize] [--heap FILE] [--cpu FILE [--seconds N]]",
			SkipFlagParsing: true,
			Description: "Show database status. With --chunks, show chunk size statistics.\n\n" +
				"   --db         show record counts by type\n" +
				"   --chunks     show chunk size statistics\n" +
				"   --tokenize   measure in tokens (requires an [embedding] model)\n" +
				"   --heap FILE  write the running server's Go heap profile to FILE (go tool pprof)\n" +
				"   --cpu FILE   profile the running server's CPU for --seconds (default 10) into FILE\n\n" +
				filterStackHelp,
			Action: filterStackAction(cmdStatus),
		},
		{
			Name: "stop", Usage: "stop the running server",
			Flags:  []ucli.Flag{&ucli.BoolFlag{Name: "f", Usage: "force-kill (ungraceful; TerminateProcess on Windows) instead of a graceful shutdown"}},
			Action: flatStopAction,
		},
		{Name: "install", Usage: "connect this project to ark (alias for ui install)", Action: flatDelegate(cmdUIInstall)},
		{
			Name:  "update",
			Usage: "refresh this install to the latest GitHub release",
			Description: "Version-checked, two-phase, manifest-aware, safe on failure.\n" +
				"   Gripes on a symlinked ~/.ark/ark. --no-exe refreshes skills,\n" +
				"   agents, apps, and the model from the running binary's own bundle\n" +
				"   without touching the exe. --check reports the latest version;\n" +
				"   --stage downloads it as ark.new for the next serve to take.",
			Flags: []ucli.Flag{
				&ucli.BoolFlag{Name: "no-exe", Usage: "refresh assets from the running bundle without touching the exe"},
				&ucli.BoolFlag{Name: "check", Usage: "print the latest release version and whether it is newer; changes nothing"},
				&ucli.BoolFlag{Name: "stage", Usage: "download the latest release as ~/.ark/ark.new for the next serve, without stopping a server"},
			},
			Action: flatUpdateAction,
		},
	}
}

// CRC: crc-CLI.md | Seq: seq-install.md#1, seq-install.md#2 | R3466, R3467
func flatSetupAction(_ context.Context, c *ucli.Command) error {
	var a []string
	if c.Bool("local") {
		a = append(a, "--local")
	}
	cmdSetup(a)
	return nil
}

// CRC: crc-CLI.md | Seq: seq-install.md#3 | R3478
func flatUpdateAction(_ context.Context, c *ucli.Command) error {
	var a []string
	for _, f := range []string{"no-exe", "check", "stage"} {
		if c.Bool(f) {
			a = append(a, "--"+f)
		}
	}
	cmdUpdate(a)
	return nil
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

// CRC: crc-CLI.md | R3284, R3287
func flatChatsListAction(_ context.Context, c *ucli.Command) error {
	var a []string
	if v := c.String("source"); v != "" {
		a = append(a, "--source", v)
	}
	if v := c.String("for"); v != "" {
		a = append(a, "--for", v)
	}
	if v := c.Int("limit"); v > 0 {
		a = append(a, "--limit", strconv.Itoa(int(v)))
	}
	if c.Bool("json") {
		a = append(a, "--json")
	}
	cmdChatsList(a)
	return nil
}

// CRC: crc-CLI.md | R3290
func flatSourcesPairAction(_ context.Context, c *ucli.Command) error {
	var a []string
	if c.Bool("auto") {
		a = append(a, "--auto")
	}
	if c.Bool("dry-run") {
		a = append(a, "--dry-run")
	}
	cmdSourcesPair(a)
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

func flatStopAction(_ context.Context, c *ucli.Command) error {
	var a []string
	if c.Bool("f") {
		a = append(a, "--f")
	}
	cmdStop(append(a, c.Args().Slice()...))
	return nil
}
