# PDFChunker
**Requirements:** R1624, R1625, R1626, R1627, R1628, R1629, R1630, R1631, R1632, R1633, R1634, R1635, R1636, R1637, R1638, R1639, R1640, R1641, R1642, R1643

Extracts text from PDF files with positions and font sizes, detects
structure (tables, paragraphs, headings), and emits chunks with
path-style locations and bounding rect attributes. Implements both
FileChunker and Chunker interfaces.

## Knows
- tiers of structure detection: tables → headings → paragraphs → page fallback
- font-size threshold for heading detection (≥20% above dominant)
- line-spacing threshold for paragraph breaks (>1.5× dominant spacing)
- column alignment tolerance for table detection

## Does
- Chunks(path, content, yield): parse PDF from bytes, extract and yield chunks (Chunker interface)
- ChunkText(path, content, rangeLabel): retrieve single chunk by range label
- FileChunks(path, oldHash, yield): parse PDF from file path, hash-based skip (FileChunker interface)
- extractPage(page): extract text spans with X, Y, FontSize from one PDF page
- mergeSpans(spans): merge spans on same line into positioned lines
- detectTables(lines): find table regions via drawn rules then column alignment
- detectHeadings(lines, dominantSize): find heading lines by font size
- groupParagraphs(lines, dominantSpacing): group remaining lines into paragraphs by gap detection
- buildChunks(page, tables, headings, paragraphs): assemble chunks with Range, Content, Attrs

## Collaborators
- seehuhn.de/go/pdf (reader): PDF parsing, page iteration, content stream callbacks
- microfts2.Chunk: output format (Range, Content, Attrs)
- microfts2.FileChunker: file-based chunking interface with hash skip
- DB: registers strategy via AddChunker at init time

## Sequences
- seq-pdf-chunk.md
