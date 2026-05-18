# PDFChunker
**Requirements:** R1630, R1633, R1635, R1637, R1638, R1639, R1640, R1641, R1642, R1643, R1665, R1670, R1671, R1672, R1673, R1675, R1676, R1719, R1720, R1721, R1722, R1724, R1725, R1726, R1727, R1728, R1729, R1730, R1731, R1732, R1733, R1734, R1735, R1736, R1737, R1738, R1758, R1759, R1760, R2076, R2388, R2428, R2429

Uses `github.com/zot/pdftext` to open a PDF, iterate pages, and
receive structure-detected `Block`s (Paragraph, Heading, Table,
List, Image, Irregular, Salvage). Each Block becomes one ark chunk
with a path-style location derived from `BlockKind`, a bounding
rect from `Block.BBox`, and tag rects extracted from `Block.Chars`.
Implements FileChunker, Chunker, and RandomAccessChunker. At index
time writes per-page compressed text blobs to the Store; at
retrieval time reads from those blobs so search avoids a full
re-parse.

## Knows
- block-kind → location mapping: Paragraph/Irregular → `para`, Heading → `heading`, Table → `table`, List → `list`, Salvage → `salvage`, Image → skipped (R1730)
- Caption prepended to Text with newline separator when Block.Caption is non-empty (R1731)
- the ark tag pattern `@([a-zA-Z][\w.-]*):\s*([^\n]*)` — matched against Block.Text and Block.Caption (R1735)
- per-kind counters: each page restarts para/heading/table/list/salvage counters at 1 (R1738)
- store: *Store — reference to ark's subdatabase, held so FileChunks can write page blobs and GetChunk can read them (R1720, R1726)

## Does
- Chunks(path, content, yield): open with `pdftext.Open`, walk pages → blocks, yield one chunk per non-Image block (R1730, R1733, R1734)
- FileChunks(path, oldHash, yield): read file, hash-check, open with `pdftext.Open`, walk pages → blocks. Staging blobs via pushPending for later FlushBlobs. On hard `pdftext.Open` error, yield nothing — microfts2 handles the log-once path (R1734)
- GetChunk(path, data, customData, chunk): RandomAccessChunker fast path. Reads `content_offset`/`content_len` + `page` from chunk.Attrs, loads the page blob from Store (or from *customData if already decompressed this session), slices, assigns to chunk.Content. Falls back to streaming FileChunks when Attrs or blob are missing. (R1726, R1727, R1728)
- FlushBlobs(path, fileid): writes staged page blobs to Store under fileid after removing any prior blobs for that file (R1720, R1724)
- blockToChunk(pageNum, kind, block, counters): maps one pdftext.Block to a microfts2.Chunk — joins Caption+Text, builds location, populates Attrs with page, rect, font_size (headings only), tag_rects, and tag_segments (R1730, R1731, R1737, R1758)
- extractTagRects(block): scan Block.Text (and Block.Caption if non-empty) for ark tag pattern; for each match, union Block.Chars/Block.CaptionChars BBoxes whose byte ranges overlap the match. Emit compact string: `name=value@x,y,w,h;...` (R1735, R1736, R1671, R1672)
- extractTagSegments(block): parallel to extractTagRects. For each tag match, compute per-segment rects — `@` (byte `[m[0], m[0]+1)`), name (`[m[2], m[3])`), `:` (`[m[3], m[3]+1)`), value (`[m[4], valueEnd)` with trailing ASCII whitespace trimmed). Emit `atRect|nameRect|colonRect|valRect1|valRect2…;nextTag…` (R1758, R1759). Value segment can be multiple rects — one per physical line — computed by charRangeRectsByLine (R1760).
- charRangeRectsByLine(chars, start, end): iterate Block.Chars in the byte range, group consecutive chars whose baseline Y differs from the running average glyph height by no more than half an average height; each group becomes one union rect. Wrapped values produce multiple rects (R1760).
- on Heading-kind chunks, the existing `rect` Attr (already populated from `Block.BBox`) is what the server uses to emit `<ark-heading rect="...">` for the tag overview's PDF heading rendering. No new pdftext output is needed — the data is already present (R2076).
- IsWritable() bool: returns `false`. PDF is a binary format with no general text-edit primitive — the workshop UI uses this to lock the chunk-text editor and force the ext toggle on for PDF chunks. (R2388)
- CommentSyntax() string: returns `""`. PDFs have no line-comment convention; inline tag insertion is not applicable. (R2388)

## Collaborators
- `github.com/zot/pdftext` (Doc, Page, Block, Char): page iteration, structure detection, NFKC-normalized text, per-glyph BBoxes
- compress/zlib (stdlib): page-blob compression/decompression
- microfts2.Chunk: output format (Range, Content, Attrs)
- microfts2.FileChunker / Chunker / RandomAccessChunker: indexing + retrieval interfaces
- Store: page-content blob storage (WritePageContent, ReadPageContent, RemovePageContents)
- DB: registers strategy via AddChunker at init time; injects Store reference on construction

## Sequences
- seq-pdf-chunk.md
- seq-pdf-chunk-retrieval.md
