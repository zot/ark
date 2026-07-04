# Sequence: File Change (fsnotify)
**Requirements:** R348, R349, R350, R351, R352, R353, R354, R355, R356, R357, R358, R359, R387, R388, R389, R390, R391, R392, R393, R394, R395, R360, R361, R362, R363, R365, R366, R367, R368, R369, R3008

Covers fsnotify events on source directories, throttled on-notify,
and append detection.

## Participants
- Server (watcher, throttle)
- Indexer
- Store
- microfts2.DB

## Throttled On-Notify

```
1. fsnotify event (CREATE | WRITE | REMOVE | RENAME)
  │
  ├── 1.1. if new directory created:
  │     └── 1.1.1. watchDirRecursive — descend/watch each subdir iff
  │               DB.IsWatchableDir (Classify isDir=true != Excluded):
  │               bypasses the file-indexability check but honors dir
  │               excludes. Watch coverage == scan coverage (R2952).
  │
  ├── 1.2. if ark.toml changed:
  │     ├── 1.2.1. Config.Load() + Server.Reconcile()
  │     │   (full reconciliation, not per-file)
  │     └── 1.2.2. clearIgnoredPaths() (invalidate negative cache)
  │
  ├── 1.3. isIgnored(path)?
  │     ├── 1.3.1. check ignoredPaths set (negative cache)
  │     ├── 1.3.2. if miss: DB.IsIndexable(path)
  │     │     ├── 1.3.2.1. find source for path
  │     │     ├── 1.3.2.2. Config.EffectivePatterns(src)
  │     │     └── 1.3.2.3. Matcher.Classify(includes, excludes, relPath, false)
  │     ├── 1.3.3. if not indexable: add to ignoredPaths, skip event
  │     └── 1.3.4. if indexable: continue to throttle
  │
  ├── 1.4. if source file changed (passes indexability check):
  │     │
  │     ├── 1.4.1. if in immediate mode (no active throttle):
  │     │     ├── 1.4.1.1. index/refresh the file immediately
  │     │     └── 1.4.1.2. start throttle window
  │     │
  │     ├── 1.4.2. if in throttle window:
  │     │     └── 1.4.2.1. ignore (filesystem has the truth)
  │     │
  │     └── 1.4.3. when throttle window expires:
  │           ├── 1.4.3.1. if events arrived during window:
  │           │     ├── 1.4.3.1.1. single re-index of current state
  │           │     └── 1.4.3.1.2. start new throttle window
  │           └── 1.4.3.2. if no events during window:
  │                 └── 1.4.3.2.1. return to immediate mode
  │
  └── 1.5. max wait ceiling: if throttle has been active for N seconds
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
  │           │     — WithFileLength and WithContentHash come from the SAME
  │           │       whole-file read snapshot: stored length = len(data), the
  │           │       exact span hashed, never a later os.Stat, so a concurrent
  │           │       append can't desync the (length, hash) pair (R3008)
  │           ├── EC embeddings re-computed for the file's chunks
  │           │     (Librarian.BatchEmbedChunks; chunkid-keyed, R1914)
  │           ├── ExtractTags(newBytes) → newTags
  │           └── Store.AppendTags(fileid, newTags)
  │
  └── if not append:
        ├── unchanged: size == FileLength && SHA-256(data) == ContentHash
        │     → skip entirely (R2864) — re-chunking byte-identical
        │       content is an expensive no-op; catches redundant /
        │       no-growth events on large append-only chat JSONLs
        └──> else (grew-but-not-append, in-place edit, truncation):
              full RefreshFile path (existing behavior)
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
