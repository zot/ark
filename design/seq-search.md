# Sequence: Search

Covers combined search and split search (--about, --contains, --regex).

## Participants
- CLI
- Searcher
- microfts2
- microvec

## Flow: Combined Search

```
CLI ──> Searcher.SearchCombined(query, opts)
         │
         ├──> microfts2.Search(query)
         │     └── returns []SearchResult{Path, StartLine, EndLine, Score}
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
         └──> Searcher.FormatResults(results, opts)
               ├── microfts2.FileInfoByID(fileid) for each result
               └── output: filepath:startline-endline [score]
```

## Flow: Split Search (both flags)

```
CLI ──> Searcher.ValidateSplitFlags(opts)
         │  error if --contains and --regex both set
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
         └──> FormatResults (same as combined)
```

## Flow: Split Search (single flag)

```
CLI ──> Searcher.SearchSplit(opts)
         │
         ├── only --about? ──> microvec.Search(text, k)
         ├── only --contains? ──> microfts2.Search(text)
         └── only --regex? ──> microfts2.SearchRegex(pattern)
              │
              └──> FormatResults (same as combined)
```
