# Sequence: tmp:// tag overlay
**Requirements:** R1941–R1952

Covers tag extraction during `AddTmpFile`, `UpdateTmpFile`, and
`AppendTmpFile`, plus the unified read path.

## Participants
- DB
- chunkAccumulator
- microfts2.Overlay
- Store
- TmpTagStore
- Inbox (read example)

## Flow: AddTmpFile / UpdateTmpFile

```
DB.AddTmpFile(path, strategy, content)
  ├── acc := newChunkAccumulator(strategy)
  ├── microfts2.AddTmpFile(path, strategy, content,
  │       WithIndexedChunkCallback(acc.callback))
  │     │
  │     └── overlay fires acc.callback(IndexedChunk) per
  │         genuinely-new chunk, in chunk order
  │           - ChunkID, Hash, ContentLen, Attrs, FileIDs,
  │             Trigrams populated
  │           - CRecord.Tx() and CRecord.DB() return nil
  │             — callback must NOT traverse to the index (R1949)
  │           - acc extracts tag values from chunk text,
  │             accumulates {ChunkID, []TagValue}
  │     └── returns fileid (counts down from MaxUint64)
  │
  ├── chunkTags := acc.zip()  // []ChunkTagValues
  ├── Store.UpdateTagValues(fileid, chunkTags)
  │     │
  │     └── high bit of fileid set → dispatch to TmpTagStore
  │           (R1947)
  │           TmpTagStore.UpdateTagValues(fileid, chunkTags)
  │
  └── db.tmpPaths[path] = fileid
```

`UpdateTmpFile` follows the same shape; the overlay drops the file's
prior chunks before re-indexing, and `Store.UpdateTagValues` replaces
the overlay's entries for the fileid.

## Flow: AppendTmpFile

```
DB.AppendTmpFile(path, strategy, content)
  ├── acc := newChunkAccumulator(strategy)
  ├── microfts2.AppendTmpFile(path, strategy, content,
  │       WithIndexedChunkCallback(acc.callback))
  │     │
  │     └── overlay fires acc.callback only for newly emitted
  │         chunks (dedup hits skipped). Auto-create path
  │         (file did not exist) routes through AddTmpFile
  │         and propagates the callback unchanged.
  │     └── returns fileid
  │
  ├── chunkTags := acc.zip()
  └── Store.AppendTagValues(fileid, chunkTags)
        └── dispatch by fileid high bit → TmpTagStore.AppendTagValues
```

## Flow: RemoveTmpFile

```
DB.RemoveTmpFile(path)
  ├── fileid := db.tmpPaths[path]
  ├── Store.RemoveTagValues(fileid)
  │     └── high bit set → TmpTagStore.RemoveFile(fileid)
  │           - drops per-chunk entries
  │           - decrements per-tag counts
  │           - cleans up overlay-only tvids (R1951)
  ├── microfts2.RemoveTmpFile(path)
  └── delete(db.tmpPaths, path)
```

The tag overlay drops first so the trigram overlay and the tag
overlay never disagree on which fileids exist.

## Flow: Unified read (inbox example)

```
Server.Inbox / cmd inbox
  └── DB.Inbox(showAll, includeArchived)
        ├── Store.TagFiles(["status"])
        │     ├── persistent: scan F prefix (the index)
        │     ├── overlay:    TmpTagStore.TagFiles(["status"])
        │     └── union both → []TagFileInfo
        │
        ├── For non-showAll: build exclusion set
        │     Store.TagValueChunks("status", "completed")
        │     Store.TagValueChunks("status", "denied")
        │       (each unions persistent + overlay results)
        │
        └── For each surviving fileid:
              Store.FileTagValues(fileid, ["ark-request",
                "ark-response", "from-project", "to-project",
                "issue", "status", "status-date",
                "response-handled", "request-handled"])
              ├── high bit set → TmpTagStore.FileTagValues
              └── high bit clear → index scan
```

This wires the orphaned `FileTagValues` (R1142, R1147, R1149) into
inbox via the unified read path. Tmp:// messages — including
`tmp://watchdog/*` and agent DMs (`tmp://from/dm-to`) — appear in
inbox alongside persistent ones with no caller-side branching.

## Notes

- The chunkAccumulator type is the same one used by the persistent
  path (defined in indexer.go). Tmp:// flows reuse it without
  modification — same regex, same per-chunk attribution.
- The microfts2 callback fires only on genuinely-new chunks (hash
  dedup miss). On `UpdateTmpFile` of unchanged content, the
  accumulator collects nothing and the Store update is a no-op.
- No I record is written for tmp:// content. No `tag_store_version`
  bump applies — the overlay is rebuilt empty on each server start.
