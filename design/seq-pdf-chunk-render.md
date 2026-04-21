# Sequence: PDF Chunk Render

First `<pdf-chunk>` for a (src, page, scaleBand) triggers the
document fetch and page render; subsequent siblings reuse the
cached blob URL.

## Participants
- `<pdf-chunk>` (crc-PdfChunkElement.md) — the element being rendered
- `<ark-search>` host (crc-ArkSearchElement.md) — owns caches and cleanup
- PDF.js library — document parse and page render
- `<ark-tag>` overlay children (crc-ArkTagElement.md)

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
  |                        |  (cache in docCache)   |
  |<---PDFDocumentProxy----|                        |
  |                                                 |
  |--getPageImage(src, page, band)-->|              |
  |                        |--doc.getPage---------->|
  |                        |<--PDFPageProxy---------|
  |                        |--page.render(canvas)-->|
  |                        |<--render complete------|
  |                        |--canvas.toBlob-------->|
  |                        |  + createObjectURL     |
  |                        |  push URL to blobUrls  |
  |                        |  cache {url, w, h}     |
  |<---{url, w, h}---------|                        |
  |                                                 |
  |--build <img src=url>-->                         |
  |  position at (-chunkX, -chunkY) scaled          |
  |                                                 |
  |--for each <ark-tag rect="..."> child:           |
  |  absolutely position at transformed coords      |
  |  apply scoped styling (font-size, width, bg)    |
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
