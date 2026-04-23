# Sequence: PDF Chunk Render

First `<pdf-chunk>` for a (src, page, scaleBand) triggers the
document fetch, page render, segment collection, and canvas
recolor pass. The resulting blob URL carries raster PDF glyphs
with their ink recolored to theme values; subsequent siblings
reuse it.

## Participants
- `<pdf-chunk>` (crc-PdfChunkElement.md) — the element being rendered
- `<ark-search>` host (crc-ArkSearchElement.md) — owns caches, theme sample, recolor pass
- PDF.js library — document parse, page render, text layer
- `<ark-tag segments>` hit-region children (transparent; pdf-chunk scope only)

## Flow — Initial Render (Cold)

```
<pdf-chunk>            <ark-search> host          PDF.js
  |                        |                        |
  |--connectedCallback---->|                        |
  |  ancestor lookup       |                        |
  |                        |                        |
  |--getDocument(src)----->|                        |
  |                        |--pdfjs.getDocument---->|
  |                        |<--PDFDocumentProxy-----|
  |                        |  cache in docCache     |
  |<---PDFDocumentProxy----|                        |
  |                                                 |
  |--getThemeColors()---->|                         |
  |                        |  mount hidden <ark-tag>|
  |                        |  probe; read computed  |
  |                        |  styles (name/value/   |
  |                        |  ::before/::after/--term-bg)
  |                        |  remove probe; cache   |
  |<---{name, value, punctuation, fontFamily, bg}---|
  |                                                 |
  |--getPageImage(src, page, band)-->|              |
  |                        |--doc.getPage---------->|
  |                        |<--PDFPageProxy---------|
  |                        |--page.render(canvas)-->|
  |                        |<--render complete------|
  |                        |                        |
  |                        |--samplePageBg(ctx, canvas)
  |                        |  sample corner pixels, |
  |                        |  take most common color|
  |                        |  fallback to theme.bg  |
  |                        |                        |
  |                        |--collectSegmentDescriptors(src, page)
  |                        |  DOM-walk <pdf-chunk[src=page]>
  |                        |  parse each <ark-tag segments>
  |                        |  build TagDescriptors with per-segment
  |                        |  rects; fall back to getTextContent
  |                        |  scan if no segments present
  |                        |                        |
  |                        |--recolorTagsInCanvas:  |
  |                        |  Phase 1 per tag:      |
  |                        |    compute region      |
  |                        |    (runBox-union + asc/|
  |                        |     desc pad, clamped  |
  |                        |     by neighbor gap,   |
  |                        |     + blur pad)        |
  |                        |    snapshot ImageData  |
  |                        |    compute runBoxes    |
  |                        |  Phase 2 per tag       |
  |                        |    (bottom-up order):  |
  |                        |    1. silhouette tile  |
  |                        |       (black text      |
  |                        |        shape, α=       |
  |                        |        textness)       |
  |                        |    2. blur silhouette  |
  |                        |       (expand shape)   |
  |                        |    3. threshold→solid  |
  |                        |       bg (α>T → opaque |
  |                        |       theme.bg)        |
  |                        |    4. small edge blur  |
  |                        |    5. text tile on top |
  |                        |       (target color    |
  |                        |        per segment)    |
  |                        |    ctx.save, clip,     |
  |                        |    drawImage(combined) |
  |                        |    ctx.restore         |
  |                        |                        |
  |                        |--canvas.toBlob-------->|
  |                        |  + createObjectURL     |
  |                        |  push URL to blobUrls  |
  |                        |  cache {url, w, h}     |
  |<---{url, w, h}---------|                        |
  |                                                 |
  |--getPageTextContent(src, page)-->|              |
  |                        |--page.getTextContent-->|
  |                        |<--text items-----------|
  |                        |  build items + joined  |
  |                        |  + offsets; cache      |
  |<---pageTextContent------|                        |
  |  (used for text layer +                         |
  |   highlight rects only                          |
  |   when segments drive                           |
  |   recolor; fallback scan                        |
  |   if no segments)                               |
  |                                                 |
  |--build <img src=url>-->                         |
  |  position at (-chunkX, -chunkY) scaled          |
  |                                                 |
  |--positionHitRegions: for each <ark-tag> child,  |
  |  parse segments="..." into TagDescriptor;       |
  |  position at segments-union rect,               |
  |  pointer-events:none, no visible content;       |
  |  fallback to chunker rect (R1745) if no         |
  |  segments and no text-content match             |
  |                                                 |
  |--mountTextLayer (PDF.js renderTextLayer)        |
  |  consuming cached getTextContent result         |
  |  positioned over the clipped chunk region       |
  |                                                 |
  |--resolveHighlightRects (reads cached items)     |
  |  position highlight overlay divs                |
```

## Flow — Sibling On Same Page (Warm)

```
<pdf-chunk>            <ark-search> host
  |                        |
  |--getPageTextContent(src, page)-->
  |                        |  cache HIT
  |<---pageTextContent-----|
  |                        |
  |--getPageImage(src, page, band)-->
  |                        |  cache HIT (baked already)
  |<---{url, w, h}---------|
  |  (no PDF.js calls — one scan + one bake
  |   per (src, page, band); every sibling shares)
```

## Flow — Click Dispatch

```
  user click on <pdf-chunk>
  |
  |--capture-phase handler on <pdf-chunk>
  |  compute click (cx, cy) in chunk-local CSS px
  |  for each <ark-tag rect-positioned> child:
  |    if (cx, cy) inside child's positioned rect:
  |      preventDefault, stopPropagation,
  |      call sliceAtTag (existing R1699-R1702 path)
  |      return
  |  miss: let event propagate → PDF.js text layer
  |        handles text selection normally
```

## Flow — Sibling On Same Page (Warm)

```
<pdf-chunk>            <ark-search> host
  |                        |
  |--connectedCallback---->|
  |                        |
  |--getPageImage(src, page, band)-->
  |                        |  cache HIT
  |<---{url, w, h}---------|
  |                                                 
  |--build <img src=url>-->
  |  (same URL as sibling — browser reuses image)
```

## Flow — Resize Within Band

```
<pdf-chunk>
  |
  |--resize observed, new scale within ±10% of band--
  |--update CSS vars: --chunk-w, --chunk-h, --page-w, --page-h, 
  |  --chunk-x, --chunk-y
  |  (browser recomposites; no new image)
```

## Flow — Resize Crossing Band

```
<pdf-chunk>            <ark-search> host          PDF.js
  |                        |                        |
  |--resize crosses band boundary                   |
  |--getPageImage(src, page, newBand)->             |
  |                        |--render at newBand---->|
  |                        |<--new blob URL---------|
  |                        |  push URL to blobUrls  |
  |<---{url, w, h}---------|                        |
  |--update <img src>------                         |
  |  every sibling in newBand reuses the same URL   |
```

## Notes
- Document cache and page-image cache both live as element
  properties on the host (`docCache`, `pageCache`, `blobUrls`) —
  no closure-captured state.
- `blobUrls` accumulates every URL created; cleanup walks this
  array on host disconnect (see seq-pdf-slice.md and
  crc-ArkSearchElement.md).
- Error paths (fetch fail, page out of range, rect invalid) skip
  image build and show `<ark-tag>` children as plain fallback.
