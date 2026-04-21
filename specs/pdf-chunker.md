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

### Blank-Line Filtering

Before any structure detection runs, lines whose text is entirely
whitespace are dropped from the line set. This matters because some
PDF generators (notably ONLYOFFICE) render blank visual lines as
real text lines containing a single space glyph at a low Y position,
rather than as vertical whitespace. Without filtering, gap-based
paragraph detection sees consistent line spacing and never trips
the threshold — the entire document becomes one paragraph. Table
detection has a similar failure: blank "rows" with no aligned X
positions dilute the column-alignment signal.

Filtering at the top of structure detection means paragraph, table,
and heading detection all see only content-bearing lines. The Y
slot once occupied by a blank line becomes a gap of the normal
line-spacing size, so paragraph separators — previously two
normal gaps flanking a blank line — now appear as a single
doubled gap, which cleanly exceeds the 1.5× dominant-spacing
threshold.

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
- `tag_rects` — per-tag bounding boxes for `@name: value` patterns
  found in the chunk, used by `<pdf-chunk>` to overlay
  interactive `<ark-tag>` widgets on the rendered region.
  Optional (absent when the chunk has no tags, and for salvage
  chunks). Format spec: `specs/pdf-chunk-element.md` §Chunker
  Extension.
- `content_offset` — byte offset of this chunk's text within the
  page's cached text blob.
- `content_len` — byte length of this chunk's text within the blob.

## Chunk Text Cache

Re-extracting a chunk's text at search time means re-parsing the PDF
from scratch — page iteration, content-stream interpretation, font
resolution, structure detection. On 217 small PDFs an uncached
`education` search takes ~5 seconds; the same query on the same
corpus without PDFs takes ~130 ms. Extraction cost is paid per hit,
so grouped-search queries against large PDF corpora scale badly.

The chunker writes each page's extracted text into a compressed
cache at index time, keyed by `(fileid, page)`. Every chunk's
attributes point into its page's blob via `content_offset` and
`content_len`. At retrieval time, the chunker reads the page blob,
decompresses, and slices — no PDF parse.

### Retrieval Via RandomAccessChunker

PDFChunker implements microfts2's `RandomAccessChunker` interface:

```go
GetChunk(path string, data []byte, customData *any, chunk *Chunk) error
```

microfts2's `ChunkCache` dispatches `GetChunk` with the chunk's
pre-filled `Range` and `Attrs` (read from the C record). The
chunker reads `content_offset`/`content_len` from Attrs, fetches
the page blob from ark's subdatabase, decompresses, slices, and
fills `chunk.Content`. `customData` holds decompressed page blobs
keyed by page number — filled on demand, dropped when the
ChunkCache expires (session TTL, minutes). No eviction policy
needed; the lifetime is short enough that unbounded growth is not
a concern.

### Blob Shape

Each page blob contains the concatenated text of every chunk on
that page, in emission order, null-byte separated. Compression is
zstd. The chunker chooses page granularity because zstd needs
dictionary mass to earn its ratio, and retrieval locality is
already aligned with pages (neighbor-chunk previews, `<pdf-chunk>`
rendering).

Salvage chunks share a single per-file blob (conceptual "page 0")
because they have no page number and typically come in small counts
per file.

### Cache Invalidation

Before writing new blobs for a file, the chunker removes all
existing blobs for that fileid. This prevents stale pages from
outliving a re-indexed document with fewer pages. On file removal
(Store's existing file-deletion path), the file's blobs are
removed alongside other per-file records.

### Fallback

If a chunk's attributes lack `content_offset`/`content_len`, or the
blob is missing (older index, corruption), `GetChunk` falls back
to the parse path — runs the streaming chunker until the target
range is found. The cache is a strict optimization; absence does
not break retrieval.

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

## Fallback Text Salvage

Some PDFs parse-reject in seehuhn even though their content streams
are legible. The most common case in our data is a malformed xref
table — the header says "0 N" but only a free-entry row follows,
so seehuhn's strict xref reader aborts before text extraction can
start. These are often tiny hand-written PDFs (for example, demo
fixtures the app once generated as placeholder résumés or cover
letters).

When `pdf.NewReader` returns any error, the chunker falls back to a
best-effort text salvage:

1. Locate every `stream\n ... \nendstream` pair in the raw bytes.
2. If the preceding object dictionary specifies `/FlateDecode`,
   decompress the stream with `compress/zlib`; otherwise use the
   bytes as-is. Unknown filters → skip the stream.
3. Inside each decoded stream, extract text from the PDF text-showing
   operators: `(literal)Tj`, `(literal)'`, `aw ac (literal)"`, and
   the array form `[(a)(b)...]TJ` (numbers inside the array are
   kerning — ignored).
4. Unescape the standard PDF string escapes inside literals: `\(`,
   `\)`, `\\`, `\n`, `\r`, `\t`, `\b`, `\f`, and octal `\ddd`.
5. Emit one chunk per content stream, location `salvage/N`
   (1-indexed). The chunk's `rect` attribute is absent because
   coordinates were not consulted. No heading/table/paragraph
   structure — salvage is flat text only.

Salvage is best-effort. PDFs with encoded fonts (ToUnicode/CMap),
LZW streams, encrypted content, or non-text operators remain
unparseable — the chunker yields nothing and the file falls into
the file-based chunker's standard log-once path.

The goal is content retrievability, not fidelity. A salvaged chunk
is searchable and previewable; the user sees plain text with no
layout.

## What This Does NOT Cover

- **Search preview rendering** — PDF.js in-browser canvas rendering,
  search hit highlighting. Separate UI concern.
- **OCR for scanned PDFs** — deferred. Scanned PDFs produce empty
  text extraction; the chunker yields no chunks.
- **CJK-specific tuning** — seehuhn has CMap/ToUnicode machinery
  but CJK correctness is unverified. Will test separately.
- **Embedding model upgrade** — nomic v1.5 doesn't handle Chinese.
  Model upgrade is a separate track.
