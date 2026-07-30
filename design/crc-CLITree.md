# CLITree
**Requirements:** R2916, R2917, R2918, R2919, R2920, R2921, R2922, R2923, R2924, R2925, R2926, R2927, R2928, R2929, R2931, R2932, R2953, R2956, R2957, R2960, R3010, R3021, R3022, R3027, R3029, R3033, R3037, R3038, R3040, R3046, R3048, R3056, R3077, R3084, R3085, R3086, R3087, R3088, R3089, R3094, R3105, R3129, R3133, R3191, R3204, R3205, R3206, R3246, R3256, R3258, R3270, R3312, R3313, R3314, R3315, R3316, R3317, R3318, R3319, R3320

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
  node, the global flags (`--dir`/`-v`/`--force-session`), the `Before` hook, the unknown-
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
- Client session identity (R3313, R3314, R3315, R3316, R3317, R3320):
  **identity is a declared property of a node, not a flag.** A node that
  acts as the calling session is wrapped in `needsClientSession(...)`,
  which records the need in the node's `Metadata` and installs the `Before`
  hook that resolves the value before the node's `Action` runs; the body
  reads it back with `clientSession(c)`. Declaring nodes: `bloodhound
  listen`, `connections recall listen`, `luhmann next`, `luhmann events`,
  `connections recall next`.
  - Resolution order is `--force-session` when given, else the live
    `$CLAUDE_CODE_SESSION_ID`. **Neither resolves → the node errors**,
    naming `--force-session`, rather than passing an empty value into a
    body whose own required-flag message would name a `--session` flag
    that no longer exists (R3315).
  - `--force-session` is a **global** flag beside `--dir` (R3316),
    consulted only by declaring nodes. Global because the need is declared
    per node: a per-node flag would have to be added by hand to each one,
    reproducing the enumeration this replaces.
  - The resolved value reaches the server as its own wire field,
    `client_session` (R3317), distinct from any verb's own session-shaped
    parameter. That is what ends the overload rather than relocating it:
    one word stops meaning both "who am I" and "which queue" depending on
    which verb is being read.
  - The environment beats anything typed, because a typed value can only
    have arrived as text and `--fork-session --resume` replays text
    rendered before a session was re-hosted — skills render
    `${CLAUDE_SESSION_ID}` at *load* time, a snapshot, whereas the live
    per-process `$CLAUDE_CODE_SESSION_ID` is read where the command runs
    (different mechanisms; the former is not an env var). A stale id fails
    **silently**: the watcher keys sessions off the JSONL *filename*, so it
    publishes for the live id while a consumer subscribed under the stale
    one blocks forever on a channel nobody writes to — indistinguishable
    from a corpus with nothing to say.
  - Why the declaration and not the old resolver (R3309–R3311, retired
    T275–T277): state A kept a `--session` flag and preferred the
    environment over it, which made the flag **decorative** (with the
    variable set, no value of the flag could change the outcome) and
    silently removed deliberate cross-session use. It also applied the rule
    body by body, so the failure mode was an omission at one call site —
    and `luhmann events` was in fact missed, despite R3145 routing events
    onto the *caller's* own tube. `--force-session` is the honest form of
    the override, and the node declaration is what makes coverage
    structural (R3320).
- `connections recall next --tube` (R3318, R3319): the flag formerly spelled
  `--session`, renamed with its wire parameter (`session=` → `tube=`)
  because the value is the pool composite `<luhmann-session>-<nonce>`
  (R3032) — deliberately no process's own id, so it answers "which tube?"
  and never "who am I?". R3311 protected that distinction with an exclusion
  list and a call-site guard; naming the flag for what it holds makes the
  protection structural. The node **also** declares a session need, so it
  sends `client_session` beside `tube` — which lets the server re-register
  `nonce → parent` on every loop iteration and makes R3305's in-memory
  registry self-healing across a bounce (R3319; the registering half lives
  in crc-RecallAgentBuilder.md).
