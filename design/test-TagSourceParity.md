# Test Design: Tag Source Parity
**Source:** crc-Store.md, crc-ExtMap.md, crc-TmpTagStore.md
**Refs:** R2344–R2354

Covers the cross-cutting concern "Tag Source Parity" — that read APIs
that enumerate tags/values/counts/per-target sets union inline,
ext-routed virtual, and tmp:// overlay sources.

## Fixture: parityFixture

`setupParity(t)` wires:
- `Store` with a `TvidMap`
- `ExtMap` (via `Store.SetExtMap`)
- `TmpTagStore` (via `Store.SetTmpTagStore`)
- A minimal `SetChunkResolver` that maps persistent fileID 1 → chunkID 100.

Three helper methods seed each source independently:
- `addInline(tag, value)` — writes T/F/V via `Store.UpdateTagValues` for chunkID 100 / fileID 1.
- `addExt(tvidExt, target, tag, value, count)` — directly populates `ExtMap.routedTagsByTvidExt`, `targetToChunk`, `chunkToTargets`, `virtualTagCount`.
- `addTmp(tag, value)` — writes overlay T/F/V via `TmpTagStore.UpdateTagValues` for overlay chunkID 0xFFFFFFFFFFFFFFF0 / fileID 0xFFFFFFFFFFFFFFFE.

## Test: TestMatchTagNamesParity
**Purpose:** R2349 — `MatchTagNames` matches against names from all three sources.
**Input:** Inline "shared-inline", ext "shared-ext", tmp "shared-tmp".
**Expected:** `MatchTagNames(["shared"])` returns all three names sorted.

## Test: TestListTagsParity
**Purpose:** R2345 — `ListTags` unions names from all three sources.
**Input:** Same as above.
**Expected:** Result contains all three names; counts sum across sources.

## Test: TestTagCountsParity
**Purpose:** R2346 — `TagCounts` sums inline + ExtMap virtual + TmpTagStore overlay counts.
**Input:** Inline "count-tag" (1 chunk), ext "count-tag" with count=3, tmp "count-tag" (1 chunk).
**Expected:** `TagCounts(["count-tag"])` returns Count=5 (1+3+1).

## Test: TestQueryTagValuesParity
**Purpose:** R2347 — `QueryTagValues` unions inline V records, ExtMap virtual values, and tmp:// overlay values.
**Input:** "qv-tag" with values "inline-val" / "ext-val" / "tmp-val" across the three sources.
**Expected:** All three values present in the result.

## Test: TestMatchTagValuesParity
**Purpose:** R2350 — `MatchTagValues` matches against values from all three sources.
**Input:** "mv-tag" with values "shared-inline" / "shared-ext" / "shared-tmp".
**Expected:** `MatchTagValues("mv-tag", ["shared"])` finds all three.

## Test: TestAllTagsForChunkParity
**Purpose:** R2351 — `AllTagsForChunk` unions inline TagsForChunk plus ExtMap routings onto the chunk.
**Input:** Inline "atc-inline" on chunkID 100, ext routing of "atc-ext" onto chunkID 100.
**Expected:** Result contains both pairs.

## Test: TestTagsForChunkInlineOnly
**Purpose:** R2344 exception clause + R2351 — `TagsForChunk` returns *only* inline tags; ext-routed tags do not leak in (write-side safety).
**Input:** Inline "tfc-inline" + ext routing of "tfc-ext" onto the same chunk.
**Expected:** Result contains "tfc-inline" but not "tfc-ext".
