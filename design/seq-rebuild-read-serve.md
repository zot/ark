# Sequence: Rebuild Read-Only Serve
**Requirements:** R2984, R2985, R2986, R2987, R2988, R2989, R2990

How `ark rebuild` keeps `ark status` / `ark search` live during the
scan, instead of letting them hang on bbolt's single-process file lock
(R2984). Rebuild stays a no-server operation, but during the scan it
*is* a server — a constrained, read-only one that exits when indexing
drains.

## Participants
- CLI (cmdRebuild)
- Server (Serve, rebuild mode)
- DB (actor + write queue)
- Reader (a second `ark status` / `ark search` process)

## 1. Rebuild orchestration (cmdRebuild)

```
1.1  serverClient(arkDir) != nil ?
       └── yes → refuse ("server is running — stop it first")   # unchanged
1.2  cmdInit("--no-setup")
       └── delete + recreate index.db, fresh I records          # drop window [R2990]
1.3  Serve(dbPath, ServeOpts{Rebuild: true})                    # runs scan, returns on idle
1.4  print "rebuild complete"; embeddings note
```

The drop window (1.2) is the one uncovered gap: a Reader arriving
before the socket binds (1.3 → 2.4) may briefly block on the file
lock. Short and self-clearing — accepted (R2990).

## 2. Serve in rebuild mode

```
2.1  bind unix socket (highlander), open DB (actor starts)
2.2  construct coordination objects as normal, but SKIP starting the
     background subsystems: filesystem watcher, embedded UI engine,
     scheduler scans/arm/check-gaps, pubsub reaper, recall watcher
     Start, the startup reconcile.                              [R2988]
       └── the indexer already tolerates nil/idle subsystems
           (its cold-start path), so the scan needs none of them.
2.3  build the read-only mux: register the curated read handlers
     (status, search, files, stale, missing, unresolved, tags,
     config); any other path → 503 "rebuild in progress".      [R2987]
2.4  go http.Serve(listener, mux)
       └── socket is now live; Readers can proxy in.            [R2985]
2.5  Sync(db, func(db){ db.Scan() })
       └── runs ON the actor → enqueueWrite is called from inside
           the actor (contract); heavy chunking/writes run OFF the
           actor in the write goroutine; returns once every file is
           enqueued.                                            [R2986]
2.6  db.WaitWritesIdle()
       └── block until writeQueue drains (writing==false &&
           len(writeQueue)==0) — the completion signal.         [R2989]
2.7  shutdown http server, close DB, return
```

## 3. Concurrent reader (the payoff)

```
3.1  ark status  (another terminal, during 2.4–2.6)
3.2  serverClient finds the socket → proxy GET /status?db=true
3.3  handleStatus → Sync(db, Status/StatusDB)
       └── rides the actor like any server read: race-free against the
           scan's cache mutations, interleaves between the brief
           write-completion closures the write goroutine sends back to
           the actor.                                           [R2986]
3.4  returns live, growing counts — not blocked on the file lock.  [R2985]
```
