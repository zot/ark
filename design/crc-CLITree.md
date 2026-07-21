# CLITree
**Requirements:** R2916, R2917, R2918, R2919, R2920, R2921, R2922, R2923, R2924, R2925, R2926, R2927, R2928, R2929, R2931, R2932, R2953, R2956, R2957, R2960, R3010, R3021, R3022, R3027, R3029, R3033, R3037, R3038, R3040, R3046, R3048, R3056, R3077, R3084, R3085, R3086, R3087, R3088, R3089, R3094, R3105, R3129, R3133, R3191, R3204, R3205, R3206

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
- Filter-stack nodes (R3204, R3205, R3206): `files`, `status`, `tag files`,
  `tag values`, and `subscribe` join `search` as `SkipFlagParsing: true`
  nodes, because the stack is order-sensitive (sticky `-with`/`-without`
  polarity, repeated `(polarity, mode, query)` tuples) and urfave cannot
  express it. Each hands its raw args to `parsePathFilterStack` + its own
  `flag.Parse` for the non-DSL flags, and documents itself through one
  hand-written `Description` as `search` does — all six sharing the single
  `filterStackHelp` constant, so the stack's help cannot drift between
  commands. This also **deletes** `flatFilesAction` and `flatStatusAction`
  from the `flat_cli.go` re-serialization path, which rebuilt a legacy
  `--filter-files V` argv for the handler body: a node whose flags are
  re-serialized *cannot* carry the stack, since order and polarity are lost
  in the rebuild. `files` and `status` become `filterStackAction`
  pass-throughs.
- **A `SkipFlagParsing` node must answer its own help (R2917, R2918).** Such a
  node's `Action` receives `--help` like any other token, so left alone the
  args reach the handler's `flag.Parse` and the command grows *two* help
  texts — `ark help X` rendering the node's `Description`, `ark X --help`
  printing the flag package's bare listing. `helpRequested(args)` intercepts
  first and returns `ucli.ShowSubcommandHelp(c)`, so the `Description` stays
  the single source. `filterStackAction` bundles that check with the raw-args
  hand-off for the `flat_cli.go` nodes, whose handlers have no `*ucli.Command`
  of their own. (`search` predates this and still prints a hand-written
  `fs.Usage` — the same two-source shape, now the only one left; noted as
  O162 rather than changed, since its help is long-standing and hand-tuned.) The `--filter-files` / `--exclude-files` pointing errors
  (R3205) are detected in the body alongside the stack parse, the way the
  connections legacy shims are (R2615): message + non-zero exit, no alias.
  The `tag values` boolean is declared `--show-files` (R3206) — `--files`
  would be normalized into a filter row by `parseFilterStack` before
  `flag.Parse` ever saw it, exactly the collision `search` resolved by
  renaming its boolean to `--file-content`. Note this is the first
  deliberate exception to the flag-surface freeze (R2932), which scoped the
  *migration*; #51 is later growth, like the bloodhound nodes.
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
- Tag readers (R3084-R3089): the `tag` group's `chunk` node lists tags at a
  file or chunk address — bare `FILE` delegates to `tag get`'s file-block
  reader (R3085); `FILE -all` calls DB.AllTagsForFilePath (R3086); a
  `FILE:TARGET` chunk address (parsed by resolveChunksTarget) calls
  DB.AllTagsAtLocation (R3087). A `-all` bool flag on the `tag get` node
  routes to the same file-wide union with the trailing `[TAG ...]` filter
  composed in (R3088). Both index-backed forms dispatch through proxyOrLocal
  — the server `/tags/chunk` endpoint when one is running, else a cold DB
  (R3089).
- Flag surface frozen (R2932): the migration adds, removes, renames no
  flag and switches none to GNU-only `--long`; it only re-homes existing
  flags onto their nodes.
