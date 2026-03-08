# Sequence: Reconciliation
**Requirements:** R342, R343, R344, R345, R346, R347

Covers the reconciliation cycle — called on startup, after config
mutations, and on ark.toml fsnotify events.

## Participants
- Server
- DB
- Scanner
- Indexer
- Store

## Flow

```
Trigger (startup | config mutation | ark.toml change)
  │
  ├──> Server.Reconcile()
  │     │
  │     ├── send to reconcileCh
  │     │   (if reconcile already running, waits for it to finish
  │     │    then runs again — not dropped)
  │     │
  │     ├──> reconcile goroutine receives
  │     │
  │     ├──> DB.SourcesCheck()
  │     │     ├── Config.ResolveGlobs()
  │     │     ├── diff against DB sources
  │     │     ├── add new sources
  │     │     └── flag MIA sources
  │     │
  │     ├──> DB.Scan()
  │     │     ├── Scanner.Scan(config)
  │     │     ├── Indexer.AddFile(path, strategy)  [for each new file]
  │     │     ├── Store.AddUnresolved(...)         [for each unresolved]
  │     │     └── Store.CleanUnresolved()
  │     │
  │     ├──> DB.Refresh(nil)
  │     │     ├── microfts2.StaleFiles()
  │     │     ├── for each stale: Indexer.RefreshFile(path)
  │     │     └── for each missing: Store.AddMissing(fileid, path)
  │     │
  │     ├──> if Phase B: Server.StartWatching(new source dirs)
  │     │                Server.StopWatching(removed source dirs)
  │     │
  │     └── log reconciliation summary
  │
  └── (trigger returns immediately — reconcile is async)
```

## Config Mutation Path

```
HandleConfigAddSource (or other mutation handler)
  │
  ├──> DB.ConfigAddSource(dir, strategy)
  │     ├── Config.AddSource(dir, strategy)
  │     └── Config.Save()
  │
  └──> Server.Reconcile()  [post-mutation hook]
       └── (async — handler returns to client immediately)
```
