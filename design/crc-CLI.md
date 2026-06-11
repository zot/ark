# CLI
**Requirements:** R29, R71, R72, R73, R74, R75, R76, R77, R78, R79, R80, R81, R82, R83, R84, R85, R86, R87, R88, R108, R109, R110, R131, R139, R140, R141, R142, R143, R144, R145, R146, R147, R159, R161, R166, R169, R170, R172, R174, R165, R173, R178, R179, R180, R181, R182, R183, R185, R189, R196, R197, R198, R199, R201, R230, R232, R233, R234, R256, R273, R274, R275, R276, R277, R278, R279, R280, R281, R259, R260, R282, R283, R284, R285, R286, R287, R288, R289, R290, R291, R292, R293, R295, R297, R298, R299, R300, R301, R302, R304, R305, R306, R307, R308, R309, R310, R311, R312, R313, R314, R315, R316, R317, R318, R323, R324, R325, R326, R327, R328, R329, R330, R331, R332, R333, R334, R335, R336, R337, R370, R371, R396, R397, R398, R399, R400, R401, R402, R429, R430, R431, R432, R433, R434, R435, R436, R437, R442, R450, R451, R452, R453, R454, R455, R456, R457, R458, R462, R464, R466, R467, R468, R469, R470, R471, R477, R479, R480, R481, R482, R483, R484, R485, R486, R487, R488, R489, R490, R491, R492, R493, R494, R495, R496, R497, R499, R500, R501, R506, R507, R508, R509, R510, R512, R513, R514, R515, R516, R525, R526, R527, R528, R529, R530, R531, R532, R533, R534, R535, R536, R537, R538, R539, R540, R572, R573, R579, R580, R581, R582, R583, R584, R585, R590, R591, R592, R594, R599, R605, R606, R607, R608, R609, R610, R611, R612, R613, R614, R615, R616, R639, R654, R655, R656, R669, R670, R671, R672, R673, R674, R675, R676, R677, R678, R679, R680, R681, R692, R693, R694, R695, R696, R708, R709, R710, R711, R712, R713, R715, R718, R722, R724, R725, R726, R727, R728, R729, R730, R731, R732, R733, R734, R738, R739, R740, R741, R742, R743, R748, R849, R850, R851, R852, R881, R882, R909, R910, R914, R915, R916, R917, R918, R919, R920, R921, R922, R923, R924, R925, R926, R937, R938, R939, R940, R944, R2473, R2474, R2475, R2476, R2477, R2480, R981, R982, R983, R984, R985, R1015, R1016, R1017, R1027, R1033, R1034, R1043, R1044, R1045, R1046, R1047, R1048, R1049, R1050, R1129, R1131, R1132, R1133, R1134, R1135, R1136, R1137, R1138, R1252, R1378, R1514, R1515, R1516, R1523, R1524, R1525, R1526, R1527, R1528, R1531, R1565, R1566, R1567, R1568, R1569, R1573, R1574, R1575, R1576, R1577, R1578, R1579, R1580, R1581, R1582, R1583, R1584, R1585, R1586, R1770, R1771, R1772, R1773, R1774, R1775, R1776, R1777, R1778, R1779, R1780, R1781, R1782, R1786, R1787, R1788, R1789, R1790, R1791, R1792, R1793, R1794, R1795, R1796, R1797, R1798, R1799, R1800, R1801, R1802, R1803, R1804, R1805, R1806, R1807, R1808, R1809, R1810, R1811, R1812, R1813, R1814, R1815, R1816, R1855, R1856, R1857, R1858, R1865, R1866, R1871, R1889, R1936, R1940, R2085, R2092, R2093, R2099, R2103, R2104, R2105, R2106, R2107, R2113, R2126, R2127, R2315, R2316, R2317, R2318, R2430, R2431, R2432, R2433, R2434, R2435, R2436, R2437, R2438, R2439, R2440, R2441, R2442, R2443, R2444, R2445, R2446, R2447, R2448, R2449, R2450, R2451, R2452, R2453, R2454, R2455, R2456, R2457, R2458, R2459, R2460, R2461, R2462, R2600, R2601, R2602, R2604, R2605, R2606, R2607, R2608, R2609, R2610, R2611, R2612, R2613, R2614, R2615, R2616, R2617, R2618, R2619, R2625, R2627, R2630, R2631, R2632, R2633, R2635, R2636, R2637, R2638, R2641, R2483, R2484, R2485, R2645, R2646, R2650, R2651, R2652, R2653, R2654, R2655, R2656, R2657, R2658, R2659, R2663, R2662, R2716, R2717, R2718, R2719, R2722, R2723, R2724, R2725, R2726, R2727, R2720, R2721, R2744, R2745, R2755, R2756, R2757, R2758, R2782, R2784, R2785, R2786, R2787, R2788, R2789, R2790, R2794, R2795, R2796, R2805, R2801, R2838, R2839, R2840, R2841, R2844, R2845, R2849, R2880, R2887, R2888, R2953, R2956, R2957, R2958, R2960

