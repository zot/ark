# Sequence: Tag Value Index
**Requirements:** R1099-R1129

V record lifecycle during indexing and querying.

## Index/Refresh via Chunk Callback (R1103, R1113-R1124)

```
Indexer              microfts2                Store              LMDB
  |                    |                       |                  |
  |--AddFileWithContent                        |                  |
  |  (path, strategy,  |                       |                  |
  |   WithChunkCallback)                       |                  |
  |                    |                       |                  |
  |  callback fires per chunk:                 |                  |
  |  <--fn(chunkText)--|                       |                  |
  |  accumulate chunks  |                      |                  |
  |  ExtractTagValues   |                      |                  |
  |  ExtractTagDefs     |                      |                  |
  |  (repeat per chunk) |                      |                  |
  |                    |                       |                  |
  |<--fileid, content--|                       |                  |
  |                    |                       |                  |
  |  merge tag values across chunks (R1120-R1122)                |
  |                    |                       |                  |
  |--UpdateTagValues-->|                       |                  |
  |  (fileid, merged)  |                       |                  |
  |                    |  scan V prefix keys   |                  |
  |                    |--for each V key------>|                  |
  |                    |  decode varints        |                  |
  |                    |  remove fileid         |                  |
  |                    |  if empty: delete key  |                  |
  |                    |  else: rewrite blob    |                  |
  |                    |                       |                  |
  |                    |  for each new value:   |                  |
  |                    |--Get V[tag\x00val]--->|                  |
  |                    |  append fileid varint  |                  |
  |                    |--Put V[tag\x00val]--->|                  |
  |                    |                       |                  |
```

Same pattern for ReindexWithContent (R1114) — callback provides
clean chunk text, eliminating splitChunks (R1124).

## Append (R1104)

```
Indexer              Store                    LMDB
  |                    |                       |
  |--AppendTagValues-->|                       |
  |  (fileid, values)  |                       |
  |                    |  for each new value:   |
  |                    |--Get V[tag\x00val]--->|
  |                    |  append fileid varint  |
  |                    |--Put V[tag\x00val]--->|
  |                    |                       |
```

No removal step — appended tags are additive.

## Remove (R1105)

```
Indexer              Store                    LMDB
  |                    |                       |
  |--RemoveTagValues-->|                       |
  |  (fileid)          |                       |
  |                    |  scan all V keys      |
  |                    |--for each V key------>|
  |                    |  decode varints        |
  |                    |  remove fileid         |
  |                    |  if empty: delete key  |
  |                    |  else: rewrite blob    |
  |                    |                       |
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
