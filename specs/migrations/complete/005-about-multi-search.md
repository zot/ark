# About multi-search and chunk-level about filter

Add "about" as a chunk-filter mode, and combine all about queries (the
primary `--about` plus any number of about filter rows) into a single
EC walk.

Language: Go for the search backend, TypeScript for the search UI.
Environment: ark Searcher / Librarian, ark-search web component.

## Problem

The previous migration (microvec → EC search) wired the primary
`--about` query through `Librarian.SearchChunks`, but left two gaps:

1. **Chunk-level "about" filter is silently dropped.** The CLI parses
   `-about` as a filter row (cmd/ark/main.go:783) and the UI shape
   carries `Mode == "about"` rows, but `BuildChunkFilters`
   (search.go:588) has no `case "about"` — those rows fall through
   `default → continue`. R1787 specced an `AboutChunkFilter` chunk
   filter; nothing ever implemented it.

2. **Each about query would do its own EC walk.** Once the filter is
   wired, a single search with one primary `--about` and three about
   filter rows would walk the EC region four times. With ~47K chunks
   × 768 dims that's wasted I/O.

This migration closes both gaps in one pass: a multi-query search
function over a single EC walk, plus the missing chunk-level filter.

## Behavior preserved

- Primary `--about` keeps its current shape — top-k chunk-level
  ranking, intersect-able with `--contains` / `--regex` / `--like-file`.
- Centroid pre-filter behavior (`about_centroid_filter`,
  `about_centroid_threshold`) is unchanged.
- `tag_model` unconfigured continues to error on `--about` and skip
  about filters with a logged warning.

## Behavior added

### Chunk-level "about" filter

The chunk-level about filter uses **top-K** semantics, not a cosine
threshold. The reason is empirical: with the nomic embedding model
ark uses, basically every chunk in the corpus has cosine similarity
> 0.3 against any query — the embedding space packs everything into
a tight neighborhood. A threshold knob is therefore either too
permissive (no narrowing) or too strict (drops most relevant
matches). Top-K is well-defined regardless of how the embedding
distribution looks: "keep the N chunks most similar to this query."

For each `Mode == "about"` filter row:

1. Embed the row's query via `Librarian.EmbedQuery`.
2. Run an `AboutTopK` request through `SearchChunksMulti` with
   `K = row.K` if set, else `cfg.AboutFilterTopK` (default 200).
3. Convert the resulting `[]ChunkScore` into a chunkID set.
4. Return a `microfts2.WithChunkFilter` closure that checks
   `crec.ChunkID` against the set. Polarity (`with`/`without`)
   negates the closure as in other modes.

`ChunkFilterRow` gains an optional `K int` field. When non-zero it
overrides the config default for that row, letting a single query
mix different filter widths.

If the embedding pipeline isn't available, about-mode filter rows
are dropped with a warning rather than failing the whole search.

The centroid pre-filter (file-level, gated by
`about_centroid_filter`) keeps its original cosine threshold
(`about_centroid_threshold`, default 0.3) — file centroids have
meaningfully different similarity distributions than chunks (a
coarse "is this file even in the neighborhood" signal where 0.3
still discriminates).

### Single-pass multi-query search

A new method on `Librarian`:

```go
// AboutRequest is one query in a multi-query EC walk. Each request
// gets its own top-K min-heap; filter rows turn their TopK into a
// chunkID set downstream.
type AboutRequest struct {
    QueryVec []float32
    K        int // top-k to retain
}

// AboutResult is the per-request top-K reduction.
// Index-parallel to the request slice.
type AboutResult struct {
    TopK []ChunkScore
}

// SearchChunksMulti walks EC records once. For each chunk and each
// request, computes cosine similarity against QueryVec and pushes
// onto that request's min-heap of size K. After the walk, every
// surviving chunk's FileID is resolved via fts.ReadCRecord (one
// shared txn).
func (l *Librarian) SearchChunksMulti(reqs []AboutRequest) ([]AboutResult, error)
```

`SearchChunks` becomes a thin wrapper:

```go
func (l *Librarian) SearchChunks(qvec []float32, k int) ([]ChunkScore, error) {
    res, err := l.SearchChunksMulti([]AboutRequest{{QueryVec: qvec, K: k}})
    if err != nil || len(res) == 0 { return nil, err }
    return res[0].TopK, nil
}
```

There's only one request shape — top-K. Filter rows are just top-K
requests whose TopK results get converted into a chunkID set by the
caller. The threshold-based "AboutSet" kind from earlier drafts is
gone: top-K always narrows, threshold doesn't.

### Searcher integration

`Searcher.SearchSplit` and `SearchCombined` build a single
`[]AboutRequest` carrying:

- The primary `--about` query as `AboutTopK` (when `opts.About != ""`).
- Each `Mode == "about"` chunk filter row as `AboutSet`, threshold from
  config.

They issue one `SearchChunksMulti` call, then pass the resulting
TopK to merge/intersect/vecOnly and the resulting Sets to filter
closures wrapped by `microfts2.WithChunkFilter`. `BuildChunkFilters`
itself stays oblivious to the multi-pass — Searcher hands it the
prebuilt closures via `extraOpts`. (For server `/search/grouped`,
the same wiring lives near the existing `BuildChunkFilters` call.)

### CLI

No new flags. `-about QUERY` already parses as a filter row
(cmd/ark/main.go:787). About queries (primary or filter) only run
through the HTTP server — the cold path errors out with an
actionable "start `ark serve`" message. Warming the embedding model
per CLI invocation is more expensive than just running the server.

### UI

Add `"about"` to the `FilterMode` union and `FILTER_MODES` array in
`ark-search/src/ark-search-element.ts` (around line 20–53). The
existing free-text input branch (lines 617+) handles about's input
shape unchanged. The web-component bundle picks up the change after
`make` rebuilds `ark-markdown-editor.js`.

A per-row threshold input is out of scope; the global
`about_centroid_threshold` is used until UX demands per-row control.

## Schema marker

No LMDB schema change. No config schema change beyond what the
previous migration added (`about_centroid_filter`,
`about_centroid_threshold` already exist).

## Order

1. Add `AboutKind`, `AboutRequest`, `AboutResult`, and
   `Librarian.SearchChunksMulti` (single EC walk; per-request reducer).
2. Reduce `Librarian.SearchChunks` to a wrapper around
   `SearchChunksMulti`.
3. Refactor `Searcher.aboutSearch` and the call sites in `SearchSplit`
   / `SearchCombined` so the primary `--about` and any about filter
   rows in `opts.ChunkFilters` are submitted as one
   `SearchChunksMulti` call. Wrap the resulting `AboutSet` results as
   `microfts2.WithChunkFilter` closures and emit them through the
   existing `extraOpts` channel.
4. CLI cold path (`cmd/ark/main.go` search dispatcher): when
   `opts.About != ""` or any chunk filter row has `Mode == "about"`,
   error out before doing local work with a message directing the
   user to start `ark serve`. Embedding-model warm-up is too costly
   per invocation; the server is the only correct host for about
   queries.
5. UI: add `"about"` to the FilterMode union and FILTER_MODES array
   in ark-search-element.ts. Run `make` to rebuild
   `ark-markdown-editor.js`.
6. Update specs/search.md to document the about filter mode.
7. Update crc-Searcher.md and crc-Librarian.md to reflect the new
   methods and the chunk-filter case.
8. New requirements for `AboutKind`, `AboutRequest`,
   `AboutResult`, `SearchChunksMulti`, the `BuildChunkFilters`
   about case, and the UI mode addition.