Command-line interface. Parses flags, detects running server,
dispatches operations via proxy or cold-start.

## Knows
- dbPath: string — database directory (from --dir flag, default ~/.ark/)
- verbosity: int — global verbose level (0–4), parsed before subcommand dispatch
- command: string — subcommand name
- flags: parsed flag values

## Does
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
- CleanStaleSocket(dbPath): if connect fails, remove socket file
- Each subcommand: init, setup, rebuild, add, remove, scan, refresh, search,
  serve, status, files, stale, missing, dismiss, config, unresolved, resolve,
  tag (with sub-subcommands: list, counts, files, set, get, check),
  ui (with sub-subcommands: run, display, event, checkpoint, audit,
  status, open, install),
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
- cmdRebuild: refuse if server running. Delete data.mdb and lock.mdb,
  re-run init (which writes fresh I records from ark.toml, clearing
  all E records), then scan. (R1542, R1568)
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
  falls back to local LMDB search if server unavailable or proxy fails.
  Server keeps caches warm (file name map, LMDB pages), avoiding
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
- cmdTagDefs: query D records from LMDB. Optional tag name args filter.
  Default: `tagname description`, deduplicated, sorted alphabetically.
  --path: `path tagname description`, lexically sorted, not deduplicated,
  spaces in paths backslash-escaped.
  Uses server proxy when available, falls back to withDB.
- cmdFetch: verify indexed, output raw file content to stdout.
  --wrap <name> wraps output in XML tags.
- cmdChunks: context expansion around search hits. Takes path, range,
  optional -before/-after counts. Calls microfts2.GetChunks() via
  withDB. Outputs JSONL (one object per chunk: path, range, content,
  index). Supports --wrap for XML wrapping.
- cmdFiles: list indexed files. Positional glob args filter the output.
  --filter-files/--exclude-files set the base file set; positional
  globs narrow within it. --status adds G/S/M status, bytes (os.Stat),
  and chunk count (DB.AllChunks) columns. -v with --status shows
  per-file chunk size stats (min/max/mean/median/p90/p95) as an
  indented detail line. --tokenize measures chunk sizes in tokens.
  Missing files show 0 for bytes and chunks, skip verbose line.
  (R1573-R1586)
- cmdStatus: --db flag triggers DB.StatusDB() and prints record counts
  per subdatabase. Without --db, unchanged. When proxied, sets
  ?db=true query parameter. (R2473, R2474, R2475, R2476, R2477, R2480)
  --chunks activates chunk size statistics. Iterates files (scoped by
  --filter-files/--exclude-files), calls DB.ChunkStats() to collect
  sizes, prints right-aligned table with "all" row + per-strategy rows.
  Columns: strategy, count, min, max, mean, median, p90, p95, p99.
  Unit is bytes (default) or tokens (--tokenize). --tokenize requires
  configured tag_model; creates a minimal Librarian tokenizer context.
  Zero chunks prints "no chunks found". (R1514-R1531)
  After normal output, if E records exist, print a "warnings:" section
  with each condition name, description, and remediation advice. (R1565,
  R1566, R1567)
- cmdServe: if server already running, print message to stderr, exit 0.
  --force flag: passed to Server.Serve as opts.Force. Clears E records
  and accepts current config even with deferred changes. (R1558)
- cmdConfigRecover: read stored config from I records via DB, write to
  ark.toml. Disaster recovery for corrupted/missing config. (R1569)
- cmdStop: read PID file, verify process is ark, send SIGTERM (or
  SIGKILL with -f), poll until exit. Exit 1 on timeout.
