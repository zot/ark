# Test Design: BibleChunker
**Source:** crc-BibleChunker.md

Pure chunking where it can be — content in, chunks out over an XHTML string
literal (a trimmed ESV fragment), so most cases need no DB or files. The
book-index write and the per-source hook touch the DB and a source dir and are
tested with a small fixture. Covers R3173, R3175, R3176, R3178, R3209–R3215,
R3218, R3219, R3224, R3225.

Verse-reference resolution that reads these attributes is separate
(test-VerseResolution.md, crc-DB.md).

## Test: one chunk per prose paragraph, verses flow within it
**Purpose:** R3173 — a `<p class="normal">` is one chunk and holds the several
verses that flow through it, exactly as the publisher set them.
**Input:** the Genesis 1 fragment — the `<p class="normal" id="v01001003">`
paragraph carrying verse-num spans 3, 4, 5.
**Expected:** one chunk for that paragraph; its `verses` is `3-5`; a following
`<p class="normal" id="v01001006">` is a separate chunk.
**Refs:** crc-BibleChunker.md, R3173, R3176

## Test: a poetry stanza is one chunk
**Purpose:** R3212 — a stanza opens at `line-group`/`line-group-after-heading`
and absorbs the following `line`/`line-indent`/`line-indent2` run, rather than
one chunk per line.
**Input:** the Psalm 1 fragment — `line-group-after-heading` followed by a run
of `line-indent`/`line` paragraphs, then a second `line-group`.
**Expected:** one chunk for the first stanza (all its lines joined), a second
chunk beginning at the next `line-group`.
**Refs:** crc-BibleChunker.md, R3212

## Test: chunk text is prose only — apparatus stripped
**Purpose:** R3211 — the sentence a reader reads, with no numbers or apparatus,
is what the index sees.
**Input:** a paragraph containing `verse-num`, `chapter-num`, `book-name`,
`footnote`, and `crossref` spans around the prose.
**Expected:** the chunk `Content` is the prose alone — none of the five span
kinds' text appears, no stray digits from the numbers.
**Refs:** crc-BibleChunker.md, R3211

## Test: chapter and verses read from the ids
**Purpose:** R3175/R3176/R3210 — identity comes from the `vBBCCCVVV`/`hBBCCCVVV`
ids, not from recognizing a mark in the text.
**Input:** blocks under `id="v01003…"` and `id="v01004…"`; a preamble block with
no verse-bearing id.
**Expected:** the chapter-3 blocks carry `chapter=3`, the chapter-4 block
`chapter=4`; the preamble carries no `chapter` and no `verses`; a block spanning
verse ids 1 and 2 carries `verses=1-2`, a single-verse block the bare number.
**Refs:** crc-BibleChunker.md, R3175, R3176, R3210

## Test: verses is a range, so its end is present
**Purpose:** R3176 — the span end is stored, not just the first verse, because
R3180 rejects a verse past a chapter's last block by that end.
**Input:** a chapter whose last block spans verses 30–31.
**Expected:** that block's `verses` is `30-31`; verse 35 (nonexistent) is not
within it.
**Refs:** crc-BibleChunker.md, R3176

## Test: editorial headings are dropped
**Purpose:** R3213 — a `<header><p class="heading">` pericope title is neither a
chunk nor part of the following chunk's text (default behavior).
**Input:** a fragment with a `heading` between two prose paragraphs.
**Expected:** two chunks (the two paragraphs); the heading's text appears in
neither.
**Refs:** crc-BibleChunker.md, R3213

## Test: only blocks inside a chapter section are chunked
**Purpose:** a text file carries its own copy of the apparatus, so excluding the
sibling files (R3209) does not exclude it. This is the rule that does.
**Input:** a fragment holding one scripture block inside
`<section epub:type="chapter">`, followed by the footnote and cross-reference
asides in a `<section class="hidden">` and the navigation templates in a
`<div class="hide">` — the shape the ESV actually appends.
**Expected:** one chunk, the scripture block; no chunk contains footnote,
cross-reference, or navigation text.
**Refs:** crc-BibleChunker.md, R3224

## Test: a chapter section reopens
**Purpose:** the apparatus sits *between* chapter sections as well as after
them, so leaving one section must not end the walk's willingness to chunk —
the failure this guards is a file yielding only its first chapter.
**Input:** the apparatus fragment above with a second
`<section epub:type="chapter">` appended.
**Expected:** two chunks, the second being the later scripture block.
**Refs:** crc-BibleChunker.md, R3224

