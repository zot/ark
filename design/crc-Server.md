# Server
**Requirements:** R4, R61, R62, R63, R64, R65, R66, R67, R68, R69, R70, R89, R90, R91, R92, R93, R94, R95, R96, R97, R98, R99, R100, R101, R102, R132, R133, R134, R152, R153, R154, R155, R156, R160, R164, R170, R171, R175, R176, R177, R165, R202, R204, R210, R211, R212, R213, R229, R257, R264, R265, R266, R267, R268, R269, R270, R271, R272, R261, R262, R263, R338, R339, R342, R343, R344, R345, R346, R347, R348, R349, R350, R351, R352, R353, R354, R355, R356, R357, R358, R359, R387, R388, R389, R390, R391, R393, R394, R395, R410, R411, R412, R414, R415, R416, R417, R419, R420, R437, R438, R440, R441, R439, R541, R542, R543, R544, R545, R546, R563, R564, R565, R566, R567, R568, R569, R570, R571, R620, R623, R641, R648, R657, R658, R659, R660, R661, R662

HTTP server on Unix domain socket. Highlander (one per database).
Keeps embedding model warm. Runs reconciliation on startup and
after config changes. Watches filesystem for changes.
Optionally starts the embedded ui-engine alongside.

## Knows
- db: *DB — ark database facade
- listener: net.Listener — Unix socket
- pidPath: string — PID file location
- noScan: bool — skip startup reconciliation
- uiRuntime: *flib.Runtime — embedded Frictionless runtime (nil if UI disabled/failed)
- watcher: *fsnotify.Watcher — filesystem watcher (nil if watching disabled)
- reconcileCh: chan struct{} — triggers reconciliation (serialized)
- ignoredPaths: map[string]struct{} — negative cache of non-indexable paths, cleared on config reload
- uiPort: int — HTTP port the ui-engine is listening on (0 if not started)
- sessions: map[string]*Session — named sessions, autocreated on demand (mutex-protected)

## Does
- Serve(dbPath, opts): bind socket (highlander lock), write PID file,
  open DB, ensure ~/.ark source, start watches, run Reconcile,
  start ui-engine, start HTTP server
- Reconcile(): sources-check → scan → refresh. Idempotent. Runs in
  background goroutine. If already running, waits then runs again.
  Called by: startup, config mutation handlers, ark.toml fsnotify.
- StartWatching(dirs): add fsnotify watches for source directories
  and ark.toml. Recursive — walks subdirectories. Starts the
  throttled event loop goroutine.
- StopWatching(dirs): remove fsnotify watches for removed sources
- handleFileEvent(path): throttled on-notify — immediate index on
  first event, then throttle window. Events during window ignored.
  Window expiry triggers single re-index of current state. Max wait
  ceiling prevents starvation.
- isIgnored(path): check negative cache, then DB.IsIndexable if miss.
  Non-indexable paths are cached. Directory events and ark.toml bypass.
- clearIgnoredPaths(): reset the negative cache — called on config reload
- EnsureArkSource(): ensure ~/.ark is a source — hardcoded, not in
  ark.toml, cannot be removed
- StartUIEngine(dbPath): configure ui-engine (Dir=dbPath), start in
  goroutine. On failure, log error and continue without UI.
- BindSocket(path): create Unix domain socket, fail if already bound
- WritePID(path): write PID file for emergency kill
- CleanupStaleSocket(path): remove socket file if exists and no server
- HandleSearch, HandleAdd, HandleRemove, HandleScan, HandleRefresh,
  HandleStatus, HandleFiles, HandleStale, HandleMissing, HandleDismiss,
  HandleConfig, HandleUnresolved, HandleResolve: HTTP route handlers,
  each delegates to DB methods, returns JSON
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
    kind, responseHandled, and requestHandled fields alongside status,
    to, from, summary, path.
  - mcp:parseJson(str) — parse JSON string into Lua table.
    Uses jsonToLua recursive converter.
  - mcp:readJsonFile(path) — read file, parse JSON into Lua table.
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
- Signal handling: catch SIGTERM, stop watcher, shut down ui-engine,
  close listener, close DB, exit 0
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

## Sequences
- seq-server-startup.md
- seq-reconcile.md
- seq-file-change.md
- seq-session-search.md
