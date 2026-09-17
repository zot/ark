# Sequence: per-file scan actor (index compute off-slot, store on-slot)

Participants: EntryPoint (ScanAsync / RefreshAsync / IndexPathsAsync),
DBActor (db.svc), OffSlotGoroutine, WriteActor (write goroutine), Registry
(db.fileScans), microfts2.

Covers R3419-R3427. The numbered steps are the anchor targets.

```
1. change event → store/retire

1.1  EntryPoint (on DBActor): for each changed content file, call startFileScan(path)
1.2  DBActor: fs = lookupOrCreateFileScan(path); fs.changeNum = nextScanSeq()   [Registry write, actor-only]  (R3420, R3421)
1.3  DBActor: dispatch by (os.Stat, fts.CheckFile):
       1.3.1  gone + known   → enqueueWrite(RemoveFile) then retire            (remove path)
       1.3.2  present + known → op = reindex-or-append (decided in compute)
       1.3.3  present + new   → op = add
1.4  DBActor: my = fs.changeNum; go prepareScan(path, strategy, oldID, known, my)   (R3421)
1.5  OffSlotGoroutine (no write txn): read file → data, fullHash, fileSize
       1.5.1  detect append (DetectAppend on a copy) — grew + prefix hash matches
       1.5.2  append  → microfts2.PrepareAppend(path, lastLocator, newBytes, strategy, WithBaseLine)   (R3426)
       1.5.3  full    → microfts2.PrepareFile(path, strategy)                       (R3426)
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
```

Notes:
- The redo-escape (step seven) applies to full AND append; a superseded stale
  append is dropped there, which is why the append-tail-moved guard in the
  append store never fires in practice (R3427).
- The content-staleness escape (full-store sub-step) is skipped for appends — an
  append's new bytes are past the committed length by construction (R3423).
- Retire happens only if the changeNum still matches, so a new event arriving
  between the store and the retire keeps its entry (R3424).
