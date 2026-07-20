# Test Design: BibleChunker
**Source:** crc-BibleChunker.md

Pure chunking — content in, chunks out. No DB, no index, no files: the
chunker is a function over bytes, so every case below runs on a string
literal. Covers R3173–R3178.

The verse-reference resolution that reads these attributes is separate
(crc-DB.md, R3179/R3180) and tested with its own fixture.

## Test: one chunk per paragraph, with line ranges
**Purpose:** R3173 — blocks are blank-line separated, in order, and a
chapter heading stays with the block it introduces rather than splitting
off.
**Input:** a two-chapter sample: a `# Book` title, a blank, a
`## Book Chapter 2` heading immediately followed by a verse paragraph,
a blank, a second verse paragraph, a blank, then `## Book Chapter 3`
with its paragraph.
**Expected:** four chunks — title; heading+first paragraph; second
paragraph; heading+its paragraph — with `Range` line spans matching the
source lines and no chunk spanning a blank line boundary.
**Refs:** crc-BibleChunker.md, R3173

## Test: chapter is carried forward and absent before the first heading
**Purpose:** R3175 — the attribute belongs to every paragraph of the
chapter, not just the one holding the heading, and is not invented for
text preceding any heading.
**Input:** the same sample.
**Expected:** the title chunk has no `chapter`; both Chapter 2 blocks
carry `chapter=2` (including the one with no heading in it); the
Chapter 3 block carries `chapter=3`.
**Refs:** crc-BibleChunker.md, R3175

## Test: verse span covers first through last mark
**Purpose:** R3176 — the span, the single-mark short form, and the
absence when a block has no marks.
**Input:** a block with marks 1 and 2; a block with only mark 3; a block
with none.
**Expected:** `verses=1-2`, `verses=3`, and no `verses` attribute
respectively.
**Refs:** crc-BibleChunker.md, R3176

## Test: only backtick-wrapped integers are verse marks
**Purpose:** R3174 — the backticks are the discriminator, so bare digits
in the prose (a year, a quantity) must not register as verses.
**Input:** a paragraph containing `` `5` `` plus bare numbers and a
backticked non-integer (`` `xii` ``).
**Expected:** `verses=5` — the bare digits and the non-integer code span
contribute nothing.
**Refs:** crc-BibleChunker.md, R3174

## Test: chapter heading is recognized at any ATX level
**Purpose:** R3175 — a file may put the book at `#` and chapters at
`##`, or nest a level deeper; the heading is identified by its text, not
its depth.
**Input:** the same chapter heading written as `##` and as `###`.
**Expected:** both yield the same `chapter` value.
**Refs:** crc-BibleChunker.md, R3175

## Test: locator byte range covers the chunk content exactly
**Purpose:** the block-level `Locator` is computed rather than inherited,
so random-access retrieval depends on it being right — an off-by-one here
returns the wrong text with no error anywhere.
**Input:** the two-chapter sample.
**Expected:** every chunk's decoded `[start,end)` slices the source back
to byte-for-byte that chunk's `Content`, trailing newline included.
**Refs:** crc-BibleChunker.md, R3173

## Test: a final line without a trailing newline still chunks
**Purpose:** the common editor artifact; the block-flush path at EOF is
separate from the flush-on-blank-line path and is easy to drop.
**Input:** a paragraph with no terminating newline.
**Expected:** one chunk whose content is that paragraph and whose byte
range ends at EOF.
**Refs:** crc-BibleChunker.md, R3173

## Test: runs of blank lines produce no empty chunks
**Purpose:** R3173 — separators are separators however many there are.
**Input:** two paragraphs divided by three blank lines, plus leading and
trailing blanks.
**Expected:** exactly two chunks, neither blank.
**Refs:** crc-BibleChunker.md, R3173

## Test: the strategy is read-only
**Purpose:** R3178 — non-writability is the whole of the read-only
behavior, so it is asserted directly; the downstream effects (no inline
tag insertion, no edit affordance) are existing machinery keyed on it.
**Input:** the chunker value.
**Expected:** `IsWritable()` is false and `CommentSyntax()` is empty.
**Refs:** crc-BibleChunker.md, R3178

## Test: attributes survive indexing
**Purpose:** the attributes are only useful if they come back out of the
index — CHAPTER.VERSE resolution (R3179) reads them via `AllChunks`, not
from the chunker. Everything above tests the chunker in isolation and
would pass even if nothing persisted.
**Input:** register the `bible` strategy on a test index, write the
two-chapter sample into the source dir, index it with that strategy, and
read the chunks back with `AllChunks`.
**Expected:** the same block count as the pure test, with `chapter` and
`verses` intact on the round-tripped chunks.
**Refs:** crc-BibleChunker.md, R3175, R3176, R3179
