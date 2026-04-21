# PdfChunkElement
**Requirements:** R1666, R1667, R1668, R1677, R1678, R1679, R1680, R1681, R1691, R1692, R1693, R1694, R1695, R1696, R1697, R1698, R1699, R1700, R1701, R1702, R1709, R1710, R1711, R1712, R1713, R1714, R1715, R1716, R1717, R1718

Custom element (`<pdf-chunk>`) that renders one PDF chunk's page
region as pixels, with interactive `<ark-tag>` overlays for any
tags found in the chunk. Composes with `<ark-search>` for tag
drill-down via slice-and-insert. No shadow DOM — inherits host
theme CSS.

## Knows
- `src` attribute: URL returning raw PDF bytes (`/raw/PATH`)
- `page` attribute: 1-indexed PDF page
- `rect` attribute: chunk bounding box `x,y,w,h` in PDF points
  (origin bottom-left)
- `<ark-tag>` children, each with a `rect="x,y,w,h"` attribute
  in the same coordinate system (from chunker's `tag_rects`)
- the nearest ancestor host element (`<ark-search>` in v1) which
  owns the document, page-image, and blob-URL caches
- the current render's `scaleBand` (±10% bucket)

## Does
- connectedCallback(): locate host via ancestor traversal; request
  the `PDFDocumentProxy` and page-image blob URL; build the image
  element and position `<ark-tag>` overlays
- renderImage(): overflow-hidden container with absolutely-positioned
  `<img>` sized to the full page, offset so the chunk rect sits at
  local `(0, 0)` (R1679)
- transformCoords(x, y, w, h): PDF points → CSS pixels, origin flip
  (R1680)
- positionOverlays(): for each `<ark-tag rect="…">` child, absolutely
  position at transformed CSS coordinates; apply scoped styling
  rules (font-size from rect height, width from rect width,
  opaque background, overflow hidden) (R1694–R1697)
- handleResize(): if new scale stays within scaleBand, CSS-only
  update; otherwise request a new page-image from the host at the
  new band (R1685)
- handleTagClick(event): intercept bubbling `ark-tag-click` from
  an overlay child; replace self with three DOM siblings — top
  slice `<pdf-chunk>` (rect above slice Y, filtered tag rects),
  inline `<ark-search>` (pre-filled with tag/value), bottom slice
  `<pdf-chunk>` (rect below slice Y, remapped tag rects)
  (R1699, R1700)
- mergeOnClose(): when the inline `<ark-search>` closes, re-merge
  the three siblings into a single `<pdf-chunk>` with the original
  rect and full tag-rect child list (R1701)
- renderErrorFallback(state): when src fetch fails, page is out
  of range, or rect is invalid, show fallback children (R1691,
  R1692, R1693)

## Collaborators
- PDF.js (`pdfjs-dist`): document parse and page render APIs;
  no viewer UI
- ArkSearchElement: host that owns the document cache, page-image
  cache, and blob-URL ledger; provides `getDocument(src)` and
  `getPageImage(src, page, scaleBand)` (R1681)
- ArkTagElement: child overlays; dispatches `ark-tag-click` on
  click, which bubbles up to `<pdf-chunk>` for slice-and-insert
- ArkSearchElement (again): instantiated inline between slices
  on tag click, same element used everywhere `<ark-tag>` drill-down
  appears

## Sequences
- seq-pdf-chunk-render.md
- seq-pdf-slice.md
