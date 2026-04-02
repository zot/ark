# Sequence: Tag Value Index
**Requirements:** R1099-R1112

V record lifecycle during indexing and querying.

## Index/Refresh (R1103)

```
Indexer              Store                    LMDB
  |                    |                       |
  |  (tag values already extracted by          |
  |   ExtractTagValues — no new work)          |
  |                    |                       |
  |--UpdateTagValues-->|                       |
  |  (fileid, values)  |                       |
  |                    |  scan V prefix keys   |
  |                    |--for each V key------>|
  |                    |  decode varints        |
  |                    |  remove fileid         |
  |                    |  if empty: delete key  |
  |                    |  else: rewrite blob    |
  |                    |                       |
  |                    |  for each new value:   |
  |                    |--Get V[tag\x00val]--->|
  |                    |  append fileid varint  |
  |                    |--Put V[tag\x00val]--->|
  |                    |                       |
```

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
