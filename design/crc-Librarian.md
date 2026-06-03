# Librarian
**Requirements:** R1235, R1236, R1237, R1238, R1239, R1240, R1241, R1242, R1243, R1244, R1245, R1246, R1247, R1248, R1249, R1250, R1251, R1252, R1253, R1254, R1268, R1269, R1270, R1271, R1272, R1273, R1274, R1277, R1278, R1279, R1296, R1297, R1298, R1299, R1300, R1301, R1306, R1307, R1308, R1315, R1316, R1292, R1293, R1295, R1378, R1379, R1380, R1381, R1382, R1529, R1530, R1587, R1593, R1594, R1595, R1596, R1597, R1609, R1610, R1611, R1612, R1613, R1614, R1615, R1616, R1617, R1621, R1622, R1623, R1830, R1831, R1846, R1847, R1848, R1854, R1862, R1863, R1864, R1915, R1916, R1922, R1913, R1914, R1927, R1928, R1929, R1930, R1931, R2158, R2164, R2165, R2166, R2167, R2168, R2169, R2170, R2171, R2172, R2173, R2163, R2194, R2195, R2196, R2197, R2198, R2199, R2200, R2201, R2202, R2203, R2204, R2205, R2206, R2207, R2208, R2209, R2210, R2211, R2212, R2213, R2214, R2215, R2216, R2217, R2218, R2219, R2220, R2221, R2222, R2223, R2224, R2225, R2228, R2230, R2232, R2233, R2234, R2235, R2236, R2237, R2238, R2239, R2240, R2241, R2242, R2243, R2244, R2245, R2246, R2247, R2248, R2249, R2250, R2251, R2252, R2253, R2254, R2255, R2256, R2257, R2313, R2314, R2315, R2316, R2317, R2318, R2319, R2320, R2321, R2324, R2326, R2327, R2328, R2329, R2330, R2331, R2332, R2333, R2334, R2335, R2336, R2337, R2338, R2339, R2340, R2341, R2342, R2343, R2409, R2567, R2569, R2570, R2571, R2572, R2573, R2574, R2575, R2576, R2577, R2578, R2579, R2580, R2581, R2582, R2583, R2584, R2585, R2586, R2587, R2588, R2589, R2590, R2591, R2592, R2593, R2594, R2595, R2596, R2597, R2598, R2599, R2603, R2609, R2568, R2617, R2618, R2620, R2621, R2622, R2623, R2624, R2625, R2626, R2629, R2634, R2639, R2640, R2641, R1604, R1608, R2642, R2643, R2644, R2655, R2656, R2657, R2658, R2659, R2660, R2662, R2647, R2667, R2668, R2669, R2670, R2671, R2672, R2673, R2674, R2675, R2676, R2677, R2681, R2684, R2685, R2686, R2742, R2743, R2905, R2906, R2907, R2908, R2909, R2910, R2911, R2912, R2913

Manages spectral search: expansion request queue (lotto tube for
sidecar agent) and tag value embeddings (local nomic model). The
queue serializes access from concurrent HTTP handlers. The model
loads on first embedding query and stays warm until TTL expiry.

