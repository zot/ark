# FileScanActor
**Requirements:** R3419, R3420, R3421, R3422, R3423, R3424, R3425, R3426, R3427, R3428, R3429, R3430, R3431, R3432

The per-file scan machinery that moves index compute (chunk + trigram + token)
off the write slot. A registry of in-flight scans lives on the DB actor; each
changed file's expensive compute runs in a plain off-slot goroutine, and only
the short store step holds the write slot. Realized across `db.go` (registry +
orchestration on the actor) and `indexer.go` (the prepare/store split).

## Knows
- fileScans: map[string]*fileScan — the registry, keyed by path. DB-actor state,
  no mutex. (R3420)
- scanSeq: uint64 — process-global monotonic changeNum counter. Never reset per
  entry, so a stale compute from a retired-and-recreated entry cannot collide
  with a fresh one. (R3421)
- fileScan.changeNum: the stamp for the path's currently-authoritative scan. (R3421)
- fileScan.reuse: map[[32]byte]*microfts2.PreparedChunk — the per-path chunk-hash
  → grams reuse map, keyed on microfts2's authoritative `SplitChunk.Hash`. Lives
  in the registry entry, survives across superseded scans of the path, DB-actor
  state (no mutex). Needs no runtime invalidation — the trigram config is fixed
  at microfts2 Create for the process lifetime. (R3429, R3432)
- scanPrep (on Indexer): the off-slot result — op kind (add/reindex/append/remove),
  the microfts2 `*PreparedFile` / `*PreparedAppend` handle, and ark's own
  content bytes + fullHash + fileSize for the length+hash escape. (R3426)

## Does
- startFileScan (on the DB actor): stamp the path's registry entry with the next
  scanSeq, dispatch by (disk-exists, index-known) into add/reindex/append/remove,
  and launch the off-slot compute tagged with the stamp. (R3421)
- prepareScan (off-slot goroutine, no write txn): read the file, compute
  fullHash/fileSize, detect append-vs-full. For a full add/reindex it runs the
  per-chunk compute — `SplitFile` once, then `buildEntries` (reuse map + chunk
  preemption), then `AssembleFile`; an append keeps the single `PrepareAppend`.
  Enqueues the store closure back through the write actor. (R3419, R3426, R3428)
- buildEntries (pure, off-slot): the per-chunk loop over split chunks. For each,
  consult the reuse gate (preempt on supersession, reuse grams on a hash hit),
  else `PrepareChunk` and cache. Returns ordered `[]PreparedEntry` for
  `AssembleFile`, or `errScanSuperseded` when a newer scan won. (R3428, R3429, R3430)
- reuseLookup / reuseKeep (on the DB actor): consult and update a path's reuse
  map. reuseLookup reports supersession (changeNum bumped past this stamp) and
  returns cached grams or nil; reuseKeep deposits freshly computed grams — even
  for a superseded scan, so the newer scan reuses them. (R3429, R3430, R3431)
- finishFileScan (on the DB actor): changeNum redo-escape — drop a superseded
  scan; else enqueueWrite the store closure. (R3422)
- storeScan (write goroutine): the length+hash content-staleness escape (full
  only; appends exempt), then `StorePrepared` / `ReindexPrepared` /
  `AppendPrepared` + all post-store bookkeeping; orphan cleanup via
  `WithRemovedChunkCallback`. Posts retire (and, on failure, one bounded
  re-emit) back to the actor. (R3423, R3425, R3426)
- retireFileScanIfCurrent (on the DB actor): remove the entry only if its
  changeNum still matches — closes the retire-vs-new-event race. (R3424)

## Collaborators
- Indexer: owns prepareScan/storeScan and the post-store bookkeeping.
- DB (write actor): owns the registry, enqueueWrite, and the monotonic counter.
- microfts2.DB: SplitFile + PrepareChunk + AssembleFile (per-chunk full compute),
  PrepareAppend (append compute), StorePrepared/ReindexPrepared/AppendPrepared
  (store), the ErrAppendTailMoved guard. (R3427, R3428)
- scanGate: the per-chunk reuse+preemption seam buildEntries calls; realized by a
  DB-actor-backed gate (reuseLookup/reuseKeep) in production, a fake in tests. (R3429, R3430)

## Sequences
- seq-file-scan-actor.md
