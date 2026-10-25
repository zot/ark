# Sequence: per-file scan actor (index compute off-slot, store on-slot)
**Requirements:** R3599, R3602, R3612

Participants: EntryPoint (ScanAsync / RefreshAsync / IndexPathsAsync),
DBActor (db.svc), OffSlotGoroutine, WriteActor (write goroutine), Registry
(db.fileScans), microfts2.

Covers R3419-R3427, R3433, and R3438 (the enumeration + per-file classification moving off the actor, and the NumCPU dispatch bound). The numbered steps are the anchor targets.

```
1. change event → store/retire

1.1  EntryPoint (on DBActor): for each changed file, call startFileScan(path)
1.2  DBActor: fs = lookupOrCreateFileScan(path); fs.changeNum = nextScanSeq()   [Registry write, actor-only]  (R3420, R3421)
       1.2.1  ark.toml → scanConfigOnActor: os.Stat + CheckFile + refresh/add/remove synchronously on the actor, then retire   (R1052, R3433)
       1.2.2  content file → capture cfg snapshot; go runScanBounded(prepareFileScan(copyIdx, cfg, path, my))   [spawn never blocks the actor; scanSem bounds to <= NumCPU scans, each holding its slot until its store finishes]  (R3433, R3438, R3612)
1.3  OffSlotGoroutine: classify off the actor — os.Stat + fts.CheckFile on the private copy   (R3433)
       1.3.1  gone + known   → SyncVoid(enqueueWrite(RemoveFile) then retire)   (remove path)
       1.3.2  gone+unknown | isDir | outside-source → SyncVoid(retire)
       1.3.3  else → strategy = cfg.StrategyForFile(rel); op decided in compute (reindex/append/add)
1.4  OffSlotGoroutine: my is the stamp startFileScan drew; proceed to the compute   (R3421)
1.5  OffSlotGoroutine (no write txn): read file → data, fullHash, fileSize
       1.5.1  detect append (DetectAppend on a copy) — grew + prefix hash matches
       1.5.2  append  → microfts2.PrepareAppend(path, lastLocator, newBytes, strategy, WithBaseLine)   (R3426)
       1.5.3  full    → per-chunk compute: SplitFile → buildEntries → AssembleFile   (R3428)
       1.5.3.1  microfts2.SplitFile(path, strategy) → []SplitChunk (hashed, no grams) + FileMeta   (R3428)
       1.5.3.2  buildEntries: for each split chunk, gate.reuse(hash) on the DBActor   (R3429, R3430)
       1.5.3.3  if superseded (changeNum bumped past my) → return errScanSuperseded (abandon between chunks)   (R3430)
       1.5.3.4  else reuse cached grams on a hit; on a miss PrepareChunk(content) then gate.keep(hash, grams)   (R3429, R3431)
       1.5.3.5  microfts2.AssembleFile(path, strategy, entries, FileMeta) → *PreparedFile   (R3428)
1.6  OffSlotGoroutine: SyncVoid(db, enqueueWrite→) finishFileScan(prep, my)          (R3419)
1.7  DBActor: if fileScans[path] == nil || fileScans[path].changeNum != my → drop (superseded)   (R3422 redo-escape)
1.8  DBActor: else enqueueWrite(storeScan(prep, my))
1.9  WriteActor (holds the slot): storeScan
       1.9.1  full only: committed (FileLength,ContentHash) == (fileSize,fullHash) → skip store (content-staleness escape)   (R3423)
       1.9.2  add    → StorePrepared(handle, WithIndexedChunkCallback)               (R3426)
       1.9.3  reindex→ ReindexPrepared(handle, WithIndexedChunkCallback, WithRemovedChunkCallback)  (R3426)
       1.9.4  append → AppendPrepared(oldID, handle, WithContentHash/ModTime/FileLength, WithIndexedChunkCallback, WithRemovedChunkCallback)   (R3426, R3427)
       1.9.5  post-store bookkeeping: UpdateTagValues/AppendTagValues, UpdateTagDefs, runExtRouting, pubsub, writeDateIndex, pdf/bible flush, recallWatcher.OnAppend   (R3426)
1.10 WriteActor → DBActor (fire-and-forget svc): retireFileScanIfCurrent(path, my)   (R3424)
       1.10.1  on store error: log + retireFileScanIfCurrent + reEmitChange(path) once (bounded)   (R3425)
       1.10.2  on ErrChunkerVersionStale: the re-emit is a forced scan (never appends)   (R3599)
       1.10.3  on success: scanBytesSinceRelease += chunked bytes; registry empty and ≥ threshold → go releaseMemory   (R3602)
```

Notes:
- The per-chunk compute (1.5.3.x) applies to the full path only; appends keep the
  single whole-append PrepareAppend (1.5.2). gate.reuse/gate.keep run on the
  DBActor, where the path's reuse map lives (no mutex); those actor hops are also
  the changeNum preemption checkpoints, so preemption is strictly between chunks.
  A superseded scan deposits its finished grams before abandoning, so the newer
  scan reuses them and a rapidly-edited file converges (R3428-R3431).
- The redo-escape (step seven) applies to full AND append; a superseded stale
  append is dropped there, which is why the append-tail-moved guard in the
  append store never fires in practice (R3427).
- The content-staleness escape (full-store sub-step) is skipped for appends — an
  append's new bytes are past the committed length by construction (R3423).
- Retire happens only if the changeNum still matches, so a new event arriving
  between the store and the retire keeps its entry (R3424).
