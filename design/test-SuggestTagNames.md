# Test Design: SuggestTagNames
**Source:** crc-Librarian.md, crc-DB.md, specs/suggest-tag-names.md

## Test: ranks tags by cosine similarity
**Purpose:** happy path — chunk with one EC vector, three tags with one
ED record each; returned order matches cosine ordering.
**Input:** write EC for chunk=1; write ED for ("a",10), ("b",10),
("c",10) with vectors crafted so cosine(EC, "a") > cosine(EC, "c") >
cosine(EC, "b"). Call SuggestTagNames(1, 5).
**Expected:** length 3; tags returned in order [a, c, b]; each
TagSuggestion.Score equals the cosine of its single ED record.
Each MotivatingFiles has length 1 with FileID=10 and Score == Tag
Score.
**Refs:** R2164, R2166

## Test: max-aggregates across multiple files for the same tag
**Purpose:** R2165 — a tag with two ED records reports the better one
as its tag-level score, and surfaces both motivating files ranked.
**Input:** write EC for chunk=1; write ED("a", 10) and ED("a", 20)
with the second crafted to score higher. Call SuggestTagNames(1, 5).
**Expected:** length 1 (only one tag); Score == cosine of ED("a",
20). MotivatingFiles length 2, ordered [{FileID:20, Score:high},
{FileID:10, Score:low}].
**Refs:** R2165, R2166

## Test: caps to k
**Purpose:** k truncates after sort.
**Input:** write 5 distinct tags' ED records; call SuggestTagNames
with k=2.
**Expected:** length 2; the two highest-scoring tags only.
**Refs:** R2164

## Test: no EC for chunk returns (nil, nil)
**Purpose:** R2169 — chunk hasn't been embedded yet.
**Input:** populate ED records but no EC for chunk=999.
Call SuggestTagNames(999, 5).
**Expected:** results == nil, err == nil.
**Refs:** R2169

## Test: no ED records returns (nil, nil)
**Purpose:** R2171 — empty corpus.
**Input:** write EC for chunk=1; no ED records.
**Expected:** results == nil, err == nil.
**Refs:** R2171

## Test: k <= 0 returns (nil, nil)
**Purpose:** R2168 — invalid request size.
**Input:** populate EC and ED. Call SuggestTagNames(1, 0) and (1, -3).
**Expected:** both calls return (nil, nil).
**Refs:** R2168

## Test: dimension mismatch skipped, not fatal
**Purpose:** R2172 — model swap mid-flight leaves orphan ED records;
they're skipped, the rest is ranked normally.
**Input:** write EC for chunk=1 (768-dim); write ED("good", 10) at
768-dim and ED("bad", 11) at 384-dim. Call SuggestTagNames(1, 5).
**Expected:** length 1; only "good" returned.
**Refs:** R2172

## Test: missing path entry leaves Path empty
**Purpose:** R2167 — an ED record's fileid no longer maps to a path.
**Input:** populate EC, populate ED("a", 999) where 999 has no FTS
path entry. Call SuggestTagNames.
**Expected:** length 1; MotivatingFiles[0].FileID == 999;
MotivatingFiles[0].Path == "". No error.
**Refs:** R2167

## Test: read-only — no LMDB writes
**Purpose:** R2173 — verify the call doesn't mutate state.
**Input:** snapshot RecordCounts() before; call SuggestTagNames;
snapshot after.
**Expected:** all per-prefix counts unchanged.
**Refs:** R2173
