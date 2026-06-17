# Sequence: Write Actor
**Requirements:** R1051, R1052, R1053, R1054, R1055, R1056, R1057, R1058, R1059, R1060, R1061, R1062, R1065, R1067

Covers the read/write path separation in the DB actor. Reads execute
directly; writes go through a copy-index-reconcile cycle.

## Participants
- Caller (Server, Watcher, CLI)
- DB (main actor)
- microfts2.DB
- WriteGoroutine

## Read Path

```
Caller ──> svcSync(fn)
            │
            DB actor executes fn directly
            │  (bbolt MVCC: readers see consistent snapshot
            │   even while a write goroutine is in flight)
            │
            └── return result to caller
```

## Write Path: Single File (watcher event)

```
Caller ──> svc(fn)  [fire-and-forget]
            │
            DB actor receives fn
            │
            ├── classifyForWrite([path])
            │     ├── if ark.toml: index in-place (synchronous), return
            │     └── if content file: continue to enqueueWrite
            │
            ├── enqueueWrite(writeClosure)
            │     ├── append to writeQueue
            │     └── if !writing && len(writeQueue) == 1:
            │           └── startWrite()
            │
            startWrite()
              ├── dequeue head of writeQueue
              ├── writing = true
              └── go func():  [WriteGoroutine]
                    │
                    ├── defer recover → send error closure to actor
                    │
                    ├── ftsCopy := fts.Copy()
                    │     └── shallow copy: shared env, nil caches,
                    │         shared chunker registry, shared overlay
                    │
                    ├── txn := ftsCopy.BeginWrite()
                    │
                    ├── writeClosure(ftsCopy, txn)
                    │     └── file I/O + indexing (OFF the actor)
                    │
                    └── svc(reconcileClosure)  → back to actor
                          │
                          DB actor receives reconcileClosure
                          │
                          ├── fts.InvalidateCaches()
                          │     └── nil pathCache, pathToID, frecordCache
                          │
                          ├── txn.Commit()
                          │
                          ├── writing = false
                          │
                          └── if writeQueue not empty:
                                └── startWrite()  [continuation]
```

## Write Path: Batch (reconciliation scan)

```
Server.Reconcile()
  │
  ├──> DB.Scan()
  │     ├── Scanner.Scan(config) → newFiles, staleFiles
  │     ├── classifyForWrite(newFiles)
  │     │     ├── config files → index synchronously in actor
  │     │     └── content files → enqueueWrite(batchClosure)
  │     │           batchClosure indexes all content files in one txn
  │     │
  │     └── return (caller doesn't wait for write to finish)
  │
  └──> DB.Refresh(patterns)
        ├── staleFiles from microfts2
        ├── classifyForWrite(staleFiles)
        │     └── content files → enqueueWrite(refreshBatchClosure)
        └── return
```

## Error Recovery

```
WriteGoroutine panics:
  │
  ├── defer/recover catches panic
  ├── send error closure to actor: svc(errorClosure)
  │
  DB actor receives errorClosure
  │
  ├── log error with batch details (R1061)
  ├── writing = false
  └── if writeQueue not empty:
        └── startWrite()  [self-healing: R1060]

Reconcile error (txn.Commit fails):
  │
  ├── log error (R1061)
  ├── writing = false
  └── if writeQueue not empty:
        └── startWrite()  [continuation resumes]

Note: failed batch files remain stale in microfts2.
Next scan/reconcile will re-queue them. (R1060)
```

## Interaction with Existing Patterns

```
RefreshStale (parallel workers):
  │
  ├── Workers read files + prepare data (parallel, off actor)
  │
  ├── Workers send prepared closures to actor via svc()
  │     └── actor receives: classifyForWrite or direct enqueueWrite
  │
  └── Write goroutine batches index mutations

Note: The worker pool (NumCPU goroutines) in RefreshStale
prepares data in parallel. The write actor serializes the
index mutations. These compose naturally — workers feed the
queue, the actor drains it one batch at a time.
```
