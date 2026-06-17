# Suggest Tag Names

Given a chunk, return tag names whose definitions describe the kind of
thing the chunk is about, ranked by similarity. Read-only query API
over the ED records introduced in Phase 1A. No model call, no agent.

Substrate for V3 manual chunk curation: the user clicks Curate on a
chunk, the UI calls `SuggestTagNames`, the user picks from the
ranked list and types a value.

Language: Go. Environment: ark server, in-process embedding context
already loaded for ED writes (no new model lifecycle).

## Public API

```go
// SuggestTagNames returns tag names whose ED vectors are nearest to
// the given chunk's EC vector, ranked by cosine similarity. k caps
// the number of distinct tag names returned.
func (l *Librarian) SuggestTagNames(chunkID uint64, k int) ([]TagSuggestion, error)
```

Lives on Librarian beside `EmbedSimilarTagValues` and `SearchChunks`,
matching the existing layering: Librarian owns vector queries; DB owns
the index data plane and does not reach into the embedding model.
HTTP-layer callers (UI handlers on Server) reach the librarian via
`srv.librarian` — the established pattern for vector queries.

Result shape:

```go
type TagSuggestion struct {
    Tag             string             // tag name
    Score           float64            // best ED match across all defining files
    MotivatingFiles []TagSuggestionRef // ranked, best score first
}

type TagSuggestionRef struct {
    FileID uint64
    Path   string  // resolved at return time via fts.FileIDPaths()
    Score  float64 // this file's ED score against the chunk
}
```

## Algorithm

1. Read `EC[chunkID]` — the chunk's embedding vector. Missing → return
   `(nil, nil)`. Mismatched dimension vs the loaded model → skip
   (consistent with `SearchChunksMulti`'s same-dim guard).
2. Walk every ED record. For each `(tag, fileid)`, cosine-similarity
   the ED vector against the EC query vector.
3. Aggregate per tag using **max** — the tag's score is the best
   score across all of that tag's ED records. Reasoning:
   averaging dilutes one sharp definition match with weaker
   definitions in other files; the UI wants to surface the *one*
   file whose definition motivated this candidate.
4. Retain motivating files per tag — keep all (fileid, score)
   contributions, sorted descending by score. The UI shows the top
   1–3 by default.
5. Sort tags by aggregate score descending, return the top k.

The walk is one cursor pass over the ED prefix. At current scale
(~270 ED records growing to ~1k) this is sub-millisecond — no
need for an index or pre-aggregation.

## Path Resolution

`MotivatingFiles[].Path` is filled by `db.FTS().FileIDPaths()` once
per call (one `map[uint64]string` lookup), not by N point reads.
This matches the existing pattern in `search.go` and inside
`SearchChunksMulti`. If a fileid has no path entry (file
unindexed since the ED was written), Path is left empty rather
than returning an error.

## Empty and Error Cases

- `k <= 0` → return `(nil, nil)`.
- Chunk has no EC record → return `(nil, nil)`. Not an error: chunks
  embed lazily, the UI may call before the chunk has been processed.
- Embedding unavailable (no `[embedding] model` configured, or model file
  missing) → return `(nil, nil)`. The UI degrades gracefully to
  manual tag entry.
- ED prefix empty (no tag defs indexed yet) → return `(nil, nil)`.
- Vector dimension mismatch on a single ED record → skip that
  record (model swap mid-flight). Don't fail the whole query.

## What This Does Not Do

- Does not call the embedding model. The chunk's EC vector is read
  from the index; ED vectors are read from the index. Pure cosine math.
- Does not invoke a search agent. No spectral expansion, no
  reranking. Phase 1.5 (`Find Connections`) is a separate slice.
- Does not propose tag *values*. Phase 1's UI keeps the value
  field blank for the user to type. EV-based value suggestion
  is deferred to Phase 2.
- Does not write anything. Read-only API.
- Does not gate the response by score threshold. Returns top-k
  regardless of absolute score. The UI may apply a minimum
  display threshold; the API is honest about ranking.
- Does not rebuild any index. ED records are written by the
  post-reconcile batch-embed pass; this API only reads them.

## Performance

- One read transaction covers the full operation.
- One ED prefix scan (~270–1000 records today).
- One `FileIDPaths` call (~N records, ~318 KB at current corpus
  size — already cached in microfts2's working set).
- Per-record work: cosine over a 768-dim float32 vector, ~3μs
  on the Steam Deck.
- Expected: well under 5 ms cold, sub-millisecond warm.

## Storage Scale

No new records. ED records are already covered by Phase 1A. The
suggested-tag UI may want to cache results per chunkID at the
viewer layer if pagination is added; the API itself is stateless.

## Lua API

`mcp.suggestTagNames(chunkID, k)` — thin Lua wrapper over
`Librarian.SuggestTagNames`. Surfaced for the Phase 1F curation
view so Lua app code can drive the chunk → tag-candidates entry
point without going through the HTTP layer.

```lua
local results = mcp.suggestTagNames(chunkID, 5)
-- results: array of suggestion tables
-- results[i] = {
--   tag = "design-decision",
--   score = 0.83,
--   motivatingFiles = {
--     { fileID = 123, path = "/abs/path/to/def.md", score = 0.83 },
--     ...
--   }
-- }
```

Field naming: lowerCamelCase mirroring the Go struct field names
(`Tag` → `tag`, `MotivatingFiles` → `motivatingFiles`,
`FileID` → `fileID`). Matches the established Lua convention used
by `mcp.inbox()`.

ID encoding: `chunkID` and `fileID` cross the boundary as Lua
numbers. Current corpus IDs (~48K chunks, ~1k file IDs) are well
within IEEE-754 double precision (2^53). The wrapper does not
yet provide a string-encoded fallback — defer until corpus growth
warrants it.

Return shape:

- Success with results → returns the array table.
- Empty result (`(nil, nil)` from Go: missing EC, no ED records,
  embedding unavailable, k ≤ 0) → returns an empty Lua table
  `{}`. Lua-friendly; callers can iterate without nil-guarding.
- Error → returns `(nil, errstring)`. Standard gopher-lua
  two-return convention, parallel to `mcp.inbox()` and
  `mcp.open()`.

Read-only. No new locks, no new write paths.
