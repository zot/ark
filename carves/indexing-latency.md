# Indexing dominates the write slot

`ark message inbox` and every other UI action that writes goes through the DB
write actor's single write slot (`specs/db-write-actor.md`, R1067: at most one
write closure at a time). Today that slot also runs file chunking, trigram
computation, and embedding — so while ark reindexes, every tmp:// write, DM,
recall doc, and config change queues behind it. Bill sees this as very bad UI
latency. Measured 2026-09-17: a tmp:// write that costs ~45 ms idle blocked
**16.2 s** during a reconcile burst, while reads (MVCC, main actor) stayed flat.
Full evidence in
[.scratch/LATENCY-FINDINGS-20260917.md](../.scratch/LATENCY-FINDINGS-20260917.md).

## Status

- [x] ~~**Item 1a — per-file actor, WHOLE-FILE compute off-slot, store on-slot.**~~ **LANDED (`23bba08`, 2026-09-17.)** R3419-R3427; per-file scan actor in db.go/indexer.go, R3005 retired T280. Measured on `3401d2b`: burst steady-state writes stay ~47ms under load (a fused batch held them all before); the single first-write-during-burst sample (16.2→10.3→12.1s across #baseline/#129/#130+#131) is too noisy to score by — the store step stays on-slot by design. See `.scratch/LATENCY-FINDINGS-20260917.md`.
- [x] ~~**Item 1b — per-chunk preemption + chunk-hash reuse map.**~~ **LANDED (`3401d2b`, 2026-09-17.)** SplitFile→reuse-map(SplitChunk.Hash)→PrepareChunk-on-miss→changeNum-between-chunks→AssembleFile; R3428-R3432, alarms 6+7. Appends kept whole-append (A78).
- [x] ~~**Item 2 — changeNum preemption + length+hash store-boundary check.**~~ **LANDED (`23bba08`, 2026-09-17.)** changeNum supersession + length+hash escape at the store boundary; appends exempt.
- [x] ~~**Item 3 — move embedding out of the write closure.**~~ **LANDED (`4eb50b9`, 2026-09-17.)** Embed compute off-slot, EC/EF store in per-batch enqueueWrite closures (R3414-R3416). Measured burst first-write stall 16.2s → 10.3s; the residual ~10s is the on-slot refresh batch (Items 1/2, not yet built), which runs before the embed. See `.scratch/LATENCY-FINDINGS-20260917.md`.
- **Item 4 — yield the slot between files in a batch reconcile.** **SUBSUMED by Items 1/2 (Bill, 2026-09-17).** No checkbox: per-file actors make yielding between files automatic — the write actor only ever holds a store.
- [ ] **Item 5 — scan enumeration + classification off the DB actor.** **OPEN (#132.)** Measured 2026-09-17: the residual ~15s hold per reconcile burst is the enumeration (`scanner.Scan()` — 8k `os.Stat` + stale reads — plus per-file `os.Stat`/`CheckFile` in `startFileScan`), synchronous on the `db.svc` actor inside `doReconcile`→`ScanAsync`. Only the per-file compute left the slot (#130/#131). Fix: enumeration + classification off the actor; only the registry stamp + enqueue stay on it.
- [ ] **Item 6 — parallelize the scan walk to ncdu/dust/gdu-class speed.** **OPEN (not queued.)** Parallel per-directory walk with a bounded worker pool; stat-only stale classification; and the cap on the currently-unbounded per-file compute goroutines.

## How it surfaced

Bill reported very bad UI latency and suspected file indexing/embedding was
dominating the write actor (`.scratch/LATENCY.md`). Reading the write path
confirmed it and a measurement quantified it: reads are unaffected (MVCC on the
main actor), but the single write slot is held for the entire chunk + gram +
store of each file (`executeFullRefresh` → `fts.ReindexWithCallback`,
indexer.go:918) and for the entire GPU embed pass (`server.go:908`,
`enqueueWrite` → `BatchEmbedChunks`). A tmp:// write behind a burst blocked
16.2 s vs 45 ms idle.

## Target design (Bill's diagram)

Convert the refresh to an **optimistic** shape:

```
goroutine (off the slot)                    write slot (short)
  file change event                              |
  get file timestamp                             |
  chunk file, hash chunks, compute grams ------->|
                                            check timestamp
                                            if changed: queue redo, return
                                            else:
                                              store new chunks
                                              remove obsolete chunks
                                              update metadata
```

The slot does only the cheap timestamp check + store; the expensive compute
happens in a plain goroutine holding no write transaction. The redo-escape
handles the file changing again mid-compute. **Appends (chat JSONL) skip the
redo-escape** — an append is additive, and a later append re-fires on its own.

Today `prepareRefresh` (indexer.go:770) is a partial version of the off-slot
step: it reads the file and detects append-vs-full off the slot, but stops
before chunking/grams, and in the enqueue path the whole batch still runs inside
one write closure (db.go:1666). There is no timestamp recheck at store time.

## Design — per-file actors (Bill, 2026-09-17)

Bill's design for Items 1 and 2 lives in the private working note
`FILE-INDEX-REDO.md`; it is folded in here. A **per-file actor** owns one
changed path:

- The **change-event goroutine** reads the file into a `chunkSlice` of structs,
  each a chunk plus empty slots for FTS data and embedding data.
- **start-scan** is queued through the **write actor**, which owns the file-actor
  registry (lookup, create, retire). start-scan bumps `fileStruct.changeNum` and
  queues the first `process-chunk` in the file actor.
- **process-chunk(index, changeNum)** runs one chunk at a time in the file actor:
  if `changeNum < fileStruct.changeNum` it returns early (a newer scan superseded
  it); otherwise it computes FTS data for that chunk, consulting a
  **chunk-hash → computed-result map** in the fileStruct first so a superseded
  scan's finished chunks are reused and a rapidly-edited file converges instead
  of starving. Remaining chunks queue the next; when none remain it queues a
  **store closure on the write actor**.
- The **store closure** re-checks `changeNum == fileStruct.changeNum` (the whole
  dirty check — one mechanism), and if current stores the chunks, hands microfts2
  the old chunk set to remove, and **retires the file actor**.
- **Embeddings are not per-chunk.** The file actor owns FTS data only; embedding
  goes to the single global embed actor (Item 3, now off-slot) in batches of
  chunk hashes, results landing in the same hash map. The `changeNum` check sits
  between FTS chunks and between embed batches, never inside a batch.

**DECIDED (Bill, 2026-09-17): a per-file actor per changed path, start-scan and
retire routed through the write actor so registry lookup/create/retire are
atomic.** Retire must stay on the write actor — moving it off reopens the
retire-versus-new-event race.

**DECIDED (Bill, 2026-09-17): preemption at chunk granularity via `changeNum`.**
A superseded scan abandons at most one chunk's compute; the hash-reuse map keeps
its finished work.

**DECIDED (Bill, 2026-09-17): the store-boundary staleness check is `changeNum`
equality**, and content staleness is length+hash (what `prepareRefresh` already
computes), never an mtime re-stat.

**DECIDED (Bill, 2026-09-17): every entry point that indexes a file emits
per-file change events into the per-file-actor machinery instead of calling the
indexer on the write slot; the write slot only ever runs the store step.** Today
these entry points run indexing inside `enqueueWrite` closures and must move
off-actor like the rest — implementer checklist:

- **watcher events** → `IndexPathsAsync` → `syncOnePath` (db.go:1685)
- **startup + periodic reconcile** → `doReconcile` (server.go:866, driven by
  `reconcile` server.go:842) → `RefreshAsync` (server.go:896) and the trailing
  embed/complete closures
- **`ark scan`** → scan → `AddFile` in the scan-content closure (db.go:1601)
- **`RefreshAsync`** → `refreshBatch` (db.go:1666)
- **`IndexPathsAsync`** (db.go:1685)

Each becomes: read the changed file, emit its chunk slice into the file actor,
and let the file actor drive compute → store as above. The whole-batch
`enqueueWrite` closures (db.go:1601/1666/1685) are replaced by per-file store
closures — which is also why Item 4 is subsumed.

**DECIDED (Bill, 2026-09-17): build the per-file actor whole-file now; per-chunk
preemption and the reuse map follow when microfts2 ships the per-chunk API.** So
Item 1 splits: **1a** (whole-file, #130) builds the per-file actor against the
current whole-file seam — the off-slot compute is a single `Prepare*` call and a
superseded scan abandons one whole-file compute (no reuse map); **1b** (per-chunk,
blocked on the request) adds chunk-granularity preemption and the chunk-hash→result
reuse map when the per-chunk API lands. The store side and the actor/registry shape
are shared, so 1b changes only the compute phase.

## Items

- **Item 1 — per-file actor (design above).** The file actor computes FTS data
  off-slot; the write actor holds only the store + retire.
  **microfts2 answer (with file:line):** microfts2 *does* diff by chunk content
  hash at store time — `dedupOrCreateChunk` (microfts2 db.go:1444) checks the H
  record and, on a hash hit, reuses the chunkid without re-storing that chunk's
  grams. But it does **not** skip the *compute*: `collectChunks` (db.go:1355, via
  `indexChunk`, R656) re-reads the file and computes every chunk's hash +
  `triCounts` (grams) + tokens into the unexported `collectedChunk` **before**
  dedup, inside the same call that opens the write txn
  (`AddFileWithContent`/`ReindexWithCallback`/`AppendChunks`, db.go:1318/1842/2123
  → `addFileInTxn`, db.go:1552). So the expensive gram compute runs on the slot
  regardless, and there is no public seam to hand in precomputed chunk data.
  **A microfts2 API change is needed:** a pure transaction-free *compute* entry
  returning prepared chunks (Chunk + hash + triCounts + tokens), and a *store*
  entry taking a fileid + those prepared chunks + the old chunk set to remove,
  doing only `addFileInTxn`'s work. Request drafted at
  `requests/precomputed-chunks-request.md`.
  `@ark-request-sent: requests/precomputed-chunks-request.md`

  **microfts2 reply — COMPLETED (2026-09-17), API implemented in ~/work/microfts2
  working tree.** They built the two-phase split as opaque handles (as ark
  preferred):
  - **Compute (no bbolt txn, any goroutine):** `PrepareFile(fpath, strategy,
    opts) (*PreparedFile, error)` (microfts2 db.go:1435), `PrepareContent(fpath,
    strategy, content, opts)` (db.go:1447), `PrepareAppend(path, lastLocator,
    content, strategy, opts) (*PreparedAppend, error)` (db.go:2346). The handle
    carries derived per-chunk data (hash, trigrams, tokens) + modTime/hash/length;
    record layout stays private.
  - **Store (opens the write txn):** `StorePrepared(*PreparedFile) (uint64,
    error)` (db.go:1518, fresh add), `ReindexPrepared(*PreparedFile) (uint64,
    error)` (db.go:2038, content-diff reindex), `AppendPrepared(fileid,
    *PreparedAppend) error`.
  - Handles are single-use (store releases the content); `WithChunkCallback`
    fires during compute, `WithIndexedChunkCallback`/`WithRemovedChunkCallback`
    during store. Fused AddFile/Reindex/AppendChunks are unchanged (now
    compute-then-store internally), so existing calls keep working during
    migration. Spec: microfts2 specs/indexing.md, specs/append.md; R676-R689.

  **What they declined/changed from our ask (affects the store step, not a
  redesign):** ark asked to hand microfts2 the *old-chunk removal set*.
  microfts2 **declined** — it derives the removal set **in-txn** from the
  committed F record (chunk refcounts are shared across files, so the orphan
  cascade must be consistent with committed state; an off-actor set could race a
  concurrent store). So **ark's store closure supplies only the prepared handle,
  not a diff**: `ReindexPrepared(handle)` looks up the old file and drops
  occurrences itself, preserving chunkids for unchanged content (add-before-drop,
  R673). For Bill's per-file-actor design this *simplifies* the store step — it
  passes the handle, and the `changeNum`-equality dirty check stays ark-side
  before the call; the "old chunk set to remove" line in the design section is
  now microfts2's job, not ark's. Append adds `ErrAppendTailMoved` (db.go:2266)
  if the committed tail moved between prepare and store — under the per-file
  actor (which owns the tail) it never fires; it guards a stale handle only.
  One open option: `ReindexPrepared` exposes only the option-based callbacks, not
  the raw positional `ReindexCallback`; ark can request a `WithReindexCallback`
  option later if the hot path needs it.

  **Granularity fork (recorded honestly, 2026-09-17).** microfts2's shipped seam
  is **whole-file and un-preemptible**: `PrepareFile` (microfts2 db.go:1435) →
  `collectChunks` (db.go:1328) computes every chunk's grams in one call, and
  `*PreparedFile` (db.go:1406) is opaque, so the per-chunk `process-chunk` loop
  and the chunk-hash→result reuse map in the Design section **cannot be built
  against it**. The two disagree: the Design section (per-chunk) was written
  ahead of the coarser seam that landed. **Per-chunk remains the target — Bill's
  DECIDED stands.** whole-file is what microfts2 has *today*. A per-chunk request
  was sent: `.scratch/microfts2-per-chunk-prepare-request-20260917.md`.
  `@ark-request-sent: requests/microfts2-per-chunk-prepare.md`

  **microfts2 reply — COMPLETED (2026-09-17), per-chunk API in ~/work/microfts2
  working tree.** They gave exactly the split we asked for (nothing declined),
  with two shaping decisions:
  - **Split (txn-free):** `SplitFile(fpath, strategy) ([]SplitChunk, FileMeta,
    error)` (microfts2 db.go:1436), `SplitContent` (db.go:1475), `SplitAppend`
    (db.go:2491) — cheap boundaries + text, no grams. `SplitChunk` carries
    `Hash = sha256(Content)`, **microfts2's authoritative hash** (db.go:1332).
  - **Per-chunk (content-pure, cacheable):** `PrepareChunk(content)
    *PreparedChunk` (db.go:1494) — grams-only (trigram counts + tokens), no
    positions.
  - **Assemble:** `AssembleFile(fpath, strategy, []PreparedEntry, FileMeta)
    (*PreparedFile, error)` (db.go:1546), `AssembleAppend` (db.go:2500) → the
    existing opaque handles → `StorePrepared`/`ReindexPrepared`/`AppendPrepared`
    unchanged.

  **Two changes from our ask (note, not a redesign):** (1) **microfts2 computes
  the hash in split, ark never hashes** — so the reuse-map key IS the dedup key
  by construction (no drift). Bill's design said "chunk-content hash" as the key;
  it becomes microfts2's `SplitChunk.Hash`. (2) `*PreparedChunk` is **grams-only**;
  positions (Range/Locator/Attrs) come from each scan's `SplitChunk` at assemble,
  so a reused token lands each occurrence at its own range — reuse is safe across
  scans *and* files.

  **What it means for the process loop + reuse map:** fully buildable. The 1b
  loop is: `SplitFile` once → key the reuse map on `SplitChunk.Hash` →
  `PrepareChunk` only on a miss → preempt between chunks on `changeNum` with
  finished grams cached → on completion `Assemble` → `StorePrepared`. The store
  side and the actor/registry from 1a are shared, so **1b changes only the
  compute phase** (replace 1a's single `Prepare*` call with split→loop→assemble).
  Requirements R690-R699 microfts2-side; standing precondition: the reuse map is
  valid while trigram config (case-fold/aliases) is stable.
  Until it lands, ark may build the **whole-file** path first (per-file actor,
  whole-file preemption — a superseded scan abandons one whole-file compute, no
  reuse map; not a regression, just the un-taken optimization) and switch the
  compute phase when per-chunk arrives; the store side is shared.
- **Item 2 — length+hash redo-escape.** On the actor, before storing, recompare
  the file's length + content hash against what prep computed; if changed,
  requeue the path (the R3005 coalescing claim/release machinery already exists
  in `IndexPathsAsync`) and return without storing. Appends exempt.
  **DECIDED (Bill, 2026-09-17): length+hash is the staleness check at the store
  boundary** — not a timestamp re-stat. `prepareRefresh` already computes both
  (`FileLength`, `ContentHash`), so the escape reuses them rather than adding an
  mtime read that can race the content.
- **Item 3 — embedding off the slot.** `BatchEmbedChunks` (server.go:908,
  librarian.go:2096) runs the whole embed inside one write closure. Move the GPU
  work off the slot and apply EC/EF writes in small actor steps (or a dedicated
  low-priority queue) so a long embed never freezes UI writes. Independent of
  Items 1–2 and possibly the biggest single win, since embedding is the longest
  hold.
- **Item 4 — SUBSUMED by Items 1/2 (Bill, 2026-09-17).** Once each changed file
  is its own actor and the write actor only ever holds a store, yielding between
  files is automatic — the whole-batch `enqueueWrite` (db.go:1666) is replaced by
  per-file store closures. No separate work remains.

- **Item 5 — enumeration off the actor.** #130/#131 moved the per-file *compute*
  off the slot, but the reconcile's *enumeration and classification* stayed on the
  `db.svc` DB actor and is the measured ~15s hold: `doReconcile` (server.go:867,
  on the actor) calls `ScanAsync` (db.go:1569) synchronously → `scanner.Scan()`
  (db.go:1574) stats ~8k files and classifies stale, then `startFileScan`
  (db.go:1760) does `os.Stat` + `fts.CheckFile` per file before dispatch. A tmp://
  write must hop onto `db.svc` to `enqueueWrite`, so it blocks for the whole
  enumeration. Fix: run `scanner.Scan()` and the per-file classification off the
  actor; only `stampFileScan` + the dispatch run on it. Evidence:
  `.scratch/LATENCY-FINDINGS-20260917.md` (36 of 37 writes ~50ms, one 15.7s at
  burst start).

  **DECIDED (Bill, 2026-09-17): enumeration and classification off the actor;
  only stamp + dispatch on it.** `scanner.Scan()`'s walk (8k `os.Stat` + stale
  classification) runs in a goroutine, reading the DB through the existing
  off-actor read path (`fts.Copy()`/`withFTS` operation objects, never a mutex);
  the per-file `os.Stat`+`fts.CheckFile` (db.go:1763-1764) move into the file
  actor's off-slot phase. `recordScannedLinks`/`NewUnresolved`/`CleanUnresolved`
  (db.go:1590-1599) stay on the actor as writes but batched after the walk
  returns, not interleaved. `doReconcile` kicks the goroutine rather than walking
  synchronously; a second reconcile while a walk is in flight coalesces on a
  single in-flight flag held on the actor (changeNum's spirit).

  **DECIDED (Bill, 2026-09-17): `ark status` reads off the actor and reports the
  last reconcile's stale count, never walking files.** Folded into the #132 pass
  (no separate item). `DB.Status()` was calling `fts.StaleFiles()` — an `os.Stat`
  (+read/hash on mtime mismatch) per F record — inside the db.svc actor Sync
  (server.go:1537), so every `ark status` re-stat'd ~8k files on the actor; at
  cold-cache startup that is the long-standing ~1min `ark status` (NOT a #132
  regression — that path was on the actor before and after). Fix: `Status()` runs
  off the actor on a bound `fts.Copy()`, and the `stale:`/`missing:`/`unresolved:`
  counts come from what the last reconcile computed (stored when
  `applyReconcileScan` lands, actor-owned, no mutex), printed with an as-of time
  (`stale: 211 (as of 12:47:03)`, `-` before the first reconcile). Status never
  hashes; length+hash stays at the store boundary.

  **DECIDED (Bill, 2026-09-17): the reconcile walk targets ncdu/dust/gdu-class
  speed.** Concrete target: the ~8k-file corpus walked **and** classified in well
  under 1s warm, ≤2s cold. Those tools hit that by a **parallel per-directory walk
  with a bounded worker pool**, using the `DirEntry` type from `readdir` so
  recursion needs no `stat`, **one `lstat` per leaf** for size+mtime, and **no
  hashing**. ark's `Scanner.Scan` (scanner.go:84-91) is today a single-goroutine
  sequential `filepath.WalkDir` per source — the opposite shape.

- **Item 6 — parallelize the scan walk.** Rewrite `Scanner.Scan` (scanner.go:84-91)
  as a parallel per-directory walk: an errgroup / worker pool sized to
  `GOMAXPROCS` or a named constant, walking subtrees concurrently, results merged
  into the batched apply (`applyReconcileScan`, db.go:1739). Stale classification
  stays **stat-only** — compare `mtime`+`size` against the stored F record and hash
  **only on a mismatch** (already the rule in `classifyFile`); the walk itself does
  one `lstat` per leaf and never reads file contents. Fold in the cap the earlier
  note flagged: the per-file compute goroutines are **currently unbounded** — one
  `go db.prepareFileScan` per in-flight file (db.go:2002), no pool/semaphore — so
  Item 6 adds a bounded worker pool there too (a reconcile of thousands of stale
  files must not spawn thousands of concurrent computes). Ship a **stamped rig**
  measuring walk wall time warm and cold (dropping caches needs root — if
  unavailable, measure warm and say so). This is the last piece of the latency
  goal after #130/#131/#132 moved the work off the actor: those made the walk not
  *block* anything; Item 6 makes it *fast*.

## Open questions

- **R3005 coalescing vs the `changeNum` registry — DECIDED (Bill, 2026-09-17):
  retire R3005 in favor of `changeNum` if fully subsumed, else layer and say why.**
  The registry keeps one file-actor per path and `changeNum` supersedes an
  in-flight scan, which is the same "don't accumulate duplicate refresh work"
  purpose `pendingRefresh` served — so the expectation is retirement (a `Tn`).
  The implementer confirms full subsumption against `claimRefreshPaths`/
  `releaseRefreshPath` while wiring the entry points; if any coalescing purpose
  remains uncovered, layer and document why instead.
- **`ErrAppendTailMoved` (microfts2 db.go:2266) — DECIDED: add the fire-alarm
  test.** "Never fires under the per-file actor" holds only if start-scan/retire
  are strictly serialized on the write actor; a fire alarm asserts the guard.
- **Store-failure / retire path — DECIDED (Bill, 2026-09-17): log + re-emit one
  change event (bounded), not an inline full reindex.** A failed store logs,
  retires the file actor, and re-emits a single change event for the path so the
  normal machinery re-scans it once; no inline `executeFullRefresh` fallback
  (indexer.go:848) under the actor.

- Does the microfts2 seam for Item 1 already partly exist (the indexed-chunk
  callback returns chunks; is there a write-free compute mode)? Verify before
  requesting.
- Item 3 vs Item 1 ordering: embedding may dominate the hold, so Item 3 alone
  might recover most of the latency at lower risk. Measure each hold separately.
- (Resolved) The redo-escape uses length+hash, not mtime — DECIDED (Bill,
  2026-09-17); see Item 2.
