# Sequence: Search

Covers combined search, split search, and chunk/file retrieval.

## Participants
- CLI
- Searcher
- microfts2
- microvec

## Flow: Combined Search

```
CLI ──> Searcher.SearchCombined(query, opts)
         │
         ├──> ResolveFilters(opts)
         │     ├── path filters: glob --filter-files/--exclude-files
         │     │   against indexed file paths → file ID set
         │     ├── content filters: FTS search --filter/--except
         │     │   queries → file ID sets
         │     ├── positives intersect, negatives subtract
         │     └── return WithOnly(ids) or WithExcept(ids)
         │
         ├──> defaultSearchOpts(filterOpt, opts.Score)
         │     └── adds WithDensity() if score="density"
         │
         ├──> microfts2.Search(query, ...searchOpts)
         │     └── returns []SearchResult (filtered by source)
         │
         ├──> if auto mode && 0 FTS results:
         │     └── retry microfts2.Search(query, ...searchOpts+WithDensity())
         │         (fuzzy escalation — OR semantics fallback)
         │
         ├──> microvec.Search(query, k)
         │     └── returns []SearchResult{FileID, ChunkNum, Score}
         │
         ├──> Searcher.Merge(ftsResults, vecResults)
         │     ├── key both by (fileid, chunknum)
         │     ├── combine scores (sum or weighted)
         │     └── sort by combined score descending
         │
         ├──> apply --after filter (check file timestamps)
         ├──> apply -k limit
         │
         ├──> if --chunks: Searcher.FillChunks(results)
         │     └── for each result: read file, extract chunk by offsets
         │
         ├──> if --files: Searcher.FillFiles(results)
         │     ├── deduplicate by fileid (best score wins)
         │     └── for each unique file: read full content
         │
         └──> FormatResults(results, opts)
               ├── default: filepath:startline-endline [score]
               └── --chunks/--files: JSONL to stdout
```

## Flow: Split Search (both flags)

```
CLI ──> Searcher.ValidateSplitFlags(opts)
         │  error if --chunks and --files both set
         │  (--contains + --regex compose: FTS + post-filter)
         │
         ├──> dispatch --about to microvec.Search(aboutText, k)
         │     └── returns vecResults
         │
         ├──> dispatch --contains to microfts2.Search(containsText)
         │    OR --regex to microfts2.SearchRegex(pattern)
         │     └── returns ftsResults
         │
         ├──> Searcher.Intersect(ftsResults, vecResults)
         │     └── keep only (fileid, chunknum) present in both
         │
         └──> FillChunks/FillFiles/FormatResults (same as combined)
```

## Flow: Split Search (single flag)

```
CLI ──> Searcher.SearchSplit(opts)
         │
         ├──> ResolveFilters(opts)
         │
         ├── only --about? ──> microvec.Search(text, k)
         ├── only --contains? ──> microfts2.Search(text, ...filterOpts)
         └── only --regex? ──> microfts2.SearchRegex(pattern, ...filterOpts)
              │
              └──> FillChunks/FillFiles/FormatResults (same as combined)
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
         ├──> ResolveFilters(opts)
         │     └── (same as combined search)
         │
         ├──> buildStrategies(query)
         │     ├── "coverage": scoreCoverage (static)
         │     ├── "density":  scoreDensity (static)
         │     ├── "overlap":  ScoreOverlap (static)
         │     └── "bm25":    db.BM25Func(queryTrigrams)
         │                     └── reads I record counters
         │
         ├──> if --proximity:
         │     └── append WithProximityRerank(2*k) to search opts
         │
         ├──> microfts2.SearchMulti(query, strategies, k, ...filterOpts)
         │     ├── single LMDB View transaction
         │     ├── collect candidates once (trigram intersection)
         │     ├── score with each strategy independently
         │     └── if proximity: rerank top-N per strategy by term span
         │          returns []MultiSearchResult (one per strategy)
         │
         ├──> deduplicate by (fileid, chunknum)
         │     ├── keep best score per chunk across strategies
         │     └── track which strategy produced each result
         │
         ├──> apply -k limit
         │
         └──> filterAndResolve(results, opts)
               └── (same as combined search)
```

## Flow: --like-file

```
CLI ──> read file content from path
         │
         ├──> Searcher.SearchSplit(opts) with LikeFile content
         │     └── microfts2.Search(content, WithDensity())
         │          density scoring handles long query naturally
         │
         ├──> if --about also set: intersect with microvec results
         │
         └──> FillChunks/FillFiles/FormatResults (same as above)
```

## Flow: Grouped Search (app)

```
Server ──> HandleSearchGrouped(req)
            │
            ├──> Searcher.SearchGrouped(query, opts)
            │     │
            │     ├──> SearchWithConsistency(query, opts)
            │     │     └── (same flow as combined search above)
            │     │
            │     ├──> FillChunks(results) — need text for previews
            │     │
            │     ├──> group by fileid, lookup strategy per file
            │     │
            │     ├──> derive highlightQuery from query, opts.Contains,
            │     │     opts.About, or opts.Regex[0] (whichever carries text)
            │     │
            │     ├──> for each chunk: RenderPreview(chunk, strategy, highlightPatterns)
            │     │     ├── markdown: goldmark → HTML
            │     │     ├── JSON: pretty-print if under threshold
            │     │     └── other: HTML-escape plain text
            │     │     └── highlight tokens with <mark> tags
            │     │
            │     ├──> sort files by best chunk score (desc)
            │     ├──> sort chunks within file by score (desc)
            │     │
            │     └──> return [[filepath, strategy, [chunk, ...]], ...]
            │           chunk = {range, score, preview}
            │
            └──> JSON response to client
```

## Flow: Click to Open

```
Server ──> HandleOpen(req)
            │
            ├──> verify path is indexed (DB lookup)
            │
            ├──> exec.Command("xdg-open", path).Start()  [Linux]
            │    exec.Command("open", path).Start()       [macOS]
            │
            └──> return 200 immediately (async)
```

## Flow: Indexing State

```
Server ──> HandleIndexing(req)
            │
            └──> return JSON(currentlyIndexing())

Lua ──> mcp:indexing()
         │
         └──> Go function (registered via WithLua)
               └──> return currentlyIndexing() as Lua table
```

## Flow: --tags

```
CLI ──> run search normally (combined, split, or --like-file)
         │
         ├──> Searcher.FillChunks(results) — need text to scan
         │
         ├──> Searcher.ExtractTags(results)
         │     ├── scan each chunk text for @tag: regex
         │     ├── count occurrences per tag across all chunks
         │     └── track best chunk score per tag
         │
         └──> output: one tag per line, "tag\tcount[\tscore]"
```
