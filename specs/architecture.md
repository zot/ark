# Architecture

A cross-cutting map of ark's in-process concurrency model: the DB closure
actor, the write-actor split, the resources the actor protects, the
operation-object discipline that governs off-actor DB access, and the two
named patterns that govern how work crosses an actor boundary. This is an
**overview** — the per-feature specs it names are canonical and win on any
disagreement; this doc states the invariants they can't see on their own,
and is where a reader looks *first* before touching DB access.

The DB actor is the largest instance of the model, not the only one: the
managed-pty host is a second closure actor with its own protected state
([managed-pty.md](managed-pty.md) canonical), and the vocabulary below is
shared by both.

## The DB closure actor

All DB operations are serialized through one closure actor: a channel of
`func()` closures fed by `db.svc`, with a single goroutine keeping the
queue. Callers reach it through `Sync` / `SyncVoid` (block for a result)
or `Do` (fire-and-forget). Exactly one closure runs at any instant, so
shared DB state has no races by construction. (R986; `db-concurrency.md`
canonical.)

**Delivery is ordered per posting goroutine.** Closures posted by one
goroutine run in the order it posted them, across `Do` and `Sync` alike,
which is what makes "post the follow-up behind the message" a valid
substitute for synchronizing against it. **Posts from *different*
goroutines interleave arbitrarily** — expected, and what the actor exists
to make safe. State both halves together: "order" names two different
relations here, a total order within one sender and a partial order
across senders, and collapsing them into "ordering isn't guaranteed"
discards the only half anything can be built on. Two constraints hold
the guarantee up:
the post is a **direct send** on the calling goroutine, and the actor's
own goroutine **never runs a closure** — each executes in its own
goroutine and posts back on completion. Escaping the send into a fresh
goroutine surrenders ordering, and running work on the loop is what makes
escaping it tempting. (`db-concurrency.md` §Delivery canonical.)

**Deadlock rule:** never call `Sync`/`SyncVoid` from inside a running
closure. The nested post queues behind the closure making it, and that
closure is blocked waiting for the nested one to run. `Do` from inside a
closure **is** safe, since the work is off the loop and the post merely
queues — do not generalize from that to `Sync`.

**Panics are not recovered** at the actor. A panicking closure takes the
process down; the actor's state is already unknown at that point, and a
central recovery that failed to release the running flag would wedge the
actor with no symptom. A closure that can survive its own panic recovers
internally, as the write goroutine does (R1059).

## The write-actor split

Reads and writes take different paths through that one actor
(`db-write-actor.md` canonical; R1051–R1068):

- A **write** is queued as a closure; the actor runs it on a fresh
  goroutine over `db.fts.Copy()` — a shallow copy sharing the bbolt handle
  but with its own nil caches — so the file I/O happens off the actor.
  When the write commits, a reconcile closure runs back **on** the actor:
  `InvalidateCaches()` nils the original's caches and the next queued write
  is dequeued (continuation pattern).
- There is no second actor — the write goroutine is just a goroutine that
  sends one closure back (R1065).
- The actor runs **at most one write closure at a time** (R1067) — the
  dequeue-after-commit continuation. This one-at-a-time execution is a
  **contract** other code may rely on: a write closure's body is atomic with
  respect to every other write closure, so a check-and-set inside a write
  closure needs no extra lock. The terminal connections-doc transition
  (`finalizeConnectionsDoc`, R3164) is the first consumer — it flips `Done`
  and writes the doc in one closure, so the write is durable before `Done` is
  observable. Parallelizing writes for throughput would silently break such
  consumers.

## Protected resources

microfts2's Go-side caches — `pathCache`, `pathToID`, `frecordCache` — are
lazily loaded on read and nil'd by the reconcile step's `InvalidateCaches`.
Go maps are unsafe for concurrent read/write, so these are
**actor-protected**: a non-actor goroutine must not read them off the
shared original. (R995; `db-concurrency.md` §Protected Resources canonical.)

The bbolt index itself is MVCC-safe (one writer + many concurrent readers),
so bbolt `View` reads (e.g. `ReadCRecord`, `ViewChunkEmbeddings`) are safe
off the actor — only the Go caches above them are not.

The parsed `Config` is **copy-on-write** instead: never written once
published, every change made to a deep copy and installed by re-pointing all
its holders at once, so any goroutine may read the one it holds. (R3673-R3675;
`db-concurrency.md` §The configuration is copy-on-write canonical.)

## The operation-object discipline

