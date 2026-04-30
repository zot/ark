# ExtMap
**Requirements:** R1992, R1993, R1994, R1995, R1996, R1997, R1998, R1999, R2000, R2001, R2002, R2003, R2004, R2005, R2006, R2007, R2008, R2009, R2010, R2011

Owns the in-memory state and orchestration for `@ext` routing.
Six maps maintained alongside DB X-record writes; canonical
re-resolution flow runs from the indexer's reindex callback;
source-side cleanup runs from the orphan callback. Rebuilt at
startup by scanning X records.

## Knows
- targetToChunk: map[uint64][]uint64 — tvid_ext → chunkids; collated
  X record contents per tvid_ext (R1992)
- chunkToTargets: map[uint64][]uint64 — chunkid → tvid_exts; inverse
  used by the orphan callback (R1992)
- fileidToTvids: map[uint64][]uint64 — fileid → tvid_exts; file-level
  reindex trigger (R1992)
- extByAnchor: map[string][]uint64 — anchor spec text (UUID or path)
  → tvid_exts; same map covers both forms because UUIDs and paths
  don't collide (R1992, R1995)
- unresolvedTargets: map[uint64]bool — tvid_exts whose target spec
  currently resolves to nothing (R1992, R1997)
- virtualTagCount: map[string]int — per-tag count of ext-routed
  contributions; sums into T-totals at query time (R1992, R2010)

## Does
- Rebuild(store *Store): startup scan of X records to repopulate all
  six maps. Reads `targetToChunk` and `virtualTagCount` directly
  from X record contents; derives `chunkToTargets`, `fileidToTvids`,
  and `extByAnchor` (the latter via TvidMap.Resolve on each
  tvid_ext to recover the spec text). (R1993)
- IndexExt(tvid_ext, value, sourceFileid, txn, tt): orchestrate one
  @ext routing. Steps: (1) ParseExtTarget(value); skip if !ok.
  (2) DB.ResolveExtTarget(target_spec). Empty → mark
  `unresolvedTargets[tvid_ext]` and add `extByAnchor[target_spec]`.
  (3) Self-reference check — if any resolved fileid equals
  sourceFileid, log error and skip routing. (4) For each accepted
  target chunkid: allocate routed-tag tvids via
  `allocIDInTxn(IFieldNextTvid)` through the txn's TvidTxn; call
  Store.WriteExtRecord(txn, tvid_ext, target_chunkid,
  routed_tvids); call Store.AppendChunkIDToVRecord (multi-set, no
  dedup) for each routed tag's V record; update in-memory maps;
  bump `virtualTagCount[routed_tag]` once per added entry.
  (R1996, R1997, R1998, R1999)
- ReresolveOnReindex(fileid, addedChunkIDs, orphanedChunkIDs, txn,
  tt): canonical re-resolution flow. Step 1: collect candidate
  tvid_exts from `fileidToTvids[fileid]`, `extByAnchor[F.path]`,
  and `extByAnchor[UUID]` for each `@id: UUID` value added or
  removed in F's chunks. Step 2: for each candidate, recover spec
  via TvidMap.Resolve → ParseExtTarget → DB.ResolveExtTarget →
  new chunkid set. Step 3: diff old (`targetToChunk[tvid_ext]`,
  scoped to F) vs new → Adds, Removes, Updates. Step 4 (Adds):
  Store.WriteExtRecord, multi-set append to V records, bump
  virtualTagCount. Step 5 (Updates): rewrite V record blobs.
  Step 6 (Removes): Store.RemoveChunkIDFromVRecord (one occurrence,
  multi-set), decrement virtualTagCount, Store.DeleteExtRecord; if
  V empties, delete it and adjust T as needed. Step 7 (Empty new
  set): drop all X records for tvid_ext, mark unresolvedTargets,
  update extByAnchor. (R2000, R2001, R2002, R2003, R2004, R2005,
  R2006)
- CleanupSource(tvid_ext, txn, tt): source-side cleanup. Prefix-scan
  X[tvid_ext] via Store.ScanExtRecords → enumerate (target_chunkid,
  routed_tvids). For each: strike target_chunkid from each routed
  tag's V record (one occurrence), decrement virtualTagCount, drop
  the X record. Drop tvid_ext from all six maps. **MUST run before
  `tt.Commit`** — TvidMap.Resolve is needed for the spec recovery
  (when called from re-resolution paths) and the V record empties
  trigger tt.Remove(tvid_ext); reversing the order loses the spec.
  (R2008, R2009)
- VirtualTagCount(tag) int: read-only accessor for T-total queries.
  Returns 0 if tag absent. (R2010)
- AppendIsDegenerate: append-only file changes use ReresolveOnReindex
  unchanged — the diff is empty for unchanged chunks; Adds fire only
  when newly-resolvable anchors land in the appended content; Removes
  fire only when the chunker drops and replaces the previous last
  chunk. No "is this an append?" branch. (R2007)

## Collaborators
- DB: ResolveExtTarget for spec → chunkid resolution; ParseExtTarget
  via the ext.go helper
- Store: X record CRUD (WriteExtRecord, ScanExtRecords,
  DeleteExtRecord); V record multi-set append/remove
  (AppendChunkIDToVRecord, RemoveChunkIDFromVRecord); T-total
  augmentation queries
- TvidMap: spec recovery via Resolve(tvid_ext); routed-tag tvid
  allocation via allocIDInTxn(IFieldNextTvid) through the
  caller-supplied TvidTxn (R1999, R2011)
- Indexer: invokes IndexExt during chunk callback, ReresolveOnReindex
  from the indexed-chunk callback, CleanupSource from the orphan
  callback

## Sequences
- seq-ext-routing.md
