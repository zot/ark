# FileScanActor
**Requirements:** R3419, R3420, R3421, R3422, R3423, R3424, R3425, R3426, R3427

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
- scanPrep (on Indexer): the off-slot result — op kind (add/reindex/append/remove),
  the microfts2 `*PreparedFile` / `*PreparedAppend` handle, and ark's own
  content bytes + fullHash + fileSize for the length+hash escape. (R3426)

## Does
- startFileScan (on the DB actor): stamp the path's registry entry with the next
  scanSeq, dispatch by (disk-exists, index-known) into add/reindex/append/remove,
  and launch the off-slot compute tagged with the stamp. (R3421)
- prepareScan (off-slot goroutine, no write txn): read the file, compute
  fullHash/fileSize, detect append-vs-full, and run one microfts2 `Prepare*`
  call. Enqueues the store closure back through the write actor. (R3419, R3426)
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
- microfts2.DB: PrepareFile/PrepareAppend (compute), StorePrepared/
  ReindexPrepared/AppendPrepared (store), the ErrAppendTailMoved guard. (R3427)

## Sequences
- seq-file-scan-actor.md
