# CLI
**Requirements:** R29, R71-88, R108-110, R131, R139-147, R159, R161, R165, R166, R169, R170, R178-183, R185, R189, R192, R196-199, R201, R230, R232-234, R256, R259, R260, R273-293, R295, R297-302, R304-318, R326-337, R370, R371, R396-402, R429-437, R442, R450-458, R462, R464, R466-471, R477, R479-497, R499-501, R506-510, R512-516, R525-540, R572, R573, R579-585, R590-592, R594, R599, R605-616, R639, R654-656, R669-681, R692-696, R708-713, R715, R718, R722, R724-734, R738-743, R748, R849-852, R881, R882, R909, R910, R914-926, R937-940, R944, R981-985, R1015-1017, R1027, R1033, R1034, R1043-1050, R1129, R1131-1138, R1252, R1378, R1514-1516, R1523-1528, R1531, R1565-1569, R1573-1586, R1770-1782, R1786-1816, R1855-1858, R1865, R1866, R1871, R1889, R1936, R1940, R2085, R2092, R2093, R2099, R2103-2107, R2113, R2126, R2127, R2315-2318, R2430-2462, R2473-2477, R2480, R2483-2485, R2600-2602, R2604-2619, R2625, R2627, R2630-2633, R2635-2638, R2641, R2645, R2646, R2650-2659, R2662, R2663, R2716-2727, R2744, R2745, R2755-2758, R2782, R2784-2790, R2794-2796, R2801, R2805, R2838-2841, R2844, R2845, R2849, R2880, R2887, R2888, R2953, R2956-2958, R2960, R2984, R2985, R2990, R2993, R2995-3003, R3035, R3191, R3193, R3197, R3204-3206, R3208, R3242, R3243, R3284-3287, R3290, R3296, R3297, R3356, R3357, R3366, R3370, R3391-3394, R3396-3398, R3403-3413, R3418, R3442-3445, R3457-3462, R3464-3470, R3512, R3516, R3518-3521, R3523-3525, R3569, R3572, R3590, R3605, R3606, R3610, R3613, R3614, R3645-3650, R3653, R3662, R3663, R3677, R3679, R3683, R3684, R3686, R3690

Command-line interface. Parses flags, detects running server,
dispatches operations via proxy or cold-start.

## Knows
- dbPath: string — database directory (from --dir flag, default ~/.ark/)
- verbosity: int — global verbose level (0–4), parsed before subcommand dispatch
- command: string — subcommand name
- flags: parsed flag values

## Does
- cmdTagSet reads with ark.ReadEditableText and writes ark.EncodeText in the file's encoding; cmdTagGet and cmdTagCheck read with ark.ReadText (R3662, R3663).
- configcheck.go: `configLoadError` reports an ark.toml that exists and does not load (a missing one is fine); `guardConfigActions` wraps every command node's Action with that check, except the exempt paths (`stop`, `status`, `config recover`) and a help request: urfave answers `--help` itself for most nodes, but a SkipFlagParsing node (search, status) gets it raw, so `asksForHelp` recognizes it in the args (R3645, R3646). cmdStatus adds a `config_parse` warning, and with no server says the index was not read and exits 1 (R3647); with a server, `withConfigParseWarning` keeps the server's own `config_parse` record when it has one and adds the CLI's only in its absence (R3653). configRecoverAction reads `ark.ReadStoredConfig` instead of opening the DB, renames an unloadable ark.toml to `ark.toml.broken`, and refuses while a server runs (R3648, R3649). patternsCollection falls back only on a missing ark.toml (R3650).
- printStatus prints the served `scans:` line before `memory:` (R3613); cmdStatus `--cpu FILE [--seconds N]` posts /status/cpu and prints `cpu profile: PATH` (R3614).
- chats rendering (chats.go): `--thinking` shows thinking as `✻ ...` (R3035) and narration as `≈ ...` (R3610).
- printStatus prints the served `memory:` line before `server:`; absent when
  unserved (R3605).
- cmdStatus `--heap FILE`: absolute path, POST /status/heap, print
  `heap profile: PATH`; refuses unserved (R3606).
- cmdSetup / extractBundleAssets: after extracting the bundle, AppActivation.ActivateApp for each bundled app (R3516)
- cmdBundle: before grafting, checkBundleSource refuses a symlink anywhere in the source dir and two `apps/*/viewdefs/` files of the same name, naming them (R3518, R3519). The Makefile strips the cache's symlinks before calling it (R3520)
- cmdUIConflicts: with a server, `GET /ui/conflicts`; without, AppActivation.ViewdefConflicts(arkDir). Prints one line per losing copy (`KEY: WINNER wins over LOSER`, then both files indented), or `no viewdef conflicts` (R3523, R3524, R3525)
- cmdUILinkapp: `add` = AppActivation.ActivateApp (app must exist under `apps/`), `remove` = AppActivation.DeactivateApp; nothing else is linked (R3521)
- Main(): expand -vvv → -v -v -v, parse --dir and -v globally,
  dispatch to subcommand handler
- expandVerbosityFlags(args): preprocess args to expand -vvv into
  -v -v -v. Matches -v... (not --verbose or -version).
- Logv(level, format, args): package-level function, emits [vN] msg
  via log.Printf when verbosity >= level
- DetectServer(dbPath): try connecting to Unix socket in dbPath,
  return connection or nil
- Proxy(conn, request): forward operation to running server as HTTP,
  return JSON response
