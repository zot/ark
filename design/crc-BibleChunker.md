# BibleChunker
**Requirements:** R3172, R3173, R3175, R3176, R3177, R3178, R3209, R3210, R3211, R3212, R3213, R3214, R3215, R3218, R3219, R3221, R3216, R3224, R3225

Chunks scripture held as a publisher's **XHTML** (an ESV epub is the worked
example) into prose blocks, reading each block's chapter and verse identity
from the publisher's own markup rather than recognizing marks in the text.
Writes a book index so a friendly `BIBLE/<Book>:ch.v` address resolves to the
right file, and does its source-specific setup through an optional per-source
hook. Read-only: a reference corpus is never edited in place.

## Why XHTML, not markdown

#41 chunked a *markdown* bible (backtick verse marks, blank-line paragraphs).
That format had no live corpus. The first real corpus is a study-bible epub
whose extracted `*.text.xhtml` files already carry the structure ark needs —
prose paragraphs holding several verses, poetry set as lines, and a stable
`id="vBBCCCVVV"` / `class="hBBCCCVVV"` on every element (book, chapter, verse).
Reading the publisher's identity is more robust than any mark recognition, and
keeping the XHTML on disk keeps the richest form of the text. So the chunker
parses XHTML (via `golang.org/x/net/html`) rather than walking markdown blocks.

## Knows
- Prose block classes: `normal`, `no-indent` — a `<p>` of one of these opens a
  prose paragraph chunk (R3173).
- Poetry block classes: a stanza opens at `line-group` / `line-group-after-heading`
  and continues through the run of `line` / `line-indent` / `line-indent2`
  paragraphs until the next opener or a prose block (R3212). `line-space` is
  **prose** despite the name — the edition styles it as a paragraph with a gap
  above, not as a line of verse.
- Apparatus classes stripped from chunk text: `verse-num`, `chapter-num`,
  `book-name`, `footnote`, `crossref` (R3211).
- Heading marker: `<header><p class="heading">` — dropped by default (R3213).
- Id/class shape: `vBBCCCVVV` / `hBBCCCVVV` — book (2), chapter (3), verse (3),
  the source of every chunk's `chapter` and `verses` (R3210, R3175, R3176).
- Only `*.text.xhtml` is handled; the `.main`/`.crossrefs`/`.footnotes`/
  `.resources` siblings are not (R3209).
- The scripture container: `<section epub:type="chapter">`. A block outside one
  is not scripture and is not chunked (R3224). Excluding the sibling apparatus
  *files* is not enough — a text file also carries its own copy of the footnote
  and cross-reference popups and the navigation templates, appended after the
  text it annotates.

## Does
- Chunks(path, content, yield): parse the XHTML and walk its blocks in
  document order, emitting one chunk per block (R3173, R3212).
  - A prose `<p class="normal">`/`no-indent` is one chunk; a poetry stanza
    (opener + its line-run) is one chunk; a `heading` is skipped (R3213).
  - **Only inside a chapter section** (R3224). The walk tracks whether it is
    within a `<section epub:type="chapter">` and yields nothing outside one,
    which is what keeps the file's appended apparatus out of the index — 46% of
    chunks before the rule. Chosen over a list of apparatus class names, which
    would need re-deriving per edition and fails silently when incomplete;
    containment fails toward an empty book instead. Not replaceable by "has a
    verse identity" (R3225): a paragraph continuing an earlier verse, a psalm
    superscription, and an acrostic letter title all lack ids and are all text.
  - **Chunk text is prose only** — the apparatus spans are stripped, leaving
    the sentence a reader reads, which is what the trigram index and embedder
    receive (R3211). `Range`/`Locator` are computed per block for
    random-access retrieval, as blocks are finer than any upstream chunker.
  - **Attributes from the ids**, not from marks: `chapter` from the block's
    `vBBCCCVVV`/`hBBCCCVVV` chapter field (absent before the first
    verse-bearing element — R3175); `verses` as the `first-last` range the
    block's verse ids span, or the bare number for one verse (R3176). The
    range end is required so R3180 can reject a verse past a chapter's last
    block.
