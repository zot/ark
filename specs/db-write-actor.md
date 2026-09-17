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

### Serialization contract

At most one write closure runs at a time (R1067) — the dequeue-after-commit
pattern above. This one-at-a-time execution is not merely a performance
property; it is a **contract other code may rely on**: a write closure's body
runs atomically with respect to every other write closure, so a check-and-set
performed inside a write closure needs no extra lock or atomic. The terminal
connections-doc transition (R3164) is the first consumer — it flips the
record's `Done` and writes the doc inside one closure, so the first terminal
write wins the dedup and `Done` becomes observable only once the write is in
flight (and thus counted by `WaitWritesIdle`). Parallelizing writes for I/O
throughput would silently break such consumers and must be weighed against
them, not done blindly.

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

## Post-reconcile embedding: compute off-slot, store on-slot

The post-reconcile embedding pass (tag name/value/definition embeddings and
chunk EC/EF records) must not hold the single write slot for the duration of
its GPU compute. A full embed of every missing chunk can run for many seconds;
held inside one write closure it freezes every interleaving write — tmp:// UI
documents, DMs, config edits — for that whole time. Measured 2026-09-17: a
tmp:// write that costs ~45 ms idle blocked 16.2 s during a reconcile burst,
while reads (MVCC) stayed flat.

The pass is therefore split:

- **Compute off the slot.** The GPU embedding runs in a plain goroutine holding
  no write transaction. The reads it needs — missing-embedding scans, chunk
  content, existing-EC lookups — use MVCC and never block.
- **Store on the slot, in small batches.** Each batch's store writes —
  `WriteChunkEmbedding` / `WriteChunkEmbeddingBatch`, `WriteFileCentroid`, and
  the tag `WriteTagNameEmbedding` / `WriteTagValueEmbedding` /
  `WriteTagDefEmbedding` — are applied inside their own write closure via
  `enqueueWrite`, so the mutation stays serialized on the write actor (cache
  invalidation and the one-at-a-time contract preserved) while the slot is
  released between batches. A file's EF centroid recompute is enqueued after
  that file's EC writes, so it reads them committed.
- **One pass at a time.** Because the pass now runs off the actor, a second
  reconcile could otherwise launch a concurrent embed goroutine. A re-entrancy
  guard, checked and set on the DB actor, admits only one.

An embedding store write is a DB mutation and routes through the write actor
like every other — it is never called directly from the off-slot goroutine.

## Per-file scan actor: index compute off-slot, store on-slot

File indexing has the same shape of problem the embedding pass had, one layer
earlier. A file's chunk + trigram + token compute (microfts2's `collectChunks`)
is the expensive part, and until now it ran *inside* the write closure, fused
with the store. So while the server reindexes a burst of files, every
interleaving write — a tmp:// UI document, a DM, a config edit — queues behind
the whole chunk-and-gram pass. Measured 2026-09-17: after the embedding pass
moved off-slot, the residual first-write stall in a reconcile burst was ~10 s,
and it is this fused refresh batch.

The three server-side async entry points — new-file add (`ScanAsync`), stale
refresh (`RefreshAsync`), and per-path watcher updates (`IndexPathsAsync`) —
therefore index each changed file through a **per-file scan actor** rather than
running the indexer on the write slot. Config files (`ark.toml`) stay
synchronous in the DB actor, as before.

The split mirrors the embedding split:

- **Compute off the slot.** For each changed file a plain goroutine reads the
  file and runs a single microfts2 `Prepare*` call — `PrepareFile` for a full
  add or reindex, `PrepareAppend` for an append — holding no write transaction.
  This is where chunking, trigram, and token computation happens. The reads it
  needs (the committed tail locator for an append) use MVCC and never block.
- **Store on the slot.** The store step runs through `enqueueWrite`:
  `StorePrepared` (fresh add), `ReindexPrepared` (content-diff reindex, which
  derives its own old-chunk removal set in-transaction from the committed F
  record — the caller hands only the prepared handle), or `AppendPrepared`. All
  post-store bookkeeping is preserved (chunkid-keyed tag values and defs, @ext
  routing, tag pubsub, the schedule date index, pdf blob flush, bible
  book-index flush, and the recall-watcher append hook), fed by microfts2's
  `WithIndexedChunkCallback` during store and `WithRemovedChunkCallback` for
  orphan cleanup.

### The registry and the changeNum

The **write actor owns a registry** of in-flight file scans, keyed by path,
each carrying a monotonically-stamped `changeNum`. Lookup, create, and retire
all run on the DB actor — no mutex, the actor serializes them. This is what
makes the per-path coalescing the old `pendingRefresh` set did (skipping
duplicate refresh work for a saturated write actor) fall out of the model: a
new change event for a path with a scan already in flight bumps the
`changeNum`, superseding the older scan, so only the last store lands.

- **start-scan** (on the DB actor) stamps the file's registry entry with the
  next `changeNum` and launches the off-slot compute tagged with that stamp.
- **The store step re-checks `changeNum` equality** before storing. A scan
  whose stamp no longer matches the registry entry (a newer change bumped it)
  is dropped without storing — the **changeNum redo-escape**. Both full and
  append respect supersession, which is why the append tail-moved guard never
  fires in practice: a superseded stale append is dropped before its store is
  ever attempted.
