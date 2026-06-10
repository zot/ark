# Server
**Requirements:** R4, R61, R62, R63, R64, R65, R66, R67, R68, R69, R70, R89, R90, R91, R92, R93, R94, R95, R96, R97, R98, R99, R100, R101, R102, R132, R133, R134, R152, R153, R154, R155, R156, R160, R164, R170, R171, R175, R176, R177, R165, R202, R204, R210, R211, R212, R213, R229, R257, R264, R265, R266, R267, R268, R269, R270, R271, R272, R261, R262, R263, R338, R339, R342, R343, R344, R345, R346, R347, R348, R349, R350, R351, R352, R353, R354, R355, R356, R357, R358, R359, R387, R388, R389, R390, R391, R393, R394, R395, R410, R411, R412, R414, R415, R416, R417, R419, R420, R437, R438, R440, R441, R439, R541, R542, R543, R544, R545, R546, R563, R564, R565, R566, R567, R568, R569, R570, R571, R620, R623, R641, R648, R657, R658, R659, R660, R661, R662, R685, R686, R687, R688, R689, R690, R691, R735, R736, R737, R748, R749, R750, R758, R759, R760, R761, R762, R763, R764, R767, R768, R769, R770, R771, R772, R773, R774, R775, R776, R777, R789, R790, R799, R804, R805, R812, R813, R835, R836, R837, R838, R839, R840, R841, R842, R843, R844, R845, R846, R847, R848, R893, R894, R895, R896, R897, R898, R914, R917, R919, R920, R921, R923, R927, R928, R929, R930, R931, R932, R961, R962, R963, R975, R976, R977, R2480, R990, R991, R992, R994, R1014, R1015, R1069, R1070, R1071, R1072, R1073, R1074, R1075, R1076, R1077, R1078, R1079, R1080, R1081, R1082, R1083, R1084, R1085, R1086, R1087, R1088, R1089, R1090, R1091, R1092, R1093, R1098, R1111, R1112, R1151, R1152, R1153, R1154, R1155, R1156, R1157, R1158, R1159, R1160, R1161, R1162, R1163, R1164, R1165, R1166, R1167, R1168, R1169, R1170, R1171, R1172, R1173, R1174, R1175, R1176, R1177, R1178, R1179, R1180, R1181, R1182, R1183, R1184, R1185, R1186, R1187, R1188, R1189, R1190, R1191, R1192, R1193, R1194, R1195, R1196, R1197, R1198, R1199, R1216, R1217, R1218, R1219, R1220, R1221, R1225, R1226, R1227, R1228, R1229, R1230, R1231, R1232, R1233, R1234, R1243, R1248, R1249, R1266, R1267, R1292, R1295, R1297, R1300, R1378, R1383, R1402, R1403, R1404, R1405, R1423, R1424, R1425, R1426, R1427, R1428, R1429, R1430, R1431, R1432, R1469, R1485, R1486, R1487, R1488, R1489, R1495, R1496, R1499, R1501, R1504, R1505, R1506, R1509, R1510, R1511, R1512, R1513, R1556, R1557, R1558, R1559, R1560, R1561, R1562, R1563, R1564, R1703, R1739, R1740, R1761, R1783, R1784, R1785, R1859, R1860, R1889, R1979, R1980, R1981, R1982, R2065, R2073, R2074, R2075, R2076, R2078, R2079, R2080, R2085, R2086, R2089, R2091, R2117, R2129, R2258, R2259, R2260, R2261, R2262, R2263, R2264, R2265, R2266, R2267, R2268, R2269, R2270, R2277, R2280, R2282, R2288, R2289, R2290, R2291, R2292, R2293, R2294, R2296, R2297, R2298, R2299, R2300, R2301, R2305, R2308, R2313, R2322, R2323, R2324, R2325, R2355, R2356, R2358, R2360, R2361, R2363, R2364, R2379, R2386, R2389, R2390, R2391, R2393, R2395, R2396, R2397, R2398, R2399, R2400, R2401, R2402, R2404, R2405, R2406, R2408, R2410, R2411, R2412, R2413, R2414, R2415, R2421, R2426, R2432, R2433, R2434, R2435, R2436, R2437, R2438, R2439, R2440, R2441, R2442, R2443, R2444, R2445, R2446, R2447, R2448, R2449, R2450, R2451, R2452, R2453, R2454, R2455, R2456, R2472, R2628, R2629, R2630, R2660, R2661, R2687, R2688, R2694, R2695, R2700, R2701, R2713, R2714, R2715, R2787, R2788, R2794, R2795, R2805, R2780, R2842, R2843, R2955

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
- recallWatcher: *RecallWatcher — ambient simple-recall subsystem; nil when `[recall].enabled` is false (R2687, R2688)

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
  record counts into the JSON response. (R2480)
  HandleSearch: if `onlyIfTmp` is set and `HasTmp()` is false, return
  HTTP 204 (no content) — CLI proceeds with local search. If `noTmp`
  is set, apply `WithNoTmp()` search option. Accepts optional
  `chunk_filters` field — wires through BuildChunkFilters as
  chunk-level post-filters (R1783, R1784, R1785).
  HandleAdd: maps `DB.Add`'s `ErrFileOutsideSource` sentinel to HTTP
  400 (client error — unknown how to chunk a file outside any source
  with no `--strategy`); other errors stay 500. (R2955)
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
- HandleSubscribers: GET /subscribers — return the count for a
  `(tag, value)` query by calling `PubSub.SubscriberCount` (R2805).
