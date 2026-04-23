# PDF Chunk Element

Interactive `<pdf-chunk>` web component that renders a single PDF
chunk's page region from its source file. Language: TypeScript
(component), Go (server-side emission).

## Problem

PDF chunks are indexed with page and bounding-rect metadata
(`pdfchunker.go`, `specs/pdf-chunker.md`), but search results
render them as extracted text only. The user sees a text
approximation, not the page region where the hit occurred.
Layout, figures, math, typography — all lost.

## The Primitive

`<pdf-chunk>` is a custom element of the same category as
`<ark-tag>` and `<ark-search>`: a zettelkasten-level primitive
that composes into larger views. It takes a source URL and a
rect (page + x,y,w,h) and renders pixels for that region only.
No viewer UI, no page navigation — it **is** the chunk's
visible content.

```html
<pdf-chunk src="/raw/home/deck/work/notes/paper.pdf" page="1" rect="72,650,468,54">
  <ark-tag rect="72,638,180,14"><name>closure-actor</name> <value>foundational</value></ark-tag>
  <ark-tag rect="72,624,120,14"><name>decision</name> <value>LMDB</value></ark-tag>
</pdf-chunk>
```

Attributes:

- `src` — URL that returns the raw PDF bytes. The server uses
  `/raw/PATH` (not `/content/`, which wraps the file in a template
  shell intended for human reading). PDF.js needs the raw file
  stream.
- `page` — 1-indexed page number (from chunk attr `page`).
- `rect` — chunk bounding box `x,y,w,h` in PDF points, origin
  bottom-left. Passes through unchanged from the chunker's
  `rect` attribute.

Children are standard `<ark-tag>` elements — same element used
in markdown and plain-text pages — each with an additional
`rect="x,y,w,h"` attribute giving the tag's bounding box in the
same coordinate system. See §Tag Interactivity below.

Without JavaScript or before the PDF loads, children render as
readable tag text (the native `<ark-tag>` fallback). The
`<pdf-chunk>` itself displays nothing visual until the canvas
is ready.

## Rendering

PDF.js provides `getDocument` + `getPage` + `render`. We use the
render APIs and drop the viewer UI.

### Host-Owned Caches

`<pdf-chunk>` does not own its own caches. It asks its nearest
ancestor host element for the document and page image it needs.
The host owns two caches and an explicit blob URL ledger, all
cleared when the host disconnects.

For v1 the host is `<ark-search>`. Future contexts (for example,
a PDF content-view page) must provide the same interface.

On the host element:

```
docCache:  Map<src, Promise<PDFDocumentProxy>>
pageCache: Map<src|page|scaleBand, Promise<{ url, w, h }>>
blobUrls:  string[]
```

- `getDocument(src)` — returns a cached `PDFDocumentProxy` or
  triggers `pdfjs.getDocument(src)` on miss. One fetch, one
  parse per src.
- `getPageImage(src, page, scaleBand)` — returns a cached
  `{ url, w, h }` or renders the page to canvas at that scale,
  converts to a blob URL via `canvas.toBlob()` +
  `URL.createObjectURL()`, pushes the URL to `blobUrls`, and
  stores the result.

`scaleBand` is the render scale bucketed to ±10%, so small
window resizes don't cause re-renders. Crossing a band
re-renders at the new scale; every sibling `<img>` src updates
together.

### Per-Element Render (CSS Clip Region)

Each `<pdf-chunk>` is an overflow-hidden container holding an
`<img>` sized to the full rendered page and positioned so the
chunk's rect sits at `(0, 0)`:

```css
:host {                              /* the <pdf-chunk> element */
  display: block;
  position: relative;
  width: calc(var(--chunk-w) * 1px);
  height: calc(var(--chunk-h) * 1px);
  overflow: hidden;
}
:host > img.pdf-page {
  position: absolute;
  left: calc(var(--chunk-x) * -1px);
  top: calc(var(--chunk-y) * -1px);
  width: calc(var(--page-w) * 1px);
  height: calc(var(--page-h) * 1px);
}
```

(`:host` shown for clarity; actual styling uses a scoped
`pdf-chunk { ... }` rule — no shadow DOM.)

Coordinates from PDF points to CSS pixels use the standard
origin flip: `y_css = (pageHeight_pdf - y_pdf - h_pdf) * scale`.

Resize behavior is free: update the CSS custom properties and
the browser recomposites. A rescale within the current
`scaleBand` requires no new image. Crossing a band rebuilds the
blob URL once for all chunks on the page.

