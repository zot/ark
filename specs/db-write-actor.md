# DB Write Actor

Separate read and write paths through the main DB closure actor.
Language: Go. Environment: ark server process.

## Problem

The DB closure actor serializes all operations — reads block behind
writes. Long writes (EnsureUpcoming file I/O, batch indexing) stall
status queries and UI responsiveness.

## Design

All requests go through the main DB actor. The main actor routes:

### Reads
Execute directly in the main actor, return immediately. MVCC
ensures readers see consistent snapshots even while a write is
in flight.

### Config files (ark.toml)
Index in-place in the main actor. Update chunkers, schedule config,
sources synchronously. Must complete before any normal writes that
depend on them. This prevents races where a write indexes a file
with a chunker that hasn't been registered yet.

### Normal writes
Queue a write closure. Processing flow:

```
Main Actor receives write request
  └── Queue write closure
      └── If queue was empty, dequeue and run in goroutine:
            goroutine:
              ├── db.Copy() — same index, nil caches
              ├── Open write transaction on the copy
              ├── Index batch (file I/O happens here, off the actor)
              └── Send reconcile closure to Main Actor channel
                    Main Actor:
                      ├── db.InvalidateCaches()
                      ├── Commit the write transaction
                      └── If write queue not empty, dequeue and
                          run next closure in goroutine
                            ^---- continuation pattern
```

### Continuation pattern

The write queue is drained by the main actor, not the goroutine.
Each goroutine runs one batch, sends the reconcile closure, and
dies. The main actor commits and decides whether to start the next.
No writing occurs until after the previous transaction commits.

This is the critical path. If the goroutine panics or the
reconcile closure errors, the failed batch is dropped — but the
system self-heals: the next write request entering the main actor
sees items in the queue and kicks off a new goroutine. The result
is hiccuppy behavior (dropped batch, re-indexed on next scan)
rather than a deadlock. Still needs robust error handling:

1. Recover from panics in the write goroutine (defer/recover,
   send error closure to main actor)
2. On reconcile error: log the failure, skip the batch, dequeue next
3. The continuation is self-healing but errors must be visible —
   silent drops lead to confusion about why files aren't indexed

The main actor can also inspect the queue between writes:
reorder items, batch small writes together, or prioritize
config files that arrived while a write was in flight.

## Batch ordering within a scan

When a scan produces N files to index:

1. Main actor partitions: config files vs content files
2. Config files processed first (in main actor, synchronous)
3. Content files queued as one or more write batches
4. Write goroutine processes each batch
5. Reconcile returns to main actor, commits, dequeues next

## microfts2 interface

Two methods needed:

- `Copy() *DB` — creates a shallow copy sharing the `*bbolt.DB`.
  Overlay pointer shared (has its own mutex). Caches set to nil
  (will lazy-load from committed index state). Chunker registry
  shared (read-only during writes, updated only by config files
  which run in the main actor).

- `InvalidateCaches()` — nils the pathCache and resets counters,
  forcing lazy reload on next access. Called by the main actor
  after committing a write transaction. No state transfer needed —
  just "your cache is stale now."

## Why not a second ChanSvc

The write actor is just a goroutine, not a separate ChanSvc.
The main actor spawns it, the goroutine sends one closure back,
the goroutine dies. No lifetime management, no second channel,
no coordination beyond the existing actor channel.

## Relationship to existing workarounds

The deferred-schedule pattern (pendingSchedule / DrainSchedule /
processScheduleItems) is a workaround for this same problem. Once
the write actor is implemented, schedule I/O moves into the write
goroutine naturally and the deferred pattern can be removed.
