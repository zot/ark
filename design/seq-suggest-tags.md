# Sequence: Suggest Tag Names (chunk → tag candidates)

**Requirements:** R2163-R2173

## Caller → Librarian → Store / FTS

```
Caller            Librarian               Store / FTS
  |                   |                          |
  |--SuggestTag------>|                          |
  |  Names(cid, k)    |                          |
  |                   |  if k <= 0 → (nil,nil)
  |                   |  if !EmbeddingAvailable → (nil,nil)
  |                   |                          |
  |                   |--ReadChunkEmbedding----->|
  |                   |  EC[chunkID]             |
  |                   |<--vec or nil-------------|
  |                   |  if vec == nil → (nil,nil)
  |                   |                          |
  |                   |--ScanTagDefEmbeddings--->|
  |                   |<--[]TagDefEmbedding------|
  |                   |  if empty → (nil,nil)
  |                   |                          |
  |                   |  for each (tag, fid, edVec):
  |                   |    skip if dim mismatch
  |                   |    score = cosine(vec, edVec)
  |                   |    perTag[tag] = max-agg + push fileScore
  |                   |                          |
  |                   |--FileIDPaths------------>| (fts)
  |                   |<--map[fid]string---------|
  |                   |                          |
  |                   |  build []TagSuggestion
  |                   |    sort each tag's MotivatingFiles desc
  |                   |    sort tags by aggregate score desc
  |                   |    truncate to k
  |                   |    fill MotivatingFiles[].Path
  |                   |                          |
  |<--results---------|                          |
```

HTTP-layer callers (UI handlers) reach the librarian via `srv.librarian`,
matching the existing pattern for `SearchChunks` and `EmbedSimilarTagValues`.

## Empty / Degenerate Paths

```
chunk has no EC record       → (nil, nil)        R2169
no [embedding] model         → (nil, nil)        R2170
no ED records yet            → (nil, nil)        R2171
k <= 0                       → (nil, nil)        R2168
ED dim != chunk EC dim       → skip that ED, continue  R2172
fileid has no path entry     → MotivatingFile.Path = ""  R2167
```

No path produces an error response. The caller's UI shows nothing
and falls back to manual tag entry.
