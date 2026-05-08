# Test Design: ChunksForTag
**Source:** crc-Librarian.md, crc-Store.md, specs/chunks-for-tag.md

## Test: ChunksForTag ranks chunks by max cosine across the tag's defs
**Purpose:** happy path — three chunks, one tag with two ED records;
returned order matches max-cosine ordering.
**Input:** write EC for chunk=1, chunk=2, chunk=3 with crafted vectors;
write ED("priority", 10) and ED("priority", 11) with vectors crafted so
that:
- cosine(EC[1], ED[10]) = 0.90, cosine(EC[1], ED[11]) = 0.30 → agg 0.90
- cosine(EC[2], ED[10]) = 0.40, cosine(EC[2], ED[11]) = 0.85 → agg 0.85
- cosine(EC[3], ED[10]) = 0.50, cosine(EC[3], ED[11]) = 0.60 → agg 0.60

Plus C-records mapping each chunk to a primary file. Call
`ChunksForTag("priority", 5)`.
**Expected:** length 3; chunks returned in order [1, 2, 3]; each
ChunkSuggestion.Score equals its aggregate. Each MotivatingDefs is
ranked by per-def score descending (chunk 1: [10, 11]; chunk 2:
[11, 10]; chunk 3: [11, 10]).
**Refs:** R2194, R2197, R2202, R2205, R2206

## Test: ChunksForTag caps to k
**Purpose:** k truncates after sort. Verifies heap-of-k bound.
**Input:** write 5 distinct chunks' EC records and one ED record for
"priority" crafted so each chunk has a distinct cosine. Call
`ChunksForTag("priority", 2)`.
**Expected:** length 2; the two highest-scoring chunks only, sorted
descending by score.
**Refs:** R2199

## Test: ChunksForTag — k <= 0 returns (nil, nil)
**Purpose:** R2207 — invalid request size.
**Input:** populate EC and ED. Call `ChunksForTag("priority", 0)` and
`(..., -3)`.
**Expected:** both return (nil, nil), no error.
**Refs:** R2207

## Test: ChunksForTag — tag has no ED records
**Purpose:** R2209 — empty for the requested tag (other tags may exist).
**Input:** populate EC. Write ED("other", 10). Call
`ChunksForTag("priority", 5)`.
**Expected:** results == nil, err == nil.
**Refs:** R2209

## Test: ChunksForTag — empty EC prefix
**Purpose:** R2211 — no chunks embedded yet.
**Input:** write ED("priority", 10) only. No EC records. Call
`ChunksForTag("priority", 5)`.
**Expected:** results == nil, err == nil.
**Refs:** R2211

## Test: ChunksForTag — EC dimension mismatch skipped
**Purpose:** R2198 — model swap mid-flight leaves orphan EC records;
they're skipped, the rest is ranked normally.
**Input:** write ED("priority", 10) at 768-dim. Write EC for chunk=1
at 768-dim and chunk=2 at 384-dim. Call `ChunksForTag("priority", 5)`.
**Expected:** length 1; only chunk 1 returned.
**Refs:** R2198

## Test: ChunksForTag — chunk with no CRecord is dropped
**Purpose:** R2200 — orphan EC. The candidate makes the heap on score
but is dropped during FileID resolution.
**Input:** write ED("priority", 10). Write EC for chunk=1 (with
CRecord pointing at file 100) and chunk=999 (no CRecord). Call
`ChunksForTag("priority", 5)`.
**Expected:** length 1; only chunk 1 returned.
**Refs:** R2200

## Test: ChunksForTag — missing path entry leaves Path empty
**Purpose:** R2201 — fileid has no FTS path entry. Both chunk path
and def path use the same lookup.
**Input:** populate EC for chunk=1 with CRecord pointing at file=999
(no path entry). Write ED("priority", 10) (also no path entry for
file 10). Call `ChunksForTag("priority", 5)`.
**Expected:** length 1; ChunkSuggestion[0].FileID == 999;
ChunkSuggestion[0].Path == ""; MotivatingDefs[0].FileID == 10;
MotivatingDefs[0].Path == "". No error.
**Refs:** R2201

## Test: ChunksForTagDef ranks by single-def cosine
**Purpose:** happy path — single ED record, multiple chunks.
**Input:** write EC for chunk=1, chunk=2, chunk=3. Write ED("priority",
10) crafted so cosine(EC[1], ED) > cosine(EC[3], ED) >
cosine(EC[2], ED). C-records map all to a primary file. Call
`ChunksForTagDef("priority", 10, 5)`.
**Expected:** length 3; order [1, 3, 2]; each MotivatingDefs has
length 1 with FileID=10 and Score == ChunkSuggestion.Score.
**Refs:** R2195, R2204

## Test: ChunksForTagDef — ED[tag, fileid] absent
**Purpose:** R2210 — the requested definition does not exist.
**Input:** populate EC. Write ED("priority", 10). Call
`ChunksForTagDef("priority", 999, 5)`.
**Expected:** results == nil, err == nil.
**Refs:** R2210

## Test: ChunksForTagDef — k <= 0 returns (nil, nil)
**Purpose:** R2207.
**Input:** populate EC and ED. Call `ChunksForTagDef("priority", 10, 0)`.
**Expected:** results == nil, err == nil.
**Refs:** R2207

## Test: read-only — no LMDB writes
**Purpose:** R2212 — verify neither call mutates state.
**Input:** snapshot RecordCounts() before; call `ChunksForTag` and
`ChunksForTagDef`; snapshot after.
**Expected:** all per-prefix counts unchanged after both calls.
**Refs:** R2212

## Test: no orphan filter applied
**Purpose:** R2214 — the API does not filter chunks already carrying
the tag; that is caller policy.
**Input:** chunk=1 already carries V record for ("priority", "high").
Write EC for chunk=1 and chunk=2; write ED("priority", 10). Call
`ChunksForTag("priority", 5)`.
**Expected:** both chunks returned (chunk 1 not filtered out).
**Refs:** R2214