- stageBookIndex / FlushBookIndex(path): the book-index write, split in two
  (R3214). `Chunks` **stages** the distinct chapters its walk found; the indexer
  calls `FlushBookIndex` once the file has committed, and it writes one record
  per chapter, `B <source> \0 <book> \0 <chapter>` → `path`, through
  Store.WriteBookIndex. The split mirrors PDFChunker's blob staging and for the
  same reason: microfts2 **re-runs `Chunks` at retrieval time**, so a chunker
  that wrote during the walk would persist on every read. Only a walk that ran
  to completion stages, since a consumer that stopped early (retrieval seeking
  one range) has not seen the whole chapter set.
  - `source` is the file's enclosing source, resolved at flush; `book` is the
    epub filename token with hyphens turned to spaces (`b09.01.1-Samuel` →
    `1 Samuel`, `Psalm` singular — R3215). A book spans **several** files
    (`b43.00.John`, `b43.02.John`), which is why the index maps chapters to
    files rather than assuming one file per book.
  - Exact key, since chapters do not span files; the source leads the key so two
    scripture sources cannot collide.
- IsWritable() bool: reports **false** (R3178) — the whole of the read-only
  behavior; existing machinery refuses inline tag insertion (annotation
  degrades to the external/mirror disposition) and suppresses the edit
  affordance. Mirrors PDFChunker.
- CommentSyntax() string: reports "" — scripture prose has no comment form.
- **ActivateForSource(source, register) error** — the optional per-source hook
  (R3217, implemented here), called at config-resolve for each source that maps
  `bible` locally:
  - Registers a source-prefixed strategy entry `<source.Dir>/BIBLE/** → bible`
    into the in-memory global strategy map via the `register` handle (R3218).
    The source prefix makes a global-map entry safe (it can only match that
    source's paths); it is matched against the file's **absolute** path (the
    `/X` filesystem-absolute form, R3196). This classifies the virtual
    `BIBLE/<Book>` addresses as bible so they dispatch to the bible resolver,
    and it is re-established every startup rather than persisted — the handle
    is `Config.AddDerivedStrategy`, which is why (see crc-Config.md).
  - **Guard:** returns an error (failing the source's load) if a real
    `<source.Dir>/BIBLE` path exists on disk, so the reserved virtual namespace
    cannot collide with real content (R3219).
- **ReconcileBookIndex(active []\*Source) error** — the companion to the hook,
  run once after the whole activation pass with the sources that actually
  activated `bible` (R3221). Deletes every book-index record whose source is
  not among them. It must run even when `active` is empty, since that is
  exactly the source-removed case; a per-source hook alone could never fire for
  a source that no longer exists. The book index is the only bible data on
  disk, so it is the only thing that can outlive its configuration.

## Collaborators
- `golang.org/x/net/html`: parses the XHTML into the node tree the block walk
  and id reads operate over.
- microfts2.Chunk / Pair: the yielded shape and per-chunk attribute carrier;
  `EncodeByteRangeLocator` builds the per-block locator.
- DB.addChunker: registers the strategy under `bible` on Open (R3172); the DB
  write actor persists the book-index records (crc-DB.md, R3214).
- DB (config-resolve dispatch): calls `ActivateForSource` on each per-source-
  mapped chunker and supplies the `register` handle + collision check (crc-DB.md,
  R3217).
- DB.resolveBibleTarget: the reader — rewrites a virtual `BIBLE/<Book>` via the
  book index and turns a CHAPTER.VERSE anchor into the chunk whose `chapter`
  matches and whose `verses` span contains the verse (crc-DB.md, R3216, R3179,
  R3180, R3220).
- BibleRenderer: shows what this divides (crc-BibleRenderer.md).

## Sequences
- seq-bible-resolve.md