- HandleMonitorControl: POST /monitor/control — append one
  `kind: "pause"` or `"resume"` record to
  `~/.ark/monitoring/<class>.jsonl` via the write actor (R2787,
  R2788). The handler enforces the state-already-set guard before
  the append.
- HandleLuhmannRecord: POST /luhmann/record — append one supervisor
  record (`spawn` / `exit` / `crash`) to
  `~/.ark/monitoring/luhmann.jsonl` via the write actor (R2794,
  R2795). The handler computes `crashes` by tailing the log if the
  caller doesn't override.
- During `Serve()` startup (before `Reconcile`), call
  `scheduler.EnsureChimesFile()` so `~/.ark/chimes.md` exists when
  `ScanScheduleLogs` runs (R2780).
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
  - mcp.tmp_get(path) — thin wrapper over `DB.TmpContent`.
    Returns the stored content of an existing tmp:// document as
    a Lua string on success, or `(nil, errstring)` on failure
    (missing `tmp://` prefix, document absent from the overlay,
    Sync error). Read-only; no overlay mutation. Used by the
    curation workshop's Find-Connections-as-service flow to read
    a result document's body on terminal-status transitions.
    (R2406)
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
  - mcp.sweepHotCorrelationsAsync() — fire-and-forget variant.
    Enqueues the same corpus-wide sweep through enqueueWrite but
    does not wait for the result; returns nothing. The Lua VM is
    released immediately. Callers observe progress and terminal
    state via the existing `tmp://sweep/hot-correlations.md`
    document (subscribe to `@sweep-status` through `mcp.subscribe`
    before invoking). Multiple async calls queue serially through
    the write actor; UI callers debounce if rapid clicks become a
    problem. (R2408, R2410)
  - mcp.subscribe(sessionID string, filter table) — register a
    subscription for the named session. Filter table mirrors
    `TagSub` with lowerCamelCase fields: `tag` (required),
    `valueRE` (regex string, optional), `filterFiles` (string array,
    optional), `excludeFiles` (string array, optional). Replace-by-
    (session, tag): the wrapper calls `PubSub.Cancel(session, tag, "")`
    followed by `PubSub.Subscribe(session, [newSub])`. First
    `mcp.subscribe` call for a session starts the listening
    goroutine. (R2288, R2289, R2290, R2299)
  - mcp.onpublish(sessionID string, callback function) — register
    (or replace) the per-session callback. One callback per session.
    Stored in `onpublishCallbacks[sessionID]`; the listening goroutine
    pulls it on every batch dispatch. (R2291)
  - mcp.cancel(sessionID string, tag string) — drop the sub on `tag`
    for `sessionID`; empty tag drops all subs for the session. When
    the session's subscription count hits zero, the listening goroutine
    stops and PubSub state for the session is cleaned up via the
    existing `Cancel` empty-tag path. (R2292, R2300)
  - All three subscription bridges take sessionID as the first
    argument explicitly, mirroring Go's PubSub APIs. Cross-session
    admin use (e.g. the Ark monitor watching `"otherapp"`) works
    via the same surface. (R2293)
  - mcp.definedTags() — read-only Lua bridge returning an array of
    {tag, description} tables drawn from the same store as POST
    /tags/defs. Sorted by tag ascending; duplicate tag names are
    deduplicated keeping the first non-empty description seen.
    Empty result is an empty Lua table; errors follow
    (nil, errstring). Replaces the curation view's text-parse of
    `io.popen("ark tag defs")`. (R2364)
  - mcp.chunkInfo(chunkID) — thin wrapper over `DB.ChunkInfo`.
    Returns a Lua table `{chunkID, fileID, path, range, byteStart,
    byteEnd, writable, commentSyntax}` with the workshop's
    per-chunk metadata bundle. `writable` is `false` for chunkers
    reporting `IsWritable() == false` or paths under
    `~/.claude/projects/**`. `commentSyntax` is the line-comment
    delimiter (`""` for markdown / raw text). Sync read; errors
    follow `(nil, errstring)`; unknown chunk → `(nil, "chunk not
    found")`. (R2386, R2389)
  - mcp.chunkText(chunkID) — thin wrapper over
    `DB.ChunkTextByID`. Returns the chunk's text bytes as a Lua
    string on success or `(nil, errstring)` on failure (unknown
    chunkID, unresolvable range, Sync error). Used by the
    workshop's `<ark-markdown-editor>` per-card embed and by the
    `> current tags` reflection (combined with `mcp.parseTagBlock`
    below). Sync read; UTF-8 preserved verbatim. (R2402)
  - mcp.parseTagBlock(text) — wraps the existing `ParseTagBlock`
    helper. Returns a Lua table `{tags = [{name, value}, ...],
    body}` parsed from the leading `@name: value` block of
    `text`. A chunk with no tag block returns
    `{tags = {}, body = text}`. Pure function: no DB lookup, no
    Sync; only a non-string argument raises a Lua type error.
    (R2404, R2405)
  - mcp.replaceRegion(path, byteStart, byteEnd, newText) — thin
    wrapper over `DB.ReplaceRegion`. Atomic byte-range write
    through the DB write actor; reindex fires in the same
    transaction. Rejects tmp:// paths and out-of-bounds ranges
    with `(false, errstring)`; success returns `(true, nil)`.
    The fundamental file-region write primitive — chunk-text edits
    in the workshop call this with the chunk's byteStart/byteEnd
    from `mcp.chunkInfo`. (R2390, R2391)
  - mcp.setExtTag(targetSpec, tag, value) — thin wrapper over
    `DB.SetExtTag`. Authors an `@ext` routing into the mirror tree
    at `~/.ark/external/<source-slug>/<target-path>.md`. Returns
    `(true, nil)` on success or `(false, errstring)` (source root
    not found, write denied, malformed targetSpec). Routes through
    the write actor; the watcher / indexer reindex the mirror
    file so the ext map updates. (R2393, R2395)
  - mcp.removeExtTag(targetSpec, tag) — thin wrapper over
    `DB.RemoveExtTag`. Removes a single (TARGET, tag) entry from
    its mirror file. Silent no-op when the file or matching line
    is missing. (R2396)
  - mcp.suggestExtLocator(chunkID) — thin wrapper over
    `DB.SuggestExtLocator`. Returns a Lua table `{base, baseValue,
    locator, locatorKind, locatorText, withinFileDupCount,
    crossFileScope = {chunks, files}}` for the workshop's
    `@ext` authoring widget. Runs the three-layer locator
    algorithm (line-prefix → rare-trigram → absolute). Layer 3
    (`absolute`) is unavailable when the chunk's range string
    starts with `"` or `/` (non-conforming per the soft chunker
    contract); the algorithm falls back to the best non-`absolute`
    result, or `locatorKind = "bare"` if no layer produced
    anything. Cross-file scope is computed by running the
    actual resolver path. (R2397, R2398, R2399, R2400, R2401)
  - sys.curation.dismiss(chunkID) — Go-registered mutator on the
    `sys` global. Removes the matching pinned entry (silent no-op
    if absent) and refreshes the Lua mirror in the same tick.
    Lua-executor-only. (R2360)
  - sys.curation.sweepOlder() — Go-registered mutator on the `sys`
    global. Drops every pin except the topmost (silent no-op for
    ≤1 pin) and refreshes the Lua mirror in the same tick.
    Lua-executor-only. (R2361)
  - mcp.findConnections(chunkIDs table, opts table) — fire-and-forget
    bridge that enqueues a Find Connections request via
    `Librarian.FindConnections` and returns the request ID string
    immediately. chunkIDs is a 1-indexed Lua array of numbers
    (Lua numbers → Go uint64). opts is an optional table:
    `timeoutSeconds` (number, default 60, clamped to [5, 300] in
    the orchestrator). Returns `(nil, "agent unavailable")` when
    `Librarian.ConnectionsAvailable()` reports false; returns
    `(nil, "chunkIDs empty")` when the array is empty. Unknown
    chunk IDs are not checked at enqueue time — they surface
    later via the sidecar's `--fetch` failure. Sub-millisecond,
    never blocks the Lua VM. Field/error conventions follow R2266.
    (R2313, R2322, R2323, R2324, R2325)
  - sys.recall(inputs table, opts table) — Go-registered Lua bridge
    function that canonicalizes inputs and retrieves the top-K chunks
    relevant to the inputs via `Librarian.Recall`. (R2628). `opts.discussed`
    is accepted as a Lua array of `{tag=..., value=...}` tables and
    mapped to `RecallOpts.Discussed`; an empty/missing array disables
    the discussed filter. `opts.session` is accepted as a string and
    forwarded so the substrate loads the session's RD records. (R2660)
  - sys.discussed table — exposes four methods that mirror the CLI verbs:
    `add(session, tags...)`, `list(session, opts)`, `clear(session)`,
    `prune(opts)`. Each delegates to `Store.AddDiscussed` /
    `ListDiscussed` / `ClearDiscussed` / `PruneDiscussed` through the
    write actor for mutations; reads run on a View txn. (R2661)