## Test: epub:type is matched by token
**Purpose:** `epub:type` may carry several tokens (`bodymatter chapter`), so
matching the whole attribute value would silently drop such an edition's text.
**Input:** a chapter section declared `epub:type="bodymatter chapter"`.
**Expected:** its block is chunked.
**Refs:** crc-BibleChunker.md, R3224

## Test: verse identity is not the scripture test
**Purpose:** guards the tempting simplification. Dropping identity-less blocks
would have discarded 157 genuine blocks on the ESV corpus while catching
nothing containment does not already catch, so the rule must not be rewritten
that way.
**Input:** a chapter section holding two identity-less blocks — a paragraph
continuing a verse that opened earlier (Exodus 34:26b in shape) and a psalm
superscription.
**Expected:** both are chunked, and both carry no `chapter` attribute — absence
of identity is not absence of text.
**Refs:** crc-BibleChunker.md, R3225

## Test: only *.text.xhtml is handled
**Purpose:** R3209 — the sibling apparatus files are not the chunker's input.
**Input:** the strategy dispatch for `*.crossrefs.xhtml` / `*.footnotes.xhtml` /
`*.main.xhtml` under a bible source.
**Expected:** they are not classified `bible` by the `**/*.text.xhtml` rule, so
the bible chunker never receives them.
**Refs:** crc-BibleChunker.md, R3209

## Test: the strategy is read-only
**Purpose:** R3178 — non-writability is the whole of the read-only behavior.
**Input:** the chunker value.
**Expected:** `IsWritable()` is false and `CommentSyntax()` is empty.
**Refs:** crc-BibleChunker.md, R3178

## Test: book-index records are written, one per chapter
**Purpose:** R3214/R3215 — the chunker persists `B<source>\0<book>\0<chapter> →
path` for each chapter, with the book name normalized.
**Input:** index a fixture `1-Samuel.text.xhtml` covering chapters 1–2 under a
bible source.
**Expected:** two records, keyed `B<source>\x001 Samuel\x001` and `…\x002` (the
hyphen turned to a space), each valued the file path.
**Refs:** crc-BibleChunker.md, R3214, R3215

## Test: ActivateForSource registers the entry and runs the guard
**Purpose:** R3218/R3219 — the per-source hook registers the source-prefixed
virtual-namespace entry and fails the load on a real `BIBLE/` collision.
**Input:** a bible source with no real `BIBLE/`, then one with a real `BIBLE/`
directory. Call `ActivateForSource` with a fake `register` handle.
**Expected:** first case — `<source>/BIBLE/** → bible` is registered (absolute
form), no error; second case — an error is returned naming the collision, and
no entry is registered.
**Refs:** crc-BibleChunker.md, R3218, R3219

## Test: a colliding source is announced durably, and the announcement clears
**Purpose:** R3219 — a hook failure is otherwise silent, since the source keeps
indexing and only its virtual addresses stop resolving. The E record is what
makes it survivable, and re-deriving it per config load is what stops a fixed
problem from being reported forever.
**Input:** a bible source with a real `BIBLE/` directory, resolved through the
per-source pass; then the same pass after the collision is removed.
**Expected:** first pass — a `source_activation` E record exists naming the
source, and the source is still in the config (dropping it would corrupt the
config diff); second pass — the record is gone, with no dismissal step.
**Refs:** crc-DB.md, crc-BibleChunker.md, R3219

## Test: a source that stops being scripture loses its book-index records
**Purpose:** R3221 — the book index is the only bible data on disk, so it is
the only thing that can outlive its configuration; the config-resolve sweep is
what keeps a removed source from leaving a stale lookup behind.
**Input:** write book-index records for two sources, then run the reconcile
with only one of them active; then run it again with none active.
**Expected:** first run — the active source's records survive, the other
source's are gone; second run — none remain. The empty-list case is the one
that matters, since it is the source-removed case a per-source hook can never
reach.
**Refs:** crc-BibleChunker.md, crc-Store.md, R3221

## Test: attributes survive indexing
**Purpose:** the attributes are only useful if they come back out of the index —
resolution (R3179) reads them via `AllChunks`, not from the chunker.
**Input:** index the ESV fragment with the `bible` strategy and read the chunks
back with `AllChunks`.
**Expected:** the same block count as the pure test, `chapter` and `verses`
intact on the round-tripped chunks.
**Refs:** crc-BibleChunker.md, R3175, R3176
