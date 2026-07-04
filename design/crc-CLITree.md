# CLITree
**Requirements:** R2916, R2917, R2918, R2919, R2920, R2921, R2922, R2923, R2924, R2925, R2926, R2927, R2928, R2929, R2931, R2932, R2953, R2956, R2957, R2960, R3010, R3021, R3022, R3027, R3029

The `urfave/cli` v3 command-tree builder and router. Assembles ark's
commands as a `*cli.Command` tree whose `--help` is generated from the
declarations, routes each invocation to the existing handler body via an
`Action`, and owns the cross-cutting CLI conventions (global flags, exit
codes, error format) that state A scattered across `main()`, `usage()`,
and the per-command help printers. Distinct from crc-CLI.md, which keeps
owning the **command bodies** — the logic, server interaction, and output;
CLITree owns how those bodies are *reached* and how their help is
*produced*.

## Knows
- root: `*cli.Command` named `ark` — the tree root carrying every command
  node, the global flags (`--dir`/`-v`), the `Before` hook, the unknown-
  command/bare-invocation `Action`, and the error/exit hook
  (`ExitErrHandler`). `main()` builds it and runs every invocation through
  it (R2916).
- node shape: each command/subcommand is a `*cli.Command` with `Name`, a
  one-line `Usage` (synopsis), and `Flags` (each a typed `cli.*Flag` with
  its own `Usage`). Children nest in a node's `Commands` slice (R2916,
  R2917, R2919).
- ucli: the urfave import alias — `github.com/urfave/cli/v3` aliased
  distinctly because `cli` already names `github.com/zot/ui-engine/cli`
  (R2916).
- verbosity, arkDir: the package globals the root `Before` hook sets from
  the parsed global flags, so handler bodies keep reading them unchanged
  (R2923, R2928).

## Does
- BuildRoot(): construct the `ark` root `*cli.Command` — every command
  node, the global flags + `Before` hook, the unknown-command `Action`,
  and `ExitErrHandler`. `main()` runs every invocation via
  `root.Run(ctx, ["ark"]+args)` (R2916).
- Self-documenting help (R2917, R2918, R2919, R2920): help is generated
  from the tree. Each node's `--help`/`-h` is produced from its own
  `Name` + `Usage` + flags' `Usage`; it resolves at every node (root,
  group, leaf) showing the full command path; a parent's help auto-lists
  its children's synopses without restating them; flag help is derived
  from the flag declarations, so adding/renaming a flag needs no second
  edit. Retires `usage()`, `printConnectionsHelp`, `uiUsage`,
  `printConfigHelp`, and the `luhmann`/`schedule` usage blocks (R2931).
- Global flags (R2923): `&cli.StringFlag{Name: "dir"}` (default `~/.ark/`)
  and `&cli.BoolFlag{Name: "v", Config: cli.BoolConfig{Count: &verbosity}}`
  for repeated-`-v` counting sit on the root; a root `Before` hook copies
  the parsed values into the `arkDir` / verbosity package globals before
  any `Action` runs. `cli.ExpandVerbosityFlags` pre-tokenizes a bundled
  `-vvvv` into `-v -v -v -v` (urfave does not bundle short flags), so
  `--dir`/`-v` are recognized before the subcommand runs.
- Single-dash-long preserved (R2922): urfave/cli rides stdlib `flag`, so
  every node's flags accept one or two dashes (`-scores` ≡ `--scores`)
  with no per-flag work — the property is inherited.
- Action wrapping (R2928): each migrated node's `Action` reads flag values
  from the command context (`c.Bool`/`c.String`/`c.Int`/`c.StringSlice`/
  `c.Count`) and positional args from `c.Args().Slice()`, then calls the
  existing handler logic. The handler's former internal
  `flag.NewFlagSet`/`fs.Parse` prologue is removed (the flags now live on
  the node); the body — `tmp://` handling, `serverClient`/`withDB`/
  `requireServer`, server-first proxy-or-cold-start, output formatting —
  is unchanged.
