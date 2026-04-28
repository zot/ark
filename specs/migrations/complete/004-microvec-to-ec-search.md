# microvec → EC search

Replace microvec with the existing chunk-embedding (EC) records and
Librarian.EmbedQuery. Remove microvec entirely from ark.

Language: Go. Environment: `ark` indexer + Searcher + DB.Open.

## Problem

ark embedded microvec to score chunks by semantic similarity. The
chunk-embeddings work (specs/chunk-embeddings.md, EC/EF records) gave
ark a native vector store: chunkid-keyed embeddings inside the same
LMDB env, file centroids for cheap pre-filtering, model lifecycle
shared with tag embeddings. microvec is now redundant — and currently
broken at the runtime path: any `--about` query fails with
`vec search: no embedding command configured` because the microvec
embedding pipeline isn't wired up under the new model code.

The deferred work in chunk-embeddings.md ("What This Does NOT Cover →
Query-time search") is exactly the work this migration does.

## Behavior preserved

`--about <text>` continues to be a user-facing search mode and
preserves its semantics in specs/search.md: semantic chunk-level
ranking, intersectable with `--contains`/`--regex`/`--like-file`,
participates in combined search and merge.

The output shape (filepath:startline-endline, JSONL chunks, JSONL
files, --wrap, --scores) is unchanged.

## Behavior changes

`--about` no longer requires an external embedding command. It uses
the same Librarian-managed embedding pipeline that drives EC writes:
`Librarian.EmbedQuery(text)` returns the query vector, then ark scans
EC records with cosine similarity to rank chunks.

When an `--about` query lands while `tag_model` isn't configured (no
embedder available), `--about` returns an actionable error instead of
silent fallback. The existing `Librarian.EmbeddingAvailable()` check
controls this — same gate that already protects `ark embed text` and
`ResolveAboutFilters`.

When EC coverage is partial (some chunks not yet embedded) the search
runs over whatever EC records exist. This matches the pre-existing
"Search during incomplete embedding" guarantee in specs/search.md
— results are valid, just incomplete.

## Components removed

- `microvec` import and module dependency.
- `DB.vec *microvec.DB` field, `DB.Vec()` accessor, and the microvec
  Open/Create calls in `db.go`.
- `Indexer.vec *microvec.DB` field and any `vec.AddFile` /
  `vec.RemoveFile` calls in `indexer.go`. The vector index is now a
  Librarian responsibility (already true for writes via
  `BatchEmbedChunks`).
- `Searcher.vec *microvec.DB` field.
- `microvec.SearchResult` references and the helpers that consumed
  them: `merge`, `intersect`, `vecOnly` get retyped to operate on
  the new chunk-score type (below).
- `vec-bench.md` spec content describing microvec benchmarks (the
  EC-tier benchmarks in `embed-subcommands.md` cover the new
  pipeline).

## Components added

### ChunkScore

New type in search.go (mirrors microvec.SearchResult shape so the
merge/intersect math doesn't change):

```go
type ChunkScore struct {
    ChunkID uint64
    FileID  uint64   // first FileID from CRecord; "primary" file for the chunk
    Score   float64  // cosine similarity in [−1, 1], higher = closer
}
```

`FileID` is recovered from the chunk's CRecord (same data the existing
resolver uses). When a chunk is shared across files, the first FileID
wins for ranking; merge/intersect by (FileID, ChunkID) tuple still
deduplicates correctly because the same chunk always reports the same
primary file.

### Librarian.SearchChunks

```go
// SearchChunks scores all EC records against queryVec by cosine
// similarity and returns the top-k highest-scoring chunks. Skips
// EC records whose dimension does not match queryVec.
func (l *Librarian) SearchChunks(queryVec []float32, k int) ([]ChunkScore, error)
```

Implementation:

1. Embed the query — caller already did this; `SearchChunks` takes
   the vector so callers can reuse a single `EmbedQuery` across
   multiple search modes.
2. Open a single LMDB View. Use a cursor walk on the EC prefix to
   stream `(chunkID, vec)` pairs (sequential read of the contiguous
   EC region, no per-chunk Get).
