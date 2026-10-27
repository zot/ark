# Searcher
**Requirements:** R46-60, R108-116, R183-186, R188-190, R192, R193, R215-228, R372-375, R403-409, R512-516, R572, R574-578, R585-589, R593-604, R652, R653, R672, R673, R683, R684, R697-700, R738, R744-747, R750, R939, R940, R950, R1094-1097, R1139-1141, R1230, R1395-1401, R1470, R1471, R1703-1708, R1783-1785, R1787, R1867-1872, R1915-1918, R1921, R1922, R1932-1935, R1939, R2128, R2433, R2434, R2453-2456, R2951, R2959, R3163, R3192, R3193, R3195, R3197, R3359, R3362, R3377, R3400-3402, R3571, R3573, R3584, R3587, R3590, R3620, R3641

Queries one or both engines and merges or intersects results.
Optionally retrieves chunk text or full file content.

## Knows
- fts: *microfts2.DB — trigram engine
- librarian: *Librarian — embeds query text and ranks chunks via EC records (R1915, R1916)
- store: *Store — store for tag index queries and centroid (EF) reads

## Does
- withFTS(fts): return a copy of the Searcher bound to an alternate fts
  (typically fts.Copy()), so an off-actor read operation (substrateOp)
  searches through a private cache — SearchFuzzy resolves result paths
  via fts.FileIDPaths, whose cache the write actor nils. Shares store,
  config, and the librarian back-reference. (R3163)
- SearchCombined(query, opts): send same query to both engines,
  merge by (FileID, ChunkID), combine scores, sort descending,
  apply -k limit. Falls back to FTS-only when the embedding pipeline
  is unavailable (Librarian.EmbeddingAvailable() reports false).
  All FTS queries use (R1916)
  dynamic trigram filtering via defaultSearchOpts, using
  microfts2.FilterByRatio(0.50, 1000) directly — skipping a trigram only when
  it is both non-discriminating and expensive (R3402), and abstaining below two
  chunks so a one-chunk corpus stays searchable (R3401).
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
- ResolveFilters(opts): build file ID set from all filters. Named by
  `SearchOpts` field, not by CLI flag — the flags that once carried these
  were retired into the filter stack, and every surface (CLI rows, Lua opts,
  bloodhound scopes) populates the same fields.
  R939, R940: if neither `FilterFiles` nor `ExcludeFiles` is set,
  inject config.SearchExclude as the default `ExcludeFiles`.
  Path filters first (`FilterFiles`, `ExcludeFiles`): glob match
  against indexed file paths, via Matcher (R3195). Tag filters next
  (`FilterFileTags`, `ExcludeFileTags`): query Store.TagFiles for file IDs
  containing specified tags. Content filters last (`Filter`, `Except`):
  run preliminary FTS searches, collect matching file IDs. Positives
  intersect, negatives subtract. Returns a microfts2 WithOnly
  search option (negatives subtracted from the ID set).
- AnchorGlobToDir(glob, dir) string (R3192, R3193, R3197): the shared
  anchoring rule for the **CLI context**, lifted out of the search CLI so
  every caller applies one rule. A glob beginning `/`, `~`, or `tmp://` is
  already absolute and passes through; anything else joins to `dir` (a
  trailing `/` is preserved). `cmd/ark`'s `anchorFilterToCwd` becomes a thin
  wrapper passing the client's cwd, and the bloodhound's two surfaces pass
  the session's cwd and the client's cwd respectively — so a hunt's globs
  and the secretary's own `-files` searches resolve identically instead of
  against two different directories. This is *anchoring*, the half of
  `-files` semantics that is per-surface; *matching*, the half that is
  universal, is crc-Matcher.md. Anchoring runs once, at the surface that
  knows the directory, so what travels onward is always absolute.
