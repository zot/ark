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

## Protected Resources

Some state on `ark.DB` is mutated by the actor (or the write
goroutine's reconcile step) and is **not** safe to touch from another
goroutine. Every accessor must go through the DB actor:

- **microfts2's Go-side caches** — `pathCache`, `pathToID`,
  `frecordCache` (inside `fts`). Lazily loaded on read, cleared by the
  reconcile step's `InvalidateCaches()` after each write. Go maps are
  unsafe for concurrent read/write, so resolving paths or FRecords via a
  bare `fts` call from a non-actor goroutine races the invalidate.
- **Write-queue state** — `writeQueue`, `writing`, `writeIdleWaiters`.
  Actor-only.

Access rule:

- **Inside the actor** (a `Svc`/`Sync` closure, or the reconcile step):
  read the resource directly — you are already serialized.
- **An off-actor read operation** (a worker goroutine — e.g. the
  find-connections substrate worker) reads through a **private
  `fts.Copy()`**. The copy carries its own lazily-loaded caches, so its
  path/FRecord reads never touch the shared original's caches that the
  reconcile step nils. `DB.withFTS(fts.Copy())` returns a read view
  bound to the copy, and rebinds the Searcher too (fuzzy search resolves
  result paths through the path cache). The copy shares the overlay
  pointer, so tmp:// documents still resolve. This mirrors what the
  write actor already does — it writes on its own `Copy()` — and keeps
  reads off the actor, so a long read pass never stalls writes.
- **Writes** go through the write actor.

Bundling the read state on an operation object (Monadic Wrapper) so all
of its DB access flows through one copy-bound handle is the shape that
makes this auditable. The find-connections substrate is the first such
operation (`substrateOp`); bringing the remaining bare `fts` readers
(search, recall normalization, fetch-payload assembly, …) onto operations
is ongoing tech debt.

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

## Refresh Coalescing

The watcher's throttle window collapses a burst of events for the same path
into one entry, but only within a single window. When the write actor is
saturated — for example, a full embed pass after a rebuild holds the write
goroutine for minutes — per-path refresh closures from successive windows pile
up behind it. They drain later as a run of redundant no-op append checks (each
reports the file "didn't grow" because an earlier one already caught it up).

The DB actor coalesces at the queue level. It keeps a set of paths that have a
refresh already queued or in flight. `IndexPathsAsync` skips any path in the set
and marks the rest before enqueueing; each path is cleared as its refresh
begins, so a change arriving during the refresh re-queues rather than being
dropped. While a path's refresh is pending, further events for it add no new
work.

## Session Interaction

Lua searches go through two actors: session actor → DB actor.
The closures sent to the DB actor *could* reference sessions (they
capture their scope), but in practice the call direction is always
session → DB. A deadlock would require a closure running inside the
DB actor to SvcSync back to the session actor that's blocked waiting
on the DB actor. This doesn't happen because the DB operations
don't need session state — they just return results.