- DSL routing (R2921, R2924, R2925): the `search` node (and any sibling
  sharing the filter-stack flags) sets `SkipFlagParsing: true`, so its
  `Action` receives **all** raw args and hands them to the unchanged
  `parseFilterStack` + the handler's own `flag.Parse` for non-DSL flags —
  preserving order-sensitive sticky `-with`/`-without` polarity, repeated
  `(polarity, mode, query)` tuples, and `search -parse` (print
  disambiguated command, exit without searching). Such a node documents
  itself through a single hand-written `Description` (single-source, not
  auto-derived).
- Exit codes (R2926): handler bodies keep calling `fatal()` (which prints
  and `os.Exit(1)`) and `os.Exit(2)` directly, so meaningful codes survive
  unchanged — `connections recall next` `2`=done/`0`=doc; removed
  connections flags → hint + exit `2`. The bodies exit the process
  themselves, so urfave never regains control on those paths.
- Error format (R2927): the root `ExitErrHandler` catches errors urfave
  itself raises (flag-parse failure, unknown flag), renders them as
  `error: <msg>` on stderr (the `fatal()` shape), and calls `os.Exit` with
  the error's `ExitCoder` code (else `1`) — it must exit itself, or the
  code is lost to `1` and the message double-prints (spike-verified).
  urfave's own "Incorrect Usage:" + help dump on a flag error can be
  quieted via `OnUsageError` if the terse ark format is wanted (cosmetic).
- Aliases / shims (R2929): sibling alias subcommands (`message set-tags` ≈
  `tag set`, `get-tags` ≈ `tag get`, `check` ≈ `tag check`) are nodes
  whose `Action` calls the shared underlying logic, or use urfave
  `Aliases`. The legacy connections flag shims (`--wait`/`--fetch`/
  `--result`/`--error` → hint + exit 2, R2615) are preserved by the
  connections node (hidden flags detected in its `Action`, or a pre-check).
- Unknown command / bare invocation (R2916): a first positional that
  matches no command node falls through to the root `Action`, which prints
  `unknown command: <name>` on stderr and exits 1, or — with no args —
  shows the generated root help (exit 0). (The staged migration's
  transitional `legacyDispatch` name-routing — formerly R2930, retired —
  is gone now that every command is a tree node. urfave's `CommandNotFound`
  hook was rejected for that routing because it fires only for flagless
  unknown commands; the root `Action` covers all cases.)
- Flag surface frozen (R2932): the migration adds, removes, renames no
  flag and switches none to GNU-only `--long`; it only re-homes existing
  flags onto their nodes.
- Bloodhound + `luhmann next` nodes (R3010, R3021, R3022, R3027, R3029):
  post-migration growth added as tree nodes like any other. A new `ark
  bloodhound` group in `cmd/ark/bloodhound_cli.go` carries `search
  TERMS... [--wait] [--timeout S]` (create the request doc + subscribe +
  block + print JSONL, R3021/R3022/R3029) and `add --result --id --path
  --chunk` (Luhmann's result stencil, R3027). A new `next --session S
  [--first|--force] [--keepalive N]` node joins the existing `luhmann`
  group in `cmd/ark/monitoring_cli.go` (R3010). Each self-documents from
  its declarations; the `search` and `next` blocking bodies own their
  stubborn-plumbing redial (crc-CLI.md) — CLITree only routes and generates
  their help.

## Collaborators
- CLI (crc-CLI.md): owns the command bodies the `Action`s call; CLITree
  routes to them and is where their former flag-parsing prologue moves to.
- ui-engine/cli: provides `ExpandVerbosityFlags` (short-flag bundling) and
  `SetVerbosity`; retained, hence the urfave alias collision.

## Sequences
- seq-cli-urfave.md
- seq-bloodhound-cli.md
