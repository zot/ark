# Server
**Requirements:** R4, R61, R62, R63, R64, R65, R66, R67, R68, R69, R70, R89, R90, R91, R92, R93, R94, R95, R96, R97, R98, R99, R100, R101, R102, R132, R133, R134, R152, R153, R154, R155, R156, R160, R164, R170, R171, R175, R176, R177, R165, R202, R204, R210, R211, R212, R213, R229, R257, R264, R265, R266, R267, R268, R269, R270, R271, R272, R261, R262, R263, R338, R339, R342, R343, R344, R345, R346, R347, R348, R349, R350, R351, R352, R353, R354, R355, R356, R357, R358, R359, R387, R388, R389, R390, R391, R393, R394, R395, R410, R411, R412, R414, R415, R416, R417, R419, R420, R437, R438, R440, R441, R439, R541, R542, R543, R544, R545, R546, R563, R564, R565, R566, R567, R568, R569, R570, R571, R620, R623, R641, R648, R657, R658, R659, R660, R661, R662, R685, R686, R687, R688, R689, R690, R691, R735, R736, R737, R748, R749, R750, R758, R759, R760, R761, R762, R763, R764, R767, R768, R769, R770, R771, R772, R773, R774, R775, R776, R777, R789, R790, R799, R804, R805, R812, R813, R835, R836, R837, R838, R839, R840, R841, R842, R843, R844, R845, R846, R847, R848, R893, R894, R895, R896, R897, R898, R914, R917, R919, R920, R921, R923, R927, R928, R929, R930, R931, R932, R961, R962, R963, R975, R976, R977, R906, R990, R991, R992, R994, R1014, R1015, R1069, R1070, R1071, R1072, R1073, R1074, R1075, R1076, R1077, R1078, R1079, R1080, R1081, R1082, R1083, R1084, R1085, R1086, R1087, R1088, R1089, R1090, R1091, R1092, R1093, R1098, R1111, R1112, R1151, R1152, R1153, R1154, R1155, R1156, R1157, R1158, R1159, R1160, R1161, R1162, R1163, R1164, R1165, R1166, R1167, R1168, R1169, R1170, R1171, R1172, R1173, R1174, R1175, R1176, R1177, R1178, R1179, R1180, R1181, R1182, R1183, R1184, R1185, R1186, R1187, R1188, R1189, R1190, R1191, R1192, R1193, R1194, R1195, R1196, R1197, R1198, R1199, R1216, R1217, R1218, R1219, R1220, R1221, R1225, R1226, R1227, R1228, R1229, R1230, R1231, R1232, R1233, R1234, R1243, R1248, R1249, R1266, R1267, R1292, R1295, R1297, R1300, R1378, R1383, R1402, R1403, R1404, R1405, R1423, R1424, R1425, R1426, R1427, R1428, R1429, R1430, R1431, R1432, R1469, R1485, R1486, R1487, R1488, R1489, R1495, R1496, R1499, R1501, R1504, R1505, R1506, R1509, R1510, R1511, R1512, R1513, R1556, R1557, R1558, R1559, R1560, R1561, R1562, R1563, R1564, R1703, R1739, R1740, R1761, R1783, R1784, R1785, R1859, R1860, R1889, R1979, R1980, R1981, R1982, R2065, R2073, R2074, R2075, R2076, R2078, R2079, R2080, R2085, R2086, R2089, R2091, R2117, R2129, R2258, R2259, R2260, R2261, R2262, R2263, R2264, R2265, R2266, R2267, R2268, R2269, R2270

HTTP server on Unix domain socket. Highlander (one per database).
Keeps embedding model warm. Runs reconciliation on startup and
after config changes. Watches filesystem for changes.
Optionally starts the embedded ui-engine alongside.

