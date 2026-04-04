# Chunk Cache Threading

Language: Go. Environment: Linux CLI + HTTP server.

## Problem

When a session-scoped search runs, the ChunkCache is passed to
`FillChunksUsing` after the search completes — so chunk text
retrieved during result population is cached across searches.

But microfts2's internal post-filters (verify, regex, except-regex)
also read chunk text during the search itself. Without the session
cache, each search creates a throwaway internal cache. In interactive
search (keystroke-per-query), this means the same files are re-read
from disk on every query — once by microfts2's post-filters, then
again by ark's FillChunks.

microfts2 now accepts `WithChunkCache(*ChunkCache)` as a SearchOption
(commit 9d3b32b). When provided, its internal `Retrieve` method uses
the cache for verify, regex, and any ChunkFilter callbacks. When not
provided, it auto-creates a per-search cache (backwards compatible).

## Change

Thread the session's ChunkCache into microfts2 search calls via
`WithChunkCache`, so the same cache serves both the search
internals and the post-search FillChunks.

### Where the cache needs to flow

1. **SearchOpts gains a Cache field** — already exists (`Cache
   *microfts2.ChunkCache`). Currently only used in FillChunksUsing
   via SearchGrouped. Now also passed to microfts2 as a SearchOption.

2. **defaultSearchOpts** — when `sopts.Cache` is non-nil, append
   `microfts2.WithChunkCache(sopts.Cache)` to the options slice.
   This threads the cache into every search path (SearchCombined,
   SearchSplit, SearchMulti, SearchFuzzy) without changing their
   signatures.

3. **No caller changes needed** — the session already sets
   `opts.Cache` before calling SearchGrouped. The HTTP handler and
   Lua function already wire session → opts.Cache → FillChunksUsing.
   Adding WithChunkCache to defaultSearchOpts completes the circuit.

### What this buys

- Interactive search: one file read per session, shared between
  microfts2 post-filters and ark's FillChunks
- Stacked filters (future): ChunkFilter callbacks that need chunk
  text can close over the same cache
- CLI without session: no change — nil cache means microfts2
  auto-creates a per-search cache as before
