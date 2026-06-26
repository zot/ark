# Sequence: Search

Covers combined search, split search, the tag-primary funnel, and
chunk/file retrieval.

## Participants
- CLI
- Searcher
- microfts2
- Librarian (EC embedding pipeline — `SearchChunks` / `SearchChunksMulti`
  cosine over `ed.Vec`; the old `microvec` module was retired by
  migration 004)

## Post-filter funnel (invariant)

Every primary mode produces an initial candidate set and then that set
flows through the **same** post-filter stack and default
`search_exclude` scope. The choice of primary mode (`-contains`,
`-fuzzy`, `-regex`, `-tag`, `-file-tag`, `-about`) selects only the
candidate set; it never decides whether post-filters run. R2951, R1778.

- Content-scan primaries (`-contains`/`-fuzzy`/`-regex`) and the FTS
  side of combined/split apply the stack *inline* during the microfts2
  scan: `BuildChunkFilters` turns each `ChunkFilterRow` into a
  `WithChunkFilter`/regex option carried in `opts.extraOpts`.
- Index-lookup primaries (`-tag`/`-file-tag` resolve via the tag index;
  `-about` ranks via embeddings) skip the FTS scan, so they apply the
  stack *post-hoc* against the resolved candidate chunkIDs via
  `Searcher.postFilterChunkIDs` (same per-row predicates as
  `BuildChunkFilters`, shared through `rowChunkFilter`).
- The default `search_exclude` scope (R939/R940) is injected the same
  way on every path: `effectiveExcludeFiles` computes it (a positive
  `-files` row disables it), and the path-glob filter applies it.

## Flow: Combined Search

```
CLI ──> Searcher.SearchCombined(query, opts)
         │
         ├──> resolveFilters(opts)
         │     ├── effectiveExcludeFiles: inject search_exclude default
         │     │   (R939/R940) unless an explicit/positive file filter
         │     ├── path filters: glob -files against indexed paths → IDs
         │     ├── content filters → file ID sets
         │     ├── positives intersect, negatives subtract
         │     └── return WithOnly(ids) (negatives already subtracted)
         │
         ├──> defaultSearchOpts(filterOpt, score, opts)
         │     ├── adds WithDensity() if score="density"
         │     └── appends opts.extraOpts — the post-filter WithChunkFilter
         │         closures BuildChunkFilters produced for this query
         │
         ├──> microfts2.Search(query, ...searchOpts)
         │     └── returns []SearchResult (source-filtered, post-filtered)
         │
         ├──> if auto mode && 0 FTS results:
         │     └── retry microfts2.Search(query, ...+WithDensity())
         │
         ├──> aboutSearch(query, k*2)  [EC pipeline]
         │     ├── librarian.EmbedQuery(query) → qvec
         │     ├── librarian.SearchChunks(qvec, k) → []ChunkScore
         │     └── embedding unavailable → fall back to FTS-only
         │
         ├──> applyAboutFilterSets(vecResults, opts.aboutFilterSets)
         │     └── about-mode filter rows applied to vec results (R1935)
         │
         ├──> Searcher.merge(ftsResults, vecResults)
         │     ├── key both by (fileid, chunknum)
         │     ├── combine scores and sort descending
         │     └── cap to -k
         │
         └──> filterAndResolve(results, opts)  →  FillChunks/FillFiles/Format
```

## Flow: Split Search (SearchSplit)

```
CLI ──> Searcher.SearchSplit(opts)
         │
         ├──> resolveFilters(opts)   (default scope + file/content filters)
         │
         ├──> if about: aboutSearch(opts.About, k*2) [EC] or precomputed
         │     └── applyAboutFilterSets(vecResults, aboutFilterSets)
         │
         ├──> if contains: microfts2.Search(text, ...extraOpts, WithVerify)
         │    if regex:    microfts2.SearchRegex(pat, ...extraOpts, regex)
         │    if like-file: microfts2.Search(content, ...+WithDensity())
         │     └── ftsResults already carry the inline post-filter stack
         │
         ├──> combine:
         │     ├── about && fts ──> intersect(ftsResults, vecResults)
         │     ├── about only   ──> vecOnly(vecResults)
         │     │     └── R2951: candidate set skipped the FTS scan, so
         │     │         apply the post-filter stack post-hoc —
         │     │         postFilterChunkIDs over the vec chunkIDs, then
         │     │         filterByPathGlobs for file scope + default exclude
         │     └── fts only     ──> ftsOnly(ftsResults)
         │
         └──> cap -k  →  filterAndResolve(results, opts)
```

## Flow: Tag-primary Search (SearchTagChunks)

A bare `-tag` / `-file-tag` primary (no text/about/fuzzy primary)
resolves straight through the tag index, bypassing the FTS scan. The
server short-circuit (`server.go`) and the CLI-direct fallback
(`cmd/ark/main.go`) both funnel through `db.SearchTagChunks`, so the
post-filter funnel lives there once for both callers. R2442, R2453,
R2951.

