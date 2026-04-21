# Sequence: PDF Chunk Extraction

**Requirements:** R1729-R1738, R1630, R1633, R1635, R1638, R1639, R1640, R1665, R1719, R1720, R1724

Triggered by reconcile when a PDF file is new or stale. The FileChunker
interface means PDFChunker owns the file read and hash comparison.

```
microfts2                PDFChunker              pdftext              Store
   |                         |                       |                   |
   |--FileChunks(path,hash)->|                       |                   |
   |                         |--os.ReadFile, sha256->|                   |
   |                         |  (hash match → skip)  |                   |
   |                         |                       |                   |
   |                         |--pdftext.Open(r)----->|                   |
   |                         |<--Doc-----------------|                   |
   |                         |                       |                   |
   |                         |  [for each page in doc.Pages()]           |
   |                         |--page.Blocks()------->|                   |
   |                         |<--[]Block-------------|                   |
   |                         |                       |                   |
   |                         |  [for each Block in page]                 |
   |                         |    kind → location suffix                 |
   |                         |    (Image → skip;                         |
   |                         |     Paragraph/Irregular → para;           |
   |                         |     Heading → heading;                    |
   |                         |     Table → table; List → list;           |
   |                         |     Salvage → salvage) R1730              |
   |                         |                                           |
   |                         |    content = Caption + "\n" + Text R1731  |
   |                         |    (already NFKC-normalized) R1732        |
   |                         |    Attrs: page, rect=Block.BBox, fontSize,|
   |                         |           tag_rects from Block.Chars      |
   |                         |                                           |
   |<----yield(chunk)--------|                                           |
   |                         |                                           |
   |                         |--sealPageBlob-->push to pending           |
   |                         |                                           |
   |  [all pages done]       |                                           |
   |<----(hash, nil)---------|                                           |
   |                                                                     |
   |  [caller invokes]                                                   |
   |--FlushBlobs(path, fileid)->|                                        |
   |                         |--RemovePageContents(fileid)-------------->|
   |                         |--WritePageContent(fileid, page, blob)---->|
   |                         |  (per pending blob) R1720, R1724          |
```

## Block → Location Mapping (R1730, R1738)

Each page restarts per-kind counters at 1.

| pdftext BlockKind | Location suffix |
|-------------------|-----------------|
| Paragraph         | `PAGE/para/N`   |
| Irregular         | `PAGE/para/N`   |
| Heading           | `PAGE/heading/N`|
| Table             | `PAGE/table/N`  |
| List              | `PAGE/list/N`   |
| Salvage           | `PAGE/salvage/N`|
| Image             | (skipped)       |

## Caption + Text (R1731)

When `Block.Caption` is non-empty (primarily on List and Table blocks),
the chunker concatenates `Caption + "\n" + Text` as the chunk's
content. FTS sees caption and body together; a search for terms from
either side resolves to the containing chunk.

## Tag Rects (R1735)

For each chunk, the tag-pattern regex scans `Block.Text` and
`Block.Caption`. For every match at byte range `[start:end]`, the
rect is the union of `Block.Chars` (or `Block.CaptionChars`) BBoxes
whose byte ranges overlap `[start:end]`. Because pdftext aligns
every expansion byte (e.g., ligature `ﬁ` → `f`+`i`) back to the
same originating-glyph BBox, hits on either the NFKC or the
pre-normalization form resolve to the same on-page region.

## Hard Errors (R1734)

If `pdftext.Open` returns a non-nil error, the chunker yields no
chunks and returns the hash so microfts2 can emit its standard
"log once, empty result" path. Graceful degradation for malformed
pages already happens inside pdftext — `Salvage` blocks flow
through the normal Block loop above.

## Chunk Emission Order

Within a page, chunks are emitted in the order pdftext yields
Blocks. pdftext returns blocks in reading order (top-to-bottom
by BBox.Y, descending).