3. Maintain a min-heap of size `k` keyed by score (ascending). Push
   each chunk; once full, replace the top when a higher-scoring
   chunk arrives. (Same shape as microvec's `topK`.)
4. After the walk, resolve `FileID` for each surviving chunk via
   the in-txn `fts.ReadCRecord(txn, chunkID)` lookup (the same call
   the existing `filesForChunk` resolver uses).
5. Return results sorted by score descending.

The cosine similarity helper already lives in `librarian.go`
(`cosineSimilarity`). No new math.

### Searcher integration

`SearchSplit` and `Search` change `s.vec.Search(query, k*2)` calls to
something like:

```go
qvec, err := s.librarian.EmbedQuery(query)
if err != nil {
    return nil, fmt.Errorf("about: embed query: %w", err)
}
vecResults, err := s.librarian.SearchChunks(qvec, k*2)
if err != nil {
    return nil, fmt.Errorf("about: chunk search: %w", err)
}
```

`merge`, `intersect`, `vecOnly` retype from
`[]microvec.SearchResult` to `[]ChunkScore`. The merge/intersect logic
is unchanged: keying still happens on (FileID, ChunkID).

### About via ChunkFilters (pre-existing)

`ResolveAboutFilters` (search.go:646) and `AboutChunkFilter` already
exist for the file-filter and chunk-filter `about` modes. They stay
as-is — they're already centroid-based, not microvec-based. This
migration only touches the *primary* `--about` query path.

## Centroid filtering — config-gated

EF (file centroid) records keep being maintained. They're cheap to
keep (count + sum vector per file, on the order of MB), and at large
corpus scale they'd be a meaningful pre-filter. At our current scale
they hide information — per the embed-variance analysis, ~1029 files
have at least one chunk more than 0.3 distance from their own
centroid, so a centroid-only filter at the existing `> 0.3` cosine
gate can suppress files whose outlier chunks would have matched.

Two new config options in `ark.toml` control centroid filtering:

```toml
about_centroid_filter = false  # default: off (chunk-level scan only)
about_centroid_threshold = 0.3 # cosine similarity gate when filter is on
```

Schema (config struct, not LMDB):

```go
AboutCentroidFilter    bool    `toml:"about_centroid_filter,omitempty"`
AboutCentroidThreshold float64 `toml:"about_centroid_threshold,omitempty"`
```

Defaults: `AboutCentroidFilter` is `false` (small/medium corpora
bypass the centroid entirely and let chunk-level cosine ranking
decide). `AboutCentroidThreshold` defaults to `0.3` — same value
the codebase has used so far — and is consulted only when the filter
is on.

The two affected call sites both consult the flag and use the
configured threshold:

- **`ResolveAboutFilters` (file-level "about" filter):** when the
  flag is false, skip the centroid scan entirely — return no early
  `WithOnly`/late `WithExcept` options, letting the chunk-level
  filter do the work. When true, gate file centroids at
  `cosineSimilarity > AboutCentroidThreshold`.
- **Primary `--about` path (`Searcher.Search` / `SearchSplit`):**
  when the flag is false, `Librarian.SearchChunks` walks all EC
  records (no pre-filter). When true, narrow the EC walk to chunks
  whose owning file passed the centroid gate at the configured
  threshold. (See "Future work" below — the narrowed walk is a
  separate optimization; the spec ships with the unconditional
  walk.)

A single global threshold is sufficient for now. Future work, if it
becomes warranted: per-fileid thresholds (e.g., derived from each
file's chunk-to-centroid spread) so loose files get a lower gate
and tight files keep the strict one. Out of scope for this
migration; would land as a separate change once the simple knob
proves insufficient.

## Schema marker

LMDB schema is unchanged — EC and EF records keep their layouts and
their write paths. The new addition is a config-only change
(`about_centroid_filter` in `ark.toml`); no version bump on the LMDB
side. The microvec storage that ark used to maintain in its own
records is no longer written — `ark init` / rebuild after this lands
will not create microvec records, and any pre-existing microvec
records are orphaned blobs in the LMDB env that get reclaimed on the
next rebuild.

`Config` validates the flag at load time the same way other booleans
are handled — default `false` if unset, no further check needed.

## Order

1. Add `ChunkScore` type and `Librarian.SearchChunks` method.
2. Add `AboutCentroidFilter` (bool, default false) and
   `AboutCentroidThreshold` (float64, default 0.3) to `Config`. Gate
   `ResolveAboutFilters` on the flag and use the configured
   threshold.
3. Wire `SearchSplit` and `Search` to use `Librarian.SearchChunks`
   instead of `s.vec.Search`. Retype `merge` / `intersect` / `vecOnly`
   to take `[]ChunkScore`.
4. Remove `Searcher.vec` field, `DB.vec` field and `DB.Vec()`
   accessor, `Indexer.vec` field. Remove microvec Open/Create in
   `db.go`. Remove any `vec.AddFile`/`vec.RemoveFile` calls in
   `indexer.go`.
5. Remove the `microvec` import from every Go file that loses its
   last reference.
6. `go mod tidy` — drop the microvec module from `go.mod`.
7. Update specs/search.md to remove microvec references, describe
   the EC path, and document `about_centroid_filter` /
   `about_centroid_threshold`.
8. Update crc-Searcher.md, crc-DB.md, crc-Indexer.md, crc-Librarian.md,
   crc-Config.md to reflect the new collaborator graph and config.
9. Retire microvec-only requirements via
   `~/.claude/bin/minispec update retire` and add new requirements
   for `Librarian.SearchChunks`, `ChunkScore`,
   `AboutCentroidFilter`, and `AboutCentroidThreshold`.
