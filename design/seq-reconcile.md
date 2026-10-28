# Sequence: Reconciliation
**Requirements:** R343-347, R2138-2142, R3433-3436, R3670-3672

Covers the reconciliation cycle — called on startup, after config
mutations, and on ark.toml fsnotify events. Since indexing-latency #132
(R3433) the scan *enumeration* runs off the DB actor; diagram 1 is the
on-actor cycle and diagram 2 is the off-actor walk it kicks.

## Participants
- Server
- DB (db.svc actor)
- Scanner (a throwaway, deferLinks copy for the off-actor walk)
- Indexer
- Store
- Goroutine (the off-actor walk)

## Flow

```
1. Reconcile cycle (Server.doReconcile — on the DB actor)

1.1  Trigger (startup | config mutation | ark.toml change) → Server.reconcile() → postCoalescedReconcile   (R345, R346)
1.2  DBActor: if reconcileWalking → set reconcileWalkPending, return (a walk is in flight; coalesce)   (R3434)
1.3  DBActor: SourcesCheck; unwatchStaleDirs; watchNewSources; setIndexing(sourceDirs)   (R347, R3670, R3671)
1.4  DBActor: SweepAsync — StaleFiles read, enqueueWrite(RemoveFile) for paths no longer Included   (R2138, R2142)
1.5  DBActor: startReconcileWalk(db) → diagram 2   (R3433)
```

```
2. Off-actor scan walk (startReconcileWalk → DB.launchReconcileWalk)

2.1  DBActor: capture cfg/matcher snapshot, ftsCopy = fts.Copy(), empties = emptyFiles.Clone()   (R3433)
2.2  DBActor: launchReconcileWalk sets reconcileWalking, then `go walk()` off the actor so the actor stays free for interleaving writes   (R3433, R3434)
       2.2.1  Goroutine: NewScanner{deferLinks}.Scan() over the private copies → ScanResults{NewFiles, NewUnresolved, EmptyFiles, PendingLinks}. CheckFile via the private copy; symlink Observe deferred (PendingLinks in walk order)   (R3433)
2.3  Goroutine → SyncVoid(db, apply): DBActor applies the result   (R3433)
       2.3.1  swap db.scanner.emptyFiles = empties (the refreshed clone)
       2.3.2  applyReconcileScan: observeScannedLink over PendingLinks (deferred Observe on the shared, actor-owned LinkTable), recordScannedLinks, AddUnresolvedRecord loop, CleanUnresolved, config-file adds, evictEmpty, startFileScan per content file   (R3383, R3433)
       2.3.2a watchWalkedDirs(results.Dirs): add each walked directory absent from WatchList that IsWatchableDir still accepts   (R3672)
       2.3.3  RefreshAsync — StaleFiles read, startFileScan per stale file
       2.3.4  captureReconcileStats(len(stale)): record stale + M/U-record counts + unresolved-by-reason + timeNow() into db.reconcileStats, so `ark status` reports staleness off-actor without a fresh walk   (R3435)
2.4  DBActor: finishReconcile — DrainSchedule (config-index schedule items), enqueueWrite(setIndexing(nil)), kick the off-slot embed pass   (R1014, R1058, R3433)
2.5  DBActor: reconcileWalking = false; checkWriteIdle; if reconcileWalkPending → relaunch one coalesced reconcile   (R3434)
```

```
3. Off-actor status read (Server.statusOp.run → DB.readStatusOffActor)

3.1  Handler goroutine: Sync(db, bindStatusRead) — one trivial actor hop captures {view = withFTS(fts.Copy()), rc = reconcileStats snapshot, tmpCount} and returns; the actor is free again   (R3436)
3.2  Handler goroutine (OFF the actor): view.Status(rc, tmpCount) — FileIDPaths + FileInfoByID enumeration for chunk/strategy/size totals; rc supplies stale/missing/unresolved + as-of; NO StaleFiles, no corpus stat   (R3435, R3436)
       3.2.1  when ?db=true: view.StatusDB() — RecordCounts scans, also off the actor   (R3436)
3.3  Handler goroutine: enrich with UI/spectral fields, write JSON
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
