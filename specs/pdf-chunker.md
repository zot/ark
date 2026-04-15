# PDF Chunker

Language: Go. Environment: CLI (part of the `ark` binary).

Ark indexes PDF files by extracting text with positions, detecting
structure (tables, paragraphs, headings), and emitting chunks with
location paths and bounding rects. Uses seehuhn.de/go/pdf for
pure-Go extraction — no external tool dependency.

## Text Extraction

The chunker opens a PDF file, iterates pages, and extracts text
spans with position (X, Y in PDF points), font size, and content.
seehuhn's callback-driven API provides per-glyph coordinates and
text matrix, which are accumulated into text spans.

Text spans on the same line (similar Y coordinate, within font-height
tolerance) are merged left-to-right. This produces positioned lines
of text with bounding boxes.

## Structure Detection

Structure detection runs per-page in priority order. The first
strategy that finds structure wins for each region of the page.

### Table Detection

Two signals, tried in order:

1. **Drawn rules** — detect horizontal and vertical line-drawing
   operations in the PDF content stream (path operators: `re` for
   rectangles, `m`/`l` for lines). If lines form a grid (≥2 rows
   and ≥2 columns), the bounding box of the grid is a table region.
   Text spans inside the region become the table chunk's content,
   concatenated row by row.

2. **Column alignment** — if no drawn rules are found, check for
   grid-like text alignment. Cluster text spans by Y coordinate
   (rows). If multiple rows share ≥2 aligned X positions (within
   tolerance), the region is a table. Alignment tolerance is
   proportional to the dominant font size.

Table chunks use location `PAGE/table/N` (1-indexed per page).

### Heading Detection

Text spans whose font size exceeds the page's dominant (most common)
font size by ≥20% are headings. A heading and the body text
following it (up to the next heading or structural boundary) form
a heading chunk.

Heading chunks use location `PAGE/heading/N`.

### Paragraph Detection

Remaining text (not in tables or headings) is grouped into
paragraphs by vertical gap detection. A gap between consecutive
lines larger than 1.5× the dominant line spacing signals a paragraph
boundary.

Paragraph chunks use location `PAGE/para/N`.

### Page-Level Fallback

If a page has no detected structure (fewer than 2 text spans, or
all text in a single undifferentiated block), the entire page is
one chunk with location `PAGE`.

## Chunk Attributes

Every chunk carries attributes:

- `page` — page number (string), for the preview renderer
- `rect` — bounding box as `x,y,w,h` in PDF points (origin =
  bottom-left per PDF spec). Used for visual preview clipping.
- `font_size` — dominant font size in the chunk (optional, present
  for headings)

## Chunk Location Format

Path-style hierarchy: `PAGE/TYPE/N`

- `3` — page-level fallback, page 3
- `3/para/2` — paragraph 2 on page 3
- `3/table/1` — table 1 on page 3
- `3/heading/1` — heading 1 on page 3

Page and chunk numbers are 1-indexed.

## Registration

The PDF chunker registers as strategy `"pdf"` via
`microfts2.AddChunker`. It implements both `FileChunker` (for
indexed files — owns the file read, hash-based skip) and `Chunker`
(for tmp documents — receives raw bytes).

Configuration in ark.toml:

```toml
[strategies]
  "*.pdf" = "pdf"
```

No `[[chunker]]` block needed — the PDF chunker is built into ark,
not config-driven like bracket/indent chunkers.

## What This Does NOT Cover

- **Search preview rendering** — PDF.js in-browser canvas rendering,
  search hit highlighting. Separate UI concern.
- **OCR for scanned PDFs** — deferred. Scanned PDFs produce empty
  text extraction; the chunker yields no chunks.
- **CJK-specific tuning** — seehuhn has CMap/ToUnicode machinery
  but CJK correctness is unverified. Will test separately.
- **Embedding model upgrade** — nomic v1.5 doesn't handle Chinese.
  Model upgrade is a separate track.