An **operation object** gathers everything a request-shaped unit of work
needs into a struct, turns its work into methods on that struct, and
becomes the **sole mediator of its DB access**. It is the Monadic Wrapper
pattern used as an access discipline: the receiver carries the actor/copy
rule so it isn't threaded through every call, and "does this access respect
the actor?" collapses to one grep of `db.fts` / `srv.db` instead of a
question asked at every call site — the property whose absence let the O154
race sit latent.

The two paths an operation takes:

- **Off-actor reads** go through a private `fts.Copy()`.
  `l.db.withFTS(l.db.fts.Copy())` returns a read view whose caches are
  private, so its `FileIDPaths` / `FileInfoByID` / `SearchFuzzy` reads never
  race `InvalidateCaches`. Reads stay off the actor, so a long read pass
  never stalls writes. The copy shares the overlay pointer (its own mutex,
  untouched by `InvalidateCaches`), so tmp:// documents still resolve.
- **Writes** go through the write actor.

**Lifecycle work is not an operation** — the actor loop, the source
watcher, and pubsub are long-lived, not request-shaped; they own their own
goroutines.

### Instances

- **substrateOp** (`connections_substrate.go`) — the normal-mode
  find-connections substrate computation; the first operation and the
  reference implementation of the read-via-`fts.Copy()` rule. (R3163;
  `find-connections-substrate.md`.)
- **recallOp** (`recall.go`) — one `Recall` computation. Holds the
  copy-bound read view as its sole fts door and routes every fts-cache read
  through it: `normalizeInputs`, `substrateChunkText`, `SearchFuzzy`,
  `resolveSearchEntryChunkID`, `ChunkInfo`, `FileStrategy`, the result-build
  `NewChunkCache`, and the `chatFunnel` helper (an `op` method). Embedding,
  `SearchChunks`, and store reads stay on the live Librarian. `FindConnections`
  runs its enqueue-time normalization through the same db-taking
  `normalizeInputs` on a one-shot copy. (R3163.)
- **BuildFetchPayload** (`connections.go`) — the in-flight fetch assembly
  reads `FileIDPaths` + chunk content through a local
  `l.db.withFTS(l.db.fts.Copy())` view: the copy-read discipline applied
  without a dedicated op struct, since the read is self-contained. (R995.)
- **Off-actor HTTP read handlers** — `handleContentView` (`server.go`) and the
  Librarian's `HandleExpandSearch` (`librarian.go`) run on the HTTP goroutine
  and bind one `withFTS(fts.Copy())` view at the top of the handler, threading
  it through the whole render subtree. Because the helpers beneath take a
  `*DB`, one binding covers every reader under it — `ChunkIDByLocation`,
  `ChunkIDsForPath`, `ResolveLink`, `resolveFilePath`,
  `renderPdfChunksByPage`, `SearchGrouped`. Their `Sync(srv.db, …)` calls keep
  the live DB: a read view carries no actor. (R3165.)

### The HTTP front door

An operation is served over HTTP by the generic `handle(srv, someOp{})`
wrapper, which copies the prototype per request, calls `init` (where the
actor-or-copy-view decision is made **once**), then `run`. `run` is
HTTP-agnostic — it returns a value and a semantically classified error, and
the wrapper picks the status code — so one operation can back HTTP, the CLI,
and in-process callers. Owned by [http-operations.md](http-operations.md)
(R3166–R3170); `statusOp` and `fetchOp` (`server.go`) and `expandSearchOp`
(`librarian.go`) are the worked examples, the last of them binding its own
`fts.Copy()` view in `init`.

Adoption is **incremental**: the remaining handlers stay ordinary
`http.HandlerFunc`s and convert as they are touched. The end-state one-grep
audit — every `srv.db` use is an operation's binding or a bug — arrives only
at full adoption and is not on its own worth a mechanical sweep (R3170).
Handlers that must write a non-JSON body are permanently outside the pattern.

### Direction (not yet built)

The `Searcher`'s own cache readers (`search.go`) need **no** migration: every
path into them comes from a server handler already inside `Sync(srv.db, …)`,
and `InvalidateCaches` runs only on the actor, so they are serialized by
construction. Raciness follows the calling goroutine, not the reader — see
`specs/db-concurrency.md`, "Raciness is a property of the caller." An earlier
plan counted those reader call sites as pending work; that was the wrong unit.

What remains is the opportunistic conversion of the rest of the handlers, and
the `srv.db` audit that only becomes meaningful once that is far along. Both
are **auditability** goals, not live races: the known racy entry points are
fixed (R3165). Tracked as **PENDING #46** (design.md **O156**). Design sketch:
[.scratch/OPERATION-OBJECTS-20260716.md](../.scratch/OPERATION-OBJECTS-20260716.md).
