# Bible Chunker

Language: Go. Environment: CLI + server (part of the `ark` binary).

A `bible` chunking strategy for scripture held as a publisher's own
**XHTML** — the format a real study bible ships in (an ESV epub is the
worked example). The files are kept on disk untouched, as the heirloom;
ark reads a paragraph's prose for the index and the publisher's own verse
identity for addressing, straight from the markup. Nothing is
pre-converted, so the richest form of the text is what stays on disk and
what a reader eventually sees.

Chosen for a scripture checkout, where a source might read:

```toml
[[source]]
dir = "~/work/bibles/esv"
strategies = { "**/*.text.xhtml" = "bible" }
ext_mirror = "mirrors"
```

## The file shape

A study-bible epub extracts to one text file per book (plus sibling files
for apparatus). Ark reads only the text files, named `*.text.xhtml`; the
`*.main.xhtml`, `*.crossrefs.xhtml`, `*.footnotes.xhtml`, and
`*.resources.xhtml` siblings are book intros and reference apparatus and
are neither indexed nor rendered.

Inside a text file the publisher has already marked every structural fact
ark needs:

- A **chapter** is a `<section epub:type="chapter">`. Its number, and the
  book and verse of every element within it, are encoded in a stable id
  and class: `id="v01001003"` and `class="h01001003"` both mean book 01,
  chapter 001, verse 003.
- A **prose paragraph** is a `<p class="normal">` (the first in a chapter
  is `no-indent`). It runs across several verses as a reader sees them:
  the paragraph that opens at verse 3 holds verses 3, 4, and 5, and the
  next paragraph opens at verse 6.
- **Poetry** is set as lines. A stanza opens with `<p class="line-group">`
  (or `line-group-after-heading` when it follows a title) and continues
  through a run of `line`, `line-indent`, and `line-space` paragraphs
  until the next stanza or prose block.
- A **verse number** is a `<span class="verse-num">`; a **chapter number**
  a `<span class="chapter-num">`; the **book label** a `<span
  class="book-name">`. All three are display apparatus, not part of the
  text.
- **Footnotes** and **cross-references** are inline `<span
  class="footnote">` / `<span class="crossref">` popup anchors, apparatus
  again.
- An **editorial heading** is a `<header><p class="heading">` — a
  pericope title the publisher supplies (`The Fall`). It is an editor's
  interpretation laid over the text, not scripture, and is dropped by
  default (see Headings below).

Because the identity is in the markup, the chunker never has to recognize
a verse from the prose. It reads the publisher's answer.

## Chunking

**One chunk per block**, where a block is a prose paragraph or a poetry
stanza:

- A `<p class="normal">` (or `no-indent`) is one chunk.
- A poetry stanza — the opening `line-group` paragraph and the run of
  `line` / `line-indent` / `line-space` paragraphs that follow it — is one
  chunk. A stanza is the reader's unit the way a paragraph is, so it holds
  together rather than fragmenting into a chunk per line.

A chunk's **text is prose only**. The verse-number, chapter-number, and
book-name spans are stripped, and so is the footnote and cross-reference
apparatus. What remains is the sentence a reader reads, which is what the
trigram index and the embedder should see and nothing else.

Each chunk records what a reference needs to find it, read from the ids
rather than from the text:

- **chapter** — the chapter number of the `<section>` the block sits in.
- **verses** — the span of verses the block covers, `first-last`
  (`"3-5"`), or a single number when the block holds one verse. The range,
  not merely the first verse: a reference to a verse past a chapter's last
  paragraph must resolve to nothing, and only the range end can tell that
  a verse falls beyond the paragraph rather than within it.

The book is deliberately *not* recorded. The file already names it and a
reference addresses a file, so storing it would duplicate an identity that
cannot disagree with itself.

### Headings

Editorial headings are dropped from the index and the rendered page by
default. They are the publisher's section titles, an interpretation
imposed on the text rather than the text itself, and indexing them would
let a search match on an editor's phrasing as if it were scripture.

A per-source option may later request that headings be shown; when shown
they are also indexed, so the page and the index never disagree about what
the corpus contains. The option is not built until a corpus wants it — the
default is drop-and-omit.

## Read-only

Bible files are a **reference corpus**, not a working document: the text
is fixed and its verse numbering is what every annotation depends on. The
strategy is therefore not writable, with two consequences that follow
automatically:

- Ark never inserts an inline `@tag:` into the file body. Annotation
  routes to the *external* disposition instead, landing in a mirror file
  — which is what `ext_mirror` exists to place inside the source, so a
  scripture checkout carries its annotations with it (see
  `curation-workshop-primitives.md`).
- The content view presents the text without an edit affordance.

## Addressing

A reference names the book, chapter, and verse — never the epub's cryptic
file layout:

```
~/work/bibles/esv/BIBLE/John:3.16
```

`~/work/bibles/esv/BIBLE/John` is a **virtual path**: no file by that name
exists on disk. The `BIBLE/` segment is what marks the reference as a book
lookup rather than a real path.

A source may hold more than scripture — notes, a README, the epub's own
directory tree — so the book names cannot simply live at the source root.
Placing them under one reserved segment, rather than reserving all
sixty-six book names at the top level, is what keeps a real file named
`Numbers` or `Acts` or `Mark` from being silently shadowed by a book of the
same name. That leaves exactly one reserved name, `BIBLE`, and a source
that declares a bible strategy **fails to activate if a real
`<source>/BIBLE` path exists**. Only bible-strategy sources pay the check.

