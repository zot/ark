# Searcher
**Requirements:** R46, R47, R48, R49, R50, R51, R52, R53, R54, R55, R56, R57, R58, R59, R60

Queries one or both engines and merges or intersects results.

## Knows
- fts: *microfts2.DB — trigram engine
- vec: *microvec.DB — vector engine

## Does
- SearchCombined(query, opts): send same query to both engines,
  merge by (fileid, chunknum), combine scores, sort descending,
  apply -k limit
- SearchSplit(opts): dispatch --about to microvec, --contains to
  microfts2 Search, --regex to microfts2 SearchRegex. If single
  flag, return that engine's results. If both, intersect by
  (fileid, chunknum).
- ValidateSplitFlags(opts): error if --contains and --regex both set
- Merge(ftsResults, vecResults): combine by (fileid, chunknum) key,
  sum or weighted-combine scores
- Intersect(ftsResults, vecResults): keep only (fileid, chunknum)
  present in both result sets
- FormatResults(results, opts): resolve fileids to paths and line
  ranges via microfts2.FileInfoByID, apply --scores and --after filters

## Collaborators
- microfts2.DB: trigram search, file info resolution
- microvec.DB: vector search

## Sequences
- seq-search.md