- listenLoops: map[string]chan struct{} — per-session stop signal for
  the listening goroutine that drains PubSub.Listen. Created on first
  mcp.subscribe for a session (R2299); closed when subscription count
  hits zero (R2300) or on server shutdown (R2301).
- onpublishCallbacks: map[string]*lua.LFunction — per-session Lua
  callback registered via mcp.onpublish. Looked up by the listening
  goroutine for each event batch. (R2291)
- runListenLoop(sessionID): goroutine body — repeatedly call
  `pubsub.Listen(sessionID, timeout)`, compress the returned batch via
  `pubsub.CompressBatch`, call `srv.uiRuntime.WithLua(...)` once per
  batch with a 1-indexed Lua array of event tables built from the
  surviving Go Events. The callback signature is one argument: the
  array of event tables, each table mirroring the Go `Event` struct
  with lowerCamelCase fields. Future Event fields surface automatically
  because the table is built from the struct's field set. (R2294,
  R2295, R2296, R2297, R2298)
- Listening-goroutine backpressure: if `WithLua` queues (Lua VM busy),
  events accumulate in the per-session pubsub channel; overflow
  increments `sub.Drops` per existing logic. No new flow control.
  (R2302)
- HTTP tmp:// handlers (`handleTmpAdd`, `handleTmpUpdate`,
  `handleTmpAppend`) stop calling `srv.pubsub.PublishAndWatch` manually.
  Publishing now happens inside `db.AddTmpFile` / `db.UpdateTmpFile` /
  `db.AppendTmpFile` (R2281); the handlers just commit the actor write
  and return. (R2282)
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
- HandleScheduleChange: POST /schedule/change — rewrite date in
  tag, re-index. Detects `tmp://` prefix: tmp:// paths read via the
  in-memory tmp:: overlay and write via `db.UpdateTmpFile` (the
  centralized tmp:// publish path, R2281); disk paths use the
  existing `os.ReadFile`/`os.WriteFile` flow. Both routes publish
  the tag-value-change notification through pubsub so
  EnsureUpcoming re-arms the chunk via its normal indexer path.
  (R921, R922, R923, R925, R2842, R2843)
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
  Also accepts optional `primary_tag_query` (sigil form) and
  `primary_file_tag` (bool) — when present, the shared TagMatcher
  parses the predicate and `resolvePrimaryTagChunks` produces the
  chunkID set straight from V/F records, bypassing FTS. With a text
  primary, the same chunkID set overlays as a WithChunkFilter.
  (R1069-R1075, R2442, R2453)
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
- HandleCuratePin: POST /curation/pin — pin a chunk from a
  web-component context that can't reach Lua directly (chunk-row
  buttons in `<ark-search>`, content-view iframes). Accepts JSON
  `{chunkID, fileID, path}`; chunkID required. Enters the Lua
  executor via `srv.uiRuntime.WithLua` and calls `Curation.pin`,
  so the Go mutation and Lua mirror refresh share a single tick.
  503 when the Lua runtime is not wired; 400 on missing chunkID
  or malformed JSON. (R2363)