The failure has to be loud and durable, because it is silent otherwise:
the source keeps indexing, and only its virtual addresses stop working, so
a reference that used to resolve simply returns nothing. Ark therefore
logs the collision naming the offending path and records a persistent
error condition, which `ark status` reports alongside the others. The
condition is re-derived on every config load, so fixing the collision and
reloading clears it without anyone having to dismiss it.

The real file stays addressable directly —
`~/work/bibles/esv/OEBPS/Text/b43.00.John.text.xhtml:3.16` resolves to the
same chunk. The virtual form is a convenience over it, not a replacement.

The reference resolves in two stages.

**Stage one — the book index.** Nobody should have to write
`OEBPS/Text/b43.00.John.text.xhtml`. At index time the chunker records, for
each chapter of each text file, a small book-index entry mapping the book
name and chapter to the file that holds them:

```
B <source> \0 <book> \0 <chapter>  →  file path
```

The source is part of the key so two scripture sources that both contain
`John 3` cannot collide. The book name is the epub's own filename token
with each hyphen turned back into a space — the natural form a person
writes (`Exodus`, `1 Samuel`, `Song of Solomon`, `Psalm`). The names are
exactly as the edition spells them, so `Psalm` is singular and aliases
(`Psalms`, `Ps`) are a later enhancement, not part of the first cut. The
`\0` bytes delimit the variable-length fields, and every chapter of a book
shares the prefix `B<source>\0<book>\0`. Chapters do not span files, so the
lookup is an exact key, not a range scan. Given
`~/work/bibles/esv/BIBLE/John`, the resolver recognizes `~/work/bibles/esv`
as the source, the `BIBLE/` segment as the book-lookup marker, and `John`
as the virtual book, looks up `(source, John, 3)`, and rewrites the target
to the real file.

**Stage two — the chunk.** With the real file in hand, `:3.16` resolves to
**the paragraph chunk whose chapter matches and whose verses span contains
verse 16** — the block a reader would find that verse in. See
`at-ext-storage.md` for the target syntax this rides on: the
chapter-and-verse occupies the anchor slot, where a line range would
otherwise sit.

A reference whose book, chapter, or verse does not exist resolves to
nothing, as any unresolvable target does. It is not an error, and it does
not fall back to the file's first chunk — a reference that named a specific
verse and silently annotated an unrelated paragraph would be worse than one
that annotated nothing.

### When a source stops being scripture

The book index is the only bible data ark keeps on disk, which makes it
the only part that can outlive the configuration that produced it. A source
that stops declaring the bible strategy, or that is removed from `ark.toml`
altogether, leaves its entries behind with nothing to invalidate them. Its
files are no longer indexed, so no re-index pass ever reaches them, and a
later `BIBLE/<Book>` lookup would resolve through a stale entry to a file
ark no longer knows.

So at config-resolve every book-index entry whose source is no longer a
bible source is discarded. The sweep runs in the same per-source pass that
registers the virtual namespace, because that pass is already the place
where the set of scripture sources is decided.

## Display

A bible file is rendered by **intermediating its XHTML**, not by serving
it and not by rendering markdown. The publisher's file is transformed into
ark's own controlled elements: the prose and its paragraph and stanza
structure are preserved, and

- each `verse-num` span becomes an addressable `<ark-verse n="N">`
  wrapping the number, so every verse has an identity in the page;
- an annotation whose target names that verse is shown inside its
  `<ark-verse>`; a routing that named the file bare, or matched by quoted
  text, has no verse to belong to and stays in the paragraph-level
  `<ark-ext-tags>` block where every other content kind shows its
  annotations;
- the front end draws a small gold tag icon after the verse number where a
  routing is placed, and the sidebar lists the file's tags as it does for
  any content.

**Every** verse is wrapped, not only the annotated ones. A verse is the
unit a reader refers to, so it needs to be addressable before it has
anything attached — a scroll-to-verse link, a highlight, or an "annotate
this verse" affordance each needs a target first.

That includes the verses the publisher prints no number for. At a chapter
opening the edition sets a large chapter number in place of the verse
number, so the first verse of every chapter has an identity in the markup
but no `verse-num` span. Left alone it would be the one verse in each
chapter a reader could not address, and an annotation targeting it would
resolve to the right paragraph and then have nowhere to appear. So a verse
whose markup carries no number of its own is given an empty `<ark-verse>`
anchor at the point its text begins. The page looks the same; the verse
becomes addressable like any other.

### Why intermediate rather than serve the file

The publisher's XHTML cannot be served as it stands. It carries inline
event handlers (`onclick="nav.show(...)"`), a `<script>` reference, external
stylesheet links, and footnote and cross-reference popups pointing at
sibling files — none of which survive being served by ark, and serving
stored markup directly is the raw-HTML injection ark refuses for every
indexed file. The apparatus is stripped and the scripts and handlers are
dropped; what ark emits is its own elements, so a bible page is exactly as
safe as any other content page while carrying far more structure than a
markdown source could.

Verse recognition happens over the parsed document, where a `verse-num`
span is structurally distinct from any number in the prose, so only real
verse marks become `<ark-verse>` elements and the sentence text is left
alone.