- ColdStart(dbPath): open DB directly, execute operation, close
- withDB(fn) (R3391): the cold-start arm for commands that can answer
  without a server. The **open is bounded** — `OpenWithTimeout` with
  `dispatchLockTimeout`, retried at `dispatchRetryInterval` through
  `dispatchStubbornWindow` — exactly as `proxyOrLocal`'s local arm is,
  because the bound belongs to the act of a CLI opening the index and not
  to one dispatcher. `ark.Open`'s unbounded form is the server's alone
  (R2994); bbolt's `flock` reads a zero timeout as wait-forever, so an
  unbounded open here blocks until one side is killed
  - **on timeout, recheck liveness before waiting.** A server *coming up*
    will accept a dial shortly, so retrying is right. A server already
    *running* owns the index for as long as it lives, so retrying cannot
    help — fail at once with the stop-the-server message rather than pay
    the whole window to reach a certain failure
- **A failed proxy is not an absent server (R3398).** Dispatch has three
  outcomes — no answer, served, refused — and only the first licenses a local
  open. `search` and `message inbox` dialed for themselves and wrote the
  natural spelling, `if err := proxy(...); err == nil { print; return }`, which
  drops the error and continues into the local arm: the index open then fails
  against the server that just answered, so the user hears about a lock and
  never hears what the server said. Both now go through `proxyOrLocal`, which
  is the only thing here that tells a bounce (wait and resume, R2996) from a
  live server's refusal (report it, at once).
- **A successful proxy ends the command's business with the index (R3392).**
  Reaching the server and then opening locally for the rest of the output
  cannot work: the server holds the lock for as long as it lives, so the
  second open hung before R3391 and now reports a *lock* problem for data
  the server could have returned. Whatever remains is fetched from the
  server too, or that part declares itself server-must-be-stopped
  (`withExclusiveDB`, R3002). The rule is stated here rather than left to
  each command because both offenders looked locally correct: each proxied
  what it obviously should and reached for the index only for a *fragment*
  — chunk statistics in one case, a suppressed-tag lookup in the other.
- **A path argument is absolutized on the client before it crosses the
  socket (R3403).** `absClientPath(p)` resolves a relative path against the
  client's cwd, because the server resolves it against *its own* (`~/.ark`
  under `ark serve`) and the caller's cwd is gone by the time the request
  arrives — so a relative path sent as-is misses the index (which stores
  absolute paths) or opens the wrong file. Called at each proxy boundary
  that ships a specific file's path: `fetch`, `chunks`/`chunks -anchor`,
  `tag chunk`/`tag get -all`, `tag inspect --target`, `config why`,
  `schedule change`, `add`. `tmp://` keys and glob-pattern filters
  (`remove`, search `-files`/`-exclude-files`) are left untouched; `Abs`
  is idempotent, so the local-dispatch arm is unaffected.
- planStatus(serverUp, wantChunks, wantTokenize) → statusPlan (R3392,
  R3393, R3394): the whole of `ark status`'s dispatch decision, as a pure
  function, so the three-way choice is testable without a socket, a lock
  or a subprocess. It answers where each part of the output comes from:
  - server up, `--chunks --tokenize` → refuse, "stop the server". Tokenizing
    loads the embedding model for its vocabulary, the same thing that makes
    `embed text` / `embed bench` / `embed validate` exclusive (R3002)
  - server up → proxy the status block, and `--chunks` proxies too, to
    `POST /status/chunks` with the filter stack (R3393). No local open
  - no server up → one local open answers everything, as before
  The warnings block is not part of this decision: it rides in the status
  response (R3395), so it prints on either path.
- printUnresolved(records, reason) / formatUnresolvedReasons(byReason)
  (R3356, R3357, R3366, R3370): the two places a U record's reason becomes
  readable. `ark unresolved` prints the reason on every line because the three
  call for different responses — edit a pattern, add a chunker, add a source —
  so a bare list of paths cannot tell a reader what to do about any of them; a
  symlink entry also names its proposing link, since the record is keyed on
  the *target* and would otherwise offer a directory the user may not
  recognise. `--reason` is what lets a caller hide the `unindexable` bulk
  without losing it. `ark status` appends the same breakdown to its count,
  in a fixed order (patterns, chunkers, sources) rather than sorted, omitting
  zeros. Both exist for one reason: `unindexable` normally dominates, and a
  count nobody can act on teaches its reader to skip the line.
- CleanStaleSocket(dbPath): if connect fails, remove socket file
- Each subcommand: init, setup, rebuild, add, remove, scan, refresh, search,
  serve, status, files, stale, missing, dismiss, config, unresolved, resolve,
  tag (with sub-subcommands: list, counts, files, set, get, check),
  ui (with sub-subcommands: run, display, event, checkpoint, audit,
  status, open, install; checkpoint's fossil machinery is
  crc-Checkpoint.md),
  bundle, ls, cat, cp, chunks,
  embed (with sub-subcommands: text, bench, validate),
  message (with sub-subcommands: new-request, new-response, set-tags,
  get-tags, check, inbox, dm — set-tags/get-tags alias ark tag set/get),
  connections (sidecar CLI for find-connections — see cmdConnections),
  subscribers (`--tag T [--quiet]`, server-required: count
  subscriptions matching a tag predicate — see crc-Monitor.md;
  R2805),
  monitor (`status` / `recent` / `pause` / `resume`: read JSONL
  logs and append control records — see crc-Monitor.md;
  R2784–R2790),
  luhmann (`spawn-record` / `exit-record` / `inspect-exit`:
  record orchestrator lifecycle events — see crc-LuhmannCLI.md;
  R2794–R2796)
- cmdSubscribers: parse `--tag` sigil, optionally `--quiet`. Proxy
  to `GET /subscribers?tag=...` on the server; print count or set
  exit code per `--quiet` (R2782, R2805).
- cmdMonitor: dispatch `status` / `recent` / `pause` / `resume`.
  `status` and `recent` run cold — open
  `~/.ark/monitoring/<class>.jsonl` directly. `pause` and `resume`
  proxy to `POST /monitor/control` on the server (R2784–R2790).
