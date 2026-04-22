# PdfChunkElement
**Requirements:** R1666, R1667, R1668, R1677, R1678, R1679, R1680, R1681, R1691, R1692, R1693, R1694, R1695, R1696, R1697, R1698, R1699, R1700, R1701, R1702, R1709, R1710, R1711, R1712, R1713, R1714, R1715, R1716, R1717, R1718, R1741, R1742, R1743, R1744, R1745, R1746, R1747, R1748, R1749, R1750, R1751, R1752, R1753, R1754, R1755, R1756, R1757, R1761, R1762, R1763, R1764, R1765, R1766, R1767, R1768, R1769

Custom element (`<pdf-chunk>`) that renders one PDF chunk's page
region as pixels. PDF.js rasterizes the page in native fidelity;
a recolor pass then replaces raster tag ink with ark's theme
colors in place, behind a blurred bg-colored halo clipped to
per-segment rects. `<ark-tag>` children are reduced to
transparent hit regions (pdf-chunk-scoped override; the
standalone `<ark-tag>` element used elsewhere is unchanged). A
PDF.js text layer rides on top for selection. Composes with
`<ark-search>` for tag drill-down via slice-and-insert. No
shadow DOM — inherits host theme CSS.

## Knows
- `src` attribute: URL returning raw PDF bytes (`/raw/PATH`)
- `page` attribute: 1-indexed PDF page
- `rect` attribute: chunk bounding box `x,y,w,h` in PDF points
  (origin bottom-left)
- `<ark-tag>` children, each with a `rect="x,y,w,h"` attribute
  in the same coordinate system (from chunker's `tag_rects`) —
  kept for fallback (R1745), slice partitioning, and diagnostics
- `<ark-tag segments="…">` on each child — chunker-supplied
  per-segment rects (`at|name|colon|val1|val2…`) in PDF points.
  Parsed into a TagDescriptor with per-run rects (R1761, R1762)
- the nearest ancestor host element (`<ark-search>` in v1) which
  owns the document, page-image, page-text-content, and theme
  caches and the blob-URL ledger
- the current render's `scaleBand` (±10% bucket)
- per-child `textRuns` — array of `{x, y, w, h, start, end}` per
  segment, in PDF points, with `start`/`end` as offsets into the
  canonical `@name: value` string (R1747)

## Does
- connectedCallback(): locate host via ancestor traversal; request
  the page-image blob URL (already recolored, R1751), the page
  text content (R1748), and the theme color sample (R1750); build
  the image element, mount the transparent `<ark-tag>` hit
  regions, and mount the PDF.js text selection layer
- renderImage(): overflow-hidden container with absolutely-positioned
  `<img>` sized to the full page, offset so the chunk rect sits at
  local `(0, 0)` (R1679)
- transformCoords(x, y, w, h): PDF points → CSS pixels, origin flip
  (R1680)
- parseSegmentsAttribute(): for each `<ark-tag segments="…">`
  child, build a TagDescriptor with one TextRun per `|`-separated
  segment rect. Run start/end offsets map the canonical match
  string: `@` at [0,1), name at [1, 1+nameLen), `:` at
  [nameLen+1, nameLen+2), value at [nameLen+2, end). Multiple
  value rects share the same start/end range (they all color as
  value per R1753). Returned TagDescriptors drive the recolor
  (R1761, R1762)
- collectSegmentDescriptors(src, page): DOM-walk all
  `<pdf-chunk src page>` on the document, gather each child's
  parsed TagDescriptor into a per-page list. Used by the host
  renderPage pass so every tag on a page is recolored in one
  blob (R1762)
- positionHitRegions(): for each `<ark-tag>` child, absolutely
  position at the segments-derived union rect in CSS pixels,
  `pointer-events: none`, no visible content (R1755); when
  segments missing and text-content matching failed, fall back
  to the chunker rect with the prior visible-overlay styling
  (R1745, R1763)
- mountTextLayer(): call PDF.js `renderTextLayer` / `TextLayer`
  over the clipped chunk region, consuming the host's cached
  `getTextContent()` (R1756); transparent spans selectable via
  browser `::selection`
- resolveHighlightRects(): read the host's cached items and
  flat-string to compute highlight bounding boxes for this
  chunk's `highlight` attribute (R1749)
- handleResize(): if new scale stays within scaleBand, CSS-only
  update; otherwise request a new page-image from the host at the
  new band (R1685) — recolor re-runs at the new band (R1751)
- handleClick(event): capture-phase handler on self; computes
  click coords in chunk-local CSS pixels and rect-tests against
  `<ark-tag>` children's positioned rects; on hit, prevents
  default and runs slice-and-insert; on miss, passes the event
  through so the PDF.js text layer can handle selection (R1755,
  R1699, R1700)
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
