# BibleRenderer
**Requirements:** R3181, R3182, R3183, R3222

Renders a bible file by **intermediating its XHTML** — transforming the
publisher's markup into ark's own controlled elements so each verse becomes an
addressable element and a verse-targeted annotation shows at its verse. A
display concern only: it reads the file's XHTML and the chunk's routings, and
writes no state.

Separate from crc-BibleChunker.md on purpose: that card owns how a bible file is
*divided* for the index, this one owns how it is *shown*. They share the verse
notion (both read the publisher's `vBBCCCVVV` ids) and nothing else.

## Why transform, not serve

The publisher's XHTML cannot be served as it stands. It carries inline event
handlers (`onclick="nav.show(...)"`), a `<script>` reference, external
stylesheet links, and footnote/crossref popups pointing at sibling files —
none of which survive being served by ark, and serving stored markup directly
is the raw-HTML injection ark refuses for every indexed file (R3183). So the
renderer parses the XHTML and emits *its own* elements: the prose and its
paragraph and stanza structure are preserved, the apparatus and scripts and
handlers are dropped, and the page a browser receives is exactly as safe as any
other content page. Recognition is over the parsed document, where a
`verse-num` span is structurally distinct from any number in the prose, so only
real verse marks become `<ark-verse>` and sentence text is untouched (R3183).

## Knows
- The class map from publisher markup to controlled output: `verse-num` →
  `<ark-verse n="N">` wrapping the number (R3181); prose/poetry block classes
  preserved as ark's own classes; `footnote`/`crossref`/`book-name`/`chapter-num`
  and all scripts/handlers/external refs stripped.
- verseOf(el): the verse number of an element, read from its `vBBCCCVVV` id —
  the same identity the chunker reads, so the wrapped verse and the chunk's
  `verses` attribute cannot disagree.
- Which verses carry no number: the first of every chapter. The edition sets a
  `chapter-num` drop cap in its place, so the verse has identity (`hBBCCCVVV`)
  but no `verse-num` span to wrap (R3222).

## Does
- render(fileXHTML, byVerse): parse the publisher's XHTML, walk it, and emit
  ark-controlled HTML. Every `verse-num` span becomes an `<ark-verse n="N">`
  wrapping the number (R3181) — **every** verse, not only annotated ones, since
  a verse is the unit a reader refers to and needs an addressable target before
  anything is attached. The front end draws a small gold tag icon after a verse
  number where a routing is placed.
  - **Numberless verses (R3222):** an element whose identity opens a verse but
    whose subtree holds no `verse-num` span gets an **empty** `<ark-verse n="N">`
    emitted where its text begins — the chapter-opening case. The lookahead is
    what keeps it from double-anchoring a verse that does have a number, and an
    `anchored` high-water mark keeps a verse from being anchored twice when both
    the `<p>` and its inner span carry the same identity. The page is unchanged;
    the verse becomes addressable.
- insertVerseExtBlocks(byVerse): places each verse's `<ark-ext-tags>` block
  inside its `<ark-verse>` element, keyed by verse number; verses absent from
  the map stay empty (R3182). A routing whose target names a verse renders
  inside that verse; a routing that named the file bare, or matched by
  quoted-text/regex, has no verse to belong to and stays in the chunk-level
  `<ark-ext-tags>` block where every other content kind shows its annotations.
  Nothing is dropped for lacking a verse, nothing invented for having one.

## Collaborators
- `golang.org/x/net/html`: parses the publisher's XHTML into the node tree the
  transform walks — the pipeline this renderer runs, replacing #41's goldmark
  markdown extension.
- Server.markdownChunk / the content-view dispatch: selects this renderer for
  bible files and supplies the verse→routing map (crc-Server.md, R3181, R3182).
- Server.partitionVerseRoutings: splits a chunk's routings by whether their
  target anchor names a verse (R3182).
- DB.resolveBibleTarget / parseChapterVerse: decides whether a routing's anchor
  names a verse (crc-DB.md).

## Sequences
- (none — a render pass, no cross-component interaction)