### Why Images, Not Canvas Per Chunk

- **One page = one rasterization.** N chunks on a page share the
  same image. No duplicated pixels.
- **Browser-native clipping.** `overflow: hidden` and positioned
  `<img>` are heavily optimized — the browser handles
  compositing, DPI, pan, and zoom without custom canvas code.
- **Resize is free.** CSS changes recomposite; canvas-based
  cropping would require an explicit `drawImage` on every
  resize.
- **Memory behavior matches usage.** One blob URL per visited
  page, released when no sibling references it.

### Blob URL Lifecycle

`URL.createObjectURL()` allocates — the browser does not
reclaim blobs while any URL handle exists. Every created URL
must be explicitly `URL.revokeObjectURL()`'d or it leaks
memory for the life of the tab.

Cleanup is host-scoped. The host's `disconnectedCallback`:

```
for (const url of this.blobUrls) URL.revokeObjectURL(url);
for (const doc of this.docCache.values()) doc.then(d => d.destroy());
this.blobUrls = [];
this.docCache.clear();
this.pageCache.clear();
```

No ref-counting, no grace windows. The host disconnects when
its results are replaced or the panel closes; everything it
owned goes with it. Slice-and-insert doesn't churn because the
page image stays in the host's `pageCache` while both halves
of the slice read from it.

Caches and the `blobUrls` ledger are **element properties**,
not closure-captured variables — so a future session (or a
human poking in the devtools console) can inspect them
directly on the live element. Closure state goes missing
under DOM surgery and is invisible from outside; element
properties survive as long as the element does and can't be
lost.

A single `beforeunload` handler walks every `<ark-search>` in
the document and runs the same cleanup, as a safety net for
tab close and navigation.

Cross-panel page sharing is not attempted in v1. Drilling down
on a tag that was already in the parent results opens a second
panel with guaranteed duplicate pages, so the duplication case
is real — just rare enough to defer. The eventual fix is to
give each page blob an ID and look it up through a higher
shared owner (for example, a registry on `document`), keeping
lifecycle ownership clear while allowing lookup by ID across
panels.

### Error States

- `src` fetch fails → element shows fallback text plus a small
  error indicator.
- `page` out of range → fallback text only.
- `rect` missing or invalid → fallback text only (salvage chunks
  fall into this case).

## Tag Interactivity

PDFs contain `@name: value` tags in their extracted text. The
text is indexed and searchable, but when the chunk renders as
pixels, the tag text is unclickable. `<pdf-chunk>` preserves
drill-down by overlaying real `<ark-tag>` elements on the
rendered page, and slicing itself when a tag opens a search
panel.

### Tag Rects From The Chunker

The PDF chunker already walks positioned text spans during
extraction. When a span matches the tag pattern
`@[a-zA-Z][\w.-]*:` through end-of-line, the chunker records a
bounding box for that tag. These per-tag rects travel with the
chunk as a `tag_rects` attribute.

Format (one compact string per chunk, semicolon-separated):

```
tag_rects = "name=value@X:Y:W:H;name=value@X:Y:W:H;..."
```

Coordinates are PDF points, origin bottom-left — same convention
as the chunk-level `rect`. Values are URL-encoded when they
contain `;`, `=`, `@`, or `:`.

When a tag's value wraps across multiple lines in the PDF
layout, only the first line's rect is emitted (the hand we're
dealt from a rendered format). Future rich-source chunkers
(docx, google docs) can emit tighter rects from authored
structure.

### Overlay Rendering

For each entry in `tag-rects`, the element creates an
`<ark-tag>` child absolutely positioned over the rendered
canvas:

```
y_css = (pageHeight_pdf - y_pdf - h_pdf) * scale
x_css = (x_pdf - chunkX_pdf) * scale
w_css = w_pdf * scale
h_css = h_pdf * scale
```

(origin flip + translate into the chunk's local coord space + scale
to CSS pixels).

Styling inherits the normal `<ark-tag>` rules — same colors, same
`@` and `:` punctuation pseudo-elements, same click cursor. An
opaque background (default: page color, overridable by CSS
variable) covers the PDF's own rendering of the tag text. The
result is visual continuity with markdown and plain-text
rendering: tags look like tags, everywhere.

A clipped or otherwise hidden tag value is fully recoverable —
clicking opens the `<ark-search>` panel, which pre-fills the
complete tag name and value from the element's DOM regardless
of what's actually visible on screen.

### Text-Layer Tag Rendering

