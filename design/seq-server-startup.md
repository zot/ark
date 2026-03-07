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
         ├──> if !noScan: Server.StartupReconciliation()
         │     │
         │     ├──> Step 1 (future): start fsnotify watches
         │     │
         │     ├──> Step 2: Scanner.Scan(config)
         │     │     ├── walk all configured directories
         │     │     ├── find new files to index
         │     │     └── find new unresolved files
         │     │
         │     ├──> Indexer.AddFile(path, strategy)  [for each new file]
         │     ├──> Store.AddUnresolved(...)         [for each new unresolved]
         │     ├──> Store.CleanUnresolved()
         │     │
         │     ├──> Step 3: Indexer.RefreshStale()
         │     │     ├── microfts2.StaleFiles() → list stale/missing
         │     │     ├── for each stale: RefreshFile(path)
         │     │     └── for each missing: Store.AddMissing(fileid, path)
         │     │
         │     └──> log reconciliation summary
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
