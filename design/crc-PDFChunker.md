# PDFChunker
**Requirements:** R1624, R1625, R1626, R1627, R1628, R1629, R1630, R1631, R1632, R1633, R1634, R1635, R1636, R1637, R1638, R1639, R1640, R1641, R1642, R1643, R1652, R1653, R1654, R1655, R1656, R1657, R1658, R1659, R1660, R1661, R1662, R1663, R1664, R1665, R1669, R1670, R1671, R1672, R1673, R1674, R1675, R1676, R1719, R1720, R1721, R1722, R1723, R1724, R1725, R1726, R1727, R1728

Extracts text from PDF files with positions and font sizes, detects
structure (tables, paragraphs, headings), and emits chunks with
path-style locations and bounding rect attributes. Implements
FileChunker, Chunker, and RandomAccessChunker. When the seehuhn
reader rejects the file, falls back to a best-effort text salvage
over raw bytes. At index time writes per-page compressed text
blobs to the Store; at retrieval time reads from those blobs so
search avoids a full re-parse.

## Knows
- tiers of structure detection: tables → headings → paragraphs → page fallback
- font-size threshold for heading detection (≥20% above dominant)
- line-spacing threshold for paragraph breaks (>1.5× dominant spacing)
- column alignment tolerance for table detection
- the text-showing operators (Tj, TJ, ', ") and the escape sequences inside PDF string literals
- the ark tag pattern `@([a-zA-Z][\w.-]*):\s*([^\n]*)` for extracting tag_rects from positioned text spans
- store: *Store — reference to ark's subdatabase, held so FileChunks can write page blobs and GetChunk can read them (R1720, R1726)

## Does
- Chunks(path, content, yield): parse PDF from bytes, extract and yield chunks; on parser error, fall back to salvage (R1660)
- FileChunks(path, oldHash, yield): parse PDF from file path, hash-based skip; on parser error, fall back to salvage. Before yielding chunks for the file, calls Store.RemovePageContents(fileID) to clear stale blobs; as pages are completed, writes each page's concatenated chunk text (null-byte separated, zstd-compressed) via Store.WritePageContent, and populates each chunk's `content_offset`/`content_len` attrs. (R1719, R1720, R1721, R1722, R1724)
- GetChunk(path, data, customData, chunk): RandomAccessChunker fast path. Reads `content_offset`/`content_len` + `page` from chunk.Attrs, loads the page blob from Store (or from *customData if already decompressed this session), slices, assigns to chunk.Content. Falls back to streaming FileChunks when Attrs or blob are missing. (R1726, R1727, R1728)
- extractPage(page): extract text spans with X, Y, FontSize from one PDF page
- mergeSpans(spans): merge spans on same line into positioned lines
- detectTables(lines): find table regions via drawn rules then column alignment
- detectHeadings(lines, dominantSize): find heading lines by font size
- groupParagraphs(lines, dominantSpacing): group remaining lines into paragraphs by gap detection
- buildChunks(page, tables, headings, paragraphs): assemble chunks with Range, Content, Attrs
- salvageText(content, yield): scan raw bytes for content streams, decode FlateDecode or raw, extract Tj/TJ/'/" text, yield one chunk per stream (R1652-R1659). Salvage chunks share a single blob stored at page 0 (R1723).
- filterBlankLines(lines): drop whitespace-only lines before structure detection so gap-based paragraph and column-alignment detection work on ONLYOFFICE-style PDFs (R1661-R1664)
- extractTagRects(chunkSpans): scan a chunk's positioned text spans for the ark tag pattern; for each match, record the bounding box (union of matched spans). Emit as chunk attribute `tag_rects` with format `name=value@x,y,w,h;…` (URL-encode `=`/`@`/`;`/`,` in name and value). First-line only for wrapped values. Omitted on salvage chunks. (R1665, R1669-R1675)

## Collaborators
- seehuhn.de/go/pdf (reader): PDF parsing, page iteration, content stream callbacks
- compress/zlib (stdlib): FlateDecode decompression in the salvage path
- github.com/klauspost/compress/zstd (or equivalent): page-blob compression/decompression
- microfts2.Chunk: output format (Range, Content, Attrs)
- microfts2.FileChunker / RandomAccessChunker: indexing + retrieval interfaces
- Store: page-content blob storage (WritePageContent, ReadPageContent, RemovePageContents)
- DB: registers strategy via AddChunker at init time; injects Store reference on construction

## Sequences
- seq-pdf-chunk.md
- seq-pdf-salvage.md
- seq-pdf-chunk-retrieval.md