- cmdSourcesCheck: expand globs, add new dirs, report MIA/orphan.
  Output: +/−/? prefix per line. Proxies to server if running.
- cmdUI: gateway for all UI operations. Reads mcp-port/ui-port from
  dbPath. No subcommand → open browser. Subcommands:
  run, display, event, checkpoint, audit, status, open, reload, install.
  Each subcommand sends HTTP requests to the mcp-port.
  Replaces the `.ui/mcp` shell script — one binary, no separate script.
- cmdUIStatus: output "ui: running (port N)" + browser count + indexing
  state. When UI not running: "ui: not available". Queries server
  via GET /status for UI fields.
- cmdSetup: extract bundled UI assets to dbPath using
  bundle.ExtractBundle, run linkapp, install global skills
  (~/.claude/skills/ark/, ~/.claude/skills/ui/) and agent
  (~/.claude/agents/ark.md) from bundled assets. Idempotent.
- cmdInit: seed case_insensitive/aliases from ark.toml if present;
  CLI flags override seeded values. Runs setup first if ~/.ark/
  not bootstrapped (no html/ dir). --no-setup skips setup.
  --if-needed skips DB creation when data.mdb already exists.
  Without --if-needed, removes existing data.mdb and lock.mdb
  before creating a fresh database. Creates ~/.ark/searching/
  with default CLAUDE.md if not present. (R1252)
- cmdUIInstall: single entry point for per-project setup. Runs
  init --if-needed internally. Starts server if not running.
  Creates .claude/skills/ and .claude/agents/ dirs if needed.
  Creates symlinks in .claude/skills/ pointing to ~/.ark/skills/,
  symlink for .claude/agents/ark.md pointing to ~/.ark/agents/ark.md.
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
  Flags: --project (filter @to-project), --from (filter @from-project),
  --all (include completed/done/denied), --include-archived (include archived),
  --counts (output status counts instead of rows),
  --unmatched (show only requests with no matching response).
  Default: filter out completed/done/denied and archived.
  --unmatched groups entries by requestId, keeps requests where no
  response shares the same requestId. Applied after all other filters.
  Output includes bookmark lag field: pair entries by requestId,
  compare each side's handled tag against counterpart's status.
  Lag format: `lag:PROJECT:STATUS`; empty when current.
  Sort @status:open first then by path, output tab-separated lines.
  With --counts: output status\tcount lines sorted alphabetically.
  Summary field uses `@issue` for requests, `ark-response:<id>` for responses.
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
- cmdScheduleSearch: query Store.QueryDayBuckets for date range.
  START END as flexible dates (dateparse). Flags: --tag (filter),
  --gaps (only unacked past events), --json (JSON output).
  Default output is markdown crank-handle style. Renders
  suppressed events with `[suppressed]` prefix. Proxies to server.
  Time-range render collapses when start==end and drops the trailing
  `: ` when the summary is empty, so zero-duration ticks don't print
  as `- 13:45–13:45:`. (R914-R920, R2845, R2849)
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
  trailing : before sending to server.
- cmdListen: long-poll for tag notifications. Outputs markdown crank
  handles. Proxies to server.

- cmdEmbed: dispatches to text, bench, validate sub-subcommands.
  No subcommand → print usage, exit 0.
- cmdEmbedText: join positional args with spaces, embed via
  Librarian.EmbedQuery, print JSON vector to stdout. Requires
  tag_model. Uses withDB.
- cmdEmbedBench: dispatches by MODE arg (tags or chunks).
  --ctx N (default 2048), --parallel N (default 8).
- cmdEmbedBenchTags: collect all tag values from LMDB, embed via
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
- cmdRecall: entry point for the recall subcommand (R2617). Parses CLI flags: --k (default 20, clamped to [1, 200]), --type (auto, chunk, text), --no-content, and --json (R2627). Normalizes positional inputs (R2618, R2619). If the server is running, proxies to GET/POST /recall (R2630). If the server is not running, checks config for tag_model (R2631): if configured (and exists), exits non-zero warning the user to start server (R2632); if no model, opens the DB locally in-process via withDB and runs the trigram-only recall (R2633, R2634). Outputs markdown stencil by default (R2635), showing warnings (R2637) or "_no results_" (R2636), or prints raw JSON if --json is set (R2638). Clamps K parameter (R2641).
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