```
CLI/Server ──> resolvePrimaryTagChunks(predicate, fileTag) → chunkIDs
         │       (TagChunkFilter / FileTagChunkFilter predicate locations)
         │
         └──> Searcher.SearchTagChunks(chunkIDs, opts)
               │
               ├──> inject default search_exclude into opts.ExcludeFiles
               │     via effectiveExcludeFiles (R939/R940-aware)
               │
               ├──> postFilterChunkIDs(chunkIDs, opts, cache)   [R2951]
               │     ├── build per-row predicates for opts.ChunkFilters
               │     │   (contains/fuzzy/regex/tag/file-tag/files) via
               │     │   rowChunkFilter — the same constructors the FTS
               │     │   path uses, so the funnel cannot drift
               │     ├── read each candidate's CRecord once
               │     └── keep chunkIDs passing every row predicate
               │
               ├──> ChunksByID(survivors) → []SearchResultEntry
               │     (reads C/F records; skips stale chunkIDs)
               │
               ├──> filterByPathGlobs(results, opts)            [R2951]
               │     └── FilterFiles / ExcludeFiles (incl. the injected
               │         default scope) via the shared Matcher
               │
               └──> cap -k  →  caller fills chunks/files as needed
```

## JSONL Output Format

```
--chunks: {"path":"...","startLine":N,"endLine":N,"score":F,"text":"..."}
--files:  {"path":"...","score":F,"text":"..."}
```

One JSON object per line. Score is the combined/best score.
--files deduplicates: multiple chunk hits from one file → one entry,
score is the best chunk score for that file.

## Flow: Multi-strategy Search (--multi)

```
CLI ──> Searcher.SearchMulti(query, opts)
         │
         ├──> resolveFilters(opts)            (same as combined search)
         │
         ├──> buildStrategies(query)
         │     ├── "coverage": ScoreCoverage
         │     ├── "density":  ScoreDensityFunc
         │     ├── "overlap":  ScoreOverlap
         │     └── "bm25":     db.BM25Func(queryTrigrams)
         │
         ├──> if --proximity: append WithProximityRerank(2*k)
         │
         ├──> microfts2.SearchMulti(query, strategies, k, ...filterOpts)
         │     ├── single read transaction
         │     ├── collect candidates once (trigram intersection)
         │     ├── score with each strategy independently
         │     └── if proximity: rerank top-N per strategy by term span
         │
         ├──> deduplicate by (fileid, chunknum), best score per chunk
         ├──> apply -k limit
         │
         └──> filterAndResolve(results, opts)  (same as combined search)
```

## Flow: Fuzzy Search (--fuzzy)

```
CLI ──> Searcher.SearchFuzzy(query, opts)
         │
         ├──> resolveFilters(opts)            (same as combined search)
         ├──> defaultSearchOpts(filterOpt, "", opts)
         ├──> if --proximity: append WithProximityRerank(2*k)
         │
         ├──> microfts2.SearchFuzzy(query, k, ...ftsSearchOpts)
         │     ├── Phase 1: OR-union of query trigrams (posting-list tally)
         │     ├── Select top-k candidates by tally count
         │     └── Phase 2: re-score top-k from C records (ScoreCoverage)
         │
         ├──> convert to []SearchResultEntry (Strategy = "fuzzy")
         ├──> apply -k limit
         │
         └──> filterAndResolve(results, opts)  (same as combined search)
```

## Flow: --like-file

```
CLI ──> read file content from path
         │
         ├──> Searcher.SearchSplit(opts) with LikeFile content
         │     └── microfts2.Search(content, WithDensity())
         │          density scoring handles long query naturally
         │
         ├──> if --about also set: intersect with EC vec results
         │
         └──> FillChunks/FillFiles/FormatResults (same as above)
```

## Flow: Grouped Search (app)

```
Server ──> HandleSearchGrouped(req)
            │
            ├──> Searcher.SearchGrouped(query, opts)
            │     │
            │     ├──> if opts.Multi: SearchMulti(query, opts)
            │     │    elif opts.Contains/About/Regex: SearchSplit(query, opts)
            │     │    elif opts.Fuzzy: SearchFuzzy(query, opts)
            │     │    elif primary tag/file-tag: GroupTagChunks(chunkIDs, opts)
            │     │    else: SearchWithConsistency(query, opts)
            │     │    NOTE: MCP bridge sets Multi only when no split-mode
            │     │    field is active (contains/about/regex)
            │     │
            │     ├──> FillChunks(results) — need text for previews
            │     ├──> group by fileid, lookup strategy per file
            │     ├──> derive highlightQuery from query/contains/about/regex
            │     │
            │     ├──> for each chunk: RenderPreview(chunk, strategy, patterns)
            │     │     ├── markdown: goldmark → HTML
            │     │     ├── JSON: pretty-print if under threshold
            │     │     └── other: HTML-escape plain text
            │     │     └── highlight tokens with <mark> tags
            │     │
            │     ├──> sort files by best chunk score (desc)
            │     ├──> sort chunks within file by score (desc)
            │     │
            │     └──> return [[filepath, strategy, [chunk, ...]], ...]
            │
            └──> JSON response to client
```

## Flow: Click to Open

```
Server ──> HandleOpen(req)
            │
            ├──> verify path is indexed (DB lookup)
            ├──> exec.Command("xdg-open", path).Start()  [Linux]
            │    exec.Command("open", path).Start()       [macOS]
            └──> return 200 immediately (async)
```

## Flow: Indexing State

```
Server ──> HandleIndexing(req)
            └──> return JSON(currentlyIndexing())

Lua ──> mcp:indexing()
         └──> Go function (registered via WithLua)
               └──> return currentlyIndexing() as Lua table
```

## Flow: --tags

```
CLI ──> run search normally (combined, split, tag-primary, or --like-file)
         │
         ├──> Searcher.FillChunks(results) — need text to scan
         ├──> Searcher.ExtractTags(results)
         │     ├── scan each chunk text for @tag: regex
         │     ├── count occurrences per tag across all chunks
         │     └── track best chunk score per tag
         │
         └──> output: one tag per line, "tag\tcount[\tscore]"
```
