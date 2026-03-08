# Sequence: File Change (fsnotify)
**Requirements:** R348, R349, R350, R351, R352, R353, R354, R355, R356, R357, R358, R359, R360, R361, R362, R363, R364, R365, R366, R367, R368, R369

Covers fsnotify events on source directories, throttled on-notify,
and append detection.

## Participants
- Server (watcher, throttle)
- Indexer
- Store
- microfts2.DB

## Throttled On-Notify

```
fsnotify event (CREATE | WRITE | REMOVE | RENAME)
  │
  ├── if ark.toml changed:
  │     └── Config.Load() + Server.Reconcile()
  │         (full reconciliation, not per-file)
  │
  ├── if source file changed:
  │     │
  │     ├── if in immediate mode (no active throttle):
  │     │     ├── index/refresh the file immediately
  │     │     └── start throttle window
  │     │
  │     ├── if in throttle window:
  │     │     └── ignore (filesystem has the truth)
  │     │
  │     └── when throttle window expires:
  │           ├── if events arrived during window:
  │           │     ├── single re-index of current state
  │           │     └── start new throttle window
  │           └── if no events during window:
  │                 └── return to immediate mode
  │
  └── max wait ceiling: if throttle has been active for N seconds
        without a re-index, force one regardless of events
```

## Append Detection (during refresh)

```
Indexer.RefreshFile(path)
  │
  ├──> Indexer.DetectAppend(path, fileInfo)
  │     │
  │     ├── read stored length, content hash from microfts2 N record
  │     ├── read file up to stored length
  │     ├── hash and compare
  │     │
  │     ├── hash differs → return -1 (not append, full reindex)
  │     │
  │     ├── hash matches → append-only change
  │     │     ├── Store.GetLastChunkOffset(fileid)
  │     │     ├── seek to offset, compare bytes vs stored chunk
  │     │     │
  │     │     ├── chunk matches (clean boundary):
  │     │     │     └── return file end offset (append from here)
  │     │     │
  │     │     └── chunk doesn't match (unclean boundary):
  │     │           └── return last chunk start offset (re-chunk from here)
  │     │
  │     └── (small files: hash is trivial, full reindex is cheap anyway)
  │
  ├── if append offset >= 0:
  │     └──> Indexer.AppendFile(path, offset, strategy)
  │           ├── chunk content from offset only
  │           ├── microfts2.AppendChunks(fileid, newChunks)
  │           ├── microvec: add vectors for new chunks
  │           ├── Store.AppendTags(fileid, newTags)
  │           └── Store.PutLastChunkOffset(fileid, lastNewChunkOffset)
  │
  └── if not append:
        └──> full RefreshFile path (existing behavior)
             └── Store.PutLastChunkOffset(fileid, lastChunkOffset)
```

## Search Consistency

```
Searcher.SearchWithConsistency(query, opts)
  │
  ├──> search(query, opts) → results
  ├──> CheckStale(results) → staleFiles
  │
  ├── if no stale files → return results
  │
  ├── retry (max 2):
  │     ├── Indexer.RefreshFile(path) for each stale file
  │     ├── search(query, opts) → results
  │     ├── CheckStale(results) → staleFiles
  │     └── if no stale files → return results
  │
  └── after 2 retries with stale files:
        ├── prune stale results
        └── return valid results
```
