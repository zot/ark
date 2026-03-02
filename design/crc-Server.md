# Server
**Requirements:** R4, R61, R62, R63, R64, R65, R66, R67, R68, R69, R70, R89, R90, R91, R92, R93, R94, R95, R96, R97, R98, R99, R100, R101, R102

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

## Collaborators
- DB: all operations delegated through the facade
- Scanner: startup reconciliation scan
- Indexer: startup reconciliation refresh

## Sequences
- seq-server-startup.md
