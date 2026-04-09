# Chunk-Level Filter Closures

Glue code for composing `WithChunkFilter` callbacks from stacked
filter row parameters. Runs during search candidate evaluation —
chunk-level narrowing that the existing file-level `resolveFilters`
cannot do.
Language: Go. Environment: ark server.

## Context

The search endpoint (`handleSearchGrouped`) accepts file-level
filters: path globs, tag names, FTS queries. These work by
prefiltering file IDs before search. But the stacked filter UI
(SEARCH-UI-IMPROVED.md) needs **chunk-level** filters: contains,
fuzzy, and tag filters that read chunk text and accept/reject
individual chunks during search.

microfts2 provides `WithChunkFilter(func(CRecord) bool)` for this.
The session chunk cache (`ChunkCache`) provides `ChunkText(path,
range)` to read chunk content. The glue code bridges them.

## resolveChunkLocation

A helper that resolves a `CRecord` to `(path string, range string)`
so the chunk cache can read its text. Steps:

1. Take the first FileID from `CRecord.FileIDs`
2. Look up the file path from a `fileIDPaths map[uint64]string`
   (pre-computed once per search from `FileIDPaths()`)
3. Get the FRecord via `CRecord.FileRecord(fileID)`
4. Find the FileChunkEntry where `ChunkID == CRecord.ChunkID`
5. Return `(path, entry.Location)`

The `fileIDPaths` map is captured in the filter closure — computed
once at filter construction time, not per-chunk.

## Filter Closure Constructors

Functions that return `microfts2.ChunkFilter` (i.e.,
`func(CRecord) bool`). Each captures the session chunk cache
and the fileIDPaths map.

### ContainsChunkFilter(term string, cache, paths) → ChunkFilter
Reads chunk text, returns `strings.Contains(lower(text), lower(term))`.

### FuzzyChunkFilter(term string, cache, paths) → ChunkFilter
Reads chunk text, returns true if fuzzy score > 0 (typo-tolerant
substring match using the existing `fuzzyMatch` in librarian.go
or a simpler token overlap).

### TagChunkFilter(tag, value string, mode, cache, paths) → ChunkFilter
Reads chunk text, extracts tags with `ExtractTagValues`, returns
true if a matching tag/value pair is found. Mode: exact, regex,
or fuzzy.

### Polarity (with/without)

The `with` variants use the filter directly.
The `without` variants negate: `func(c CRecord) bool { return !filter(c) }`.

## Endpoint Integration

`handleSearchGrouped` gains a new request field:
```json
{
  "chunk_filters": [
    {"polarity": "with", "mode": "contains", "query": "design"},
    {"polarity": "without", "mode": "regex", "query": "tool_result"}
  ]
}
```

Each filter row becomes a `WithChunkFilter` option. Multiple
filters AND together (microfts2 semantics). The `fileIDPaths`
map is computed once per search before building the filters.

Regex filters use the existing `WithRegexFilter`/`WithExceptRegex`
(more efficient than ChunkFilter for regex). Files filters use
the existing `resolveFilters` path (ID-level, not chunk-level).
