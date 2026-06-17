# Sequence: Hot Correlations Sweep + Read

**Requirements:** R2216-R2257

## SweepHotCorrelations — Caller → Librarian → Store / FTS / DB

```
Caller       Librarian                Store               FTS / DB
  |              |                      |                    |
  |--Sweep------>|                      |                    |
  |  Hot         | progress.start()     |                    |
  |  Correlations|--UpdateTmpFile------>|                    |  R2240, R2244
  |              |  status=running                            (closure-actor write)
  |              |                      |                    |
  |              |--iGetCounter(I:hcsweep)--->|             |  R2230, R2232
  |              |<--last_sweep_serial--------|              |
  |              |  fromScratch = (last_sweep_serial == 0)
  |              |                      |                    |
  |              |--WalkRecordsSinceSerial(prefixEmbedDef, last)--->|  R2233
  |              |<--changed_ed: []{tag, fileid}--------------------|
  |              |                      |                    |
  |              |--WalkRecordsSinceSerial(prefixEmbedChunk, last)-->|  R2233
  |              |<--changed_ec: []chunkid--------------------------|
  |              |                      |                    |
  |              |  affected_tags = unique(changed_ed[*].tag)
  |              |                      |                    |
  |              |  ── Phase 3: Tag Rebuild ──                |  R2234
  |              |  for each tag in affected_tags:           |
  |              |    progress.tick(phase=tag-rebuild)       |
  |              |    defs = ScanTagDefEmbeddings filtered by tag
  |              |    heap-of-K over EC walk:               |
  |              |      ViewChunkEmbeddings → cosine vs each def
  |              |      max-aggregate per chunk             |
  |              |    delete tag's existing HC entries      |  per-tag txn (R2238)
  |              |    write new top-K HC entries (stamped)  |  R2229
  |              |                      |                    |
  |              |  ── Phase 4: Chunk Displace ──             |  R2235
  |              |  for each chunkid in changed_ec, skip if affected_tags covers it:
  |              |    progress.tick(phase=chunk-displace)
  |              |    chunkVec = ReadChunkEmbedding(chunkid)
  |              |    for each tag in (all_tags \ affected_tags):
  |              |      score = max cosine vs tag's defs      |
  |              |      currentTopK = ReadHotCorrelations(tag)
  |              |      if score > min(currentTopK):
  |              |        delete displaced HC entry         |  per-tag txn (R2238)
  |              |        write new HC entry (stamped)
  |              |                      |                    |
  |              |  ── Phase 5: Bookmark ──                   |  R2236
  |              |  iSetCounter(I:hcsweep, max_seen_serial)
  |              |                      |                    |
  |              |  ── Phase 6: Surface Result ──             |  R2237
  |              |--UpdateTmpFile------>|                    |
  |              |  status=complete                           (immediate flush — R2243)
  |              |                      |                    |
  |<--SweepResult|                      |                    |
```

If a phase returns an error: bookmark stays unchanged (R2236),
progress doc flips to `@sweep-status: error` with `@sweep-error:`
(R2243), error returned to caller. Next invocation re-walks the
same work.

## Progress Throttle (in-memory)

```
sweep tick:
  current = now()
  accumulate counters in memory
  if current - last_flush >= 250 ms:                          R2242
    UpdateTmpFile with current counters
    last_flush = current

terminal transition (running→complete or running→error):
  UpdateTmpFile immediately, regardless of last_flush          R2243
```

The throttle counter and accumulators live as fields on the
sweep's local state struct — no shared mutable state.

## TopKChunksForTag — Caller → Librarian → Store / FTS

```
Caller       Librarian                  Store              FTS
  |             |                         |                  |
  |--TopK------>|                         |                  |
  |  ChunksFor  |  if k <= 0 || !EmbeddingAvailable → (nil,nil)
  |  Tag(tag,k) |                         |                  |
  |             |--ReadHotCorrelations--->|                  |
  |             |<--[]HotCorrelation------|                  |
  |             |  if empty → (nil,nil)                       R2220
  |             |                         |                  |
  |             |  ── Alibi-Stamp Filter ──                   R2219, R2249
  |             |--ScanTagDefEmbeddings (filter by tag)-->|   |
  |             |<--defs: []{fileid}----------------------|   |
  |             |--RecordSerial per def of tag-->         |   |
  |             |  ed_max = max of those serials             |
  |             |                         |                  |
  |             |  for each (chunkid, score) in HC:          |
  |             |    hc_serial  = RecordSerial(HC, key)      |
  |             |    ec_serial  = RecordSerial(EC, chunkid)
  |             |    if missing(EC, chunkid) → drop           R2249 (a)
  |             |    if hc_serial < ec_serial  → drop          R2249 (b)
  |             |    if hc_serial < ed_max     → drop          R2249 (c)
  |             |    survivor                                |
  |             |                         |                  |
  |             |--fts.Env().View--------------->|           |
  |             |    for each survivor:                      |
  |             |      ReadCRecord(chunkid)                  |
  |             |      drop if no FileIDs                    |
  |             |<--CRecords---------------------|           |
  |             |                         |                  |
  |             |--FileIDPaths------------------>|           |
  |             |<--map[fid]string---------------|           |
  |             |                         |                  |
  |             |  build []ChunkSuggestion                   |
  |             |    each MotivatingDefs is a single-entry
  |             |    placeholder — TopK doesn't preserve
  |             |    per-def winner; set to []DefMatch{}.
  |             |    (Caller wanting MotivatingDefs uses
  |             |     ChunksForTag for live computation.)
  |             |    sort by score desc, truncate to k
  |             |                         |                  |
  |<--results---|                         |                  |
```

## Tag → Tag Queries

```
RelatedTags(tag, k):                                          R2221
  defs_self = ScanTagDefEmbeddings filtered by tag
  for each other_tag in all tags except tag:
    defs_other = ScanTagDefEmbeddings filtered by other_tag
    score = max cosine(d_self, d_other) over all (d_self, d_other)
    track (other_tag, score, src_fileid, dst_fileid)
  sort desc by score, truncate to k
  resolve fileids → paths via FileIDPaths
  return []TagSimilarity

TagPairConflict(tagA, tagB):                                   R2222
  defs_a = ScanTagDefEmbeddings filtered by tagA
  defs_b = ScanTagDefEmbeddings filtered by tagB
  score = max cosine(da, db) over all pairs
  return TagSimilarity{Tag:"", Score, SrcFileID, DstFileID, paths}

TagDrift(tag):                                                 R2223
  defs = ScanTagDefEmbeddings filtered by tag
  for i, di in defs:
    for j, dj in defs[i+1:]:
      pairs = append(pairs, DriftPair{di.FileID, dj.FileID, cosine(di, dj)})
  resolve fileids → paths via FileIDPaths
  sort desc by score
  return pairs
```

All three tag-tag queries are live; they do not read or write the
HC cache. `ScanTagDefEmbeddings` is a single read txn shared
across the call.
