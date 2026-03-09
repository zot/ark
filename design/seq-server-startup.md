# Sequence: Server Startup

Covers `ark serve` startup including highlander check and
reconciliation sequence.

## Participants
- CLI
- Server
- DB
- Scanner
- Indexer
- Store
- ui-engine (cli.Server)

## Flow

```
CLI ──> Server.Serve(dbPath, opts)
         │
         ├──> Server.BindSocket(socketPath)
         │     ├── attempt net.Listen("unix", socketPath)
         │     ├── if error (address in use) → "another server is running", exit 1
         │     └── if stale socket file exists but bind succeeds → ok (OS cleaned it)
         │
         ├──> Server.WritePID(pidPath)
         │     └── write os.Getpid() to file outside db dir
         │
         ├──> DB.Open(dbPath)
         │     ├── microfts2.Open (creates LMDB env)
         │     ├── microvec.Open (receives env)
         │     └── Store.Open (receives env)
         │
         ├──> Server.EnsureArkSource()
         │     └── ensure ~/.ark is a source (hardcoded, not in ark.toml)
         │
         ├──> if !noScan:
         │     ├──> Server.StartWatching(source dirs + ark.toml)
         │     │     └── fsnotify watches before reconciliation
         │     │         so nothing changes unseen during scan
         │     │
         │     └──> Server.Reconcile()
         │           └── see seq-reconcile.md
         │
         ├──> Server.StartUIEngine(dbPath)
         │     ├── cfg := cli.DefaultConfig()
         │     ├── cfg.Server.Dir = dbPath
         │     ├── cfg.Server.Port = 0 (auto-select)
         │     ├── uiSrv := cli.NewServer(cfg)
         │     ├── go uiSrv.Start()
         │     ├── if error → log warning, continue without UI
         │     └── ui-engine writes ui-port, mcp-port to dbPath
         │
         └──> http.Serve(listener, router)
               └── blocks, serving requests until shutdown

## Flow: UI Reload (keep port)

```
CLI ──> POST /api/reload (via unix socket)
         │
Server ──> ReloadUIEngine()
            │
            ├──> savedPort = uiPort (from initial start)
            │
            ├──> uiRuntime.Shutdown(ctx)
            │     └── ui-engine cleans up sessions, closes port
            │
            ├──> uiRuntime = flib.New(Config{Dir, Host, Port: savedPort})
            │     └── Port field tells ui-engine to bind same port
            │
            ├──> uiRuntime.Configure()
            ├──> uiRuntime.Start()
            │     ├── if savedPort busy → fall back to auto (port 0)
            │     └── log warning if port changed
            │
            ├──> RegisterLuaFunctions()
            │     └── re-register mcp:indexing() on new Lua session
            │
            ├──> uiRuntime.RunLua(`mcp:display("ark")`)
            │
            └──> browser reconnects via WebSocket auto-reconnect
```

Note: Requires `flib.Config.Port` field (upstream change in
Frictionless). Without it, reload always picks a new port.

## Shutdown

```
Signal (SIGTERM/SIGINT)
  │
  ├──> if watcher != nil: watcher.Close()
  │     └── stop filesystem watches
  │
  ├──> if uiServer != nil: uiServer.Shutdown(ctx)
  │     └── ui-engine cleans up sessions, closes ports
  │
  ├──> listener.Close()
  │     └── stops accepting new ark API requests
  │
  └──> db.Close()
        ├── Store.Close
        ├── microvec.Close
        └── microfts2.Close (closes LMDB env)
```
