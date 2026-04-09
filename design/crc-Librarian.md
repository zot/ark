# Librarian
**Requirements:** R1235, R1236, R1237, R1238, R1239, R1240, R1241, R1242, R1243, R1244, R1245, R1246, R1247, R1248, R1249, R1250, R1251, R1252, R1253, R1254, R1268, R1269, R1270, R1271, R1272, R1273, R1274, R1277, R1278, R1279, R1296, R1297, R1298, R1299, R1300, R1301, R1306, R1307, R1308, R1315, R1316

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
- modelCtx: *llama.Context — embedding context
- modelPath: string — path to GGUF file from config
- modelTimer: *time.Timer — TTL for model unloading

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
- EmbedSimilarTagValues(query string, k int) ([]TagMatch, error):
  two-step narrowing — cosine scan T record embeddings (~270) to
  find top-K tags, then cosine scan EV records only for those tags.
  Returns top-K similar tag-value compounds. Same result shape as
  FuzzyMatchTags. (R1297, R1315)
- loadModel(): load GGUF model from modelPath, create context
  with embeddings enabled. Start TTL timer.
- unloadModel(): close context and model, nil them. Called on
  TTL expiry.
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
- Store: V record queries, ET/EV record reads for cosine scan,
  tag-value-id allocation, ScanVRecordTvids for reverse lookup
- Searcher: fetch grouped results for curated tags
- gollama: model loading and embedding computation
- Config: tag_model path, search_exclude patterns

## Sequences
- seq-spectral-expand.md
- seq-tag-embed.md
