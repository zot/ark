# Searcher
**Requirements:** R46, R47, R48, R49, R50, R51, R52, R53, R54, R55, R56, R57, R58, R59, R60, R108, R109, R110, R111, R112, R113, R114, R115, R116, R183, R184, R185, R186, R188, R189, R190, R191, R192, R193, R215, R216, R217, R218, R219, R220, R221, R222, R223, R224, R225, R226, R227, R228, R372, R373, R374, R375, R403, R404, R405, R406, R407, R408, R409, R512, R513, R514, R515, R516, R572, R574, R575, R576, R577, R578, R585, R586, R587, R588, R589, R593, R594, R595, R596, R597, R598, R599, R600, R601, R602, R603, R604, R652, R653

Queries one or both engines and merges or intersects results.
Optionally retrieves chunk text or full file content.

## Knows
- fts: *microfts2.DB — trigram engine
- vec: *microvec.DB — vector engine
- store: *Store — LMDB store for tag index queries

## Does
- SearchCombined(query, opts): send same query to both engines,
  merge by (fileid, chunknum), combine scores, sort descending,
  apply -k limit. Falls back to FTS-only when no embedding command
  is configured (vec search unavailable). All FTS queries use
  dynamic trigram filtering (FilterByRatio 0.50) via defaultSearchOpts.
  Scoring strategy from opts.Score: "density" uses WithDensity(),
  "coverage" or default uses coverage. In auto mode (empty Score),
  if coverage returns 0 FTS results, retries with density scoring.
- SearchSplit(opts): dispatch --about to microvec, --contains to
  microfts2 Search, --regex to microfts2 SearchRegex. If single
  flag, return that engine's results. If both, intersect by
  (fileid, chunknum).
- ValidateSplitFlags(opts): error if --chunks and --files both set
  (--contains and --regex compose: contains drives FTS, regex filters)
- ResolveFilters(opts): build file ID set from all filters.
  Path filters first (--filter-files, --exclude-files): glob match
  against indexed file paths. Tag filters next (--filter-file-tags,
  --exclude-file-tags): query Store.TagFiles for file IDs containing
  specified tags. Content filters last (--filter, --except):
  run preliminary FTS searches, collect matching file IDs. Positives
  intersect, negatives subtract. Returns microfts2 WithOnly or
  WithExcept search option.
- Merge(ftsResults, vecResults): combine by (fileid, chunknum) key,
  sum or weighted-combine scores
- Intersect(ftsResults, vecResults): keep only (fileid, chunknum)
  present in both result sets
- FormatResults(results, opts): resolve fileids to paths and line
  ranges via microfts2.FileInfoByID, apply --scores and --after filters
- FillChunks(results, cache): for each result, retrieve chunk text via
  microfts2 ChunkCache. If cache is nil, creates a per-query cache
  (existing behavior). If non-nil, uses the provided cache (session
  path). Calls ChunkText per result. The chunker handles
  format-specific extraction (e.g. chat-jsonl text extraction).
- FillFiles(results): deduplicate results by fileid, read full file
  content for each unique file. Best chunk score becomes file score.
- SearchLikeFile(path, opts): read file content, use as FTS query
  with density scoring. Participates in split search (can combine
  with --about via intersect).
- ExtractTags(results): scan result chunks for @tag: patterns,
  return tag names with counts and best scores.
- CheckStale(results): check each result file for staleness via
  microfts2 CheckFile. Returns stale file paths.
- SearchWithConsistency(query, opts): search, check staleness,
  re-index stale files and re-search. Max 2 retries. After that,
  prune stale results and return what's valid.
- SearchGrouped(query, opts): run SearchWithConsistency, then group
  results by fileid. Returns tuple array: [[filepath, [chunk, ...]]]
  where files are sorted by best chunk score (descending) and chunks
  within each file are sorted by score (descending). Each chunk
  includes range, score, and preview (pre-rendered HTML). Highlight
  tokens derived from the effective query — falls back to
  opts.Contains, opts.About, or opts.Regex[0] when mode extraction
  clears the query string.
- RenderPreview(chunk, strategy, queryTokens): render chunk text as
  HTML for app display. Strategy determines renderer: goldmark for
  markdown, JSON pretty-print for JSON (under length threshold),
  plain text with HTML escaping otherwise. Query tokens highlighted
  with <mark> tags in all formats.
- SearchMulti(query, opts): run query through all four strategies
  (coverage, density, overlap, bm25) via a single microfts2
  SearchMulti call. Resolves filters, initializes BM25 from index
  counters, deduplicates results by (fileid, chunknum) keeping best
  score per chunk. Tracks which strategy produced each result.
  Passes WithProximityRerank to microfts2 if opts.Proximity is set
  (microfts2 handles reranking per-strategy inside SearchMulti).
  Runs the standard filterAndResolve pipeline.
- buildStrategies(query): build the map[string]ScoreFunc for
  SearchMulti. Coverage and density are static functions. Overlap is
  ScoreOverlap. BM25 uses db.BM25Func(queryTrigrams) which reads
  I record counters (totalTokens, totalChunks).

## Collaborators
- microfts2.DB: trigram search, file info resolution, ChunkCache for chunk text retrieval
- microvec.DB: vector search
- Store: LMDB tag index (TagFiles queries for tag-based filtering)
- Indexer: re-index stale files during consistent search
- goldmark: markdown → HTML rendering for previews

## Sequences
- seq-search.md