- **Complement (R3312): the skills stop capturing the id.** No
  `sessionid=${CLAUDE_SESSION_ID}` header and no identity argument typed
  from one, in `/bloodhound`, `/recall`, or `/luhmann` — the three whose
  captured value reached a session-scoped verb. `/ark` and `/diag` are
  deliberately **out of scope** (their captured id only suffixes a filename
  or prints a diagnostic line, so a stale value misnames a file rather than
  silently disabling recall) and keep the header pending gap O181. The
  declaration removes the need for the capture, and the capture is what
  made the staleness reachable — so the two halves land together or the
  hazard survives in the skill text. Where a spawner still needs a value
  the CLI will not resolve for it (`recall next`'s `--tube`), the skill
  directs it to read the id live with `printenv` at spawn time rather than
  from earlier text.
- Global flags (R2923): `&cli.StringFlag{Name: "dir"}` (default `~/.ark/`)
  and `&cli.BoolFlag{Name: "v", Config: cli.BoolConfig{Count: &verbosity}}`
  for repeated-`-v` counting sit on the root; a root `Before` hook copies
  the parsed values into the `arkDir` / verbosity package globals before
  any `Action` runs. `cli.ExpandVerbosityFlags` pre-tokenizes a bundled
  `-vvvv` into `-v -v -v -v` (urfave does not bundle short flags), so
  `--dir`/`-v` are recognized before the subcommand runs.
  `--force-session` joins them (R3316) but is **not** copied into a package
  global: it is read from the command context by the declaring node's
  `Before`, so a node that never declares a session need cannot consume it.
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
  line per call, `--done` writes the result doc + flips its tag, R3027). A new `next
  [--first|--force] [--keepalive N]` node joins the existing `luhmann`
  group in `cmd/ark/monitoring_cli.go` (R3010) — its seat identity is
  declared rather than flagged (R3313), so it carries no `--session`. Each self-documents from
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
  The `amend` leaf takes `<tvid> <tag> [value]` plus `--insight`,
  `--disposition`/`--internal`, `--replace`, and `--target`, dispatching to
  `DB.AmendExtCandidate` (POST `/ext/amend`). Its flag reads go through
  `c.IsSet` before `c.String`/`c.Bool`, because an unset flag must reach
  `ExtAmendOpts` as nil (carry the old line's value forward) and `c.Bool`
  alone cannot tell an unset `--replace` from `--replace=false`. (R3256)
- `ext pending` leaf (R3258): `[--chunk <target>] [--limit N] [--json]`,
  the group's only **read** verb, so it breaks its siblings' hand-rolled
  proxy-or-**exclusive** shape and takes the standard `proxyOrLocal`
  dispatch instead — proxy to GET `/ext/pending` when a server is up,
  ordinary local open when none is, with the Stubborn Plumbing retry.
  (There is no read-only index open to take: bbolt is single-process and
  `ark.Open` locks the file either way.) `--chunk` takes a TARGET spec
  (not a chunkid), so the CLI addresses chunks the way every other `ark
  ext` verb does; it is resolved through `DB.ResolveExtTarget` **wherever
  the index lives** — server-side via `?target=` when a server holds the
  lock, locally otherwise — and a TARGET resolving to nothing is an error,
  never a silent widening into the corpus-wide scan. Renders
  `Store.PendingCandidates`'s rows as a baby-food markdown table (em dash
  for an unattached row's chunks), or JSON under `--json`; the CLI does no
  sorting or truncation of its own, since the ranking and limit are the
  query's (R3253).

## Collaborators
- CLI (crc-CLI.md): owns the command bodies the `Action`s call; CLITree
  routes to them and is where their former flag-parsing prologue moves to.
- ui-engine/cli: provides `ExpandVerbosityFlags` (short-flag bundling) and
  `SetVerbosity`; retained, hence the urfave alias collision.

## Sequences
- seq-cli-urfave.md
- seq-bloodhound-cli.md
