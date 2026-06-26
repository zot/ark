# Searcher
**Requirements:** R46, R47, R48, R49, R50, R51, R52, R53, R54, R55, R56, R57, R58, R59, R60, R108, R109, R110, R111, R112, R113, R114, R115, R116, R183, R184, R185, R186, R188, R189, R190, R191, R192, R193, R215, R216, R217, R218, R219, R220, R221, R222, R223, R224, R225, R226, R227, R228, R372, R373, R374, R375, R403, R404, R405, R406, R407, R408, R409, R512, R513, R514, R515, R516, R572, R574, R575, R576, R577, R578, R585, R586, R587, R588, R589, R593, R594, R595, R596, R597, R598, R599, R600, R601, R602, R603, R604, R652, R653, R672, R673, R683, R684, R697, R698, R699, R700, R738, R744, R745, R746, R747, R750, R939, R940, R1094, R1095, R1096, R1097, R1139, R1140, R1141, R1230, R1395, R1396, R1397, R1398, R1399, R1400, R1401, R1470, R1471, R1703, R1704, R1705, R1706, R1707, R1708, R1783, R1784, R1785, R1787, R1867, R1868, R1869, R1870, R1871, R1872, R1915, R1916, R1917, R1918, R1921, R1922, R1932, R1933, R1934, R1935, R1939, R2128, R2433, R2434, R2453, R2454, R2455, R2456, R950, R2951, R2959

Queries one or both engines and merges or intersects results.
Optionally retrieves chunk text or full file content.

## Knows
- fts: *microfts2.DB — trigram engine
- librarian: *Librarian — embeds query text and ranks chunks via EC records (R1915, R1916)
- store: *Store — store for tag index queries and centroid (EF) reads

## Does
- SearchCombined(query, opts): send same query to both engines,
  merge by (FileID, ChunkID), combine scores, sort descending,
  apply -k limit. Falls back to FTS-only when the embedding pipeline
  is unavailable (Librarian.EmbeddingAvailable() reports false).
  All FTS queries use (R1916)
  dynamic trigram filtering (FilterByRatio 0.50) via defaultSearchOpts.
  When opts.Cache is non-nil, defaultSearchOpts also appends
  WithChunkCache so microfts2's internal post-filters (verify, regex)
  share the session cache instead of re-reading files.
  Scoring strategy from opts.Score: "density" uses WithDensity(),
  "coverage" or default uses coverage. In auto mode (empty Score),
  if coverage returns 0 FTS results, retries with density scoring.
- SearchSplit(opts): dispatch --about to Librarian.EmbedQuery +
  Librarian.SearchChunks, --contains to microfts2 Search, --regex
  to microfts2 SearchRegex. If single flag, return that engine's
  results. If both, intersect by (fileid, chunkid). (R1916, R1918)
- ValidateSplitFlags(opts): error if --chunks and --files both set
  (--contains and --regex compose: contains drives FTS, regex filters)
- ResolveFilters(opts): build file ID set from all filters.
  R939, R940: if no explicit --filter-files or --exclude-files,
  inject config.SearchExclude as default ExcludeFiles.
  Path filters first (--filter-files, --exclude-files): glob match
  against indexed file paths. Tag filters next (--filter-file-tags,
  --exclude-file-tags): query Store.TagFiles for file IDs containing
  specified tags. Content filters last (--filter, --except):
  run preliminary FTS searches, collect matching file IDs. Positives
  intersect, negatives subtract. Returns a microfts2 WithOnly
  search option (negatives subtracted from the ID set).
- Merge(ftsResults []microfts2.SearchResult, vecResults []ChunkScore):
  combine by (FileID, ChunkID) key, sum or weighted-combine scores (R1918)
- Intersect(ftsResults []microfts2.SearchResult, vecResults []ChunkScore):
  keep only (FileID, ChunkID) tuples present in both result sets (R1918)
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
  includes range, score, content (raw text), contentType
  (strategy-derived: "markdown"|"text"|"json"|"code"), and preview
  (pre-rendered HTML). ContentType mapping: "markdown" strategy →
  "markdown", "chat-jsonl" → "json", "bracket"/"indent" → "code",
  everything else → "text". Highlight tokens derived from the
  effective query — falls back to opts.Contains, opts.About, or
  opts.Regex[0] when mode extraction clears the query string.
