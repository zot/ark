# Bible Chunker

Language: Go. Environment: CLI + server (part of the `ark` binary).

A `bible` chunking strategy for scripture texts held as markdown. The
files read as ordinary markdown — a chapter heading followed by
paragraphs — but carry a second addressing scheme inside the prose:
**verses**, marked inline. The strategy exists so an annotation can name
a verse (`mark:12.1`) while storage stays coarse and ordinary.

Chosen for a KJV checkout, where a source might read:

```toml
[[source]]
dir = "~/work/KJV"
strategies = { "books/*" = "bible" }
ext_mirror = "mirrors"
```

## The file shape

A chapter opens with an ATX heading naming the book and chapter:

```markdown
## Zechariah Chapter 2
`1` I lifted up mine eyes again, and looked. `2` Then said I, Whither goest thou?

`3` And, behold, the angel that talked with me went forth, `4` And said unto him, Run.
```

Verse numbers are backtick-wrapped so they render as small code spans in
ordinary markdown viewers, and so nothing in the text can be mistaken for
one. A verse begins at its mark and runs until the next mark; verses are
prose-sized, so several share a paragraph and one may run past a
paragraph break.

## Chunking

**One chunk per paragraph** — blank-line-separated blocks, exactly as a
reader sees them. A chapter heading is not split from the block it
introduces; a heading standing alone before a blank line is its own
chunk, which is what markdown already does.

Paragraph granularity is a deliberate middle: a chapter is too coarse to
be a useful search result or annotation target, and a verse is too fine —
verse-level chunks would multiply the index by an order of magnitude to
serve an addressing need that costs nothing to satisfy at display time.

The chunker records what a verse reference needs to find its paragraph:

- **chapter** — the number from the most recent chapter heading. Carried
  forward across the chapter's paragraphs. Absent before the first
  heading (a file preamble or title).
- **verses** — the span of verse marks in this chunk, `first-last`
  (`"1-2"`), or a single number when the chunk holds one mark. Absent
  when the chunk has no marks at all.

The book is deliberately *not* recorded: the file already names it
(`books/mark`), and a reference addresses a file. Adding it would
duplicate an identity that cannot disagree with itself today.

A chunk carrying neither attribute is ordinary markdown and behaves as
such — bible files may hold front matter, notes, or a title without
special treatment.

## Read-only

Bible files are a **reference corpus**, not a working document: the text
is fixed and its verse numbering is what every annotation depends on. The
strategy is therefore not writable, with two consequences that follow
automatically:

- Ark never inserts an inline `@tag:` into the file body. Annotation
  routes to the *external* disposition instead, landing in a mirror file
  — which is what `ext_mirror` exists to place inside the source, so a
  KJV checkout carries its annotations with it (see
  `curation-workshop-primitives.md`).
- The content view presents the text without an edit affordance.

## Addressing

A reference names chapter and verse after the file:

```
~/work/KJV/books/mark:12.1
```

This resolves to **the paragraph chunk containing verse 12:1** — chapter
12, verse 1. Storage has no verse dimension; the reference is refined to
the verse only when it is displayed. See `at-ext-storage.md` for the
target syntax this rides on: the verse occupies the anchor slot, where a
line range would otherwise sit.

A reference whose chapter or verse does not exist resolves to nothing, as
any unresolvable target does — it is not an error, and it does not fall
back to the file's first chunk.

## Display

A bible file renders as ordinary markdown, with one addition: **each
verse mark becomes an addressable element**, and any annotation
targeting that verse is shown at it rather than at the top of the
paragraph.

```html
<ark-verse n="3"><code>3</code><ark-ext-tags>…</ark-ext-tags></ark-verse>
```

The `<code>` is what the mark already rendered as, kept so the page looks
like the markdown it is. The wrapper is what gives the verse an identity
in the page.

**Every** verse mark is wrapped, not only the annotated ones. A verse is
the unit a reader refers to, so it needs to be addressable before it has
anything attached — that is what a scroll-to-verse link, a highlight, or
an "annotate this verse" affordance needs a target for. An element per
verse on a page that already has one per paragraph is not a cost worth
optimizing away.

**Routings that name a verse are placed at it; the rest stay at the
paragraph.** A chunk's annotations are partitioned by whether their
target actually names a verse. A `books/mark:12.1` routing renders inside
its verse; a routing that named the file bare, or matched by quoted text,
has no verse to belong to and stays in the paragraph-level block where
every other chunk shows its annotations. Nothing is dropped for lacking a
verse, and nothing is invented for having one.

### Why a separate renderer

The verse pass is a **second markdown renderer configured for bible
files**, not a post-processing step over rendered HTML, and not a
loosening of the shared one.

The shared content renderer deliberately does not enable raw HTML, so
markup cannot be smuggled in through any indexed file — bible files are
third-party text like any other, and that protection is not worth
trading for a verse indicator. Two alternatives were measured and
rejected:

- **Embedding the HTML in the markdown before rendering** does not
  survive: raw HTML is stripped, inline and block alike.
- **Substituting a sentinel into rendered HTML afterwards** would work
  but cannot tell prose from code — a numeric span inside a fenced code
  block would be rewritten as a verse.

The renderer instead recognizes verse marks in the parsed document,
where a code span inside a code block is structurally distinct from one
in a paragraph, and emits the wrapper as a first-class element. Only
numeric marks are recognized, so ordinary inline code in a bible file is
untouched.
