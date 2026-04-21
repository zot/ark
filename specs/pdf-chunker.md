# PDF Chunker

Language: Go. Environment: CLI (part of the `ark` binary).

Ark indexes PDF files by handing them to `github.com/zot/pdftext`,
which returns a document of `Page`s, each carrying a slice of
structure-detected `Block`s (paragraph, heading, table, list, image,
irregular, salvage). The chunker maps each Block to one ark chunk —
no span merging, no in-house structure detection, no separate
salvage codepath.

pdftext is pure-Go, MIT-licensed, purpose-built as ark's PDF
extraction layer. No external tool dependency.

## Block → Chunk Mapping

pdftext's `doc.Pages()` yields pages. For each page, `page.Blocks()`
yields blocks already classified by kind. The chunker emits one ark
chunk per block (skipping `Image` blocks — no indexable text).

Block kind maps to the ark location format:

| pdftext `BlockKind` | ark location      |
|---------------------|-------------------|
| `Paragraph`         | `PAGE/para/N`     |
| `Heading`           | `PAGE/heading/N`  |
| `Table`             | `PAGE/table/N`    |
| `List`              | `PAGE/list/N`     |
| `Irregular`         | `PAGE/para/N`     |
| `Salvage`           | `PAGE/salvage/N`  |
| `Image`             | (skipped)         |

N is 1-indexed per page per kind. Location format is
`PAGE/TYPE/N` as before; page numbers are 1-indexed.

`Irregular` blocks fold into the `para` counter because from an
indexer's perspective they're prose with unusual layout — search
still wants the text.

### Caption Handling

pdftext's `Block.Caption` holds introductory headings or lead
paragraphs that precede lists and tables. The chunker prepends
`Caption` to the chunk's content (newline-separated) so search
matches the caption alongside the body. Caption is empty for most
block kinds; concatenation is a no-op in those cases.

### Page-Level Fallback

If a page emits no blocks at all (e.g., scanned image pages), the
chunker emits nothing for that page.

## Chunk Attributes

Every chunk carries attributes:

- `page` — page number (string), for the preview renderer
- `rect` — bounding box as `x,y,w,h` in PDF points (origin =
  bottom-left per PDF spec). Sourced from `Block.BBox`. Used for
  visual preview clipping.
- `font_size` — present for headings, sourced from `Block.FontSize`
- `tag_rects` — per-tag bounding boxes for `@name: value` patterns
  found in the block's text, used by `<pdf-chunk>` to overlay
  interactive `<ark-tag>` widgets. Optional (absent when the chunk
  has no tags). Format spec: `specs/pdf-chunk-element.md` §Chunker
  Extension.
- `content_offset` — byte offset of this chunk's text within the
  page's cached text blob.
- `content_len` — byte length of this chunk's text within the blob.

### NFKC Text

pdftext emits `Block.Text` and `Block.Caption` in Unicode NFKC
normalized form. Ligatures (`ﬁ`, `ﬀ`, `ﬂ`) decompose to plain
ASCII (`fi`, `ff`, `fl`) in the indexed text, so a search for
`financial` matches without any normalization step on ark's side.
Fullwidth Latin and digit superscripts decompose the same way.

### Tag Rect Extraction

Tag rects are derived from `Block.Chars`, which is byte-aligned with
`Block.Text` and carries a per-glyph `BBox`. A match of the ark tag
pattern `@name: value` at bytes `[start:end]` in `Block.Text` gives
the bounding box as the union of the Chars covering `[start:end]`.
When one source glyph expands to multiple NFKC-normalized runes
(ligature `ﬁ` → `f` + `i`), every byte in the expansion carries the
same originating-glyph BBox — a hit on either the original or the
normalized form resolves to the same on-page region.

Captions get the same treatment via `Block.CaptionChars`.

## Chunk Text Cache

Re-extracting a chunk's text at search time means re-parsing the PDF
from scratch. On 217 small PDFs an uncached `education` search takes
~5 seconds; the same query on the same corpus without PDFs takes
~130 ms. Extraction cost is paid per hit, so grouped-search queries
against large PDF corpora scale badly.

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
zlib. The chunker chooses page granularity because compression needs
dictionary mass to earn its ratio, and retrieval locality is
already aligned with pages (neighbor-chunk previews, `<pdf-chunk>`
rendering).

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

- `3/para/2` — paragraph 2 on page 3 (includes Irregular blocks)
- `3/table/1` — table 1 on page 3
- `3/heading/1` — heading 1 on page 3
- `3/list/1` — list 1 on page 3
- `3/salvage/1` — salvage block 1 on page 3

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

## Graceful Degradation

When pdftext cannot parse a PDF at all (hard error from
`pdftext.Open`), the chunker yields nothing and lets the file take
microfts2's standard log-once path. No byte-stream fallback inside
ark — graceful degradation is pdftext's concern, and malformed
pages already surface as `Salvage` blocks with reduced `Confidence`
through the normal flow.

## What This Does NOT Cover

- **Search preview rendering** — PDF.js in-browser canvas rendering,
  search hit highlighting. Separate UI concern.
- **OCR for scanned PDFs** — deferred. Image-only pages yield no
  blocks; the chunker emits nothing for them.
- **Embedding model upgrade** — nomic v1.5 doesn't handle Chinese.
  Model upgrade is a separate track. pdftext's extraction on CJK is
  good, but semantic embeddings for CJK text need a different model.
