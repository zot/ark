# Librarian
**Requirements:** R1235, R1236, R1237, R1238, R1239, R1240, R1241, R1242, R1243, R1244, R1245, R1246, R1247, R1248, R1249, R1250, R1251, R1252, R1253, R1254, R1268, R1269, R1270, R1271, R1272, R1273, R1274, R1277, R1278, R1279, R1296, R1297, R1298, R1299, R1300, R1301, R1306, R1307, R1308, R1315, R1316, R1292, R1293, R1295, R1378, R1379, R1380, R1381, R1382, R1529, R1530, R1587, R1593, R1594, R1595, R1596, R1597, R1609, R1610, R1611, R1612, R1613, R1614, R1615, R1616, R1617, R1621, R1622, R1623, R1830, R1831, R1846, R1847, R1848, R1854, R1862, R1863, R1864, R1915, R1916, R1922, R1913, R1914, R1927, R1928, R1929, R1930, R1931, R2158, R2164, R2165, R2166, R2167, R2168, R2169, R2170, R2171, R2172, R2173, R2163, R2194, R2195, R2196, R2197, R2198, R2199, R2200, R2201, R2202, R2203, R2204, R2205, R2206, R2207, R2208, R2209, R2210, R2211, R2212, R2213, R2214, R2215, R2216, R2217, R2218, R2219, R2220, R2221, R2222, R2223, R2224, R2225, R2228, R2230, R2232, R2233, R2234, R2235, R2236, R2237, R2238, R2239, R2240, R2241, R2242, R2243, R2244, R2245, R2246, R2247, R2248, R2249, R2250, R2251, R2252, R2253, R2254, R2255, R2256, R2257

Manages spectral search: expansion request queue (lotto tube for
sidecar agent) and tag value embeddings (local nomic model). The
queue serializes access from concurrent HTTP handlers. The model
loads on first embedding query and stays warm until TTL expiry.

## Knows
- mu: sync.Mutex — protects all mutable state
- available: bool — whether `claude` was found on PATH at startup
- db: *DB — for V record queries, Store access, search
- pending: []ExpandRequest — lotto tube request queue
- waiters: []chan struct{} — signaled when a request is queued
- results: map[string]*ExpandResult — requestID → result
- model: *llama.Model — warm embedding model (nil when unloaded)
- modelCtx: *llama.Context — default embedding context (2048/8, for tags/queries)
- tierCtxs: []*llama.Context — per-tier contexts for chunk embedding (R1594)
- tiers: []EmbedTier — sorted by byte limit ascending (from Config)
- modelPath: string — path to GGUF file from config
- modelTimer: *time.Timer — TTL for model unloading
- (removed: lastEmbedded — superseded by chunkID-based dedup, R1847)

## Does
### Expansion Queue (Sidecar Pattern)
- QueueExpand(tag, value) string: add request, signal waiters, return ID
- DrainPending() []ExpandRequest: atomically drain the queue
- WaitForRequest(timeout) bool: block until requests available
- SetResult(id, results, error): store sidecar's result
- WaitForResult(id, timeout) *ExpandResult: block until result ready
- CleanResults(): cap result store size

### Fuzzy Matching
- FuzzyMatchTags(alternatives []TagAlt) []TagMatch: trigram/word
  fuzzy match against V records, resolve paths, filter by
  search_exclude. Returns (tag, value, count, score, paths).

### Embeddings
- EmbedQuery(text string) ([]float32, error): embed a query string
  using the warm model. Loads model on first call. Resets TTL.
- EmbedBatch(texts []string) ([][]float32, error): embed multiple texts
  in one GPU dispatch. Used by BatchEmbed for efficiency. (R1295)
- EmbedSimilarTagValues(query string, k int) ([]TagMatch, error):
  two-step narrowing — (1) cosine scan T record embeddings (~270) to
  find top-K tags, (2) cosine scan EV records only for those tags.
  Single query embedding for both steps (hybrid approach). Multiply
  tag × value scores. Returns top-K similar tag-value compounds.
  Same result shape as FuzzyMatchTags. (R1297, R1315, R1316)
- BatchEmbed(): scan MissingTagNameEmbeddings + MissingTagValueEmbeddings
  + MissingTagDefEmbeddings, resolve tvids via ScanVRecordTvids, embed
  in batches of 50 using EmbedBatch (GPU dispatch off actor), write T
  vectors, EV records, and ED records through DB actor. Hyphens→spaces
  for tag names; "tag: value" format for compounds; raw description
  text alone for tag defs (no tag name in the embedded text — see
  specs/tag-def-embeddings.md "What Gets Embedded"). Called
  post-reconcile from the write goroutine. (R1292, R1293, R1295, R2158)
- NewTokenizer() (*Tokenizer, error): load model, create minimal
  context (WithContext(64), no WithEmbeddings) for tokenization only.
  Returns a Tokenizer that wraps the context. Caller must Close().
  Uses modelPath from config. (R1529, R1530)
- BatchEmbedChunks(): scan files in priority order, read each file's
  F-record chunk list, check EC[chunkID] for each entry, queue missing
  chunkIDs for embedding via tier buckets. Cross-pass dedup: chunkID
  EC existence check (R1846, R1847). In-batch dedup: local seen set
  skips chunkIDs already queued from another file reference (R1862,
  R1863). After all tiers flush, recompute EF centroid per file from
  its chunk list's EC records (R1848). Logs embedded, skipped, and
  deduped counts (R1864). Priority sort, tier bucket dispatch, EC
  writes by chunkID, EF centroids written once after all tiers flush
  (R1830). Called post-reconcile after BatchEmbed. (R1609-R1617,
  R1846-R1848, R1862-R1864)
