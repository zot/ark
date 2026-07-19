# Test Design: Recall
**Source:** crc-Librarian.md, crc-CLI.md, crc-Server.md

## Test: Recall merged scoring and ranking
**Purpose:** Validate that Vector-EC and Trigram-EC passes run, scores are normalized (vector via `(cos+1)/2`, trigram via Jaccard), merged using max across inputs/substrates, and sorted descending.
**Input:** Corpus with 3 chunks. Input queries that trigger both Vector-EC (with mock embeddings) and Trigram-EC matches.
**Expected:** The final score is the max of the normalized vector score and the Jaccard trigram score. Results are sorted descending. ED-side substrates are not run.
**Refs:** crc-Librarian.md, seq-recall.md#1.5, seq-recall.md#1.6, R2617, R2620, R2622, R2626, R2586, R2643

## Test: Self-chunk exclusion
**Purpose:** Verify that when the input is a chunkID or resolves to a chunkID, that chunk is excluded from its own recall results.
**Input:** Input ConnectionsInput list contains ChunkID A.
**Expected:** The output Chunks slice does not contain ChunkID A, but does return other relevant chunks.
**Refs:** crc-Librarian.md, seq-recall.md#1.6, R2623

## Test: Metadata and tag resolution
**Purpose:** Verify that each recalled chunk contains resolved path, range, and tags (using AllTagsForChunk V-records).
**Input:** Recall query matching chunks with configured tags.
**Expected:** Each returned chunk has populated Path, Range, and Tags.
**Refs:** crc-Librarian.md, seq-recall.md#1.7, R2624

## Test: IncludeContent option
**Purpose:** Verify that IncludeContent option controls whether the full content of the chunk is read from the chunk cache.
**Input:** Recall opts with IncludeContent=true and IncludeContent=false.
**Expected:** If true, Content is populated with chunk text. If false, Content is empty.
**Refs:** crc-Librarian.md, seq-recall.md#1.7, R2625

## Test: Trigram-only fallback when embedding is unavailable
**Purpose:** Verify that if the embedding model is unavailable, the Vector-EC pass is skipped, Trigram-EC runs, and a warning is returned.
**Input:** Recall run with no warm model and EmbeddingAvailable() returning false.
**Expected:** Results returned via Trigram-EC matches only; Warning field set to "embedding unavailable".
**Refs:** crc-Librarian.md, seq-recall.md#1.15, R2634

## Test: Input validation rejects empty and unknown inputs
**Purpose:** Validate that empty input list is rejected and unknown chunkIDs return errors.
**Input:** Case A: Empty inputs after normalization. Case B: ConnectionsInput with unknown ChunkID.
**Expected:** Case A returns error `chunkIDs/text/range empty`. Case B returns error `unknown chunk <id>`.
**Refs:** crc-Librarian.md, R2639, R2640

## Test: Option clamping
**Purpose:** Verify that K option is clamped to [1, 200].
**Input:** RecallOpts with K=0, K=250.
**Expected:** K=0 clamps to 1 (or default 20/clamped to 1-200), K=250 clamps to 200.
**Refs:** crc-Librarian.md, R2641

## Test: CLI Routing logic
**Purpose:** Verify CLI routes correctly based on server running state and model configuration.
**Input:**
- Case A: Server running -> proxy to HTTP.
- Case B: Server down, model configured AND file exists -> exit non-zero with server-not-running error.
- Case C: Server down, model configured BUT file missing -> exit non-zero with embedding-model-not-found error.
- Case D: Server down, no model configured -> run in-process Trigram-only.
**Expected:**
- Case A executes proxy request to POST /recall.
- Case B prints `error: server not running; model configured. Please start the server with: ark serve` and exits non-zero.
- Case C prints `error: configured embedding model not found at <PATH>` and exits non-zero.
- Case D opens local DB read-only via withDB and returns Trigram-only results.
**Refs:** crc-CLI.md, seq-recall.md#1.2, seq-recall.md#1.12, seq-recall.md#1.13, R2630, R2631, R2632, R2633, R2646