## Knows
- db: *DB — ark database facade
- listener: net.Listener — Unix socket
- pidPath: string — PID file location
- noScan: bool — skip startup reconciliation
- verbosity: int — verbose level (0–4), propagated to Logv and ui-engine
- uiRuntime: *flib.Runtime — embedded Frictionless runtime (nil if UI disabled/failed)
- watcher: *fsnotify.Watcher — filesystem watcher (nil if watching disabled)
- reconcileCh: removed — reconcile closures go through DB actor (R990)
- ignoredPaths: map[string]struct{} — negative cache of non-indexable paths, cleared on config reload
- uiPort: int — HTTP port the ui-engine is listening on (0 if not started)
- sessions: map[string]*Session — named sessions, autocreated on demand (mutex-protected)
- pubsub: *PubSub — subscription registry and notification delivery
- scheduler: *EventScheduler — time-based event queue
- librarian: *Librarian — Haiku co-process for spectral search expansion

## Does
- Serve(dbPath, opts): bind socket (highlander lock), write PID file,
  open DB, ensure ~/.ark source. After Open, check for deferred config
  changes or unresolved E records — if found and !opts.Force, error out
  with diagnostic. If opts.Force, clear E records and accept current
  config. Apply fix-minimal and benign changes. Start watches, run
  Reconcile, start ui-engine (propagate opts.Verbosity to
  cfg.Logging.Verbosity), start HTTP server. Store opts.Verbosity for
  Logv calls. (R1556, R1557, R1558, R1559, R1560)
- Reconcile(): sources-check → scan → refresh. Idempotent. Runs in
  background goroutine. If already running, waits then runs again.
  Called by: startup, config mutation handlers, ark.toml fsnotify.
  On ark.toml reload: diff new config against I records, apply
  classified changes (deferred → E record + log, fix-minimal → apply,
  benign → update I records), then proceed with reconcile using the
  effective config. (R1561, R1562, R1563, R1564)
- StartWatching(dirs): add fsnotify watches for source directories
  and ark.toml. Recursive — walks subdirectories. Starts the
  throttled event loop goroutine.
- StopWatching(dirs): remove fsnotify watches for removed sources
- handleFileEvent(path): throttled on-notify — immediate index on
  first event, then throttle window. During window, accumulates
  specific changed/removed paths. Window expiry sends one closure
  to DB actor processing only those paths (R991). Max wait ceiling
  prevents starvation. Full reconcile on config change/startup (R992).
- isIgnored(path): check negative cache, then DB.IsIndexable if miss.
  Non-indexable paths are cached. Directory events and ark.toml bypass.
- clearIgnoredPaths(): reset the negative cache — called on config reload
- EnsureArkSource(): ensure ~/.ark is a source — hardcoded, not in
  ark.toml, cannot be removed. Scoped with include patterns:
  `["ark.toml", "schedule/**", "apps/**", "storage/**"]`. (R961, R962)
- CheckScheduleConfig(): compare current [schedule] config with stored
  version in LMDB. On change: re-scan affected files, re-materialize
  day buckets. Includes filter/lifecycle changes. (R975, R976, R977)
- StartUIEngine(dbPath): configure ui-engine (Dir=dbPath), start in
  goroutine. On failure, log error and continue without UI.
- BindSocket(path): create Unix domain socket, fail if already bound
- WritePID(path): write PID file for emergency kill
- CleanupStaleSocket(path): remove socket file if exists and no server
- HandleSearch, HandleAdd, HandleRemove, HandleScan, HandleRefresh,
  HandleStatus, HandleFiles, HandleStale, HandleMissing, HandleDismiss,
  HandleConfig, HandleUnresolved, HandleResolve: HTTP route handlers,
  each delegates to DB methods, returns JSON.
  HandleStatus: when ?db=true, also calls DB.StatusDB() and merges
  record counts into the JSON response. (R906)
  HandleSearch: if `onlyIfTmp` is set and `HasTmp()` is false, return
  HTTP 204 (no content) — CLI proceeds with local search. If `noTmp`
  is set, apply `WithNoTmp()` search option. Accepts optional
  `chunk_filters` field — wires through BuildChunkFilters as
  chunk-level post-filters (R1783, R1784, R1785).
