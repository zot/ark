# Chunks For Tag

Given a tag, return chunks whose EC vectors are nearest the tag's ED
vectors, ranked by cosine similarity. Read-only query API over the EC
records and the ED records introduced in Phase 1A. The dual of
`SuggestTagNames`: where SuggestTagNames goes chunk → tag candidates,
ChunksForTag goes tag → chunk candidates.

Substrate for V3 manual chunk curation: in the curation view, a user
focuses on a tag and the workshop surfaces unattributed chunks the tag
might apply to (entry-point 2 — tag → chunk via orphan detection). No
agent in the loop. No model call.

Language: Go. Environment: ark server, in-process embedding context
already loaded for ED writes (no new model lifecycle).

## Public API

```go
// ChunksForTag returns chunks whose EC vectors are nearest to any of
// the named tag's ED vectors, ranked by cosine similarity. k caps the
// number of distinct chunks returned. The aggregate per chunk is the
// max cosine across all of the tag's definition files.
func (l *Librarian) ChunksForTag(tag string, k int) ([]ChunkSuggestion, error)

// ChunksForTagDef restricts scoring to a single (tag, fileid) ED
// record — useful when reconciling divergent definitions of the same
// tag across files. k caps the number of distinct chunks returned.
func (l *Librarian) ChunksForTagDef(tag string, fileid uint64, k int) ([]ChunkSuggestion, error)
```

Lives on Librarian beside `SuggestTagNames`, `SearchChunks`, and
`EmbedSimilarTagValues`. HTTP-layer callers (UI handlers on Server)
reach the librarian via `srv.librarian` — the established pattern for
vector queries.

Result shape:

```go
type ChunkSuggestion struct {
    ChunkID        uint64           // the candidate chunk
    FileID         uint64           // primary file owning the chunk (CRecord.FileIDs[0])
    Path           string           // resolved path for FileID
    Score          float64          // max cosine across MotivatingDefs
    MotivatingDefs []DefMatch       // the tag's definition files, ranked desc
}

type DefMatch struct {
    FileID uint64   // the definition file's id (the file containing the @tag-def)
    Path   string   // resolved path for the definition file
    Score  float64  // this def's cosine against the chunk
}
```

The chunk's `FileID` and `Path` orient the UI to where the chunk lives.
`MotivatingDefs` is the symmetric counterpart of SuggestTagNames'
`MotivatingFiles`: it tells the UI which definition file(s) of the tag
made this chunk score well. For `ChunksForTagDef`, `MotivatingDefs`
has length 1 (the single requested definition file).

## Algorithm

### ChunksForTag

1. Walk the ED prefix; collect every record whose tag matches the
   requested tag. Empty → return `(nil, nil)`.
2. One `View` over the EC prefix. For each EC record, cosine each ED
   query vector against it, taking the **max** as the chunk's
   aggregate score. Skip EC records whose dimension does not match
   the ED dim (mid-flight model swap; mirrors `SearchChunksMulti`'s
   guard).
3. Maintain a min-heap of size k over chunk aggregates. Per chunk,
   retain the per-def scores in order to fill `MotivatingDefs` for
   surviving chunks; drop the per-def scores when a chunk is
   evicted from the heap. Memory cost is O(k × |defs|), bounded.
4. After the walk, resolve each surviving chunk's primary `FileID`
   via `fts.ReadCRecord` inside one shared txn (matches
   `SearchChunksMulti`). Chunks with no CRecord or empty FileIDs
   are dropped.
5. Resolve all referenced fileids → paths via one
   `db.fts.FileIDPaths()` call (chunk file + def files share the
   same lookup).
6. Sort each surviving chunk's `MotivatingDefs` by per-def score
   descending. Sort chunks by aggregate score descending. Return.

### ChunksForTagDef

1. `ReadTagDefEmbedding(tag, fileid)`. Missing → return
   `(nil, nil)`. Mismatched-dim records simply don't surface
   matches (they fall through the per-EC skip in step 2).
2. Same EC walk, single query vector. The min-heap is over a
   plain cosine — no max-aggregate needed.
3. Same FileID resolution and path lookup as `ChunksForTag`.
4. `MotivatingDefs` is a single-entry slice with the requested
   `(fileid, path, score)`.

## Path Resolution

Chunk → primary `FileID` → `Path` and definition-file `FileID` →
`Path` both go through one `db.FTS().FileIDPaths()` call. Matches
the existing pattern in `search.go` and inside `SearchChunksMulti`.
A fileid with no path entry leaves Path empty rather than failing
the call.

## Empty and Error Cases

- `k <= 0` → return `(nil, nil)`.
- Embedding unavailable (no `tag_model` configured, model file
  missing) → return `(nil, nil)`.
- `ChunksForTag`: tag has no ED records → return `(nil, nil)`.
- `ChunksForTagDef`: `ED[tag, fileid]` absent → return
  `(nil, nil)`.
- EC prefix empty (no chunks embedded yet) → return `(nil, nil)`.
- Vector dimension mismatch on a single EC record → skip that
  record. Don't fail the whole query.
- Chunk has no CRecord or empty FileIDs → drop the chunk from
  results (orphan EC). Not an error.
- Chunk's primary FileID has no path entry → `Path` empty;
  definition file FileID has no path entry → `DefMatch.Path`
  empty. No error.

## What This Does Not Do

- Does not call the embedding model. The ED vectors and EC
  vectors are read from LMDB. Pure cosine math.
- Does not invoke a search agent. No spectral expansion, no
  reranking.
- Does not propose new tags or new tag values. Read-only over
  the existing tag definition.
- Does not write anything. Read-only API.
- Does not gate the response by score threshold. Returns top-k
  regardless of absolute score. The UI may apply a minimum
  display threshold; the API is honest about ranking.
- Does not maintain a hot-correlations cache. Phase 1E layers
  caching on top of this primitive; 1D is live, on-demand.
- Does not filter chunks already tagged. The "already carries
  tag X" filter belongs to the caller (UI side or a Phase 1E
  refinement); 1D returns the raw nearest-EC ranking so callers
  can apply their own orphan-detection policy.

## Performance

- ED prefix scan: ~270–1000 records, microseconds.
- EC walk: ~48K records at current corpus.
  - `ChunksForTagDef`: one cosine per EC record, ~3μs/chunk on
    the Steam Deck → expected ~150 ms.
  - `ChunksForTag` (1–5 defs typical): max cosine across all
    defs per EC record → expected 200–700 ms depending on def
    count.
- One LMDB View txn for the EC walk; one fts.View txn for
  CRecord lookups across all surviving chunks; one
  `FileIDPaths` call.
- Result cardinality bounded by `k`; per-chunk
  `MotivatingDefs` length bounded by the tag's def count.

## Storage Scale

No new records. ED records (Phase 1A) and EC records (chunk
embeddings, pre-existing) are both already populated. The
suggestion UI may want to cache results per (tag, fileid) at
the viewer layer if pagination is added; the API itself is
stateless.