The chunker's `tag_rects` give one rect per tag, but PDF.js
rasterizes tag text in the PDF's own embedded fonts — not
ark's. Any HTML overlay on top of that raster, or any
canvas-drawn replacement text we paint in ark's font, leaves
a seam: browser glyph metrics, hinting, and antialiasing
never quite match the rasterizer's. Two text renderers
sharing a pixel space can't agree.

`<pdf-chunk>` closes the seam by letting PDF.js render the
text in its native fidelity, then **recoloring** those
rasterized pixels in place. The glyph shapes, widths, and
edges all come from the PDF; only the ink color changes.
The `<ark-tag>` child becomes a transparent hit region —
no visible text, no background, just a click target. PDF.js's
text layer rides on top for selection.

#### Exact Bounds From The Chunker

For a recolor to land pixels precisely into `@`, name, `:`,
and value regions, the element needs per-segment bounds at
glyph fidelity. The chunker owns per-glyph BBoxes
(`Block.Chars[].BBox`), so it emits a parallel
`tag_segments` attribute aligned with `tag_rects`:

```
tag_segments = "atRect|nameRect|colonRect|valRect1|valRect2|…;nextTag…"
```

Each rect is `x,y,w,h` in PDF points (same coords as
`tag_rects`). The first three rects — `@`, name, `:` — are
single-line. The value is a list: one rect per physical line
in the PDF, for wrapped values. Chunker line-splits by
grouping glyphs whose baseline Y differs from the running
average by more than half a glyph height.

Salvage chunks (no `rect`) also produce no `tag_segments`.
Generic tag extraction — T/F/V/D LMDB records — is unchanged;
this attribute is a presentation enrichment only.

The server emits the corresponding `segments="…"` attribute
on each `<ark-tag>` child it writes, index-aligned with the
existing `rect` attribute. The element parses `segments` into
a descriptor — name, value, per-segment rects — used to
drive the recolor.

#### Flow — Per Page

When a chunk asks the host for a page image at a given scale
band (cold cache):

1. PDF.js renders the page to an offscreen canvas at the band's
   scale (existing R1684 path).
2. The host samples the page's background color from a corner
   pixel of the rendered canvas (falls back to the theme bg
   if sampling fails).
