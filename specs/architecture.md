# Architecture

A cross-cutting map of ark's in-process concurrency model: the DB closure
actor, the write-actor split, the resources the actor protects, and the
operation-object discipline that governs off-actor DB access. This is an
**overview** — the per-feature specs it names are canonical and win on any
disagreement; this doc states the invariants they can't see on their own,
and is where a reader looks *first* before touching DB access.

## The DB closure actor

All DB operations are serialized through one closure actor: a channel of
`func()` closures (`db.svc`) drained by a single goroutine (`runSvc`).
Callers reach it through `Sync` / `SyncVoid` (block for a result) or `Do`
(fire-and-forget). Code inside the actor runs sequentially, so shared DB
state has no races by construction. (R986; `db-concurrency.md` canonical.)

**Deadlock rule:** never call `Sync`/`SyncVoid` from inside the actor — it
enqueues a closure and blocks on the reply, but the actor is the caller,
so it waits for itself.

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

## Protected resources

microfts2's Go-side caches — `pathCache`, `pathToID`, `frecordCache` — are
lazily loaded on read and nil'd by the reconcile step's `InvalidateCaches`.
Go maps are unsafe for concurrent read/write, so these are
**actor-protected**: a non-actor goroutine must not read them off the
shared original. (R995; `db-concurrency.md` §Protected Resources canonical.)

The bbolt index itself is MVCC-safe (one writer + many concurrent readers),
so bbolt `View` reads (e.g. `ReadCRecord`, `ViewChunkEmbeddings`) are safe
off the actor — only the Go caches above them are not.

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

### Direction (not yet built)

The remaining off-actor bare-`fts` readers — Recall normalization, the
search-side `FileIDPaths` callers, `BuildFetchPayload` — still read the
shared original and are the last live instances of the O154 race class.
Migrating them onto operations, and converting HTTP handlers into operation
wrappers (`srv.handler(SomeOp{})`, copying an empty prototype per request)
so the discipline is grep-auditable across the server, is tracked as
**PENDING #46** (design.md **O156**). Design sketch:
[.scratch/OPERATION-OBJECTS-20260716.md](../.scratch/OPERATION-OBJECTS-20260716.md).
