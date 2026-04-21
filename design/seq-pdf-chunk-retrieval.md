# Sequence: PDF Chunk Retrieval

**Requirements:** R1719, R1720, R1721, R1722, R1723, R1724, R1725,
R1726, R1727, R1728

Triggered by search fill-content. microfts2's ChunkCache dispatches
GetChunk on the PDFChunker; the chunker reads the page blob from
ark's Store and slices, avoiding a PDF reparse.

## Participants
- microfts2.ChunkCache (crc-external) — caller
- PDFChunker (crc-PDFChunker.md) — chunker
- Store (crc-Store.md) — page-content blob storage
- customData (map[page][]byte) — per-cachedFile scratch

## Flow — Cached Hit

```
ChunkCache                 PDFChunker                Store
   |                           |                       |
   |--GetChunk(path,           |                       |
   |   data=nil,               |                       |
   |   customData,             |                       |
   |   chunk=                  |                       |
   |   {Range, Attrs})-------->|                       |
   |                           |--read page, offset,   |
   |                           |  len from Attrs       |
   |                           |                       |
   |                           |--customData[page]?--  |
   |                           |  hit: use it          |
   |                           |                       |
   |                           |--slice [offset:       |
   |                           |   offset+len]         |
   |                           |--chunk.Content = ...  |
   |<--nil (success)-----------|                       |
```

## Flow — Cold Page (Blob Present)

```
ChunkCache                 PDFChunker                Store
   |                           |                       |
   |--GetChunk(...)----------->|                       |
   |                           |--read page, offset,   |
   |                           |  len from Attrs       |
   |                           |                       |
   |                           |--customData[page]?--  |
   |                           |  miss                 |
   |                           |                       |
   |                           |--ReadPageContent----->|
   |                           |  (fileid, page)       |
   |                           |<--zstd(blob)----------|
   |                           |                       |
   |                           |--zstd decompress      |
   |                           |--customData[page] =   |
   |                           |   decompressed        |
   |                           |                       |
   |                           |--slice [offset:       |
   |                           |   offset+len]         |
   |                           |--chunk.Content = ...  |
   |<--nil (success)-----------|                       |
```

## Flow — Fallback (Missing Attrs or Blob)

```
ChunkCache                 PDFChunker
   |                           |
   |--GetChunk(...)----------->|
   |                           |--Attrs lack offset/len,
   |                           |  OR ReadPageContent
   |                           |  returned not-found
   |                           |
   |                           |--run FileChunks(path, zero-hash),
   |                           |  yield until chunk.Range matches
   |                           |  target, copy Content, stop
   |<--nil (success on match)--|
   |<--err (no match found)----|
```

## Notes
- customData is `*any` holding a `map[uint32][]byte` the chunker
  populates lazily on first miss per page. Lifetime = ChunkCache
  TTL (session, minutes), so no eviction needed. (R1727)
- The Store write at index time is the companion to the reads
  shown here — see seq-pdf-chunk.md and seq-pdf-salvage.md for
  the write paths.
- Salvage chunks use page=0 and hit the per-file salvage blob.
  Retrieval path is identical; only the `page` attribute
  differs. (R1723)