- **A full store also runs a content-staleness escape**: before storing, the
  committed (FileLength, ContentHash) is compared against the prepared file's
  length + hash; if already identical, the store is a redundant no-op and is
  skipped. **Appends are exempt** from this escape — an append's new bytes are
  by definition beyond the committed length.
- **Retire runs on the DB actor, after the store, and only if the entry's
  `changeNum` still matches** — a newer scan keeps the entry. Retiring on the
  actor (never off it) closes the retire-versus-new-event race.
- **A store failure logs, retires the entry, and re-emits a single (bounded)
  change event** for the path so the normal machinery re-scans it once. It is
  never an inline full reindex.

The stamp is drawn from a single monotonic counter, not reset per entry, so a
stale compute from a retired-and-recreated entry can never collide with a fresh
one (the ABA guard).

### Per-chunk compute: preemption and the reuse map

The whole-file compute above abandons a *whole file's* work when a newer event
supersedes it, and recomputes every chunk's grams on each scan even when only
one chunk changed. For a large, rapidly-edited file both costs add up. The
compute phase is therefore taken to chunk granularity — the store phase, the
registry, and the changeNum machinery are unchanged.

For a full add or reindex the off-slot goroutine:

- **Splits once.** `SplitFile` cuts the file into content-hashed chunks with no
  gram computation — cheap. Each chunk carries microfts2's authoritative
  `SplitChunk.Hash` (SHA-256 of its content), which is also the store's dedup
  key, so the reuse map keyed on it never drifts from what the store deduplicates.
- **Computes grams per chunk, reusing where it can.** A **chunk-hash → grams
  reuse map** lives in the path's registry entry. For each split chunk, on a map
  hit the cached grams are reused; on a miss `PrepareChunk` computes them and the
  result is cached. Positions come from the current scan's `SplitChunk`, so a
  reused gram lands each occurrence at its own range — reuse is safe across scans.
- **Preempts between chunks.** Before computing each chunk the goroutine consults
  the registry entry on the DB actor; if a newer event has bumped the path's
  `changeNum` past this scan's stamp, the scan abandons **between chunks** (never
  inside a `PrepareChunk`) and stores nothing. Its already-finished per-chunk
  grams stay in the reuse map, so the newer scan reuses them and a rapidly-edited
  file converges instead of starving.
- **Assembles and stores unchanged.** `AssembleFile` folds the per-chunk grams and
  file metadata into the same opaque `*PreparedFile` the store step already takes;
  the store side (`StorePrepared` / `ReindexPrepared`) is unchanged.

The reuse map lives in the registry entry and is touched only on the DB actor
(no mutex); the off-slot loop consults and updates it through actor hops, which
are also the changeNum preemption checkpoints. It survives across superseded
scans of the path (that is what makes convergence work) and is discarded when
the entry retires after the store.

**No runtime invalidation is needed.** Grams are a pure function of chunk content
and the trigram configuration, and ark's trigram configuration (case-insensitivity
and character aliases) is fixed at microfts2 `Create` and immutable for the
process lifetime — a case-fold change is deferred to a restart. So a cached gram
can never observe a trigram-config change within its lifetime. A chunker change
alters chunk boundaries and therefore hashes, which produces reuse *misses*, not
stale hits.

Appends keep the single whole-append `PrepareAppend` compute: an append is
additive and usually one new chunk, per-chunk preemption of a one-chunk compute
buys nothing, and a superseded append is already dropped whole at the changeNum
redo-escape.

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

The per-file scan actor additionally needs the transaction-free compute /
transaction-bound store split:

- `PrepareFile(fpath, strategy, opts) (*PreparedFile, error)` and
  `PrepareAppend(path, lastLocator, content, strategy, opts) (*PreparedAppend,
  error)` — the off-slot compute. They open no bbolt transaction and return an
  opaque, single-use handle carrying the derived per-chunk data.
- `StorePrepared(*PreparedFile, opts)`, `ReindexPrepared(*PreparedFile, opts)`,
  and `AppendPrepared(fileid, *PreparedAppend, opts)` — the on-slot store. They
  open the write transaction. `ReindexPrepared` derives the old-chunk removal
  set in-transaction from the committed F record. `WithIndexedChunkCallback`
  and `WithRemovedChunkCallback` fire during store; `AppendPrepared` returns
  `ErrAppendTailMoved` if the committed tail moved since prepare.

The per-chunk compute (above) uses the finer split of the same seam:

- `SplitFile(fpath, strategy) ([]SplitChunk, FileMeta, error)` — chunk boundaries
  and content hashes with no grams, transaction-free. `SplitChunk.Hash` is the
  authoritative dedup key.
- `PrepareChunk(content) *PreparedChunk` — one chunk's grams (trigram counts +
  tokens), a pure function of content and the trigram config, cacheable by hash.
- `AssembleFile(fpath, strategy, []PreparedEntry, FileMeta) (*PreparedFile, error)`
  — folds the ordered per-chunk grams and file metadata into the same opaque
  `*PreparedFile` the store accepts. Positions come from each entry's `Chunk`.

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
