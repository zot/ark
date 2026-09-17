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

## Delivery

Sequential execution is only half the contract. The other half is
**order**: closures posted by one goroutine run in the order that
goroutine posted them. Without it, the standard idiom of posting a
follow-up behind a message instead of synchronizing against it does
not work, because queue position stops meaning anything.

The guarantee is that ordering holds **per posting goroutine**, across
both fire-and-forget and synchronous posts, since both enter by the
same door. Posts from *different* goroutines interleave arbitrarily,
which is expected and is what the actor exists to make safe.

Two design constraints follow, and they depend on each other.

**A post is a direct send.** The posting goroutine puts the closure on
the channel itself. Handing the send to a freshly spawned goroutine
would surrender ordering entirely, because two such goroutines race
and the scheduler picks the winner. This is not a hypothetical: it is
what ark did until 2026-07-31, and the ordering guarantee above was
absent for as long as that was true.

**The actor's own goroutine never runs a closure.** It maintains a
queue and an "a closure is running" flag, and each closure executes in
its own goroutine, which on completion posts back so the next one can
start. Only one closure runs at any instant, so serialization is
unchanged.

Running work on the loop goroutine instead is what makes a direct send
untenable, and is the reason ark's send was originally escaped into a
goroutine. A closure that takes a long time (a search, say) stops the
loop receiving, senders back up behind it, and the pressure to escape
the send becomes irresistible. With work off the loop, the loop is
never held for longer than a queue operation, so it is effectively
always at the receive and a direct send does not block in practice.

Back-pressure remains available and is not the normal case. Bursts are
absorbed by the queue, which is unbounded, and not by the channel,
which is **one slot deep**. The previous arrangement did not remove
the backlog either; it stored it in parked sender goroutines holding
whole stacks, and delivered it out of order afterwards.

**One slot is a diagnostic choice, not a capacity one.** Since the loop
does only bookkeeping, depth would buy a poster nothing beyond the
chance to skip a rendezvous. What it would cost is the ability to
notice a loop that has wrongly been given real work: a deeper channel
absorbs the first several posts, so the stall appears later and
somewhere other than its cause, while one slot blocks the very next
poster at the site. A misbehaving actor should be loud.

**Panics are not recovered.** A closure that panics takes the process
down, which is deliberate: a panic inside the actor means the state
the actor protects is already in an unknown condition, and continuing
on it is worse than stopping. A closure that can meaningfully survive
its own panic recovers internally, as the write goroutine does
(R1059). Recovering centrally would also introduce a failure mode with
no symptom: a recovery path that forgot to release the running flag
would queue every subsequent closure forever, silently.

### Calling the actor from inside a closure

**Fire-and-forget from inside a running closure is safe.** The closure
runs off the loop goroutine, so the loop is free to receive the post,
and it simply queues behind the closure making it.

**Synchronous calls from inside a running closure deadlock**, and this
is the rule that has always applied here, unchanged. The nested post
is queued because a closure is already running; the queued closure can
only start when the active one finishes; the active one is blocked
waiting for it. Nothing breaks the cycle.

The asymmetry is worth stating plainly because the natural inference
from the first rule to the second is wrong. It is also not worth
"fixing" by detecting the nested case and running it immediately:
that is re-entrancy, which would let the inner closure observe the
outer one's half-finished mutations, and preventing exactly that is
why the actor exists.

**Where this is auditable.** Go has no dynamically-scoped values, so
there is no way to ask at runtime whether the current call is inside a
closure. Threading an operation object is the stand-in: a scoped value
made lexical, carried as the receiver rather than consulted. An
operation holding its DB handle as a private field cannot reach an
actor call by accident, so "does this operation call the actor
synchronously?" is answerable by inspection.

That reach is currently partial. `substrateOp` and `recallOp` have it;
search, recall normalization, and fetch-payload assembly are still
bare `fts` readers, and in those the question is still a whole-call-
graph one. Completing that migration is the tech debt named under
Protected Resources below, and it is what would make the rule fully
checkable rather than mostly checkable.

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

### Raciness is a property of the caller, not the reader

`InvalidateCaches` has exactly one call site, and it runs **on the
actor**. So a cache read is safe or racy according to the goroutine it
runs on, never according to which function performs it. A helper that
reads `FileIDPaths` is perfectly safe when every path into it comes from
inside a `Sync(db, …)` closure, and the same helper is a race when one
caller reaches it from an HTTP goroutine.

The consequence for auditing: **counting reader call sites overstates the
work.** The unit to inventory is the set of *off-actor entry points*, and
for each one the whole subtree it can reach. Most of the Searcher's cache
readers, for instance, are reached only from server handlers that already
run inside `Sync(srv.db, …)`, and need no migration at all.

### HTTP handlers are off-actor read operations

An `http.HandlerFunc` runs on its own goroutine. A handler that reads
cache-backed state without entering the actor is therefore an off-actor
read operation and owes a private `fts.Copy()`, bound once at the top of
the handler and threaded through everything it calls. Binding it at the
handler — rather than at each reader — is what covers the whole render
subtree: helpers that take a `*DB` inherit the view from their caller, so
one binding fixes every reader beneath it.

Handlers that reach the DB only through `Sync`/`SyncVoid` closures are
already serialized and need no view. A handler that mixes both keeps the
live `srv.db` for its `Sync` calls — a read view carries no actor — and
uses the bound view for everything off-actor.

`ark status` is the canonical mixed case. The status read enumerates every
file record for the chunk total, per-strategy tally, and total size, and
`ark status --db` additionally scans every record class for its counts —
both are index-wide reads that must not run on the actor. So the handler
does a single trivial actor hop that binds an `fts.Copy()` view and
snapshots the last-reconcile counts and the tmp-file count, then runs the
enumeration (`Status`) and the record-count scan (`StatusDB`) off the actor
on that view. The actor is held only for the bind, never for the walk, so a
cold-cache `ark status` never blocks an interleaving write.

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