- cmdLuhmann: dispatch `spawn-record` / `exit-record` /
  `inspect-exit`. The two write verbs proxy to `POST
  /luhmann/record` on the server; `inspect-exit` runs cold and
  reuses the nonce → `.meta.json` lookup from `connections recall
  context` (R2794–R2796).
- cmdAdd: if path starts with `tmp://`, read content from stdin,
  proxy to server via POST /tmp/add. Server must be running.
  Otherwise, existing add behavior (walk dirs, index files).
- cmdRemove: if path starts with `tmp://`, proxy to server via
  POST /tmp/remove. Otherwise, existing remove behavior.
- cmdRebuild: refuse if server running. Run init --no-setup (delete
  `index.db`, write fresh I records from ark.toml, clearing all E
  records), then `Serve(dbPath, ServeOpts{Rebuild: true})` — a
  constrained read-only server that runs the scan and exits when the
  write queue drains. The socket stays up during the scan, so `ark
  status` / `ark search` from another terminal return live progress
  instead of blocking on bbolt's single-process file lock. The brief
  init drop window before the socket binds is uncovered (R2990).
  (R1542, R1568, R2984, R2985, R2990; seq-rebuild-read-serve.md#1)
- cmdConfig: dispatches to show (default), add-source, remove-source,
  add-include, add-exclude, remove-pattern, show-why sub-subcommands.
  Config subcommands with positional args + optional flags use
  reorderArgs() to ensure flags are parsed before positional args
  (Go's flag package stops at first non-flag argument).
- cmdSearch: --cpuprofile FILE writes pprof CPU profile wrapping the
  full command. --memprofile FILE writes heap profile post-GC on exit.
  --trace FILE writes runtime/trace execution trace (goroutines,
  syscalls, GC, blocked time). All optional and independent.
  (R981, R982, R983, R984, R985)
  Server-first dispatch: always tries proxy to running server first,
  falls back to local search if server unavailable or proxy fails.
  Server keeps caches warm (file name map, index pages), avoiding
  cold-start DB open cost. All flags sent in one request struct.
  --session NAME included in the server request (no longer requires
  special dispatch — just another field).
  --chunks and --file-content flags (mutually exclusive),
  outputs JSONL when either is set. --wrap <name> wraps output in
  XML tags of that name. --like-file <path> uses file content as
  FTS density query (mutually exclusive with --contains/--regex).
  --tags outputs extracted tag names instead of chunk content.
  --score <mode> selects scoring strategy: auto (default), coverage,
  density. Unknown values produce error and exit.
  --multi runs all four strategies (coverage, density, overlap, bm25)
  via SearchMulti and merges results. Mutually exclusive with --score.
  Does not apply to --regex, --about, or --like-file.
  --fuzzy runs typo-tolerant search via SearchFuzzy. Takes positional
  query. Mutually exclusive with --multi, --score, --about, --regex,
  --like-file, --contains. Composes with all filters, --proximity,
  --no-tmp, -k, output flags.
  --proximity enables post-search proximity reranking on top 2*k
  candidates. Composes with any search mode including --multi and --fuzzy.
  --no-tmp excludes tmp:// documents from results.
  Old flags removed: --filter, --except, --filter-files,
  --exclude-files, --filter-file-tags, --exclude-file-tags,
  --except-regex. Superseded by filter stack.
  -system lifts the system_exclude fence for that search and is sent as
  `system` in the search request; no -files row implies it (R3590).
- parseFilterStack(args): custom arg walker that extracts filter
  entries from raw args before flag.Parse. Walks args left-to-right,
  tracking current polarity (default "with"). Mode flags (-contains,
  -fuzzy, -regex, -tag, -about, -files) consume the next arg as the
  query. Bare terms coalesce into a -contains group. -with/-without
  toggle polarity. --filter-k N after an -about entry sets K on the
  most recent filter entry (only meaningful for about mode; warns on
  non-about entries). Returns filter entries and remaining args for
  flag.Parse. The first entry becomes the primary search; the rest
  become ChunkFilterRow entries. (R1940)
- **The filter stack is the one path-glob surface (R3204).** `files`,
  `status`, `tag files`, `tag values`, and `subscribe` take path globs
  through the same repeatable `-files GLOB` rows with `-with` (default) /
  `-without` polarity that `search` does. With no positive row, all paths
  are candidates. `anchorGlobsToCwd` then anchors every row (R3197/R2958),
  so what leaves the client is always absolute and no server-side component
  invents a working directory. The five stop carrying per-command matchers
  of their own: `matchBaseSet` and `matchPath` become one-line calls to
  `ark.FilterPaths` / `ark.MatchPathFilters` (crc-Matcher.md), as does
  pubsub's pair (crc-PubSub.md).
- parsePathFilterStack(cmd, args): the **path-only subset** of the search
  DSL — sticky `-with`/`-without` polarity plus repeatable `-files GLOB`
  rows, with every other token (flags *and* positional arguments) passed
  through to the caller's own `flag.Parse`. Returns cwd-anchored globs.
  It is deliberately a sibling of `parseFilterStack` rather than a call
  into it: the search walker coalesces bare terms into a `-contains`
  group, which would swallow these commands' positional arguments —
  `ark tag files mytag` would lose its TAG and `ark files '*.md'` its
  glob. The two share the polarity grammar and give a `-files` row
  identical meaning; only the mode vocabulary differs. (R3204, R3197)
- retiredPathFlagError(cmd, flag): builds the R3205 message. Extracted
  from the parser so the *content* of the error is unit-testable —
  `fatal()` exits the process, so the message would otherwise only ever
  be checked by hand.
- **Positional globs are the same mechanism (R3208).** `filterPaths` — the
  trailing-glob shorthand on `files`, `stale`, and `missing` — anchors
  through `anchorGlobsToCwd` too, so `ark files '*.md'` and `ark files
  -files '*.md'` mean the same thing. Anchoring is idempotent, so the
  repeated per-call pass is safe. Without this a single command would carry
  two glob rules, which is the divergence R3195 exists to remove.
  **Not** `unresolved` or `refresh`, despite both advertising
  `[PATTERN...]`: `cmdUnresolved` never applies its patterns at all
  (pre-existing, O161), and `refresh`/`resolve`/`remove`/`dismiss` pass
  theirs onward to be matched where the work happens — `RefreshStale` already
  takes `db.matcher`, so R3195 holds there — with no client cwd in play.
- **`--filter-files` / `--exclude-files` on those five exit non-zero
  (R3205)** with a message naming `-files` **and** the semantic change —
  a bare no-slash glob is now top-level-only, `/**/*` is the explicit
  any-depth form. They are deliberately **not** aliased: the single shape
  whose meaning changed fails by returning *fewer results*, never an error,
  so an alias would silently reproduce exactly the failure mode this
  unification exists to remove. The user must learn both facts at once.
- **`tag values` boolean is `--show-files`, not `--files` (R3206).**
  `parseFilterStack` normalizes `--files` → `-files` *before* `flag.Parse`,
  so a boolean spelled `files` would be eaten as a filter row and would
  consume the following TAG as its glob — a silent misparse, not an error.
  `search` hit this same collision and resolved it the same way, renaming
  its boolean to `--file-content`.
- -parse flag: prints the disambiguated command (explicit mode flags,
  quoted values, polarity toggles) and exits without searching.
- -tag strips optional leading @ from TAG. Name-only (no :value)
  matches any value.
- --wrap: parameterized context wrapper. Escapes closing tag in content.
  Convention: "memory" for experience, "knowledge" for facts.
- cmdTag: dispatches to list/counts/files/defs/set/get/check sub-subcommands,
  --context flag on files sub-subcommand.
  set/get/check are generic tag block operations (no DB needed).
  check accepts optional heading arguments for allowed-heading validation.
  cmdTagSet: when setting `status`, also auto-sets `status-date` to today
  (YYYY-MM-DD). Only triggers for the exact tag name "status".
- cmdTagDefs: query D records from the index. Optional tag name args filter.
  Default: `tagname description`, deduplicated, sorted alphabetically.
  --path: `path tagname description`, lexically sorted, not deduplicated,
  spaces in paths backslash-escaped.
  Uses server proxy when available, falls back to withDB.
- cmdFetch: verify indexed, output raw file content to stdout.
  --wrap <name> wraps output in XML tags.
- cmdChunks: context expansion around search hits. Takes path, range,
  optional -before/-after counts. Calls microfts2.GetChunks() via
  proxyOrLocal (POST /chunks when a server holds the index, else local
  cold-start). Outputs JSONL (one object per chunk: path, range, content,
  index). Supports --wrap for XML wrapping. A missing chunk keeps
  microfts2's `"missing": true` in JSONL; under --wrap its element holds
  `ark.StaleChunkPlaceholder(path)` (R3572).
- cmdFiles: list indexed files. Positional glob args filter the output.
  The `-files` filter stack sets the base file set (R3204); positional
  globs narrow within it. --status adds G/S/M status, bytes (os.Stat),
  and chunk count (DB.AllChunks) columns. -v with --status shows
  per-file chunk size stats (min/max/mean/median/p90/p95) as an
  indented detail line. --tokenize measures chunk sizes in tokens.
  Missing files show 0 for bytes and chunks, skip verbose line.
  (R1573-R1586)
- cmdStatus: --db flag triggers DB.StatusDB() and prints record counts
  per bucket. Without --db, unchanged. When proxied, sets
  ?db=true query parameter. (R2473, R2474, R2475, R2476, R2477, R2480)
  --chunks activates chunk size statistics. Iterates files (scoped by
  the `-files` filter stack, R3204), calls DB.ChunkStats() to collect
  sizes, prints right-aligned table with "all" row + per-strategy rows.
  Columns: strategy, count, min, max, mean, median, p90, p95, p99.
  Unit is bytes (default) or tokens (--tokenize). --tokenize requires
  configured [embedding] model; creates a minimal Librarian tokenizer context.
  Zero chunks prints "no chunks found". (R1514-R1531)
  After normal output, if E records exist, print a "warnings:" section
  with each condition name, description, and remediation advice. (R1565,
  R1566, R1567)
- cmdServe: if server already running, print message to stderr, exit 0.
  --force flag: passed to Server.Serve as opts.Force. Clears E records
  and accepts current config even with deferred changes. (R1558)
- cmdConfigRecover: read stored config from I records via DB, write to
  ark.toml. Disaster recovery for corrupted/missing config. (R1569)
- cmdStop: request graceful shutdown by POSTing `/shutdown` over the
  unix socket (200 or dropped connection = teardown initiated, R3442),
  then poll for release by dialing the socket (refused dial = down,
  R3443), bounded by a timeout. On timeout the fallback is platform-
  split (R3444): Linux force-kills the PID from the PID file and
  re-polls; Windows errors without killing. `-f` force-kills
  immediately on every platform (`TerminateProcess` on Windows, R3445).
  Exit 1 when not running, on Windows graceful-stop timeout, or if a
  kill fails to release. `timeoutKill` is the platform-split fallback
  (`stop_fallback_unix.go` / `stop_fallback_windows.go`).
- cmdSourcesCheck: expand globs, add new dirs, report MIA/orphan. Reached by
  `ark sources check` only; bare `ark sources` is sourcesListAction.
- sourcesListAction: bare `ark sources`. Gets the effective Config (the
  server's `GET /config` when one runs; else LoadConfig with the built-in
  sources ensured on that private copy) and prints one baby-food bullet per
  source via formatSourceList: the directory, or a glob's pattern, with
  `from glob`, `logs for` and `missing` marks. Read-only (R3679).
- Config mutation actions (add-source, remove-source, add-include,
  add-exclude, remove-pattern, add-strategy): on success each prints one line
  from configMutationLine naming what changed and where (global or the
  source), on the server path and the cold path alike (R3677).
  Output: +/−/? prefix per line. Proxies to server if running.
- cmdUI: gateway for all UI operations. Reads mcp-port/ui-port from
  dbPath. No subcommand → open browser. Subcommands:
  run, display, event, checkpoint, audit, status, open, reload, install.
  Each subcommand sends HTTP requests to the mcp-port.
  Replaces the `.ui/mcp` shell script — one binary, no separate script.
- cmdUIStatus: output "ui: running (port N)" + browser count + indexing
  state. When UI not running: "ui: not available". Queries server
  via GET /status for UI fields.
- cmdSetup: the install path. An install is the marker — the database file —
  plus the executable in ~/.ark, not ~/.ark existing (R3683); with both present
  the global part does nothing but the Windows registration. With the marker
  and no executable (what `ark uninstall` leaves) it reinstalls over the kept
  data: the same steps without creating the database, the bundle written
  through Updater.RefreshAssets rather than extracted over the tree, and every
  bundled app activated (R3684). When the marker is absent it runs the
  global install in order (seq-install.md#1): stop a running server first,
  through the graceful `ark stop` path (`stopServerForInstall`, never starts
  one, and aborts the install if the stop does not complete, R3481);
  create ~/.ark (R3457); copy
  the running executable in, ark.exe on Windows, skipped when already
  running from ~/.ark (R3458); extract the bundle (R3459); seed the install
  manifest with each extracted asset's hash via crc-Updater.md SeedManifest
  (R3484); create the
  database via setupDatabase, the shared DB-setup function (R3460); make the
  ~/.claude/ directory links (R3461, R3462); provision llama libs (R2969);
  download the configured model if missing (R3463, delegated to
  crc-Updater.md ProvisionModel via provisionModelForInstall, which prints the
  missing-keys guidance rather than failing when the model is absent and
  model_url/model_sha256 are unset, R3483); print that new agents appear in the
  next session (R3464). Before that report, on every run, crc-Registration.md
  Register writes the Windows registration (R3680), and the user is told to
  open a new terminal when `Path` changed. It copies no skills or agents
  anywhere (R3465).
- cmdUninstall(--yes): crc-Uninstaller.md. Refuses on a symlinked ~/.ark/ark,
  prints the plan, asks unless --yes (only `y`/`yes` proceeds), then runs it
  (R3690).
- cmdWingetInstall: the hidden `_winget-install` verb, crc-WingetInstall.md
  built with the real effects (R3686).
- stopServerForInstall(arkDir): the R3481 precondition shared by cmdSetup's
  global install and cmdUpdate. Reuses the graceful `ark stop` path
  (`serverClient` + POST /shutdown + `socketReleasedWithin`) rather than
  shelling out, and never starts a server; a no-op when none is running.
  Returns an error when the graceful stop does not complete within the grace
  period, so cmdSetup/cmdUpdate abort and tell the user to run `ark stop -f`
  (R3481).
- installLinks(claudeDir): make one directory link per bundled skill
  directory into claudeDir/skills/ (glob ~/.ark/skills/*, no hardcoded list,
  R3461) and one link claudeDir/agents/ark → ~/.ark/agents (R3462), both via
  crc-DirLink.md MakeDirLink. Shared by cmdSetup (global, ~/.claude) and
  cmdSetupLocal (project, cwd/.claude). It makes exactly the links
  installLinkSet lists.
- installLinkSet(arkDir): the one list of (path relative to a .claude
  directory, real target directory) pairs that installLinks makes. `ark
  wsl-init` (crc-WSLBridge.md) prints links from the same list, so the native
  and WSL link sets cannot drift (R3512).
- retireOldAgentCopies(claudeDir): remove a flat claudeDir/agents/<name>.md
  only when it is a regular file byte-identical to the bundled agent of that
  name (R3469); a differing file or a hand-made link is reported and left,
  since either may be the user's (R3470).
- cmdSetupLocal: the `--local` arm. Refuses without the marker (R3466),
  since it connects a project to an install rather than creating one. Calls
  installLinks against cwd/.claude (R3467) and prints the crank-handle
  CLAUDE.md hint; never edits CLAUDE.md (R3468). First setup path that reads
  the cwd, so its help says so (seq-install.md#2).
- setupDatabase: the database-creation step split out of cmdInit so cmdSetup
  and cmdRebuild both call it, ending the init→setup recursion (R3460). Not
  a new command — a shared internal.
- cmdUpdate: stop a running server first (stopServerForInstall, R3481), then
  dispatch to crc-Updater.md Update(noExe). Parses `--no-exe`
  (R3478). Gripes on a symlinked ~/.ark/ark without --no-exe (R3477).
  See seq-install.md#3.
- cmdInit: seed case_insensitive/aliases from ark.toml if present;
  CLI flags override seeded values. Runs setup first if ~/.ark/
  not bootstrapped (no html/ dir). --no-setup skips setup.
  --if-needed skips DB creation when index.db already exists.
  Without --if-needed, removes the existing index.db
  before creating a fresh database. Creates ~/.ark/searching/
  with default CLAUDE.md if not present. (R1252)
- cmdUIInstall: single entry point for per-project setup. Runs
  init --if-needed internally. Starts server if not running.
  Creates .claude/skills/ and .claude/agents/ dirs if needed.
  Creates symlinks in .claude/skills/ pointing to ~/.ark/skills/, and
  a symlink in .claude/agents/ for each ~/.ark/agents/*.md (globbed —
  no hardcoded ark.md; warns once if none found).
  Symlinks are idempotent — re-running updates existing ones.
  Prints crank-handle prompt for CLAUDE.md bootstrap line.
- cmdInstall: alias for cmdUIInstall (`ark install` = `ark ui install`).
- cmdBundle: graft a directory onto a binary as a zip appendix.
  Calls bundle.CreateBundle(src, dir, output). Build-time command.
- cmdLs: list embedded assets. Calls bundle.ListFilesWithInfo,
  prints one file per line (symlinks show target). Exit 1 if not bundled.
- cmdCat: print an embedded file to stdout. Calls bundle.ReadFile.
  Exit 1 if not bundled.
- cmdCp: extract embedded files matching a glob to a directory.
  Matches against basename and full path. Preserves permissions,
  recreates symlinks. Exit 1 if not bundled or no matches.

- cmdMessage: dispatches to new-request, new-response, set-tags,
  get-tags, check, inbox sub-subcommands. Most operate on plain files
  via TagBlock — no server, no DB. Exception: inbox requires the database.
- cmdMessageSetTags: alias for cmdTagSet. Help text points to `ark tag set`.
- cmdMessageGetTags: alias for cmdTagGet. Help text points to `ark tag get`.
- cmdMessageNewRequest: validate FILE doesn't exist, build TagBlock
  from flags (ark-request=ID, status=open), set status-date to today
  (YYYY-MM-DD), render with heading scaffold. Issue from --issue or
  --issue-file (verbatim file read, trailing newlines trimmed, single
  line, mutually exclusive, one required). Body from --content flag
  (preferred), --content-file (verbatim file read, mutually exclusive
  with --content), or stdin (if not a terminal, read until lone `.`).
  --content/--content-file skip stdin reading. Write file. The *-file
  flags exist so a caller hands a path and the payload is never retyped
  by a relaying agent. (R2956, R2957)
- cmdMessageNewResponse: same pattern as new-request with ark-response tags.
  Default status is "accepted" (response = ack). Sets status-date to
  today. Body from --content, --content-file (verbatim), or stdin, same
  as new-request. (R2957)
- readStdinBody(): if stdin is not a terminal, read lines until lone `.`
  or EOF. Returns the collected body text (may be empty).
- cmdMessageCheck: wrapper that calls cmdTagCheck with the standard
  message heading list. Crank-handle for agents — terser than passing headings.
- cmdMessageInbox: query DB.Inbox(), apply CLI post-filters, output.
  Flags: --project (filter either @to- or @from-project), --to (filter
  @to-project), --from (filter @from-project),
  --all (include completed/done/denied), --include-archived (include archived),
  --compact (legacy TSV instead of the tree), --counts (status counts),
  --unmatched (show only requests with no matching response).
  Default: filter out completed/done/denied and archived.
  Entries are paired by requestId **before** any CLI post-filter, so the
  pair is available to --unmatched, lag, and the display even when a
  directional filter or the completed/denied hide would drop one side.
  --unmatched keeps requests whose global pair has no response.
  **Default output is the baby-food markdown tree** (printInboxTree,
  R3404-R3408): one bullet per request (status, from → to, summary,
  @status-date, path), its paired response nested beneath (status,
  from → to, @status-date, path), each side's bookmark lag stated in
  words, and an explicit `no response` line when none exists. The
  response nests even when it was filtered out of the row set.
  --compact (printInboxCompact, R3409) emits the legacy tab-separated
  lines byte-for-byte: status-date, status, to, from, summary, path,
  and `lag:PROJECT:STATUS` (empty when current). --counts takes
  precedence and prints status\tcount sorted alphabetically.
  On an empty filtered set the tree names the from-/to-projects present
  in the unfiltered set (printInboxEmptyHint, R3410) so a typo is
  correctable. Summary is `@issue` for requests, `ark-response:<id>`
  for responses. Sort is most-recent-first by the pair's latest date.
- cmdMessageDM: parse flags (--from / --from-service, --to repeatable,
  --subject, --ref, --content), build a head-of-chunk tag block via the
  shared composeDM function, POST to `/tmp/append` at
  `tmp://<sender>/dm-<to0>` where sender is the --from session UUID or
  the --from-service identity. --from and --from-service are mutually
  exclusive; exactly one must be present (R2722). With --from-service,
  emit `@from-service: NAME` in place of `@from: <session>` (R2723) and
  use NAME as the tmp:// sender segment (R2724). Multi-recipient via
  repeated --to: recipients joined by single spaces in the `@dm` value
  (R2725). --subject TEXT appends `: TEXT` to the `@dm` value; empty
  TEXT exits non-zero (R2726). The composeDM internal function is
  shared with in-process callers — the simple-recall watcher invokes
  it directly without shelling out (R2727).

- cmdSchedule: dispatches to search, change, parse, tags, upcoming,
  logs, suppress, unsuppress sub-subcommands. (R926)
- cmdScheduleSearch: query EventScheduler.QueryRange for date range.
  START END as flexible dates (dateparse). Flags: --tag (filter),
  --gaps (only unacked past events), --json (JSON output).
  Default output is markdown crank-handle style. Renders
  suppressed events with `[suppressed]` prefix.
  Time-range render collapses when start==end and drops the trailing
  `: ` when the summary is empty, so zero-duration ticks don't print
  as `- 13:45–13:45:`. (R914-R920, R2845, R2849)
  - **Dispatches through `proxyOrLocal` (R3396).** The proxy arm posts to
    `/schedule/search`; the local arm builds an `EventScheduler` from the
    opened config and `<arkDir>/schedule` and runs the same `QueryRange`.
    R1027 has claimed a server-free path since the command was written and
    the CLI refused at the door for just as long — the *query* never needed
    a server, but the command had no arm to run it in. Nothing about
    `QueryRange` resists this: `NewEventScheduler` takes a pubsub, a
    reporter, a directory and a config, and the query half uses none of the
    first two.
  - **Suppression arrives on the event, not from a second lookup (R3397).**
    The renderer reads `ev.Suppressed`; it consults no config of its own.
    Whichever side computed the events read the config for suppression, so
    the two arms cannot disagree and neither reaches for state the other
    owns. Doing it the other way is what broke the command: it asked the
    server for events and then opened the index purely to look suppression
    up, which cannot work while that same server holds the index (R3392).
- cmdScheduleChange: rewrite date in schedule tag value.
  PATH TAG NEWSTART [NEWEND]. PATH accepts both disk paths and
  `tmp://` URIs. Preserves trailing description text. --dry-run
  shows diff without writing. Re-indexes after write. Proxies to
  server. (R921, R922, R923, R925, R2842, R2843)
- cmdScheduleTags: show declared schedule tags. Marks suppressed
  tags with `[suppressed]` and non-default lifecycle with
  `[lifecycle=tmp]` or `[lifecycle=none]`. Cold (reads ark.toml).
  (R2844)
- cmdScheduleUpcoming: print next-fire for TAG (or all tags with
  `--all`) from the in-memory priority queue. Markdown by default,
  `--json` emits per-entry objects. Server-required. (R2838)
- cmdScheduleLogs: print audit log for TAG (and optional SOURCE).
  Reads disk log for lifecycle=disk tags (cold), tmp:// doc for
  lifecycle=tmp (server-required), prints `(no log)` for
  lifecycle="none". Output includes spec history + recent fires
  (default 50). `--json` emits a structured object.
  (R2839)
- cmdScheduleSuppress: set `[schedule.tag.TAG] suppress = true` in
  ark.toml via the config-mutation path. Errors if no
  `[schedule.tag.TAG]` block exists. Server-required. (R2840)
- cmdScheduleUnsuppress: set `suppress = false` (or remove key) for
  TAG. Server-required. (R2841)

- cmdSubscribe: register/cancel/list tag subscriptions. Proxies to
  server. R937: normalizes --tag values by stripping leading @ and
  trailing : before sending to server. Path scoping comes from the
  `-files` filter stack (R3204), anchored client-side like every other
  member of the stack, so `subscribe -files 'specs/**'` scopes to this
  project's specs instead of silently matching nothing (O160, R3207).
- cmdListen: long-poll for tag notifications. Outputs markdown crank
  handles. Proxies to server.

- cmdEmbed: dispatches to text, bench, validate sub-subcommands.
  No subcommand → print usage, exit 0.
- cmdEmbedText: join positional args with spaces, embed via
  Librarian.EmbedQuery, print JSON vector to stdout. Requires
  [embedding] model. Uses withDB.
- cmdEmbedBench: dispatches by MODE arg (tags or chunks).
  --ctx N (default 2048), --parallel N (default 8).
- cmdEmbedBenchTags: collect all tag values from the index, embed via
  batch and single paths, report timing comparison and speedup ratio.
- cmdEmbedBenchChunks: sample 200 chunks via file-first random
  sampling, embed via batch and single paths, report timing,
  chunk size distribution, and skip rate.
- cmdEmbedValidate: cross-reference EC/EF records against FTS
  chunk data. Five checks: orphan EC (fileID missing or chunkIdx
  out of range), EF/EC count mismatch, missing EC (chunks without
  embeddings), orphan EF (no matching EC or FTS entry), dimension
  consistency (report distribution, flag minority dimensions).
  --fix: delete orphan EC, orphan EF, and wrong-dimension EC records.
  --verbose: per-file detail. Exit 0 if clean, exit 1 if problems.
  Uses withDB. Read-only without --fix.
- cmdConnections: positional-subcommand CLI for find-connections.
  Requires a running server; all subcommands proxy through the
  Unix socket. (R2313, R2604–R2615) Dispatch table:

  **Public (humans + downstream agents):**
    - `find <inputs>...` → cmdConnectionsFind: parse mixed
      input list (decimal chunkID, `PATH:N-M` / `PATH:N`,
      quoted text), build []ConnectionsInput, POST
      /connections/find with {inputs, opts}. Print the
      returned `tmp://connections/<id>.md` on stdout. With
      `--wait`, subscribe to the doc's @connections-status,
      block until terminal, print body on completion.
      `--json` switches the wait-output to JSON projection.
      Flags: --mode normal|turbo (default normal), --k N
      (default 20), --purpose curate|recall (default curate),
      --timeout SEC (default 30), --wait, --json. (R2604, R2605)
    - `wait <path>` → cmdConnectionsWait: subscribe to the
      named tmp:// doc, block until @connections-status reaches
      a terminal value, print body (markdown or JSON with
      --json). `--timeout SEC` flag; on timeout, exit non-zero
      and print the last-seen status to stderr. (R2606)
    - `show <path>` → cmdConnectionsShow: fetch the doc body,
      parse via ParseConnectionsDoc, apply projection flags.
      Flags: --status, --tags, --tag NAME, --threshold N, --json.
      Does not block on status. Distinct from `ark fetch <path>`
      which dumps the raw body unparsed. (R2607, R2608)
    - `list` → cmdConnectionsList: GET /connections/list,
      print markdown table (default) or JSON with `--json`.
      (R2609)

  **Sidecar (turbo agent internal protocol — replaces the old
  --wait/--fetch/--result/--error flags):**
    - `sidecar-wait` → block on GET /connections/wait, print
      JSON array of {id, chunkIDs, timeoutSeconds}. (R2315, R2611)
    - `sidecar-fetch ID` → GET /connections/fetch?id=ID, print
      JSON array of {chunkID, fileID, path, content}. Non-zero
      exit when a chunk ID is unknown; error message carries
      the offending chunk ID. (R2316, R2612)
    - `sidecar-result ID` → read JSON payload from stdin,
      POST /connections/result. (R2317, R2613)
    - `sidecar-error ID MESSAGE` → POST /connections/error
      (MESSAGE is a positional argument, not `ID=MESSAGE`).
      (R2318, R2614)

  Removed flags (`--wait`, `--fetch`, `--result`, `--error`)
  produce a one-line hint pointing at the new subcommand and
  exit with status 2. (R2615)

  All public subcommands fail with `server not running` when
  no server is detected. (R2610)
- ParseConnectionsDoc(body []byte) *ConnectionsDoc: shared
  parser used by cmdConnectionsShow and cmdConnectionsWait
  (with --json). Reads header tags (@purpose, @connections-mode,
  @connections-status, @connections-warning, @proposal-count,
  ...) and `## Proposals` rows. Each row's `@proposal-kind`
  selects the variant (tag-name / theme / shared-tag) and the
  parser populates the kind-specific fields (R2593–R2596).
  Lives next to connections.go so the server can use the same
  parser for its own projections. (R2607)
- cmdRecall: entry point for the recall subcommand (R2617). Parses CLI flags: --k (default 20, clamped to [1, 200]), --type (auto, chunk, text), --no-content, and --json (R2627). Normalizes positional inputs (R2618, R2619). If the server is running, proxies to GET/POST /recall (R2630). If the server is not running, checks config for the [embedding] model (R2631): if configured (and exists), exits non-zero warning the user to start server (R2632); if no model, opens the DB locally in-process via withDB and runs the trigram-only recall (R2633, R2634). Outputs markdown stencil by default (R2635), showing warnings (R2637) or "_no results_" (R2636), or prints raw JSON if --json is set (R2638). Clamps K parameter (R2641).
- cmdRecall also parses `--session SID` (load discussed-tag set
  from the session's RD records) and `--discussed @t[:v][,...]`
  (caller-supplied exclusion list). Both flags populate the
  same `RecallOpts.Discussed` slice; when both are present the
  union is sent (R2655). The substrate applies the filter per
  chunk and per the matching rule (R2656, R2657), before the
  KeepTagless decision (R2658).
- cmdDiscussed: dispatch for the `ark discussed` family
  (add/list/clear/prune). Parses `--session SID`, the positional
  `@tag[:value]` arguments, and the per-subcommand flags
  (`--since DUR`, `--json`, `--ttl DUR`). Tag input grammar
  matches the ark tag parser: bare `@name` or `@name:value`,
  no `\x00` in name or value (R2654). Server-proxy when
  available; cold-start via `withDB` otherwise.
  - `add` rejects missing `--session`, empty session value, or
    no tag arguments (R2650). Writes one RD record per tag via
    `Store.AddDiscussed` (R2650). This is the writer interface
    for the recall agent's mark-all-N policy — every tag emitted
    by the agent for a target session is marked discussed,
    regardless of whether the user engages with it. (R2662)
  - `list` calls `Store.ListDiscussed`, formats one entry per
    line as `@name` or `@name: value` or, with `--json`, emits
    `[{"tag", "value", "timestamp"}, ...]` (R2651).
  - `clear` calls `Store.ClearDiscussed`, reports the deleted
    count on stderr (R2652).
  - `prune` calls `Store.PruneDiscussed`; rejects an invalid
    `--ttl` value before any deletion (R2653).
- TTL resolution: the discussed family reads
  `Config.Recall.DiscussedTTL` (`[recall].discussed_ttl` in
  ark.toml). Empty/missing falls back to 24h default; `"0"`
  disables expiry; an unparseable value logs a warning at
  server start and falls back to 24h (R2659, R2663).

- `chats list` (R3284–R3287): answers "what conversations exist" before
  `ark chats` renders one. Enumerates `chat-jsonl` files from the **index**, not
  a directory walk (R3285) — chat dirs are already sources, so this is a
  files-in-a-source query. For each, reads the file head for the first user turn
  as the row's **label** (R3286, on demand — no record class, so no staleness
  class), plus mtime, turn count, and path; sorts newest-first and caps at
  `--limit` (default 20). `--source` narrows to one chat source; `--for` resolves
  through `Config.SourcesLoggingFor` (R3287, R3288); `--json` emits one object
  per line. The path column is what `ark chats` consumes, so list and render
  compose rather than duplicating.
- `sources pair [--auto] [--dry-run]` (R3290): calls `Config.ProposeLogsFor`,
  prints each proposal, and — with `--auto` and without `--dry-run` — writes the
  accepted ones through the config-mutation path (which proxies to the server
  when one is running, per the standing rule that config mutation never bypasses
  it). Already-paired sources are skipped; a proposal naming no existing source
  is printed as skipped rather than written. The command exists so the vendor
  path-mangling heuristic runs **once** here instead of living in the lookup
  (R3289).

## Collaborators
- Server: proxy target when server is running
- DB: direct access for cold-start operations
- Store: EC/EF record scanning for embed validate
- Librarian: model loading and embedding for embed text/bench
- TagBlock: tag block parsing and manipulation for message subcommands

## Sequences
- seq-cli-dispatch.md
- seq-embed-validate.md
- seq-message.md
- seq-discussed.md
