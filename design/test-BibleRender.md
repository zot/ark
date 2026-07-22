# Test Design: Bible rendering
**Source:** crc-BibleRenderer.md, crc-Server.md

Covers R3181–R3183 — `verse-num` spans becoming addressable elements, and
verse-targeted annotations rendering at their verse instead of at the
paragraph. Two layers: the pure render (publisher XHTML in, ark-controlled
HTML out, no DB) and the content view end to end, where the routings actually
come from.

## Test: verse-num spans become verse elements
**Purpose:** R3181 — every `verse-num` is wrapped, annotated or not, the number
kept, giving each verse an identity in the page.
**Input:** a `<p class="normal">` paragraph carrying two `verse-num` spans (1, 2),
rendered by the bible renderer.
**Expected:** `<ark-verse n="1">` around the number and the same for 2, both
inside the paragraph, with the surrounding prose intact.
**Refs:** crc-BibleRenderer.md, R3181

## Test: apparatus, scripts, and handlers are stripped
**Purpose:** R3183 — ark emits its own elements, never the publisher's unsafe
markup; serving the file directly would be raw-HTML injection.
**Input:** a fragment carrying an inline `onclick`, a `crossref`/`footnote`
popup anchor, and a `<script>` reference.
**Expected:** none appear in the output — no `onclick`, no `<script>`, no
popup-link markup; the prose and the `<ark-verse>` wrappers remain.
**Refs:** crc-BibleRenderer.md, R3183

## Test: only verse-num spans are verses
**Purpose:** R3183 — the recognition is over the parsed document, so a number
sitting in the prose is not mistaken for a verse.
**Input:** a paragraph whose prose contains a bare digit alongside a real
`verse-num` span.
**Expected:** only the `verse-num` span becomes an `<ark-verse>`; the prose
digit is left as text.
**Refs:** crc-BibleRenderer.md, R3183

## Test: a chapter's first verse is addressable though it has no number
**Purpose:** R3222 — the edition prints a `chapter-num` drop cap instead of a
verse number, so verse 1 of every chapter has identity but no `verse-num` span.
Without an anchor it is the one verse per chapter nothing can address, and a
routing targeting it resolves and then has nowhere to render.
**Input:** a chapter-opening paragraph as the edition ships it — an
`hBBCCC001` span holding a `book-name` and a `chapter-num` and then prose, with
a following `hBBCCC002` span that does carry a `verse-num`.
**Expected:** an empty `<ark-verse n="1">` appears where verse 1's text begins,
and verse 2 still gets its ordinary number-wrapping element. Verse 1 is
anchored exactly once even when the enclosing `<p>` repeats its identity, and a
verse that *does* have a number gets no extra empty anchor.
**Refs:** crc-BibleRenderer.md, R3222, R3181

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
