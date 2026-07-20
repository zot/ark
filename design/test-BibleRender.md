# Test Design: Bible rendering
**Source:** crc-BibleRenderer.md, crc-Server.md

Covers R3181–R3183 — verse marks becoming addressable elements, and
verse-targeted annotations rendering at their verse instead of at the
paragraph. Two layers: the pure render (markdown in, HTML out, no DB) and
the content view end to end, where the routings actually come from.

## Test: verse marks become verse elements
**Purpose:** R3181 — every mark is wrapped, annotated or not, and the
`<code>` is preserved so the page still reads as markdown.
**Input:** a chapter paragraph with two verse marks, rendered by the
bible renderer.
**Expected:** `<ark-verse n="1"><code>1</code></ark-verse>` and the same
for 2, both inside the paragraph, with the surrounding prose intact.
**Refs:** crc-BibleRenderer.md, R3181

## Test: only numeric marks are verses
**Purpose:** R3183 — the discrimination that keeps ordinary inline code
in a bible file untouched.
**Input:** a paragraph containing `` `3` ``, `` `xii` ``, and
`` `someFunc()` ``.
**Expected:** only `3` becomes a verse element; the other two render as
plain `<code>`.
**Refs:** crc-BibleRenderer.md, R3183

## Test: a numeric span inside a fenced code block is not a verse
**Purpose:** R3183 — the reason this is an AST pass rather than a rewrite
of rendered HTML. A pass over output could not tell this case from a
verse mark in prose.
**Input:** a fenced code block whose body is a bare number, plus a real
verse mark in a following paragraph.
**Expected:** the fenced block renders as an ordinary code block with no
verse element inside it; the paragraph's mark becomes one.
**Refs:** crc-BibleRenderer.md, R3183

## Test: ext blocks land in their own verse
**Purpose:** R3182 — placement is keyed by verse number, and a verse with
nothing mapped to it stays empty.
**Input:** rendered HTML with verses 1, 2, 3; a map supplying a block for
verse 2 only.
**Expected:** the block appears inside verse 2's element, before its
close; verses 1 and 3 are unchanged. An empty map returns the HTML
untouched.
**Refs:** crc-BibleRenderer.md, R3182

## Test: a verse-targeted routing renders at its verse
**Purpose:** R3182 end to end — the whole point of #41. An `@ext`
naming `…:2.1` must appear inside verse 1, not at the top of the
paragraph.
**Input:** index the bible fixture with the `bible` strategy; author an
`@ext: <path>:2.1 @note: …`; index and rebuild; fetch the content view.
**Expected:** the routed tag's markup appears within the
`<ark-verse n="1">` element for that chunk.
**Refs:** crc-Server.md, R3182

## Test: a routing with no verse stays at the paragraph
**Purpose:** R3182's other half — nothing is dropped for lacking a verse.
A bare-path target has no verse to belong to.
**Input:** as above, but the `@ext` names the file with no narrower, so
it resolves to the first chunk.
**Expected:** the routed tag appears in that chunk's chunk-level
`<ark-ext-tags>` block, and inside no verse element.
**Refs:** crc-Server.md, R3182
