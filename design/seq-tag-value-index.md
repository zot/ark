# Sequence: Tag Value Index
**Requirements:** R1099-R1129

V record lifecycle during indexing and querying. V records are
chunkid-keyed: each (tag, value) record's value blob is a multi-set
of the chunkids that carry that pair (see record-formats.md, V
section). File-level callers resolve chunkids → fileids afterward.

## Index/Refresh via Chunk Callback (R1103, R1113-R1124, R1873-R1876)

```
Indexer                 microfts2            Store              LMDB
  |                        |                   |                  |
  |--AddFileWithContent--->|                   |                  |
  |  (path, strategy,      |                   |                  |
  |   WithChunkCallback)   |                   |                  |
  |                        |                   |                  |
  |  callback fires per chunk:                 |                  |
  |  <--fn(chunkID, text)--|                   |                  |
  |  ExtractTagValues      |                   |                  |
  |  ExtractTagDefs        |                   |                  |
  |  group values per chunkID (R1120-R1122)    |                  |
  |  (repeat per chunk)    |                   |                  |
  |                        |                   |                  |
  |<--fileid, content------|                   |                  |
  |                        |                   |                  |
  |--UpdateTagValues([]ChunkTagValues)-------->|                  |
  |                        |   for each chunk: |                  |
  |                        |   resolve/alloc tvid per (tag,value) |
  |                        |   append chunkID to V blob (multi-set)
  |                        |--Put V[tag\x00val\x00tvid]---------->|
  |                        |   write chunk tvids                  |
  |                        |--Put F[chunkID]--------------------->|
  |                        |                   |                  |
```

Same per-chunk write for AppendTagValues — chunkid-keyed records
don't distinguish first-write from append, so no removal step is
needed. Old chunkids are cleaned by the orphan path below, not here.

## Remove — orphan-chunkid cleanup (R1899, R1900)

Driven by microfts2's removed-chunk callback when a chunk is orphaned
(file removed or re-indexed). Targeted, not a full V scan.

```
microfts2                 Store                        LMDB
  |                          |                          |
  |--RemovedChunkCallback--->|                          |
  |  (chunkID)               |                          |
  |                          |  read F[chunkID] → tvids |
  |                          |--Get F[chunkID]--------->|
  |                          |  for each tvid:          |
  |                          |  remove chunkID from      |
  |                          |  V[tag\x00val\x00tvid]    |
  |                          |--Put/Del V record------->|
  |                          |  delete F[chunkID],       |
  |                          |  decrement T totals       |
  |                          |--Del F[chunkID]--------->|
  |                          |                          |
```

## Query — Tag Value Completion (R1108, R1109, R1111)

```
Browser         Server              Store           LMDB
  |                |                   |               |
  |--POST /tags/values--------------->|               |
  |  {tag,prefix}  |                   |               |
  |                |--QueryTagValues-->|               |
  |                |  (tag, prefix)    |               |
  |                |                   |--prefix scan->|
  |                |                   |  V[tag\x00pfx]|
  |                |                   |  decode counts|
  |                |<--[]TagValueCount-|               |
  |                |  sort by count    |               |
  |<--200 JSON-----|                   |               |
```

## (tag, value) → chunkids lookup (R1309, R2120)

`TagValueChunks(tag, value)` resolves the tvid via TvidMap, reads the
single V record by exact key, and decodes the chunkid multi-set —
then unions `TmpTagStore.TagValueChunks` (overlay) and
`ExtMap.ExtTagValueChunks` (ext-routed targets). File-level callers
(`DB.TagValuesWithFiles`, `Librarian.resolveTagValuePaths`) pass the
chunkids through `Store.FilesForChunks` → `DB.resolveFilePath` to get
paths, never `FileInfoByID` directly.
