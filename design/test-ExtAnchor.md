# Test Design: @ext target anchor for display
**Source:** crc-ExtMap.md, specs/tag-overview.md

Covers R2073's `externalTarget` contract: the anchor part of an `@ext`
TARGET reaches the rendered element. Two layers — the pure split, and the
end-to-end path proving the field is actually populated when a real
routing resolves.

Motivation: `IncomingExtRouting.TargetAnchor` was declared, rendered into
the HTML, and never assigned, so `externalTarget` shipped empty for every
anchored routing. The integration test is what would have caught that; the
pure tests pin the display shape.

## Test: anchor part is whatever followed the colon
**Purpose:** `extTargetAnchorPart` returns the raw anchor with delimiters
intact, so a string anchor stays distinguishable from a regex one.
**Input:** targets `/a/b.md` (bare), `%uuid` (bare uuid), `/a/b.md:3-5`
(range), `/a/b.md:"some text"` (string), `/a/b.md:/re.*x/` (regex),
`/a/b.md[2]:"t"` (modifier before the colon), and one with padding
around the anchor.
**Expected:** `""` for both bare forms; `3-5`; `"some text"` *with* the
quotes; `/re.*x/` *with* the slashes; `"t"` for the modifier case (the
`[2]` is base-side, never anchor-side); padded input trimmed.
**Refs:** crc-ExtMap.md, R2073

## Test: a resolved anchored routing carries its anchor
**Purpose:** the field is populated on the real read path — the defect
that shipped was precisely that this end never ran.
**Input:** index a target file with several line chunks; index a second
file declaring `@ext: <target>:"<text unique to one chunk>" @note: …`;
resolve that chunk's incoming routings via
`ExtMap.ExtRoutingsForTargetChunk`.
**Expected:** exactly one routing, its `Routed` carrying the `@note`, and
its `TargetAnchor` equal to the quoted anchor **including quotes**.
**Refs:** crc-ExtMap.md, R2073, R2376, R2377

## Test: a bare target carries no anchor
**Purpose:** R2073's "empty when the target was a bare path or bare UUID"
holds on the same path — the anchor is absent, not a stray artifact of the
target text.
**Input:** as above, but the `@ext` names the target file with no
narrower; routing lands on the file's first chunk (preamble convention).
**Expected:** one routing whose `TargetAnchor` is `""`.
**Refs:** crc-ExtMap.md, R2073, R2377