- **Path-glob matching delegates to Matcher (R3195).** `matchFilesGlob`
  keeps its job — expand `~` (R950), then test each candidate path — but
  the test itself is `Matcher.Match(glob, path, "", false)` rather than the
  local `pathMatchesGlob`, which retires. The retiring test was
  basename-first (`filepath.Match` on the base name, then full-path
  doublestar): a hand-rolled `**/X` that agreed with `Matcher`'s bare form
  for a no-slash pattern but matched *nothing* for a slash-bearing one. So
  a rootless `search_exclude = ["specs/**"]` silently excluded nothing —
  O160's failure in a second place. CLI-supplied globs arrive absolute
  (anchored above) and take Matcher's `/X` form; config-supplied ones
  arrive bare and take the rootless `**/X` form (R3199). One call site,
  both contexts, because the *form* carries the context.
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
  A result whose ChunkText is unavailable is dropped, not kept with empty
  text (R3573); the PDF attrs fill skips a missing chunk (R3571).
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
  markdown, ChatVoice's voice segments (goldmark per segment) for
  chat-jsonl (R3620), JSON pretty-print for JSON (under length threshold),
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
- SearchTagChunks(chunkIDs, routed, opts): flat SearchResultEntry list from a
  tag-derived chunkID set (routed: see the R3587 bullet below). R2951: the post-filter funnel for index-lookup
  primaries — injects the default scope (defaultExcludes: fence + noise),
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
- defaultExcludes(opts) (fence, noise []string): the default scope, computed
  once from the caller's opts before any are written. fence is
  `config.SystemExcludes()` unless `opts.System` (R3584, R3590); noise is
  `config.SearchExcludes()` under the R939/R940 rule (nil when an explicit or
  positive file filter is present). Callers **append** both to
  `opts.ExcludeFiles`, never replace it, so the fence holds alongside an
  explicit filter. Replaces effectiveExcludeFiles, whose replace-on-assign was
  safe only while the default was nil whenever ExcludeFiles was set.
  Used by resolveFilters, the about vec-only path and SearchTagChunks.
  (R939, R940, R2951, R3584, R3590)
- SearchTagChunks(chunkIDs, routed, opts): the tag-primary funnel. fence and
  the user's filters apply to every candidate; noise applies to every candidate
  **except** the chunk IDs in `routed`, those a matching tag value reached
  through an `@ext` routing (R3587). GroupTagChunks passes `routed` through.
- routedPrimaryChunks(p, store, candidates) map[uint64]bool: the chunk IDs
  among a tag primary's candidates that carry a **routed** tag value matching
  the predicate (`ExtMap.RoutedTagsForChunk`, name among `matchingTagNames`,
  value passing `p.MatchValue`). Beside resolvePredicateLocations so the
  server's tag primary and the CLI's cold path compute the same set (R3587).
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
- BuildChunkFilters(rows, links, cache, paths, store):
  convert filter rows into microfts2 search options. Each row's
  `Mode` selects the constructor: `tag` → TagChunkFilter,
  `file-tag` → FileTagChunkFilter (R2453), `contains`/`fuzzy`/
  `regex` → existing chunk-text filters, `about` → AboutChunkFilter
  with threshold from the config the store carries. Tag and
  file-tag rows arrive carrying a pre-parsed `MatchPredicate`
  (R2442) so the parser is shared with the CLI and subscribe
  paths. Polarity from each row maps to `WithChunkFilter` (with)
  or `WithChunkExclude` (without). (R1471, R1787, R1932, R1933,
  R2454)
- BuildChunkFilters resolves every `files` row against `links` before
  building its closure, and takes the table as a **required** parameter
  (R3400). This is the point where a path stops being data and becomes a
  decision, and three of its four callers — the CLI and both server sites —
  are outside any Searcher, so they never reach `normalizeOpts` beforehand.
  Resolving only at the Searcher's entry points left an alias-spelled
  `-files` glob matching nothing while the canonical spelling matched the
  file, on both the cold CLI and the proxied path: the options were resolved
  correctly and too late, after the closure had already been built from the
  raw glob (gap O204/O206). The parameter is mandatory rather than optional
  so a caller with no table cannot build a filter at all — forgetting is
  inexpressible, not merely discouraged. It overlaps `resolvePatterns`
  deliberately: `Resolve` iterates to a fixed point, so resolving an
  already-canonical path is a no-op, and the overlap is what keeps the
  defect from returning through whichever door is added next.
