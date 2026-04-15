# Sequence: PDF Chunk Extraction

**Requirements:** R1624-R1643

Triggered by reconcile when a PDF file is new or stale. The FileChunker
interface means PDFChunker owns the file read and hash comparison.

```
microfts2                PDFChunker              seehuhn/pdf
   |                         |                       |
   |--FileChunks(path,hash)->|                       |
   |                         |--open PDF file------->|
   |                         |<--document, pages-----|
   |                         |                       |
   |                         |  [for each page]      |
   |                         |--ParsePage(page)----->|
   |                         |<--text callbacks------|
   |                         |  (glyph, font, pos)   |
   |                         |                       |
   |                         |--mergeSpans---------->|
   |                         |  (same-line merge)    |
   |                         |                       |
   |                         |--detectTables-------->|
   |                         |  (drawn rules first,  |
   |                         |   then col alignment) |
   |                         |                       |
   |                         |--detectHeadings------>|
   |                         |  (font size > 1.2×    |
   |                         |   dominant)           |
   |                         |                       |
   |                         |--groupParagraphs----->|
   |                         |  (gap > 1.5× spacing) |
   |                         |                       |
   |                         |--buildChunks--------->|
   |                         |  Range: "3/table/1"   |
   |                         |  Attrs: page, rect,   |
   |                         |         font_size     |
   |                         |                       |
   |<----yield(chunk)--------|                       |
   |                         |                       |
   |  [repeat for all pages] |                       |
   |                         |                       |
   |<----(hash, nil)---------|                       |
```

## Structure Detection Order (per page)

1. **Tables** — drawn rules (path ops in content stream), then column
   alignment. Table regions are removed from the line pool.
2. **Headings** — font size ≥ 1.2× dominant. Heading + following body
   text form one chunk. Heading lines removed from pool.
3. **Paragraphs** — remaining lines grouped by vertical gap.
4. **Page fallback** — if < 2 spans remain after detection, emit
   entire page as one chunk.

## Chunk Emission Order

Within a page, chunks are emitted top-to-bottom by their rect's Y
coordinate (highest on page first). This gives natural reading order
in search results.