- Bloodhound + `luhmann next` nodes (R3010, R3021, R3022, R3027, R3029):
  post-migration growth added as tree nodes like any other. A new `ark
  bloodhound` group in `cmd/ark/bloodhound_cli.go` carries `search
  [CLUE...] [--file PATH|-] [--filter-files G]... [--exclude-files G]...
  [--wait] [--timeout S] [--raw] [--markdown]` (create the
  request doc + subscribe + block + print, R3021/R3022/R3029) and `add --result --loc --note
  [--chunk] | --result --done` (Luhmann's result stencil — one curated JSON
  line per call, `--done` writes the result doc + flips its tag, R3027). A new `next --session S
  [--first|--force] [--keepalive N]` node joins the existing `luhmann`
  group in `cmd/ark/monitoring_cli.go` (R3010). Each self-documents from
  its declarations; the `search` and `next` blocking bodies own their
  stubborn-plumbing redial (crc-CLI.md) — CLITree only routes and generates
  their help.
- Hunt scope (R3191, R3193): `bloodhound search`'s repeatable
  `--filter-files` / `--exclude-files` carry `search -files` semantics, so
  the body anchors unanchored globs to the **client's** cwd via the shared
  `ark.AnchorGlobToDir` before submitting — the same CLI-side move
  `search` makes for `-files` (R2958), and for the same reason: only the
  client knows its own working directory. What reaches the request doc is
  always absolute, so no server-side component ever infers a cwd.
- Bloodhound search **output modes** (R3037, R3038, R3040): `search` picks
  its output client-side from the flag it sent — the server returns the
  result-doc body either way (crc-Server.md), the CLI just formats it. Default
  prints the curated JSONL verbatim; `--markdown` unmarshals that JSONL into a
  local `{path,range,note,chunk}` mirror (the wire shape, not an imported type —
  the CLI stays a thin client) and renders a locator list `- \`path:range\` —
  note` with the `chunk` as a blockquote, "no findings" when empty (R3037);
  `--raw` sets `curate: false` in the submitted payload so the watcher relays the
  secretary's *uncurated* findings (already markdown) which the CLI prints
  verbatim — redundant with `--markdown` (R3038, R3040). Pure formatting +
  one payload marker; no new server route, no protocol change.
- Bloodhound search **clue input** (R3046): the **clue** is the searchable
  content, taken from positional `CLUE...` (joined into one line) or `--file PATH`
  (read the clue from a file); `--file -` reads stdin — the **heredoc** path for a
  multi-paragraph markdown clue (`argv` can't carry paragraph breaks). `CLUE...`
  and `--file` are mutually exclusive (error if both). The action builds the
  request payload **metadata-first** — `scope`/`depth`/`want` (and `curate: false`
  for `--raw`) as leading `key: value` lines, then the clue body last — so the
  watcher's `clueOf` (crc-RecallWatcher) strips the metadata and splits only the
  clue for the per-paragraph seed (R3043). The file is read byte-for-byte (fidelity
  by construction, as with the messenger's `--content-file`).
- `ext` node (R3048): a new `ark ext` group in `cmd/ark/ext_cli.go` with
  `set`/`add`/`remove` leaves, each taking `<target> <tag>` positionals plus a
  `<value>` (required for set/add, optional for remove). The `Action`s follow the
  `config` add/remove dispatch: proxy to the running server (POST `/ext/set`,
  `/ext/add`, `/ext/remove` — crc-Server.md) when `serverClient` connects, else
  `withExclusiveDB` calling `DB.SetExtTag` / `DB.AddExtTag` / `DB.RemoveExtTag`.
  Mirror-file-only scope lives in the DB primitive (crc-DB.md); CLITree only routes.
  The staging leaves `candidate`/`accept`/`reject` (candidate also takes an
  `--insight "why"` flag and a `--disposition internal|external` / `--internal`
  choice, default external — R3094) dispatch the same way to `DB.CandidateExtTag` /
  `AcceptExtTag` / `RejectExtTag` (POST `/ext/{candidate,accept,reject}` —
  crc-Server.md); `value` is optional for all three. (R3056)

## Collaborators
- CLI (crc-CLI.md): owns the command bodies the `Action`s call; CLITree
  routes to them and is where their former flag-parsing prologue moves to.
- ui-engine/cli: provides `ExpandVerbosityFlags` (short-flag bundling) and
  `SetVerbosity`; retained, hence the urfave alias collision.

## Sequences
- seq-cli-urfave.md
- seq-bloodhound-cli.md