## Knows
- mu: sync.Mutex — protects all mutable state
- available: bool — whether `claude` was found on PATH at startup
- db: *DB — for V record queries, Store access, search
- pending: []ExpandRequest — lotto tube request queue (spectral)
- waiters: []chan struct{} — signaled when a request is queued
- results: map[string]*ExpandResult — requestID → result (spectral)
- model: *llama.Model — warm embedding model (nil when unloaded)
- modelCtx: *llama.Context — default embedding context (2048/8, for tags/queries)
- tierCtxs: []*llama.Context — per-tier contexts for chunk embedding (R1594)
- tiers: []EmbedTier — sorted by byte limit ascending (from Config)
- modelPath: string — path to GGUF file from config
- modelTimer: *time.Timer — TTL for model unloading
- (removed: lastEmbedded — superseded by chunkID-based dedup, R1847)
- pendingConnections: []ConnectionsRequest — lotto tube queue (find-connections, R2321)
- connectionsWaiters: []chan struct{} — signaled when a connections request is queued (R2321)
- connectionsResults: map[string]*ConnectionsRecord — requestID → in-flight record (chunkIDs, deadline, status, timer) (R2319, R2331)
- connectionsLastWait: time.Time — last observed `ark connections --wait` consumer (R2320)
- connectionsAvailWindow: time.Duration — availability window (config or constant; mirrors spectral) (R2320)
- (substrate) `Librarian.modelCtx` is reused to embed bare-text inputs ad-hoc (R2572); no separate context.

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
  (R1830). Strips ark tags (`stripArkTags`) from each chunk's content
  before embedding so the EC (meaning axis) is tag-free; an all-`@tag:`
  chunk strips to empty and is skipped — the trigram index and retrieval
  keep the original content (R2913). Strips every text strategy including
  pdf (a pdf chunk's content is extracted text). Called post-reconcile
  after BatchEmbed. (R1609-R1617, R1846-R1848, R1862-R1864)
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
- SweepHotCorrelationsAsync(): fire-and-forget variant of
  `SweepHotCorrelations`. Enqueues the same closure through
  `DB.enqueueWrite` and returns immediately; the result reaches
  callers through the `tmp://sweep/hot-correlations.md`
  progress doc. The inner `*HCSweepResult` from the run is
  logged on error and otherwise discarded. Used by the
  curation workshop's sweep-button retrofit (`mcp.sweepHot
  CorrelationsAsync`). Multiple invocations queue serially
  through the write actor. (R2409)
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

### Find Connections (Sidecar Pattern, 1G)
- FindConnections(chunkIDs []uint64, opts FindConnectionsOpts) (string, error):
  orchestrator entry point. Allocates a request ID, creates the
  tmp:// doc through the write actor with pending header tags,
  enqueues the request, schedules a deadline timer, returns the
  request ID immediately. Non-blocking on the sidecar. (R2319,
  R2326, R2327, R2331)
- ConnectionsAvailable() bool: reports true when `ark connections
  --wait` has been observed within `connectionsAvailWindow`.
  Mirrors `Available()` for spectral. (R2320)
- QueueConnectionsRequest(req ConnectionsRequest): append to
  pendingConnections, signal connectionsWaiters. (R2321)
- DrainPendingConnections() []ConnectionsRequest: atomically drain
  the queue. Updates `connectionsLastWait` as a side effect (the
  drain itself is evidence of an active `--wait` consumer). (R2321)
- WaitForConnectionsRequest(timeout time.Duration) bool: block
  until requests are available; updates connectionsLastWait on
  entry. (R2321)
- SetConnectionsResult(id string, result *ConnectionsResult) error:
  validate non-empty Evidence on every theme and shared-tag entry;
  on protocol violation route to SetConnectionsError. On success,
  render body and call UpdateTmpFile through the write actor with
  body + @connections-status: completed +
  @connections-completed:<RFC3339> + @connections-progress: done.
  Cancel the deadline timer. Late call after terminal state: log
  and return nil. (R2317, R2329, R2330, R2333)
- SetConnectionsError(id string, message string): UpdateTmpFile
  through the write actor with @connections-status: errored +
  @connections-error:<message> + @connections-completed + done.
  Cancel deadline timer. Idempotent on terminal state. (R2318,
  R2329, R2333)
- TickConnectionsElapsed(id string): the orchestrator's elapsed-
  tick goroutine. Every ~5 s, UpdateTmpFile with new
  @connections-elapsed value (and @connections-progress when
  caller advances it). Throttle policy: 5 s minimum between
  non-terminal updates; centralized publish dedups unchanged
  tag values. (R2328)
- BuildFetchPayload(id string) ([]ChunkFetchEntry, error): for a
  pending/working request, walk chunkIDs, resolve each chunk's
  primary FileID + path via fts.ReadCRecord, read the chunk
  content (DB.AllChunks or chunk-store accessor), return
  []{chunkID, fileID, path, content}. Reports unknown chunk IDs
  in the error chain; the caller (CLI `--fetch`) surfaces them
  as exit non-zero. (R2316, R2324)
- CleanConnectionsResults(): cap the connectionsResults map by
  age; long-completed entries fall off. Mirrors CleanResults.
  (R2321)

### Find Connections — Substrate (2A, no agent)
- FindConnections(inputs []ConnectionsInput, opts FindConnectionsOpts) (string, error):
  unified entry for both normal and turbo modes (R2567). Normalizes
  inputs (chunkID, path:range, text), validates at enqueue
  (R2569–R2573), allocates a request ID, writes the pending
  tmp:// doc with `@purpose: curate` and `@connections-mode:
  normal|turbo` headers (R2590, R2591), then dispatches: turbo
  → queue for sidecar (existing 1G path); normal → launch
  substrate goroutine. Returns request ID immediately.
- normalizeInputs(rawInputs []ConnectionsInput) ([]ConnectionsInput, error):
  expand each entry to its canonical form. ChunkID entries pass
  through after `ReadCRecord` (R2569). PathRange entries resolve
  the path, intersect chunks with the line range, expand into
  one ConnectionsInput per overlapping chunk (R2570, R2571).
  Text entries pass through (R2572). Empty input list →
  `chunkIDs/text/range empty` (R2573).
- runSubstrate(rec *ConnectionsRecord): in-process worker for
  normal mode. Opens one LMDB View txn (R2579), runs per-input
  passes, merges across substrates (R2580) then across inputs
  (R2581), sorts top-K (R2585), renders body, calls
  SetConnectionsResult to flip to completed.
- substrateForInput(input ConnectionsInput, txn *lmdb.Txn) PerInputResult:
  runs the four substrate passes for one normalized input
  (R2575–R2578). Returns per-tag scores keyed by substrate,
  with supporting-chunk and motivating-file evidence.
- mergePerInput(per []PerInputResult, k int) []TagNameCandidate:
  aggregate across inputs (R2581), preserve per-substrate
  per-input detail (R2582), cap supporting chunks at 10 per
  tag (R2583), sort by aggregate score desc, return top k
  (R2585).
- ListConnections() []*ConnectionsRecord: snapshot copy of
  in-flight records (those still in `connectionsResults`),
  for `ark connections list` (R2609).
- (existing) `SetConnectionsResult` is reused by the substrate
  worker. The turbo-only `validateEvidence` check is bypassed
  when the result was built in-process (substrate guarantees
  non-empty supporting chunks by construction).
- (existing) ConnectionsAvailable() — used by `FindConnections`
  only when `opts.Mode == "turbo"` (R2603); normal mode does
  not check.

### HTTP Handlers
- HandleExpand: POST /search/expand — queue request, return ID
- HandleExpandWait: GET /search/expand/wait — lotto tube
- HandleExpandResult: POST /search/expand/result — receive result
- HandleExpandGet: GET /search/expand/result/{id} — retrieve result
- HandleFuzzyMatch: POST /search/expand/fuzzy — trigram fuzzy match
- HandleExpandSearch: POST /search/expand/search — search curated tags
- HandleEmbedMatch: POST /search/expand/embed — embedding similarity
- HandleConnectionsWait: GET /connections/wait — lotto tube for find-connections sidecar (R2315, R2321)
- HandleConnectionsFetch: GET /connections/fetch?id=... — returns BuildFetchPayload JSON (R2316)
- HandleConnectionsResult: POST /connections/result — SetConnectionsResult with stdin JSON body (R2317)
- HandleConnectionsError: POST /connections/error — SetConnectionsError (R2318)
- HandleConnectionsFind: POST /connections/find — accepts {inputs, opts}, invokes Librarian.FindConnections, returns {requestID, path} (R2567, R2604)
- HandleConnectionsList: GET /connections/list — JSON array of in-flight records, used by `ark connections list` (R2609)
- HandleRecall: POST /recall — parses {inputs, opts}, invokes Librarian.Recall, returns RecallResult JSON (R2629)

### Recall (Phase 2B)
- Recall(inputs []ConnectionsInput, opts RecallOpts) (*RecallResult, error): retrieves top-K chunks ranked by similarity (R2617). Normalizes inputs (R2618), runs Vector-EC and Trigram-EC (R2620) — Vector-EC via `(cos+1)/2` (R2586), Trigram-EC via Jaccard over trigram sets with a query-coverage floor (R2643, R2644) — merges via max across substrates and inputs (R2622), excludes self-chunks for any input that normalizes to a chunkID (R2623), resolves metadata and tags (R2624), reads content from cache if configured (R2625), sorts descending and returns top-K (R2626). Handles missing model gracefully by skipping Vector-EC and setting a warning (R2634). Rejects empty inputs (R2639) and unknown chunks (R2640). Clamps K option (R2641). Constructor `NewLibrarian` succeeds whether or not `claude` is on PATH; `Available()` reports spectral-expansion capability (R2642).
- Recall with `opts.Propose=true` runs the derivation pass on the substrate's full scored chunk set — internally retaining tagless chunks for derivation (forcing `KeepTagless=true` for the derivation chunk set) while still applying the caller's effective `KeepTagless` to the surfaced result. The pass produces RC records as a side effect (R2667, R2668). The caller's surfacing is unaffected — `--propose` does not change which chunks appear in the result stencil.
- derivationPass(txn *lmdb.Txn, scored []ChunkScore) error: internal step. Reads `maxED = Store.MaxEDSerial()` once for the batch (R2669). For each scored chunk: read RF[chunkid]; if `RF >= maxED`, skip derivation and proceed to stencil-time similarity-only read (R2669). Otherwise, cosine-compare chunk's EC vector against all ED records, take top-N by similarity (`derivationK` default 10) (R2670), filter against already-attached tags (F-record probe per candidate) (R2671), filter against ext-routed tagnames on the chunk (bare-name rule via ExtMap) (R2672), filter against existing RJ records via `Store.HasDerivedRejection` (R2673), then call `Store.WriteDerivedProposal` for each survivor (tally increment-or-create) and `Store.WriteDerivedFreshness(chunkid, maxED)` to stamp the chunk (R2674, R2675). All writes in one batched txn (R2675).
- enrichProposedTags(result *RecallResult, chunkSimilarities map[uint64][]proposalSim) error: for each surfaced chunk with at least one RC record, populate `RecalledChunk.ProposedTags` with the accumulated RC tagnames ordered by similarity descending (R2684, R2685, R2686). Similarity sources: for chunks the pass derived this call, scores are passed through `chunkSimilarities`; for fresh-skip chunks, compute on demand by cosine-comparing the chunk's EC vector against the ED records of each RC tagname (max across the tag's def files) (R2685). The stencil renderer omits the line when `ProposedTags` is empty (R2684).
- Admission-time tag filter: the substrate's scoring map only
  admits candidate chunks that carry V records, unless
  `opts.KeepTagless` is true. `-all` is the CLI surface; the
  Lua bridge is `keepTagless`. The filter runs during admission
  so the substrate's top-K contract honors only tagged
  candidates by default. The tag lookup performed for the filter
  is cached on the per-chunk accumulator and reused during the
  result-enrichment phase. (R2647)
- Recall consults `opts.Discussed` (carries the exclusion-set
  union from `--session SID` and `--discussed @t...`) before the
  requireTags / KeepTagless decision (R2658). For each candidate
  chunk, strip any `(tag, value)` pair that matches the exclusion
  set, then drop the chunk only if its tag list becomes empty —
  permissive filter (R2656). Membership uses the matching rule:
  bare-name entries match any value, exact-pair entries match
  only the exact pair (R2657). When the caller passed `--session
  SID`, the substrate loads the session's unexpired RD records
  via `Store.ListDiscussed` before scoring and unions them into
  the exclusion set; explicit `--discussed` entries contribute
  alongside (R2655, R2659). `RecallOpts.Discussed
  []Discussed{Tag, Value}` is the wire shape; empty slice
  disables the filter (R2660). The substrate itself never writes
  RD records — the recall agent's `ark discussed add` is the
  only writer (R2662).

### Recall — substrate v3 (tag axis, 2×2 allocation, chat funnel)
The substrate gains a tag axis and a 2×2 allocation; it scores over
**tag-free** EC (stripped at embed) and **full-text** trigram (tags kept,
R2913) and ranks within a guaranteed budget per axis rather than one
global top-K.
- **Tag axis (retrieval, value→chunk, R2905):** score each tag-value
  against the input — vector cosine over EV records (the same EV scan
  `EmbedSimilarTagValues` walks) plus trigram-Jaccard computed on the fly
  from the short value string — take the top values and gather the chunks
  carrying them via V records (`ScanVRecordTvids` / the tvid→chunk
  edges), each chunk carrying the value's score as its tag-axis
  component. A chunk surfaces because its *tag* matches the input even
  when its prose does not. ~1162 short values → brute-force scan; no
  stored TV record.
- **Four-component score (R2906):** each candidate carries
  `<text-trigram, text-vector, tag-trigram, tag-vector>` — the text pair
  from the content substrates, the tag pair from the tag axis.
- **2×2 allocation (R2907):** allocate results across (main-corpus,
  conversation) × (meaning, tags), N per cell (default 3, `[recall]`
  config R2912). Within a cell: rank by that axis's score (`meaning` =
  max(text-tri, text-vec); `tags` = max(tag-tri, tag-vec)), cap ≤2 chunks
  per file, sort `<final score, size>` with the size tiebreak **larger
  when the winning substrate was vector, smaller when trigram** (vector
  size-robust; Jaccard size-sensitive). Per-axis budget means content and
  tags never compete in one score — the cross-axis merge dissolves.
- **Dedup + backfill (R2908):** a chunk matching in both a meaning and a
  tags cell keeps its higher-scoring cell; the other backfills. An
  underfilling cell (the sparse conversation×tags) redistributes across
  the four cells to the per-call target.
- **chat funnel (R2910):** the conversation pool re-chunks matched JSONL
  turns with the markdown chunker and ranks **sub-chunks** —
  trigram-filter → sub-chunk → trigram-sort → embed only survivors →
  vector-check against the input → sort `<final score, size>`. Surfaces
  the sub-chunk's `path:range`, not the whole turn. The pre-embed
  survivor count is a `[recall]` knob (R2912), logged.
- **Per-cell logging (R2909):** the recall monitor record carries each
  surfaced result's cell + per-component scores, for data-driven tuning.
- **Propose EV leg (R2911):** `derivationPass` scores chunk-EC against
  tag-**EV** (existing values) in addition to tag-**ED** (definitions) —
  a chunk earns a tag for resembling an existing *value*, not only the
  *definition*. Same `min_propose_similarity` floor; extends the O(N·M)
  ED scan (O115) by the value set.

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
- seq-find-connections.md
- seq-find-connections-substrate.md
- seq-discussed.md
- seq-derived-tags.md
