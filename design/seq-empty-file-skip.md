# Sequence: Empty File Skip

**Requirements:** R1644, R1645, R1646, R1647, R1648, R1649, R1650, R1651

Zero-byte files produce no chunks from any chunker. Without special
handling, each scan re-attempts them, and the PDF chunker (and any
other parser) logs a parse error every time. The in-memory
`EmptyFiles` set remembers paths we've already classified as empty so
re-scans can skip them without reading or parsing.

```
Scanner              EmptyFiles          microfts2          DB (addDirectory)
   |                      |                   |                   |
   |--walk file---------->|                   |                   |
   |  (size == 0?)        |                   |                   |
   |                      |                   |                   |
   |--Has(path, mtime)--->|                   |                   |
   |<--hit / miss---------|                   |                   |
   |                      |                   |                   |
   |  [hit] skip          |                   |                   |
   |                      |                   |                   |
   |  [miss]              |                   |                   |
   |--Record(path,mtime)->|                   |                   |
   |  results.EmptyFiles  |                   |                   |
   |  += path             |                   |                   |
   |                      |                   |                   |
   |--size > 0------------|------------------>|                   |
   |  CheckFile(path)     |                   |                   |
   |<--fresh/stale/new----|-------------------|                   |
   |  (existing logic)    |                   |                   |
   |                      |                   |                   |
   |---return ScanResults------------------------------------->   |
   |                      |                   |                   |
   |                      |                   | [for each path in EmptyFiles]
   |                      |                   |<--RemoveFile(path)|
   |                      |                   |  (microfts2       |
   |                      |                   |   handles chunk   |
   |                      |                   |   refcounting —   |
   |                      |                   |   other paths may |
   |                      |                   |   share the       |
   |                      |                   |   fileid/hash)    |
```

## Key points

- The set key is `path`; the value is `mtime`. Size is not stored
  because by definition an empty file has size zero. (R1645)
- When the file's `mtime` changes, the set entry is replaced and the
  path is re-reported — the file may have been restored with content
  (non-zero size → normal flow) or may still be zero-size (re-record
  and re-evict).
- `fts.RemoveFile(path)` is chosen over `Indexer.RemoveFile(path)`:
  chunks and fileid-level state may be shared with other paths, so
  microfts2 owns the refcount decision. Ark does not force-delete
  chunks. (R1648)
- The set is process-lifetime only. On restart, the first scan
  re-checks each empty file once (a single `os.Stat` per file), then
  re-populates the set. (R1650)
- Access to the set is serialized through the DB actor. Scanner.Scan
  runs on the actor goroutine (reads and writes to the set happen
  there). LMDB evictions from `ScanAsync` are routed through the
  write queue (`enqueueWrite`) so they serialize behind any in-flight
  write transaction rather than contending with it on the actor.
  Synchronous scans (`Scan`, `addDirectory`) run eviction in the
  actor because the rest of their indexing also runs there. No mutex
  is required — the actor model does the serialization. (R1651)
