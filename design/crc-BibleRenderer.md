# BibleRenderer
**Requirements:** R3181, R3182, R3183

Renders bible markdown so each verse mark becomes an addressable element
and a verse-targeted annotation shows at its verse. A display concern
only — it reads chunk content and routings, and writes no state.

Separate from crc-BibleChunker.md on purpose: that card owns how a bible
file is *divided*, this one owns how it is *shown*. They share the verse
notion and nothing else.

## Knows
- kindArkVerse: ast.NodeKind — the custom node kind. Being unknown to
  goldmark is the point: the unsafe gate that strips raw HTML guards
  goldmark's own RawHTML/HTMLBlock nodes, so a custom kind with its own
  renderer emits markup while raw HTML stays disabled for every indexed
  file (R3183)

## Does
- bibleVerseTransformer.Transform(doc, reader, ctx): replaces every
  numeric code span with an arkVerseNode wrapping the same `<code>` the
  mark already rendered as (R3181). Operates on the parsed document, so
  a numeric span inside a fenced block is structurally distinct from a
  verse mark in prose — a distinction no pass over rendered HTML could
  make (R3183). Collects the spans before mutating; replacing during the
  walk would disturb traversal. Carries no routing state.
- arkVerseRenderer.RegisterFuncs(reg): registers the node renderer that
  writes a verse node's prepared HTML to the output buffer.
- codeSpanText(cs, src): a code span's literal text, assembled from its
  child text segments rather than the deprecated Node.Text.
- insertVerseExtBlocks(html, byVerse): places each verse's
  `<ark-ext-tags>` block inside its `<ark-verse>` element, keyed by verse
  number; verses absent from the map stay empty (R3182). Runs **after**
  wrapTagElements rather than being emitted by the transformer, because
  ext markup must never pass through that function — it re-wraps any
  `@word:` pattern and does not skip `<ark-tag>` interiors (gap C2), so a
  routed tag *value* containing an `@word:` (ordinary for ark's compound
  tags) would be nested into a `<value>`. Every other content kind keeps
  this invariant by writing the ext block outside wrapTagElements in
  chunkDiv. The scan is structural — it locates an element this renderer
  emitted, by an attribute it controls, and verses do not nest.

## Collaborators
- Server.markdownChunk: the caller — selects this renderer for bible
  files and supplies the verse→block map (crc-Server.md, R3181, R3182)
- Server.partitionVerseRoutings: splits a chunk's routings by whether
  their target anchor names a verse (R3182)
- BibleChunker: supplies the verse marks this recognizes, and
  parseChapterVerse, which decides whether a routing names a verse
- goldmark: the markdown pipeline this extends via an AST transformer
  and a node renderer, the same extension point contentLinkRewriter uses

## Sequences
- (none — a render pass, no cross-component interaction)