- HandleTags: GET /tags — list all tags
- HandleTagCounts: POST /tags/counts — counts for specified tags
- HandleTagFiles: POST /tags/files — files for specified tags
- HandleConfigAddSource: POST /config/add-source
- HandleConfigRemoveSource: POST /config/remove-source
- HandleConfigAddInclude: POST /config/add-include
- HandleConfigAddExclude: POST /config/add-exclude
- HandleConfigRemovePattern: POST /config/remove-pattern
- HandleConfigShowWhy: POST /config/show-why
- HandleFetch: POST /fetch — return file content for indexed path
- HandleTmpAdd: POST /tmp/add — add tmp:// document (path, strategy, content)
- HandleTmpUpdate: POST /tmp/update — update existing tmp:// document
- HandleTmpRemove: POST /tmp/remove — remove tmp:// document
- HandleTmpList: GET /tmp/list — list all tmp:// paths
- HandleSourcesCheck: POST /config/sources-check — run glob reconciliation
- RegisterLuaFunctions(): called after flib.Start(). Uses
  flib.WithLua (passive path) to register Go functions on the
  Lua mcp table:
  - mcp.search_grouped(query, opts) — calls Searcher.SearchGrouped,
    returns results as nested Lua tables. Registered as plain function
    (dot syntax, not colon — no self parameter). opts supports mode
    (combined/contains/about/regex), k, filter_files, exclude_files,
    filter_file_tags, exclude_file_tags, filter, except. Array-valued
    fields accept Lua string or table via luaStringSlice helper.
  - mcp:open(path) — open indexed file with system viewer
    (xdg-open on Linux, open on macOS). Returns immediately.
    Error if not indexed.
  - mcp:indexing() — returns Lua array of source dirs currently
    being indexed. Empty table when idle.
  - mcp:inbox(show_all) — returns Lua table of InboxEntry records
    from the tag index. Calls DB.Inbox(). Each entry includes requestId,
    kind, responseHandled, requestHandled, and statusDate fields
    alongside status, to, from, summary, path.
  - mcp:sort(table, property, isDate, descending) — sort a Lua array
    of tables in place by a named field. Case-insensitive string
    comparison; nil/missing values sort to end. Returns the table.
  - mcp:parseJson(str) — parse JSON string into Lua table.
    Uses jsonToLua recursive converter.
  - mcp:readJsonFile(path) — read file, parse JSON into Lua table.
  - mcp.tmp_add(path, content, strategy) — add tmp:// document
  - mcp.tmp_update(path, content, strategy) — update tmp:// document
  - mcp.tmp_remove(path) — remove tmp:// document
  - mcp.tmp_list() — list all tmp:// paths
  - mcp.setTags(path, tags) — bulk tag write. Reads file, parses
    tag block, sets each key/value pair, auto-sets status-date when
    setting status, writes file back. Dot syntax, no self.
  - mcp.readMessage(path) — read message file. Returns Lua table
    with tags (name/value pairs from tag block only) and html (body
    rendered via goldmark). Dot syntax, no self.
  - mcp:scheduled(startDate, endDate) — query day-bucket index for
    items overlapping the date range. Returns Lua array of tables
    with date, endDate, tag, summary, path, recurring, allDay. (R893)
  - mcp:reschedule(path, tag, newDate, newEndDate) — rewrite date
    in tag value, preserve trailing text, re-index. (R894)
  - mcp:tagComplete(prefix) — tag name/value completions from
    the index (D records + tag values). (R895)
  - mcp:fileStatus(path) — indexed? tags? schedule info? (R896)
  - mcp:subscribe(opts, callback) — UI-side tag-change subscription.
    opts: tag, value (RE2), filterFiles, exceptFiles. Callback fires
    on matching events. (R897, R898)
  - mcp:listSource(sourcePath, prototype) — list one directory level
    within a configured source. Returns Lua array of entry tables
    with name, relPath, fullPath, isDir, state, whyPatterns,
    whySources, whyConflict, isMissing, hasIgnoreFile. Classification
    uses Config.ShowWhy logic in-process. Missing files from DB
    included at listed level. If prototype is non-nil, all entries
    go through session:create for mutation tracking. Checks once
    for `new` in prototype chain: if present, calls
    prototype:new(table) instead (session:create + init). Entries
    sorted dirs-first then alphabetically.
  - mcp.suggestTagNames(chunkID, k) — thin wrapper over
    Librarian.SuggestTagNames. Returns Lua array of suggestion tables
    {tag, score, motivatingFiles=[{fileID, path, score}, ...]}.
    Empty result → empty table; error → (nil, errstring). Read-only,
    Sync read txn. (R2258, R2266-R2270)
  - mcp.chunksForTag(tag, k) — thin wrapper over
    Librarian.ChunksForTag. Returns Lua array of suggestion tables
    {chunkID, fileID, path, score, motivatingDefs=[{fileID, path,
    score}, ...]} ranked by max-aggregate score across the tag's
    defs. Same shape as mcp.topKChunksForTag — swappable. Read-only.
    (R2259, R2266-R2270)
  - mcp.chunksForTagDef(tag, fileID, k) — thin wrapper over
    Librarian.ChunksForTagDef. Same shape as mcp.chunksForTag;
    motivatingDefs has length 1 (the requested definition file).
    Read-only. (R2260, R2266-R2270)
  - mcp.topKChunksForTag(tag, k) — thin wrapper over
    Librarian.TopKChunksForTag. HC-cached read with the alibi-stamp
    staleness filter (R2218, R2219). Same shape as mcp.chunksForTag.
    Read-only. (R2261, R2266-R2270)
  - mcp.relatedTags(tag, k) — thin wrapper over Librarian.RelatedTags.
    Returns Lua array of {tag, score, srcFileID, srcPath, dstFileID,
    dstPath} ranked by ED↔ED cosine. Read-only. (R2262, R2266-R2270)
  - mcp.tagPairConflict(tagA, tagB) — thin wrapper over
    Librarian.TagPairConflict. Returns a single Lua table
    {tag="", score, srcFileID, srcPath, dstFileID, dstPath} (tag empty
    because both tag names are inputs; src/dst identify the
    best-matching definition file from each side). Read-only.
    (R2263, R2266-R2270)
  - mcp.tagDrift(tag) — thin wrapper over Librarian.TagDrift. Returns
    Lua array of {fileIDA, pathA, fileIDB, pathB, score} (fileIDA <
    fileIDB by convention) sorted by score descending. Read-only.
    (R2264, R2266-R2270)
  - mcp.sweepHotCorrelations() — thin wrapper that triggers the
    corpus-wide sweep through enqueueWrite, identical to HTTP POST
    /sweep/correlations. Returns HCSweepResult table {startedAt
    (RFC3339), completedAt (RFC3339), durationMs, changedEDs,
    changedECs, tagsRebuilt, tagsTouched, orphanTotal, fromScratch}.
    Returns {status = "embedding-unavailable"} when the embedding
    model is missing, matching the HTTP path's degraded reply. The
    one writer in this bridge set. (R2265, R2270)