- HandleCurateDismiss: POST /curation/dismiss — mirror of
  HandleCuratePin for the toggle path. Accepts JSON `{chunkID}`;
  enters the Lua executor and calls `Curation.dismiss`. Silent
  no-op when the chunkID is not pinned (200 OK regardless,
  matching the Lua semantic). 400 / 503 follow the same shape
  as `/curation/pin`. (R2411, R2412)
- HandleCuratePinned: GET /curation/pinned — read-only snapshot
  of the pinned chunkID list for web-component consumers.
  Returns `{chunkIDs: [uint64, ...]}` in newest-first order
  (same as the in-memory slice). Reads through
  `Curation.pinnedSnapshot()`; no Lua-executor entry needed.
  503 when the curation store is not wired. Used by the
  content-view pin buttons to seed pinned-state on load.
  (R2413, R2414)
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
    `<div class="ark-chunk" data-range="..." data-chunkid="..." data-fileid="...">`
    with per-chunk wrapTagElements. `data-chunkid` sourced from
    `srv.db.ChunkIDsForPath(path)` (already called in this path for
    `@ext` block lookup); `data-fileid` sourced once per file via
    `db.fts.CheckFile(path).FileID`. The two new identifier
    attributes power the curate-button inline JS in
    content-markdown.html / content-plain.html. (R2415)
    JSONL chunks rendered through goldmark. PDF chunks grouped by
    `page` — one `<pdf-chunk>` per page covering the full page,
    carrying every `tag_rects` entry from every chunk on that page,
    producing all `<ark-tag>` overlays on the rendered page (R1739,
    R1740). Groups consecutive same-role chunks in `ark-role-group`
    wrappers with sticky icon headers (👤/🤖). Skill groups use
    `<details>` collapse with 📋 + skill name. Single-chunk views
    get role border/icon without grouping. Falls back to raw `<pre>`
    if no chunks exist.
    (R1160-R1164, R1168-R1189, R1495-R1496, R1499, R1504-R1513, R1739, R1740, R2415)
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
- StartRecallWatcher(): on Serve() and on every `[recall]` config
  reload, construct (or reload) the RecallWatcher with current
  `[recall]` values. Wires `composeDM` for in-process emission;
  registers Enqueue with the indexer's post-chunk-append callback.
  When `enabled = false`, the field stays nil and the indexer skips
  the hook entirely. (R2687, R2688, R2694, R2695)
- composeDM(sender, recipients, subject, ref, body): shared
  internal DM-compose function used by both `cmdMessageDM`
  (crc-CLI.md) and the RecallWatcher worker. Identical tag-block
  output and tmp:// destination for the same inputs; the watcher
  invokes the Go path directly without HTTP. (R2700, R2727)
- HandleRecallEmit (worker-internal): the RecallWatcher's worker
  goroutine emits DMs via composeDM and writes RD records via the
  Store write actor. Hot-reload of `[recall]` does not restart the
  worker; the config struct is swapped in place. (R2713, R2714,
  R2715)
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
- RecallWatcher: ambient-recall subsystem; Server constructs it on
  Serve() and reload, shares `composeDM` for in-process emit

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
- seq-recall-watcher.md