- RenderPreview(chunk, strategy, queryTokens): render chunk text as
  HTML for app display. Strategy determines renderer: goldmark for
  markdown, JSON pretty-print for JSON (under length threshold),
  plain text with HTML escaping otherwise. Query tokens highlighted
  with <mark> tags in all formats.
  The `pdf` strategy emits a `<pdf-chunk>` element wrapping one
  `<ark-tag rect="…"><name>…</name> <value>…</value></ark-tag>`
  child per entry in the chunk's `tag_rects` attribute. `src` is
  constructed as `/raw/PATH` (URL-encoded file path). A PDF chunk
  with no `tag_rects` yields a childless `<pdf-chunk>`. A `pdf`
  chunk that lacks a `rect` attribute (salvage chunk) falls through
  to the plain-text preview path with `wrapTagElements` applied —
  no `<pdf-chunk>` wrapper. (R1703, R1704, R1706, R1707, R1708)
- SearchResultEntry and GroupedChunk carry a chunk attributes field
  populated from microfts2.CRecord.Attrs so the pdf case of
  RenderPreview can read `page`, `rect`, and `tag_rects` without
  an extra index read. FillChunks propagates the attrs alongside
  chunk text. (R1705)
- SearchMulti(query, opts): run query through all four strategies
  (coverage, density, overlap, bm25) via a single microfts2
  SearchMulti call. Resolves filters, initializes BM25 from index
  counters, deduplicates results by (fileid, chunknum) keeping best
  score per chunk. Tracks which strategy produced each result.
  Passes WithProximityRerank to microfts2 if opts.Proximity is set
  (microfts2 handles reranking per-strategy inside SearchMulti).
  Runs the standard filterAndResolve pipeline.
- buildStrategies(query): build the map[string]ScoreFunc for
  SearchMulti. Coverage, density, and overlap are static ScoreFuncs.
  BM25 uses db.BM25Func(queryTrigrams). Returns four strategies.
  (Bigram strategy removed — typo tolerance now via SearchFuzzy.)
- SearchFuzzy(query, opts): typo-tolerant search via
  microfts2.SearchFuzzy(query, k, ...searchOpts). Uses OR-union of
  trigrams with posting-list tally, then C-record re-scoring. Resolves
  filters, applies proximity reranking if requested, runs the standard
  filterAndResolve pipeline. Sets Strategy to "fuzzy" on all results.
- TagChunkFilter(predicate MatchPredicate, store): resolve chunkIDs
  from T/V records at construction time (F-record ChunkID for
  name-only and exact-value modes; V-record chunkIDs via
  MatchTagValues for name+value contains/regex modes), return a
  ChunkFilter that checks chunkID set membership. Chunk-precise —
  no chunk text reads. Predicate carries the parsed sigil match
  syntax (R1399, R2442, R2451).
- FileTagChunkFilter(predicate MatchPredicate, store, fileIDPaths):
  resolve fileIDs whose per-file tag aggregate matches the
  predicate (consults `Store.FileTagValues` or equivalent — never
  reads chunk text), then return a `ChunkFilter` whose closure
  accepts any chunk whose `CRecord.FileIDs` includes a member of
  that fileID set. The fileID approval set is computed once at
  filter construction and cached for the duration of one search
  (R2453, R2454, R2455, R2456).
- chunkIDChunkFilter(set): membership predicate over crec.ChunkID,
  used by chunk-precise tag filters. (R1399)
- ChunksByID(chunkIDs): resolve chunkIDs to SearchResultEntry list
  by reading C records (FileID), looking up paths via FileIDPaths,
  and recovering Range from the F record's chunk list. Stale chunkIDs
  (deleted/replaced chunks) are silently skipped. No FTS pass. (R2442)