- currentlyIndexing(): returns []string of source dirs with active
  scan or refresh in progress. Read by HandleIndexing and the
  mcp:indexing() Lua function.
- ReloadUIEngine(): stop current ui-engine, restart on same port.
  Reads uiPort saved from initial start. If port unavailable, fall
  back to new port and log warning. Re-registers Lua functions after
  restart.
- UIStatus(): returns struct with running bool, port int, browser
  count int, indexing state. Browser count queried from ui-engine
  server connection state.
- HandleUIStatus: enriches GET /status with UI fields (port, browsers,
  indexing) when ui-engine is running.
- SetupLogging(dbPath): create ~/.ark/logs/ dir, open log file, truncate if >10MB (keep last 1MB), set log.SetOutput to MultiWriter(stderr, file)
- GetOrCreateSession(name): look up session by name, create if not
  found. Session receives DB reference and TTL from config. Mutex
  protects the map; session actor runs in its own goroutine.
- HandleScheduleSearch: POST /schedule/search — query day buckets, return events with ack status (R914-R920)
- HandleScheduleChange: POST /schedule/change — rewrite date in tag, re-index (R921-R925)
- CheckScheduleConfig(): on startup and config reload, compare current
  [schedule] vs stored in LMDB. If different, re-materialize day buckets
  for affected tags. Store new config after. (R927-R932)
