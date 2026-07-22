# Test Design: CHAPTER.VERSE resolution
**Source:** crc-DB.md, crc-BibleChunker.md

Covers R3179/R3180/R3214/R3216/R3220 — turning a friendly
`<source>/BIBLE/John:3.16` into the paragraph chunk holding that verse.
Layers: the pure format parsing, the book-index virtual-path rewrite, and the
resolution itself driven through `resolveBibleTarget` against a real indexed
bible file, since the attributes it reads only exist after indexing.

The regression case matters as much as the feature: a range anchor on a
**non-bible** file must keep resolving by exact chunk location, because the
bible path is reached only when the target's strategy is `bible`, and a mistake
there would silently change every existing `@ext` range routing in the corpus.

## Test: CHAPTER.VERSE parses, other anchor shapes don't
**Purpose:** R3179 — the reference form is exactly one dot between two
positive integers; everything else must fall through to ordinary anchor
handling rather than being misread as a verse.
**Input:** `12.1`; and the non-references `3-6` (a line range), `12`,
`12.`, `.1`, `0.1`, `12.0`, `a.b`, `12.1.3`, and the empty string.
**Expected:** `(12, 1, true)` for the first; `ok=false` for every other.
**Refs:** crc-BibleChunker.md, R3179

## Test: verse span containment
**Purpose:** R3176/R3179 — a chunk's `verses` attribute is what decides
whether it holds the verse, in both the span and single-mark forms.
**Input:** span `1-2` against verses 1, 2, 3; span `3` against 3 and 4;
and the malformed spans `""` and `"x"`.
**Expected:** `1-2` covers 1 and 2 but not 3; `3` covers only 3;
malformed spans cover **nothing** — a bad attribute drops its chunk from
consideration rather than matching every reference.
**Refs:** crc-BibleChunker.md, R3176

## Test: a verse resolves to its paragraph
**Purpose:** R3179 end-to-end — the reference finds the one chunk whose
chapter matches and whose verse span contains the verse.
**Input:** index the two-chapter fixture with the `bible` strategy; then
resolve `<path>:2.1`, `<path>:2.2`, `<path>:2.3`, and `<path>:3.1`.
**Expected:** 2.1 and 2.2 both land on the chapter-2 block spanning
verses 1–2 (one paragraph, two verses — the case that motivates
paragraph chunking); 2.3 lands on the following paragraph; 3.1 lands in
chapter 3. Each resolves to exactly one chunk.
**Refs:** crc-DB.md, R3179

## Test: the virtual BIBLE/<Book> address resolves through the book index
**Purpose:** R3216/R3214/R3220 — the friendly form rewrites to the real file
via the book index, then resolves to the chunk.
**Input:** index a `John` fixture under a bible source; write its book-index
records; resolve `<source>/BIBLE/John:3.16`.
**Expected:** stage one looks up `B<source>\x00John\x003` → the real
`*.text.xhtml`; stage two lands on the chapter-3 chunk whose verse span
contains 16 — the same chunk the real path `<source>/OEBPS/.../John.text.xhtml:3.16`
resolves to.
**Refs:** crc-DB.md, R3216, R3214, R3220

## Test: the address book name is the normalized form
**Purpose:** R3215 — the address carries the spaced name, and it matches the
records the chunker wrote from the hyphenated filename token.
**Input:** index a `1-Samuel` fixture; resolve `<source>/BIBLE/1 Samuel:1.1`.
**Expected:** resolves via `B<source>\x001 Samuel\x001` to the chapter-1 chunk;
`<source>/BIBLE/1-Samuel:1.1` (the raw token) does **not** resolve.
**Refs:** crc-DB.md, crc-BibleChunker.md, R3215

## Test: a nonexistent chapter or verse resolves to nothing
**Purpose:** R3180 — no match means no chunks, with no fall-through to
the location match or the bare-path first-chunk convention.
**Input:** `<path>:9.1` (no such chapter) and `<path>:2.99` (no such
verse in that chapter).
**Expected:** empty both times. Specifically **not** the file's first
chunk, which is what a bare path would have returned.
**Refs:** crc-DB.md, R3180

## Test: a range anchor on a non-bible file is unaffected
**Purpose:** ordinary `@ext` range routings keep resolving by exact chunk
location — the bible dispatch (R3220) is reached only for bible-strategy
targets.
**Input:** a line-strategy file; resolve an anchor equal to a real
chunk's location, and a dotted anchor that would parse as a verse.
**Expected:** the location anchor resolves to its chunk as before; the
dotted anchor resolves to nothing (no location matches it) rather than
being interpreted as a verse.
**Refs:** crc-DB.md, R2377, R3179, R3220

**What this test does NOT cover — measured, 2026-07-20.** It is tempting
to read this as the guard on the strategy dispatch. It is not.
Routing a non-bible file's dotted anchor into the bible decoder leaves every
test here passing, because a non-bible file carries no `chapter` attribute, so
the attribute check reaches the same empty answer unaided.

The dispatch is therefore **defensive, not proven**. What it actually buys is
confining R3180's no-fall-through rule to bible files: without it, a
dotted anchor on *any* file would skip the exact-location match entirely.
That difference is unobservable today because no shipped chunker emits a
location containing a dot — which is exactly why no test can reach it,
and exactly why this note exists instead of a test.

A future chunker whose locations contain dots would make the dispatch
load-bearing overnight, and this is the paragraph that says so.