- SearchTagChunks(chunkIDs, opts): flat SearchResultEntry list from a
  tag-derived chunkID set. R2951: the post-filter funnel for index-lookup
  primaries — injects the default search_exclude scope (effectiveExcludeFiles),
  runs the chunkIDs through postFilterChunkIDs (the user filter stack), calls
  ChunksByID, then filterByPathGlobs (file scope + default exclude), then -k.
  Shared by the server short-circuit and the CLI-direct fallback. (R2442, R2951)
- GroupTagChunks(chunkIDs, opts): build GroupedResult straight from
  a tag-derived chunkID set — calls SearchTagChunks (so it inherits the
  R2951 funnel), fills chunk text via cache, groups via groupResults.
  Used when a sigil-form primary tag query has no other text primary. (R2442, R2951)
- postFilterChunkIDs(chunkIDs, opts, cache): the post-hoc half of the
  post-filter funnel. Builds per-row predicates for opts.ChunkFilters
  (excluding about-mode rows) via rowChunkFilter, reads each candidate's
  CRecord once, and returns the chunkIDs passing every row predicate.
  Lets an index-lookup primary (tag/file-tag, and the about vec-only path)
  apply the same filter stack the FTS scan applies inline. (R2951)
- rowChunkFilter(row, cache, paths, store): shared per-row predicate
  constructor for contains/fuzzy/regex/tag/file-tag/files, polarity applied.
  Single source of truth for filter semantics — both BuildChunkFilters
  (wraps each as a microfts2 option) and postFilterChunkIDs (applies them
  post-hoc) build from it, so the funnel cannot drift from the FTS path. (R2951)
- effectiveExcludeFiles(opts): the R939/R940 default-scope rule extracted
  for reuse — returns config.SearchExclude when no explicit/positive file
  filter is present, else nil. Used by resolveFilters and the index-lookup
  funnel. (R939, R940, R2951)
- filterByPathGlobs(results, opts): apply FilterFiles/ExcludeFiles (incl.
  the injected default scope) to a resolved candidate set via the shared
  Matcher. Extracted from SearchTagChunks so the about vec-only path reuses
  it. (R2951)
- groupResults(results, tokenPatterns): factored grouping/preview
  pipeline shared by SearchGrouped and GroupTagChunks. Sorts files
  and chunks by score (descending). (R2442)
- AboutChunkFilter(query, librarian, threshold): chunk-level
  similarity filter. Embeds the query via Librarian, runs a single
  AboutSet request through Librarian.SearchChunksMulti, returns a
  closure that checks crec.ChunkID against the resulting chunkID
  set. Polarity negation handled by BuildChunkFilters. (R1787,
  R1916, R1932, R1934)
- BuildChunkFilters(rows, cache, paths, store, librarian, cfg):
  convert filter rows into microfts2 search options. Each row's
  `Mode` selects the constructor: `tag` → TagChunkFilter,
  `file-tag` → FileTagChunkFilter (R2453), `contains`/`fuzzy`/
  `regex` → existing chunk-text filters, `about` → AboutChunkFilter
  with threshold from cfg.AboutCentroidThreshold. Tag and
  file-tag rows arrive carrying a pre-parsed `MatchPredicate`
  (R2442) so the parser is shared with the CLI and subscribe
  paths. Polarity from each row maps to `WithChunkFilter` (with)
  or `WithChunkExclude` (without). (R1471, R1787, R1932, R1933,
  R2454)
- Multi-query about coordination: SearchSplit and SearchCombined
  collect the primary --about (AboutTopK) plus every Mode == "about"
  filter row (AboutSet), submit one Librarian.SearchChunksMulti
  call, route TopK to merge/intersect/vecOnly, and convert each
  AboutSet ChunkIDs map into a microfts2.WithChunkFilter closure
  delivered through extraOpts. (R1935)

## Collaborators
- microfts2.DB: trigram search, file info resolution, ChunkCache for chunk text retrieval
- Librarian: embeds queries (EmbedQuery) and ranks chunks via EC records (SearchChunks) (R1915, R1916)
- Store: the tag index (TagFiles queries for tag-based filtering, MatchTagNames/MatchTagValues for contains-tokens), file centroid (EF) reads (R1921)
- Indexer: re-index stale files during consistent search
- goldmark: markdown → HTML rendering for previews

## Sequences
- seq-search.md