- HandleSubscribe: POST /subscribe — add or cancel subscriptions, delegates to PubSub
- HandleListen: GET /listen — long-poll for notifications, delegates to PubSub.Listen + FormatMarkdown
- StartPubSub(): create PubSub, start reaper ticker (1 minute)
- StartScheduler(): create EventScheduler, call ScanDayBuckets to
  read upcoming events from LMDB, crank forward any expired recurring
  events, add quarter chime, fire overdue events, set timer to head.
  (R874, R875, R876)
- HandleSearchGrouped: POST /search/grouped — grouped search with
  content + contentType + preview in each chunk. Accepts query, mode,
  k, session, filter/exclude options. Delegates to SearchGrouped.
  Also accepts optional structured tag query fields (name_tokens,
  value_tokens, name_match, value_match) — when present, resolves
  matching tag names via Store.MatchTagNames, values via
  Store.MatchTagValues, builds a regex query OR'ing resolved pairs,
  and optionally uses V record file IDs as WithOnly prefilter.
  (R1069-R1075, R1469)
- HandleTagComplete: POST /tags/complete — tag name completion from
  D records. Accepts prefix, returns {name, description} array.
  Empty prefix returns all tags from T records with D descriptions.
  Deduplicates by tag name. (R1076-R1080)
- HandleTagValues: POST /tags/values — tag value completion. Accepts
  tag + prefix, returns {value, count} array. Delegates to
  Store.QueryTagValues for O(1) LMDB lookup. (R1081-R1085, R1111)
- HandleSave: POST /save — write file + re-index. Validates path
  is within indexed source. Writes content, triggers single-file
  refresh. (R1086-R1089)
- HandleSetTags: POST /set-tags — atomic tag block update via HTTP.
  Same logic as Lua mcp.setTags: read file, parse tag block, set
  tags, auto-set status-date, write back. (R1090-R1093)
- HandleShowInFolder: POST /file/show — open native file manager with
  file selected. Linux: gdbus FileManager1.ShowItems. macOS: open -R.
  Windows: explorer /select. Validates path in source. (R1216-R1221)
- RegisterContentRoutes(): called after flib.Start() succeeds.
  Registers three GET routes on the UI server via UIHandleFunc:
  - GET /fetch/PATH — JSON content retrieval ({path, content, contentType}).
    contentType from strategy mapping. (R1157-R1159)
  - GET /content/PATH — rich HTML presentation. Markdown: server-side
    goldmark rendering with link/image rewriting, plus a pencil/eye
    toggle button that lazy-loads ink-mde editor on demand via the
    ark-markdown-editor.js bundle. Other types: chunked view —
    reads all chunks via ChunkCache, renders each as
    `<div class="ark-chunk">` with per-chunk wrapTagElements.
    JSONL chunks rendered through goldmark. PDF chunks grouped by
    `page` — one `<pdf-chunk>` per page covering the full page,
    carrying every `tag_rects` entry from every chunk on that page,
    producing all `<ark-tag>` overlays on the rendered page (R1739,
    R1740). Groups consecutive same-role chunks in `ark-role-group`
    wrappers with sticky icon headers (👤/🤖). Skill groups use
    `<details>` collapse with 📋 + skill name. Single-chunk views
    get role border/icon without grouping. Falls back to raw `<pre>`
    if no chunks exist.
    (R1160-R1164, R1168-R1189, R1495-R1496, R1499, R1504-R1513, R1739, R1740)
  - GET /raw/PATH — raw file bytes with mime-type Content-Type
    header. (R1165-R1167)
  All three validate path is within a source dir (R1154-R1156). Registered on
  the UI server HTTP port only (R1151-R1153).