3. The host collects all segment-based tag descriptors from
   every `<pdf-chunk>` on the page (each contributes its own
   `<ark-tag>`'s `segments` attribute).
4. The **recolor pass** walks each tag's region and replaces
   the raster glyph ink with ark's theme colors, behind a
   blurred bg-colored halo (§Recolor). Chunker-supplied
   segments are the source of truth for per-segment bounds;
   PDF.js `getTextContent()` detection remains the fallback
   for chunks that somehow arrive without segments.
5. The canvas converts to a blob URL and is cached by
   `(src, page, scaleBand)`.

Each `<pdf-chunk>` that lands on the page then uses the
(now-recolored) blob URL for its clipped `<img>`, positions
its `<ark-tag>` children as invisible hit regions, and
mounts a PDF.js text layer for selection.

#### Recolor

The recolor runs per page in two phases:

**Phase 1 — Geometry and snapshots.** For each tag, compute:

- The tag's region on the canvas: the union of all four
  segment rects plus pad — an ascender pad above (up to 30%
  of glyph height, clamped by the gap to the line above),
  a descender pad below (up to 40% of glyph height, clamped
  by the gap to the line below), and a blur pad (~3× the
  blur radius) on all sides so the background shape has room
  to extend past the glyph edges.
- Per-segment runBoxes on the canvas — one per segment rect,
  plus a clamped ascender/descender extension so each
  runBox covers its glyph including ascenders/descenders but
  never reaches a neighbor line's glyph. Each runBox carries
  its `start`/`end` offsets into the canonical match string
  (so pixel-to-color classification can read the segment
  type from a charIdx).
- A snapshot `ImageData` of the region, captured before any
  writes. Because later compositing can extend past a tag's
  own bounds (via the blur pad), all snapshots must be taken
  before any tag writes — otherwise a neighbor's partial
  write contaminates the next snapshot.

**Phase 2 — Composite.** For each tag, in bottom-up order,
a five-step solid-background pipeline:

1. **Silhouette tile** — For every snapshot pixel that's a
   text pixel (luminance distinctly darker than page bg) and
   lies inside one of this tag's runBoxes, paint black at
   `alpha = textness * 255`. This extracts the glyph shape.
2. **Expansion blur** — Draw the silhouette tile with
   `ctx.filter = blur(blurPx)`. This spreads the shape
   outward along glyph contours, producing a soft expanded
   silhouette larger than the original text.
3. **Threshold to solid background** — Read back the blurred
   silhouette and convert every pixel with `alpha > threshold`
   to the theme's bg color at full opacity (alpha = 255).
   This creates an opaque, glyph-shaped background surface
   that fully covers the PDF page color beneath the tag.
4. **Edge blur** — Apply a small blur to the thresholded
   surface so the boundary doesn't look like a hard cutout
   against the PDF page.
5. **Recolored text** — Build a text tile (same pixel walk
   as step 1, but paint each pixel in the theme color for
   its segment — punctuation, name, or value). Draw this
   sharp text tile on top of the softened background.

The combined result is drawn onto the main canvas **clipped
to a generous rect** around the runBox union. The solid
background means tag text sits on a surface with known,
constant contrast — no PDF page color mixing in. The two
blur stages (expansion + edge softening) give independent
control over background size and boundary quality.

Bottom-up ordering means top-of-page tags composite last —
any neighbor-edge pixels they overwrite are theirs to own.

#### Text-Detection Threshold

Text pixels are identified by luminance distance from the
sampled page background:

```
textness = clamp(1 - pLum / bgLum, 0, 1)
```

Pixels with `textness < 0.05` are treated as background and
skipped. In the silhouette tile (step 1), the alpha at a text
pixel is `round(textness * 255)` — so antialiased edge pixels
contribute proportional alpha to the silhouette, which the
expansion blur (step 2) then spreads into a natural shape.
After thresholding (step 3), the background surface is fully
opaque, so the original glyph-edge alpha no longer matters
for the backing — it served only to shape the expansion.

The text tile (step 5) also uses `round(textness * 255)` as
alpha, preserving the PDF's native glyph antialiasing in the
final colored text.

For pure-black-on-white PDFs this works cleanly. For colored
or textured page backgrounds, the fallback pageBg sampling
may mis-identify some text; the recolor degrades gracefully
(pixels stay as their PDF originals where classification
fails).

#### Color Sampling

A small helper mounts a hidden `<ark-tag>` element at load
time, reads its computed colors, and caches them on the host:

```js
const probe = document.createElement('ark-tag');
probe.innerHTML = '<name>x</name> <value>y</value>';
probe.style.position = 'absolute';
probe.style.visibility = 'hidden';
document.body.appendChild(probe);

const nameEl = probe.querySelector('name');
const valueEl = probe.querySelector('value');
const colors = {
  name:        getComputedStyle(nameEl).color,
  value:       getComputedStyle(valueEl).color,
  punctuation: getComputedStyle(nameEl, '::before').color,
  fontFamily:  getComputedStyle(nameEl).fontFamily, // retained for fallback path
  bg:          getComputedStyle(document.documentElement)
                 .getPropertyValue('--term-bg').trim() || '#ffffff',
};

probe.remove();
```

Cached on the host; theme-change invalidation (flush
`pageCache` on theme switch) is deferred.

The background surface color is `theme.bg` (ark's UI
background), chosen for contrast against the name and value
colors, not against the PDF's page color. Because the
threshold step (step 3) makes the surface fully opaque, the
PDF page color is completely hidden beneath the tag — the
tag reads as an ark element no matter what the PDF's page
color happens to be.

#### Fallback — No Segments

If an `<ark-tag>` child arrives without a `segments` attribute
(server skipped emission, chunker couldn't resolve per-glyph
bounds), the element falls back to a PDF.js-driven scan:

1. Call `page.getTextContent()` and build flat-string +
   offsets (cached per `(src, page)`).
2. Run the tag regex across the flat string.
3. For each match, use the contributing text items' rects
   as an approximation of per-segment bounds.

The per-segment precision is coarser than the chunker's
(PDF.js items don't always split on punctuation boundaries),
but the recolor pipeline is otherwise identical.

When `getTextContent()` also fails (encrypted page, malformed
stream, OCR-less scan), no recolor runs — the raster text
appears in its original PDF colors and `<ark-tag>` children
fall back to the chunker-rect visible-overlay treatment
(R1745).

#### Text Selection Layer

PDF.js exposes `renderTextLayer(parameters)` (or the
`TextLayer` class in newer builds). The element mounts a
text layer over each `<pdf-chunk>`'s visible region,
consuming the same cached `getTextContent()` result. Text
spans are transparent; browser selection highlights them via
`::selection` styling. Selection spans across tags because
the underlying text items are still present in the layer —
the rendered image carries recolored tag ink, but the text
content is the original PDF text.

`<ark-tag>` hit regions sit below the text layer with
`pointer-events: none`. The `<pdf-chunk>` capture-phase
click handler hit-tests click coordinates against the tag
rects (translated into chunk-local CSS pixels) to decide
whether to slice or let the click pass through to the text
layer's selection handling.

#### Scope

Everything in §Text-Layer Tag Rendering applies only to
`<ark-tag>` elements that are direct children of
`<pdf-chunk>` — `pdf-chunk > ark-tag` in CSS terms.
`<ark-tag>` is a shared element used in markdown read
views, plain-text pages, and other non-editable contexts,
where it renders as a visible, clickable tag widget with
its own `::before`/`::after` punctuation. None of the
transparent-hit-region, canvas-recoloring, or
`pointer-events: none` behavior touches those standalone
uses. The pdf-chunk context is a scoped override carried
by a `pdf-chunk > ark-tag` CSS rule and by logic inside
the `<pdf-chunk>` element itself.

#### No-JS Path

When JavaScript is disabled, the `<pdf-chunk>` element does
nothing — no canvas rendering, no recolor, no text layer.
The `<ark-tag>` children fall through to their normal
standalone rendering (HTML-rendered, styled by CSS,
clickable). The move to canvas recoloring does not change
this.

### Slice-And-Insert On Click

The `<ark-tag>` element's built-in click handler dispatches a
bubbling `ark-tag-click` event and opens an inline
`<ark-search>` panel (per `specs/ark-tag-element.md`). In
markdown, the panel inserts after the tag's parent block. In a
`<pdf-chunk>`, the parent block **is** the chunk itself — so the
chunk intercepts the click and reshapes:

1. Compute the slice Y (the clicked tag's top edge, in PDF points).
2. Replace self in the DOM with three siblings:
   - **Top `<pdf-chunk>`** — same src, page, x, width; rect height
     trimmed to just above the slice Y. Tag rects above the slice
     survive; rects at or below are stripped.
   - **`<ark-search>`** — the standard inline search panel, tag
     and value pre-filled from the clicked tag.
   - **Bottom `<pdf-chunk>`** — same src, page, x, width; rect
     starts just below the sliced tag's line. Tag rects below the
     slice survive (remapped to the new local coord space); rects
     above are stripped.
3. Closing the panel re-merges the three siblings back into one
   `<pdf-chunk>` with the original rect and full tag-rects list.

The two slices share the document cache and may share the
offscreen page render if their scales are close — same
amortization path as any sibling `<pdf-chunk>` pair.

Clicking a tag in one of the slices recurses: that slice splits
again. Only one `<ark-search>` panel per container is open at a
time — opening a new one closes the previous, per the existing
`<ark-tag>` / `<ark-search>` convention.

## Server-Side Emission

The Go server generates `<pdf-chunk>` elements in search result
previews for chunks with strategy `pdf`. The structure is
emitted directly from chunk metadata — no `wrapTagElements`-style
post-processing pass, because the tag set and their coordinates
come from the chunker, not from a text scan of a rendered HTML
preview.

### What The Renderer Needs

The per-chunk preview path (currently `RenderPreview` in
search.go, which takes only `text` and `strategy`) gains a `pdf`
case that takes the full chunk structure:

- File path (for `src="/raw/PATH"`) — already on
  `SearchResultEntry` as `Path`.
- Chunk attrs (`page`, `rect`, and the tag-rect list described
  below).

Chunk attrs need to flow through `SearchResultEntry` →
`GroupedChunk`. Today `SearchResultEntry` has `{Path, Range,
Score, Text}` — chunk attrs are absent and must be added (full
`Attrs` pair slice, or a narrow `Page`, `Rect`, `TagRects`
triple). Design phase decides the shape.

### Emission Shape

For a PDF chunk with tags, the server emits:

```html
<pdf-chunk src="/raw/home/deck/work/notes/paper.pdf" page="1" rect="72,650,468,54">
  <ark-tag rect="72,638,180,14"><name>closure-actor</name> <value>foundational</value></ark-tag>
  <ark-tag rect="72,624,120,14"><name>decision</name> <value>LMDB</value></ark-tag>
</pdf-chunk>
```

For a PDF chunk with no tags, `<pdf-chunk>` has no children.

### No-JS Fallback

Children are visible without JavaScript: the `<ark-tag>` widgets
stack as normal tag text and remain clickable, inheriting the
standard `<ark-tag>` behavior. No PDF pixels render, but the
tags are still there for drill-down. The text of surrounding
prose is not included — no-JS PDF viewing is a degraded mode,
not a primary path.

### Salvage And Missing-Rect Chunks

Chunks with strategy `pdf` but no `rect` attribute — salvage
chunks per `specs/pdf-chunker.md` §Fallback Text Salvage — fall
through to the default text preview path (`<pre>`-escaped text
with `wrapTagElements` applied). No `<pdf-chunk>` wrapper for
these; they have no coordinates to render from.

## Chunker Extension

The PDF chunker gains a per-tag rect extractor that runs during
positioned-text-span processing. For each chunk's span set, it
scans for the tag pattern
`@([a-zA-Z][\w.-]*):\s*([^\n]*)` — identical to ark's generic
tag grammar — and records a bounding box for each match.

Recorded rects go in a new chunk attribute `tag_rects`. Compact
encoding so it round-trips through microfts2 pair storage:

```
tag_rects = "name=value@x,y,w,h;name=value@x,y,w,h;..."
```

Tag `name` and `value` URL-encode any of `=`, `@`, `;`, `,` that
appear in them (ark's tag grammar already restricts `name`, so
this matters mainly for values containing commas or semicolons).
Coordinates are floats in PDF points, origin bottom-left — same
convention as chunk-level `rect`.

Tag value bounding boxes are measured across the first line of
the value only. Values that wrap in the PDF layout record only
their first-line rect; the wrapped tail is not rendered as an
overlay (see §Tag Interactivity, wrapped value behavior).

Salvage chunks produce no `tag_rects` (no coordinates exist to
record). Generic tag extraction from chunk text — T/F/V/D
records in the LMDB index — continues unchanged for all PDF
chunks including salvage; `tag_rects` is a presentation enrichment
on top of the existing tag tracking, not a replacement for it.

## Cross-Reference

`specs/pdf-chunker.md` §Chunk Attributes must be updated to
list `tag_rects` alongside `page`, `rect`, and `font_size`, with
a pointer back here for the format spec.

## Script Loading

The `<pdf-chunk>` component ships as its own bundled JS file
with PDF.js embedded. PDF.js alone is ~1 MB, too large to inline
per-page. The bundle registers the custom element on load;
pages that need it include a single `<script src>`.

Following ark's offline-first stance (see the `<ark-tag>` and
`<ark-markdown-editor>` bundles in `~/.ark/html/`), PDF.js is
bundled locally, not loaded from a CDN.

Integration points — pages that render search results or
display PDF content include `<script src="/pdf-chunk-element.js">`:

- Content page for PDF files (when and if PDF content view
  lands — deferred)
- Pages hosting `<ark-search>` results
- Frictionless search view

## What This Does NOT Cover (v2 and later)

- **Text layer overlay (selection/copy)** — invisible PDF.js
  text spans over the whole chunk for selection + copy.
  Deferred to v2. (v1.5 uses `getTextContent()` for tag and
  highlight positioning — see §Text-Layer Tag Positioning —
  but does not surface a selectable text layer for prose.)
- **Sub-hit highlighting** — query-token boxes painted at text
  coordinates. Deferred to v2.
- **Per-page render cache** — two chunks from the same page
  currently each render the full page. Deferred until profiling
  shows it matters.
- **Server-side rendering** — no `/pdf/FID/page/N.png`.
  Browser-only for v1. Revisit if no-JS rendering or file-browser
  thumbnails are needed.
- **Form fields, annotations, encryption** — beyond what
  `getDocument` handles natively.
- **OCR overlays** — the chunker yields nothing for scanned PDFs,
  so there is nothing to render.
- **Pagination viewer** — a scroll-viewer composed of full-page
  `<pdf-chunk>` elements. Later, compose from the primitive.
- **Salvage chunk rendering** — salvage chunks have no rect; they
  render as plain text and stay that way.

## Package Structure

New `pdf-chunk/` directory, sibling to `markdown-editor/` and
`ark-search/`:

- `pdf-chunk/src/pdf-chunk-element.ts` — the custom element
- `pdf-chunk/src/pdf-document-cache.ts` — shared document cache
- `pdf-chunk/src/index.ts` — exports
- `pdf-chunk/package.json` — pdfjs-dist as a bundled dependency
- `pdf-chunk/tsconfig.json` — same settings as `ark-search/`

Build output: `~/.ark/html/pdf-chunk-element.js` (installed via
the same pattern as `ark-search-element.js` and
`ark-markdown-editor.js`).
