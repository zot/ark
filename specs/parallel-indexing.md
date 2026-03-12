# Parallel Indexing

Rebuild and refresh index files in parallel. File preparation
(read, chunk, extract tags/trigrams) is independent per file.
LMDB writes must be serial (single-writer constraint).

## Design

Worker pool fans out file preparation. A ChanSvc actor serializes
all LMDB writes. Workers send closures that capture their prepared
data and call the write methods.

```
workers (N goroutines)          ChanSvc (owns LMDB writes)
  read file from disk             for fn := range ch {
  chunk into pieces                   fn()
  extract tags + defs             }
  extract trigrams (via microfts2 content prep)
  send write-closure ──────►
```

## Scope

Applies to:
- `ark rebuild` (all files — biggest win)
- `ark refresh` (stale files — wins on first scan or large batches)
- `doReconcile` in the server (calls Scan then Refresh)

Does not change:
- microfts2 API (all writes go through existing methods)
- fsnotify coordination (reconcileLoop already serializes via channel)
- Single-file operations (AddFile, RefreshFile still work as-is)

## Worker count

Default to `runtime.NumCPU()`. No flag needed — CPU-bound work
(chunking, trigram extraction) scales with cores. LMDB writes are
I/O but serialized, so they don't benefit from parallelism.

## Error handling

A worker error (file read failure, chunk failure) skips that file
and logs a warning. Does not abort the batch. The ChanSvc collects
errors; RefreshStale returns the first N errors or a summary.

Missing files are still collected and returned as before.

## Ordering

No ordering guarantee on which files get indexed first. This is
fine — the index is a set, not a sequence. Results are the same
regardless of insertion order.