## Test: CLI output formatting
**Purpose:** Verify CLI prints baby-food markdown stencil by default, warning headers, no-results text, JSON, and per-value sub-list items.
**Input:**
- Case A: Default stdout with matches.
- Case B: Default stdout with warning.
- Case C: Default stdout with no matches.
- Case D: --json flag enabled.
- Case E: Chunk with tag values (mix of value-bearing and value-less tags).
**Expected:**
- Case A matches the @chunk-id markdown stencil.
- Case B prepends `@recall-warning: <msg>`.
- Case C prints `## Chunks\n\n_no results_`.
- Case D prints raw JSON of RecallResult.
- Case E `@chunk-tags` lists only names (comma-separated, no values); each non-empty value emits a `- @chunk-tag-value: <name>: <value>` sub-list item under the chunk, in the same order names appear.
**Refs:** crc-CLI.md, seq-recall.md#1.10, seq-recall.md#1.20, R2627, R2635, R2636, R2637, R2638, R2645

## Test: Trigram-EC coverage floor short-circuits
**Purpose:** Verify the Jaccard pass skips chunks whose query-coverage is below the floor without paying for the union computation.
**Input:** Query text with trigrams that overlap a "near" chunk above the floor and a "far" chunk that shares only one or two stray trigrams (coverage < 0.1).
**Expected:** The "near" chunk receives a non-zero Jaccard score; the "far" chunk's trigram-EC score is exactly 0. The shared intersection used for the coverage check matches the one used in the Jaccard numerator (single computation).
**Refs:** crc-Librarian.md, R2644

## Test: NewLibrarian succeeds without claude on PATH
**Purpose:** Verify Librarian construction is independent of claude availability; recall and substrate operations function without it.
**Input:** Test environment with no `claude` binary on PATH. Construct a Librarian and call Recall against an indexed corpus.
**Expected:** NewLibrarian returns a non-nil Librarian whose `Available()` reports false. Recall returns results (warning may be set if no embedding model is configured). No exec.LookPath-related error is raised.
**Refs:** crc-Librarian.md, R2642

## Test: Self-chunk exclusion covers path:range inputs
**Purpose:** Verify a path:range input is excluded from its own recall results after normalization, not just direct chunkID inputs.
**Input:** Corpus with chunk c1 at `path/foo.md:3-5`. Input `{Path: "path/foo.md", Range: "3-5"}`.
**Expected:** The output Chunks slice does not contain c1.
**Refs:** crc-Librarian.md, seq-recall.md#1.6, R2623

## Test: HTTP Server /recall handler and Lua sys.recall bridge
**Purpose:** Verify HTTP POST /recall endpoint and Lua sys.recall function route request to Librarian.Recall.
**Input:** 
- Case A: HTTP POST /recall with inputs and opts.
- Case B: Lua sys.recall call.
**Expected:** Both invoke Librarian.Recall and return JSON/Lua table results.
**Refs:** crc-Server.md, seq-recall.md#1.3, seq-recall.md#2.1, R2628, R2629

## Test: recallOp reads through a private fts.Copy()
**Purpose:** Verify newRecallOp binds op.db to a private copy (not the shared original) so Recall's fts-cache reads can't race the write actor's InvalidateCaches, keeps op.l on the live Librarian for embedding/store, rebinds the Searcher to the copy, and the copy still resolves reads — a guard so a future refactor aliasing op.db to l.db (silently restoring the O154 race) fails loudly.
**Input:** setupRecall + one indexed chunk. Build op := l.newRecallOp().
**Expected:** op.db.fts != l.db.fts; op.l == l; op.db.search bound to op.db.fts; op.db.ChunkInfo resolves the indexed chunk's path.
**Refs:** crc-Librarian.md, R995, R3163