- SearchChunks(queryVec []float32, k int) ([]ChunkScore, error):
  thin wrapper over SearchChunksMulti with a single AboutTopK
  request. (R1915, R1922, R1931)
- SearchChunksMulti(reqs []AboutRequest) ([]AboutResult, error):
  one EC cursor walk, one txn. For each chunk, cosine vs every
  req.QueryVec; push onto that request's min-heap of size K. After
  the walk, every surviving chunk's FileID is resolved via
  fts.ReadCRecord inside one shared txn. Result slice is
  index-parallel to reqs. Skips EC records whose dimension does
  not match the query. (R1928, R1929, R1930)
- SuggestTagNames(chunkID, k) ([]TagSuggestion, error):
  one View txn. Read EC[chunkID]; if absent or model not
  available, return (nil, nil). Walk the ED prefix once;
  cosine-similarity each ED vector vs the chunk vector,
  skipping dimension mismatches. Aggregate per tag with **max**
  — the tag's score is the best score across its definition
  files; all contributing files retained as MotivatingFiles
  ranked descending. Resolve fileid → path once via
  fts.FileIDPaths(); a fileid with no path entry leaves Path
  empty. Sort tags by aggregate score, return top k. Read-only;
  no model invocation. (R2164-R2173)
- ChunksForTag(tag, k) ([]ChunkSuggestion, error):
  the dual of SuggestTagNames — tag → chunk candidates. Walk
  ED collecting every record whose tag matches; one EC walk;
  per chunk, max cosine across the tag's ED records; min-heap
  of size k tracks survivors with their per-def scores
  (memory O(k × |defs|)). Skip EC dim mismatches. After the
  walk, resolve each survivor's primary FileID via
  fts.ReadCRecord in one shared txn (drop chunks with no
  CRecord or empty FileIDs). Resolve all referenced fileids
  via one fts.FileIDPaths(). Sort MotivatingDefs per chunk
  desc, sort chunks by aggregate score desc. Read-only.
  (R2194, R2196-R2202, R2205-R2215)
- ChunksForTagDef(tag, fileid, k) ([]ChunkSuggestion, error):
  restricts scoring to the single ED[tag, fileid] record —
  useful when reconciling divergent definitions. ReadTagDef
  Embedding; missing → (nil, nil). Same EC walk and
  resolution as ChunksForTag, single query vector. Each
  result chunk's MotivatingDefs has length 1, the requested
  (fileid, path, score). Read-only. (R2195, R2198-R2208,
  R2210-R2215)
- SweepHotCorrelations() (*SweepResult, error): incremental
  corpus-wide sweep producing the HC top-K cache per tag.
  Reads I:hcsweep, walks SED and SEC for changed records,
  runs phase-3 tag rebuilds (full EC walk per affected tag,
  ReplaceHotCorrelations per tag) and phase-4 chunk-displace
  passes (per-tag delete+write for each new chunk that
  exceeds a tag's current min). Per-tag write transactions
  for crash safety. Bookmark advances only on full success.
  Surfaces progress through tmp://sweep/hot-correlations.md
  with 250 ms throttle; terminal transitions flush
  immediately. (R2216, R2217, R2230, R2232-R2244)
- TopKChunksForTag(tag, k) ([]ChunkSuggestion, error): read
  HC top-K from the cache. Performs alibi-stamp filtering
  per entry — drops if EC missing, EC current serial >
  HC stamp, or any tag-def's current serial > HC stamp.
  Result may be shorter than k. Returns (nil, nil) for
  empty HC, k <= 0, or embedding unavailable. (R2218,
  R2219, R2220, R2249-R2252)
- RelatedTags(tag, k) ([]TagSimilarity, error): live ED↔ED
  scan, max-pair aggregation per other tag. Returns top-k
  nearest tags with their best def-pair. (R2221, R2224)
- TagPairConflict(tagA, tagB) (TagSimilarity, error): live
  max-pair cosine across every (def_a, def_b). (R2222,
  R2224)
- TagDrift(tag) ([]DriftPair, error): live within-tag
  pairwise cosines, sorted descending. (R2223, R2225)
- SetCtxSize(n int): set embedding context window (bench only). (R1587)
- SetParallel(n int): set parallel sequences (bench only). (R1587)
- loadModel(): load GGUF model from modelPath, create default context
  and all tier contexts. Start TTL timer. (R1593, R1594, R1595)
- unloadModel(): close all tier contexts, default context, and model.
  Called on TTL expiry. (R1596)
- Available() bool: whether claude is on PATH (spectral search)
- EmbeddingAvailable() bool: whether tag_model is configured and
  the GGUF file exists

### HTTP Handlers
- HandleExpand: POST /search/expand — queue request, return ID
- HandleExpandWait: GET /search/expand/wait — lotto tube
- HandleExpandResult: POST /search/expand/result — receive result
- HandleExpandGet: GET /search/expand/result/{id} — retrieve result
- HandleFuzzyMatch: POST /search/expand/fuzzy — trigram fuzzy match
- HandleExpandSearch: POST /search/expand/search — search curated tags
- HandleEmbedMatch: POST /search/expand/embed — embedding similarity

## Collaborators
- Server: owns the Librarian, routes HTTP, status flags
- Store: V record queries, T/EV/EC/EF/HC record reads and writes,
  tag-value-id allocation, ScanVRecordTvids for reverse lookup,
  S-substrate stamping/freshness queries
- DB: AllChunks for chunk content retrieval, enqueueWrite for actor
  writes, AddTmpFile/UpdateTmpFile for the sweep progress doc
- Searcher: fetch grouped results for curated tags
- gollama: model loading, multi-context creation, embedding computation
- Config: tag_model path, embed_tiers, search_exclude patterns

## Sequences
- seq-spectral-expand.md
- seq-tag-embed.md
- seq-chunk-embed.md
- seq-tag-embed.md
