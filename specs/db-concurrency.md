# DB Concurrency: Closure Actor

Go. Linux.

## Problem

Three concurrent accessors share one `ark.DB` with no coordination:

1. **Watcher goroutine** — reconcile, AddFile, RemoveFile. Mutates
   microfts2's Go-side caches (pathCache, pathToID).
2. **HTTP handler goroutines** — search, status, files, config.
   net/http runs each in its own goroutine. Reads the same caches.
3. **Lua/UI goroutine** — search_grouped and other registered
   functions. Runs in the flib event loop.

bbolt is safe internally (MVCC: one writer plus many concurrent
readers within a process). The Go-side caches above it are not. Go
maps are not safe for concurrent read/write — this is a data race.

bbolt is also **single-process** — a process opening the database
holds an exclusive file lock — so the cross-process concurrency LMDB
allowed (e.g. `ark status` reading while a standalone `ark rebuild`
writes) is gone. rebuild-read-serve.md covers how rebuild preserves
that observe-during-rebuild behavior with an in-process read-only
server.

## Solution

Wrap `ark.DB` in a closure actor (ChanSvc). All DB operations go
through the actor's channel. The code inside the actor is sequential
— no races by construction.

## Caller Categories

**Fire-and-forget (Svc):**
- Watcher file changes — reindex, remove. If the DB is broken the
  actor stops and likely the whole program. The watcher doesn't need
  to know the outcome.
- Source add from Lua — updates happen asynchronously through the
  Lua session's own closure actor.

**Synchronous (SvcSync):**
- HTTP handlers — need informative response codes and result data.
- CLI search — the user is waiting for results.
- Any operation where the caller needs the return value.

## Watcher Batching

The watcher currently triggers a full reconcile (walk all source
dirs, diff against index) on every throttle expiry. With the actor,
the watcher should instead:

- Accumulate specific changed/removed paths during the throttle
  window.
- On expiry, send a single closure to the actor that processes
  only those paths.
- Full reconcile still runs on config change and startup.

This is a smaller, more predictable operation per event.

## reconcileLoop Absorption

The existing `reconcileLoop` (channel with buffer 1, serial
processing) merges into the DB actor. The watcher sends reconcile
closures instead of signaling a separate channel. This eliminates
the dedicated reconcile goroutine.

## Session Interaction

Lua searches go through two actors: session actor → DB actor.
The closures sent to the DB actor *could* reference sessions (they
capture their scope), but in practice the call direction is always
session → DB. A deadlock would require a closure running inside the
DB actor to SvcSync back to the session actor that's blocked waiting
on the DB actor. This doesn't happen because the DB operations
don't need session state — they just return results.
