# Sequence: Chunks For Tag (tag → chunk candidates)

**Requirements:** R2194-R2215

## ChunksForTag — Caller → Librarian → Store / FTS

```
Caller            Librarian               Store / FTS
  |                   |                          |
  |--ChunksForTag---->|                          |
  |  (tag, k)         |                          |
  |                   |  if k <= 0 → (nil,nil)
  |                   |  if !EmbeddingAvailable → (nil,nil)
  |                   |                          |
  |                   |--ScanTagDefEmbeddings--->|
  |                   |<--[]TagDefEmbedding------|
  |                   |  filter to records where Tag == tag
  |                   |  if filtered empty → (nil,nil)
  |                   |                          |
  |                   |--ViewChunkEmbeddings---->|  one EC walk
  |                   |  for each (chunkID, vec):
  |                   |    skip if dim mismatch
  |                   |    score chunk against every filtered ED vec
  |                   |    aggregate = max(scores)
  |                   |    push (chunkID, aggregate, perDef[]) to heap-of-k
  |                   |    on eviction, drop perDef payload
  |                   |<--walk done--------------|
  |                   |                          |
  |                   |  if heap empty → (nil,nil)
  |                   |                          |
  |                   |--fts.Env().View--------->|
  |                   |    for each survivor:
  |                   |      ReadCRecord(chunkID)
  |                   |      drop if no FileIDs
  |                   |      attach FileIDs[0] as primary FileID
  |                   |<--CRecords---------------|
  |                   |                          |
  |                   |--FileIDPaths------------>|
  |                   |<--map[fid]string---------|
  |                   |                          |
  |                   |  build []ChunkSuggestion
  |                   |    sort each MotivatingDefs desc
  |                   |    sort chunks by aggregate score desc
  |                   |    fill chunk Path and DefMatch.Path
  |                   |                          |
  |<--results---------|                          |
```

## ChunksForTagDef — Caller → Librarian → Store / FTS

```
Caller            Librarian               Store / FTS
  |                   |                          |
  |--ChunksForTagDef->|                          |
  |  (tag, fid, k)    |                          |
  |                   |  if k <= 0 → (nil,nil)
  |                   |  if !EmbeddingAvailable → (nil,nil)
  |                   |                          |
  |                   |--ReadTagDefEmbedding---->|
  |                   |<--ed-vec or nil----------|
  |                   |  if ed-vec == nil → (nil,nil)
  |                   |                          |
  |                   |--ViewChunkEmbeddings---->|  one EC walk, single query
  |                   |  for each (chunkID, vec):
  |                   |    skip if dim mismatch
  |                   |    score = cosine(vec, ed-vec)
  |                   |    push to heap-of-k (no aggregate; single def)
  |                   |<--walk done--------------|
  |                   |                          |
  |                   |  resolve survivors as in ChunksForTag
  |                   |    each ChunkSuggestion has
  |                   |    MotivatingDefs = [{fid, path, score}]
  |<--results---------|                          |
```

HTTP-layer callers (UI handlers) reach the librarian via `srv.librarian`,
matching the existing pattern for `SuggestTagNames` and `SearchChunks`.

## Empty / Degenerate Paths

```
k <= 0                                 → (nil, nil)         R2207
no tag_model configured                → (nil, nil)         R2208
ChunksForTag — tag has no ED records   → (nil, nil)         R2209
ChunksForTagDef — ED[tag,fid] absent   → (nil, nil)         R2210
EC prefix empty                        → (nil, nil)         R2211
EC dim != ED dim (per-record)          → skip that EC       R2198
chunk has no CRecord / empty FileIDs   → drop from results  R2200
fileid has no path entry               → Path = ""          R2201
```

No path produces an error response. The caller's UI shows nothing
and the curation surface stays empty for that tag.
