# CLITree
**Requirements:** R2916, R2917, R2918, R2919, R2920, R2921, R2922, R2923, R2924, R2925, R2926, R2927, R2928, R2929, R2930, R2931, R2932

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
- root: `*cli.Command` named `ark` — the tree root carrying the migrated
  command nodes and the error/exit hook (`ExitErrHandler`); in the end
  state it also carries the global flags. The legacy catch-all lives in
  `main()`, not on the root (R2916).
- migrated: the set of command names handled by the urfave tree; `main()`
  consults it to route un-migrated names to `legacyDispatch` (R2930).
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
- BuildRoot(): construct the `ark` root `*cli.Command` — the migrated
  command nodes and `ExitErrHandler` (plus global flags + `Before` in the
  end state). `main()` runs it via `root.Run(ctx, args)` for a migrated
  command, else calls `legacyDispatch` (R2916, R2930).
- Self-documenting help (R2917, R2918, R2919, R2920): help is generated
  from the tree. Each node's `--help`/`-h` is produced from its own
  `Name` + `Usage` + flags' `Usage`; it resolves at every node (root,
  group, leaf) showing the full command path; a parent's help auto-lists
  its children's synopses without restating them; flag help is derived
  from the flag declarations, so adding/renaming a flag needs no second
  edit. Retires `usage()`, `printConnectionsHelp`, `uiUsage`,
  `printConfigHelp`, and the `luhmann`/`schedule` usage blocks (R2931).
- Global flags, two-phase (R2923). End state: `&cli.StringFlag{Name:
  "dir"}` (default `~/.ark/`) and `&cli.BoolFlag{Name: "v", Config:
  cli.BoolConfig{Count: &verbosity}}` for repeated-`-v` counting; a root
  `Before` hook copies the parsed values into the `arkDir` / verbosity
  package globals before any `Action` runs. During the staged transition,
  `main()` instead keeps the existing pre-parse (the `--dir`/`-v` strip
  loop) so global handling is byte-identical to state A, and the root
  declares **no** global flags (they are already consumed). Either way
  `cli.ExpandVerbosityFlags` pre-tokenizes a bundled `-vvvv` into `-v -v -v
  -v` (urfave does not bundle short flags), and `--dir`/`-v` are recognized
  before the subcommand runs.
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
- Legacy catch-all (R2930): during the staged migration `main()` checks
  the command name against a `migrated` set; an un-migrated name routes to
  the existing hand-rolled `switch` (extracted into a `legacyDispatch`
  function) **before urfave parses it**, so an un-migrated command's own
  flags never trip the urfave root. (urfave's `CommandNotFound` hook was
  rejected — it fires only for flagless unknown commands; `ark stop -f`
  trips root flag parsing first. Spike-verified.) Cases move from
  `legacyDispatch` into the tree group by group; when it is empty, the
  main-level catch-all and `legacyDispatch` are deleted.
- Flag surface frozen (R2932): the migration adds, removes, renames no
  flag and switches none to GNU-only `--long`; it only re-homes existing
  flags onto their nodes.

## Collaborators
- CLI (crc-CLI.md): owns the command bodies the `Action`s call; CLITree
  routes to them and is where their former flag-parsing prologue moves to.
- ui-engine/cli: provides `ExpandVerbosityFlags` (short-flag bundling) and
  `SetVerbosity`; retained, hence the urfave alias collision.

## Sequences
- seq-cli-urfave.md
