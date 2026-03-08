# Sequence: File Change (fsnotify)
**Requirements:** R348, R349, R350, R351, R352, R353, R354, R355, R356, R357, R358, R359, R360, R361, R362, R363, R365, R366, R367, R368, R369

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
Indexer.RefreshFile(path, strategy)
  │
  ├──> Indexer.DetectAppend(path, fileid)
  │     │
  │     ├── FileInfoByID(fileid) → stored FileLength, ContentHash
  │     ├── stat file: if size <= FileLength → not append
  │     ├── read first FileLength bytes, SHA-256, compare
  │     │
  │     ├── hash differs → return false (not append, full reindex)
  │     │
  │     ├── hash matches → append confirmed
  │     │     (current strategies always have clean chunk boundaries;
  │     │      future: back-seek from last chunk to find match point,
  │     │      chunker provides boundary-check capability)
  │     │
  │     └── return true
  │
  ├── if append:
  │     └──> Indexer.AppendFile(path, fileid, strategy)
  │           ├── read file from FileLength to EOF (new bytes)
  │           ├── parse last ChunkRange for base line
  │           ├── microfts2.AppendChunks(fileid, newBytes, strategy,
  │           │     WithBaseLine, WithContentHash, WithModTime, WithFileLength)
  │           ├── microvec: remove + re-add all vectors (full refresh)
  │           ├── ExtractTags(newBytes) → newTags
  │           └── Store.AppendTags(fileid, newTags)
  │
  └── if not append:
        └──> full RefreshFile path (existing behavior)
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
