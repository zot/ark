# BibleChunker
**Requirements:** R3172, R3173, R3174, R3175, R3176, R3177, R3178

Chunks scripture markdown by paragraph, stamping each chunk with the
chapter it belongs to and the verse marks it contains, so a
CHAPTER.VERSE reference can find its paragraph without storage carrying a
verse dimension. Read-only: a reference corpus is never edited in place.

## Why not wrap MarkdownChunker

The obvious implementation is a wrapper around `microfts2.MarkdownChunker`
that adds attributes, the way `internalTagChunker` wraps it to add
`InsertTag`. **That does not work, and the reason is not visible from the
type signatures**: `MarkdownChunker` chunks by *heading section*, so an
entire chapter — every paragraph under one `## Book Chapter N` — arrives
as a single chunk. Measured, 2026-07-20, by running it over a bible
sample.

Paragraph granularity therefore has to be produced here, which is also
why `Range` and `Locator` are computed per block rather than inherited:
the blocks are finer than anything microfts2 yields, so no upstream
locator describes them.

## Knows
- verseMarkRe: regexp — a backtick-wrapped integer (`` `12` ``), the
  only thing in the prose that counts as a verse mark (R3174)
- chapterHeadingRe: regexp — an ATX heading of any level whose text ends
  `Chapter N`; supplies the chapter number carried across the chapter's
  paragraphs (R3175)

## Does
- Chunks(path, content, yield): walks the file's blank-line-separated
  blocks in order, emitting one chunk per block (R3173). Each chunk's
  `Range` is its `startLine-endLine` label and its `Locator` is the
  byte-range encoding microfts2 uses for random-access retrieval, both
  computed for the block rather than inherited, since the blocks are
  finer than any chunker microfts2 supplies. Attributes are attached per
  block: `chapter` from the most recent chapter heading, carried forward
  and absent before the first (R3175); `verses` spanning the block's
  marks as `first-last`, the bare number for a single mark, absent when
  the block has none (R3176). Neither attribute is fabricated — a block
  with no marks is ordinary markdown and says so by omission.
- IsWritable() bool: reports **false** (R3178). This is the whole of the
  read-only behavior — existing machinery does the rest, refusing inline
  tag insertion (so annotation degrades to the external disposition and
  lands in a mirror file) and suppressing the content view's edit
  affordance. Mirrors PDFChunker, the existing non-writable chunker.
- CommentSyntax() string: reports "" — scripture prose has no comment
  form, and the pair travels with IsWritable as microfts2's
  ChunkerMetadata.

## Collaborators
- microfts2.Chunk / Pair: the yielded shape and per-chunk attribute
  carrier; `EncodeByteRangeLocator` builds the per-block locator
- DB.addChunker: registers the strategy under the name `bible` on Open,
  the same path every ark-side chunker takes (R3172)
- DB.resolveExtPathBase: the reader of these attributes — turns a
  CHAPTER.VERSE anchor into the chunk whose `chapter` matches and whose
  `verses` span contains the verse (R3179, R3180)

## Sequences
- (none — a single pass over the file, no cross-component interaction)
