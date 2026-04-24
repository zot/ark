# Librarian
**Requirements:** R1235, R1236, R1237, R1238, R1239, R1240, R1241, R1242, R1243, R1244, R1245, R1246, R1247, R1248, R1249, R1250, R1251, R1252, R1253, R1254, R1268, R1269, R1270, R1271, R1272, R1273, R1274, R1277, R1278, R1279, R1296, R1297, R1298, R1299, R1300, R1301, R1306, R1307, R1308, R1315, R1316, R1292, R1293, R1295, R1378, R1379, R1380, R1381, R1382, R1529, R1530, R1587, R1593, R1594, R1595, R1596, R1597, R1609, R1610, R1611, R1612, R1613, R1614, R1615, R1616, R1617, R1621, R1622, R1623, R1830, R1831, R1846, R1847, R1848, R1854, R1862, R1863, R1864

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
- BatchEmbed(): scan MissingTagNameEmbeddings + MissingTagValueEmbeddings,
  resolve tvids via ScanVRecordTvids, embed in batches of 50 using
  EmbedBatch (GPU dispatch off actor), write T vectors and EV records
  through DB actor. Hyphens→spaces for tag names, "tag: value" format
  for compounds. Called post-reconcile from the write goroutine. (R1292,
  R1293, R1295)
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
- Store: V record queries, T/EV/EC/EF record reads and writes,
  tag-value-id allocation, ScanVRecordTvids for reverse lookup
- DB: AllChunks for chunk content retrieval, enqueueWrite for actor writes
- Searcher: fetch grouped results for curated tags
- gollama: model loading, multi-context creation, embedding computation
- Config: tag_model path, embed_tiers, search_exclude patterns

## Sequences
- seq-spectral-expand.md
- seq-tag-embed.md
- seq-chunk-embed.md
- seq-tag-embed.md