- Multi-query about coordination: SearchSplit and SearchCombined
  collect the primary --about (AboutTopK) plus every Mode == "about"
  filter row (AboutSet), submit one Librarian.SearchChunksMulti
  call, route TopK to merge/intersect/vecOnly, and convert each
  AboutSet ChunkIDs map into a microfts2.WithChunkFilter closure
  delivered through extraOpts. (R1935)

- trigram selection (R3401, R3402): ark supplies
  `microfts2.FilterByRatio(trigramRatio, trigramMinCount)` unchanged. The
  second parameter is the **cost floor** — the ratio answers "does this
  discriminate?" and nothing was answering "is this expensive?", though the
  filter exists for both. `trigramMinCount = 1000` is ark's number (microfts2
  declined to choose it) and is chosen rather than profiled, because the
  parameter bounds its own blast radius: inert above `minCount/ratio` chunks,
  and on a corpus that small both the extra scan and the extra verification it
  admits are bounded by that same number. Ark has exactly **one** ratio and one
  call site; a second at a lower ratio would widen that reach. The one-chunk case is the dependency's guarantee — it abstains
  below two chunks (microfts2 R674) — because a rule that cannot discriminate
  should decline rather than be nudged into discriminating. Ark states the
  requirement and guards it with a `sleeping-sentry` pair rather than owning
  the mechanism. **Ark's own copy is retired (2026-08-05) and the reason is
  the useful part:** it floored the threshold at `max(1, int(total × ratio))`
  and claimed the floor was provably a no-op above one chunk because
  `int(total × 0.50) >= 1` for `total >= 2`. That holds only at ratio 0.50
  while the function took `ratio` as a parameter, so the floor fired whenever
  `ratio < 1/totalChunks` and kept trigrams the contract says to skip on
  corpora of any size. The guard that was supposed to prove the claim swept
  `totalChunks` 2..200 with the ratio pinned — the safe axis — so it could not
  have caught it. A term carried by more than half a small corpus is still
  skipped, deliberately: that is the rule working at that size, and the
  broader fallback would buy only stopword queries at the cost of the fattest
  postings the filter exists to avoid.

- resolvePatterns(pats) (R3359, R3362): maps each file-glob row into the
  canonical namespace before `resolveFilters` matches anything. Stored paths
  are canonical, so an alias-spelled filter matches nothing while looking like
  a filter that simply excluded everything. `ResolvePattern`, not `Resolve`:
  only the longest wildcard-free prefix resolves and the remainder rides along
  unchanged. A nil table (a Searcher built without a scanner, as some tests do)
  resolves nothing, which is the pre-symlink behaviour. **This is not the only
  place a file filter resolves, and it is not the last one to run** — see
  BuildChunkFilters above (R3400), which resolves the same rows where they
  become closures, for the callers that never pass through here.
- links: the shared `LinkTable`, read off the actor — safe because the table
  publishes immutable snapshots (crc-LinkTable.md). It is the **same** table
  the Scanner writes: two tables would mean the filter door resolving against
  something the scan never updated, which is the silent disagreement R3359
  names. Assembled at the one point that builds both (`db.go`), so the sharing
  is visible rather than assumed.

## Collaborators
- microfts2.DB: trigram search, file info resolution, ChunkCache for chunk text retrieval
- Librarian: embeds queries (EmbedQuery) and ranks chunks via EC records (SearchChunks) (R1915, R1916)
- Store: the tag index (TagFiles queries for tag-based filtering, MatchTagNames/MatchTagValues for contains-tokens), file centroid (EF) reads (R1921)
- Indexer: re-index stale files during consistent search
- goldmark: markdown → HTML rendering for previews
- ChatVoice: RenderPreview renders chat-jsonl by voice segment (R3620)

## Sequences
- seq-search.md
