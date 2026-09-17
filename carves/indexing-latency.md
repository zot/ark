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

- [ ] **Item 1 — per-file actor: compute chunks/grams off-slot, store on-slot.** **OPEN (not queued.)** Needs a microfts2 compute/store seam (request drafted).
- [ ] **Item 2 — changeNum preemption + length+hash store-boundary check.** **OPEN (not queued.)** Folded into the Item 1 actor design. Appends exempt.
- [ ] **Item 3 — move embedding out of the write closure.** **OPEN (#129.)**
- **Item 4 — yield the slot between files in a batch reconcile.** **SUBSUMED by Items 1/2 (Bill, 2026-09-17).** No checkbox: per-file actors make yielding between files automatic — the write actor only ever holds a store.

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
  `microfts2-precomputed-chunks-request-20260917.md` (private working note; not
  sent — for the ark-messenger).
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

## Open questions

- Does the microfts2 seam for Item 1 already partly exist (the indexed-chunk
  callback returns chunks; is there a write-free compute mode)? Verify before
  requesting.
- Item 3 vs Item 1 ordering: embedding may dominate the hold, so Item 3 alone
  might recover most of the latency at lower risk. Measure each hold separately.
- (Resolved) The redo-escape uses length+hash, not mtime — DECIDED (Bill,
  2026-09-17); see Item 2.
