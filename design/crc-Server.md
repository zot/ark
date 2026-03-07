# Server
**Requirements:** R4, R61, R62, R63, R64, R65, R66, R67, R68, R69, R70, R89, R90, R91, R92, R93, R94, R95, R96, R97, R98, R99, R100, R101, R102, R132, R133, R134, R152, R153, R154, R155, R156, R160, R164, R170, R171, R175, R176, R177, R165, R202, R204, R210, R211, R212, R213, R229, R257

HTTP server on Unix domain socket. Highlander (one per database).
Keeps embedding model warm. Runs startup reconciliation.

## Knows
- db: *DB — ark database facade
- listener: net.Listener — Unix socket
- pidPath: string — PID file location
- noScan: bool — skip startup reconciliation

## Does
- Serve(dbPath, opts): bind socket (highlander lock), write PID file,
  open DB, run startup reconciliation, start HTTP server
- StartupReconciliation(): scan then refresh (unless --no-scan)
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
- SetupLogging(dbPath): create ~/.ark/logs/ dir, open log file, truncate if >10MB (keep last 1MB), set log.SetOutput to MultiWriter(stderr, file)
- Signal handling: catch SIGTERM, close listener, close DB, exit 0
- Never remove PID file (stale PID is safe — stop verifies before kill)

## Collaborators
- DB: all operations delegated through the facade
- Scanner: startup reconciliation scan
- Indexer: startup reconciliation refresh

## Sequences
- seq-server-startup.md
