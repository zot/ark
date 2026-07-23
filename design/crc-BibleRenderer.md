# BibleRenderer
**Requirements:** R3181, R3182, R3183, R3222, R3226, R3227, R3228, R3229, R3230, R3232, R3233

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
  `<ark-verse n="N">` wrapping the number (R3181); `chapter-num` →
  `<ark-chapter n="N">` (R3232); prose/poetry block classes preserved as ark's
  own classes; `footnote`/`crossref`/`book-name` and all
  scripts/handlers/external refs stripped.
- The distinction the strip list draws: **reference apparatus a reader can
  lose** (a footnote marker, a cross-reference letter, a repeated book label)
  versus **structure a reader navigates by** (the chapter number). The chunker
  strips both, since neither is prose; the renderer strips only the first.
- verseOf(el): the verse number of an element, read from its `vBBCCCVVV` id —
  the same identity the chunker reads, so the wrapped verse and the chunk's
  `verses` attribute cannot disagree.
- Which verses carry no number: the first of every chapter. The edition sets a
  `chapter-num` drop cap in its place, so the verse has identity (`hBBCCCVVV`)
  but no `verse-num` span to wrap (R3222).
- The page's appearance, taken from the edition's own stylesheet and expressed
  over ark's classes (R3226): a verse number small, bold, and raised; a
  first-line indent on prose, suppressed at a chapter opening; poetry inset
  with a hanging first row, a deeper class inset further, and a blank line
  above each stanza. The lengths are the publisher's, not invented.
- That the class map must **preserve every distinction the edition styles**
  (R3227). Two publisher classes share an ark class only where the edition
  styles them identically; an unmapped class still falls back to prose, so an
  unseen edition reads.

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
- **The verse element carries its chapter** (R3229): `<ark-verse n="N" c="C">`.
  A text file spans several chapters, so a verse number alone does not identify
  a verse within the page — Genesis 1 and 2 share a file and each has a verse
  3. Both come from the one `vBBCCCVVV` token already being read, so the
  chapter costs no extra parsing. **`n` is written first and must stay first**:
  `insertVerseExtBlocks` finds a verse by scanning for the literal
  `<ark-verse n="`, so the attribute order is load-bearing, and a test pins it.
- **The chapter number is shown** (R3232): a `chapter-num` span becomes
  `<ark-chapter n="N">`, set large where the edition sets it, at the head of the
  chapter's opening paragraph. Display only — the chunker still strips it, so
  R3211's prose-only chunk text is untouched and no search matches a chapter
  number. Where the markup supplies a book label beside it (every chapter
  opening in this edition), it rides along as an attribute rather than being
  printed: a reader in Genesis does not need telling eleven times.
- **The running head** (R3233): one page-level element naming the book and
  chapter under the reader's eye, kept current as they scroll. Not a sticky
  element per chapter — the page is a flat run of per-chunk containers, so a
  chapter owns no box a sticky child could travel the length of. It reads the
  `<ark-chapter>` elements the render emitted and shows the last one the reader
  has passed.
- **Reaching a verse** (R3230): the page reads `#C.V` or `?verse=C.V`, scrolls
  that verse into view, and marks it briefly. Two marks, since neither alone
  suffices — the number is the precise target but a chapter-opening anchor has
  no number to mark, and the enclosing block's tint is visible in that case
  while being too coarse to locate a verse on its own. This is the first of the
  three affordances R3181 wraps every verse to enable; until one existed, the
  every-verse rule and R3222's numberless anchor paid a cost nothing collected.
- **The in-verse annotation icon is inline** (R3228). The chunk-level
  `<ark-ext-tags>` indicator floats left into its chunk's margin; inherited
  unchanged inside an `<ark-verse>` it would pull the icon to the paragraph's
  left edge, away from the verse it marks. The in-verse case overrides the
  float and renders gold, distinguishing a verse annotation from the
  accent-orange chunk indicator.
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
