# Chunk Callback Tag Extraction

Extract tags from clean chunk text instead of raw file content.
Language: Go. Environment: ark indexer, microfts2 dependency.

## Problem

Tag value (V) records have noise from JSONL raw-file extraction.
When ark indexes a `.jsonl` conversation log, `ExtractTagValues`
runs on the full raw file content — including JSON metadata fields,
timestamps, and role markers. The chat-jsonl chunker in microfts2
extracts only the `content` field, but ark never sees that clean
text — it extracts tags from the raw bytes.

Additionally, `splitChunks` re-reads chunk ranges from microfts2's
FileInfo to reconstruct chunk text for microvec embeddings. This is
a redundant second pass over data that microfts2 already chunked.

## Solution

microfts2 now provides `WithChunkCallback(fn ChunkCallback)` as an
`IndexOption` and `WithAppendChunkCallback(fn ChunkCallback)` as an
`AppendOption`. The callback type is `func(chunkText string)`. It
fires once per chunk, in order, after UTF-8 validation.

Wire the callback into ark's indexer to:
1. Accumulate chunk text slices (replacing `splitChunks` for microvec)
2. Extract tags from each chunk's clean text (replacing raw-content extraction)

## AddFile

Pass `WithChunkCallback` to `AddFileWithContent`. In the callback,
append chunk text to a slice and run `ExtractTagValues` on the chunk.
After the call returns, pass accumulated chunks to microvec and
accumulated tag values to Store. Remove the `splitChunks` call.

## RefreshFile (full refresh)

Pass `WithChunkCallback` to `ReindexWithContent`. Same accumulation
as AddFile. Remove `splitChunks` from `executeFullRefresh`. Tag
extraction moves from `prepareRefresh` to `executeFullRefresh` —
the prep struct no longer carries pre-extracted tags for full
refresh. (Append prep still extracts from `tagWindowForAppend`.)

## RefreshFile (append path)

Pass `WithAppendChunkCallback` to `AppendChunks`. Accumulate only
the new chunks' text. Extract tags from callback text instead of
`tagWindowForAppend`. The `splitChunks` call for microvec still
needs all chunks (old + new) — use `WithChunkCallback` on the
`ReindexWithContent` fallback or keep the existing full-refresh
microvec path.

Note: for the append path, `splitChunks` is still needed for
microvec (needs ALL chunks for re-embedding). Only the tag
extraction switches to callback. The `splitChunks` call for microvec
in the append path stays until microvec supports incremental updates.

## Parallel Refresh

`prepareRefresh` continues to read the file and detect appends.
For full refresh, it no longer extracts tags — that happens in
`executeRefresh` via the callback. For append, prep still extracts
tags from the append window (unchanged). The prep/execute split
stays; only what moves between them changes.

## Tag Merging

`ExtractTagValues` runs per chunk. Results merge across chunks:
- Tag counts: sum counts for the same tag name
- Tag values: collect all (tag, value) pairs; duplicates are harmless
  (Store deduplicates by fileid)
- Tag defs: last-writer-wins per tag name (same as current behavior)

A helper `mergeTagValues(accumulated [][]TagValue) []TagValue` or
inline accumulation handles the merge.

## splitChunks Removal

After wiring callbacks into all paths, `splitChunks` is only needed
in the append microvec path (needs all chunks). For AddFile and
full refresh, it's eliminated. If this is the only remaining caller,
consider keeping it for the append path only or switching microvec
to use the callback too (accumulate all chunks via a Reindex callback
after append).

## Sort Tag Values by Count

`ark tag values` output should sort by count descending (high-count
values first). This is a display change in the CLI command, not an
indexing change.
