# Searcher
**Requirements:** R46, R47, R48, R49, R50, R51, R52, R53, R54, R55, R56, R57, R58, R59, R60, R108, R109, R110, R111, R112, R113, R114, R115, R116, R183, R184, R185, R186, R188, R189, R190, R191, R192, R193, R215, R216, R217, R218, R219, R221

Queries one or both engines and merges or intersects results.
Optionally retrieves chunk text or full file content.

## Knows
- fts: *microfts2.DB — trigram engine
- vec: *microvec.DB — vector engine

## Does
- SearchCombined(query, opts): send same query to both engines,
  merge by (fileid, chunknum), combine scores, sort descending,
  apply -k limit. Falls back to FTS-only when no embedding command
  is configured (vec search unavailable).
- SearchSplit(opts): dispatch --about to microvec, --contains to
  microfts2 Search, --regex to microfts2 SearchRegex. If single
  flag, return that engine's results. If both, intersect by
  (fileid, chunknum).
- ValidateSplitFlags(opts): error if --contains and --regex both set;
  error if --chunks and --files both set;
  error if --source and --not-source both set
- ResolveSourceFilter(opts): match --source/--not-source patterns
  against source directories, collect file IDs, return microfts2
  WithOnly or WithExcept search option
- Merge(ftsResults, vecResults): combine by (fileid, chunknum) key,
  sum or weighted-combine scores
- Intersect(ftsResults, vecResults): keep only (fileid, chunknum)
  present in both result sets
- FormatResults(results, opts): resolve fileids to paths and line
  ranges via microfts2.FileInfoByID, apply --scores and --after filters
- FillChunks(results): for each result, read chunk text from file
  using offsets from microfts2.FileInfoByID. Uses same readChunks
  logic as Indexer but reads only the matched chunk.
- FillFiles(results): deduplicate results by fileid, read full file
  content for each unique file. Best chunk score becomes file score.
- SearchLikeFile(path, opts): read file content, use as FTS query
  with density scoring. Participates in split search (can combine
  with --about via intersect).
- ExtractTags(results): scan result chunks for @tag: patterns,
  return tag names with counts and best scores.

## Collaborators
- microfts2.DB: trigram search, file info resolution, chunk offsets
- microvec.DB: vector search

## Sequences
- seq-search.md
