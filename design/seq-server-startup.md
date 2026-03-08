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