- HandleSearchExpand: POST /search/expand — query expansion via
  Librarian. Accepts {mode, name, value}, returns {alternatives}.
  Returns 503 if claude not available. (R1243-R1247)
- StartLibrarian(): check for claude on PATH (exec.LookPath),
  create Librarian with TTL from config. Called during Serve().
  (R1248-R1250)
- EmbedAfterReconcile(): enqueue a write that calls
  Librarian.BatchEmbed(). Queued after the reconcile-complete
  write closure so embeddings run after indexing finishes. The
  embedding model loads eagerly at this point. (R1292, R1295)
- HandleEmbedMatch: POST /search/expand/embed — embedding
  similarity search. Accepts {query, k}, delegates to
  Librarian.EmbedSimilarTagValues. Returns 503 if embedding
  not available. (R1297, R1300)
- wrapTagElements(html, db): post-process rendered HTML to wrap `@tag: value`
  patterns in `<ark-tag><name>TAG</name> <value>VALUE</value></ark-tag>`
  elements. Matches tag names `[a-zA-Z][\w.-]*`, values to end of line
  or `<br>`. Skips tags already inside `<ark-tag>`. Must not match inside
  HTML attributes. When `name == "link"` and `db != nil`, calls
  `db.ResolveLink(value)`: a resolved value renders as
  `<a class="ark-link" href="/content/PATH?range=LOC">@link: VALUE</a>`,
  unresolved values fall back to `<ark-tag class="ark-link-broken">`.
  Applied to both goldmark output and HTML-escaped plain text in
  handleContentView. (R1485-R1489, R1979, R1980, R1981, R1982)
- enrichContent(html, chunks, db): tag-overview enrichment of the
  /content/ response — emits `<ark-ext-tags>` containers at the
  top of any chunk that has ext routings, with one `<ark-tag>`
  child per ext-routed tag carrying `externalFile` and
  `externalTarget` attributes (R2065, R2073). Also assigns `id`
  attributes to inline `<ark-tag>` elements and to `<h1>`–`<h6>`
  headings for sidebar DOM anchoring (R2074, R2075). For PDF
  content, emits `<ark-heading rect="...">` elements positioned
  over `<pdf-chunk>` for each Heading-kind chunk (R2076). All
  inline in the existing `/content/` response — no separate
  endpoint (R2078). Consults the inline tag store via
  `tagsForChunk` and `chunkToTargets` (ext routings) plus
  chunker / pdftext heading data (R2079).
- tagsForChunk(chunkID): new Go method — returns the inline tags
  for a single chunk. Used by enrichContent for per-chunk
  ext-tag and inline-tag enumeration. No equivalent method
  exists today (R2080).
- Signal handling: catch SIGTERM, stop watcher, stop scheduler,
  kill librarian, shut down ui-engine, close listener, close DB, exit 0
- Never remove PID file (stale PID is safe — stop verifies before kill)

## Collaborators
- Session: named closure actors for cross-query caching
- DB: all operations delegated through the facade
- Scanner: reconciliation scan
- Indexer: reconciliation refresh, append detection
- Config: RemoveSource guard for ~/.ark
- fsnotify: filesystem change detection
- ui-engine (cli.Server): embedded UI server, started alongside ark API
- flib.Runtime: WithLua for passive Lua function registration
- PubSub: subscription registry and notification delivery
- EventScheduler: time-based event queue
- Librarian: Haiku co-process for spectral query expansion
- TagBlock: parse/set/render tag blocks for setTags and readMessage
- goldmark: markdown→HTML rendering for readMessage body and /content/ view
- ArkTagElement: server wraps tag patterns in `<ark-tag>` elements for content pages

## Sequences
- seq-server-startup.md
- seq-reconcile.md
- seq-file-change.md
- seq-session-search.md
- seq-pubsub.md
- seq-editor-endpoints.md
- seq-content-fetching.md
- seq-spectral-expand.md
- seq-ark-tag-click.md
