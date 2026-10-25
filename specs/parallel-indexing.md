# Parallel Indexing

Rebuild and refresh index files in parallel. File preparation
(read, chunk, extract tags/trigrams) is independent per file.
Index writes must be serial (single-writer constraint).

## Design

File preparation runs off the write slot: each changed file is its own
scan (the per-file scan actor — see crc-FileScanActor.md), computing
chunks, tags, and trigrams in a plain goroutine that holds no write
transaction. A single write actor serializes every index write; a file's
short store step is enqueued back to it as a closure that captures the
prepared data.

```
per-file scans (bounded, off-slot)     write actor (owns index writes)
  read file from disk                    for fn := range ch {
  chunk into pieces                          fn()   // one store at a time
  extract tags + defs                    }
  extract trigrams (microfts2 prep)
  enqueue store-closure ──────►
```

Concurrency is **bounded** (see "Worker count"): a reconcile that finds
thousands of stale files must not run thousands of preparations at once.

## Scope

Applies to:
- `ark rebuild` (all files — biggest win)
- `ark refresh` (stale files — wins on first scan or large batches)
- `doReconcile` in the server (calls Scan then Refresh)

The directory walk that *finds* the files to prepare is parallelized too
(see "Directory walk").

Does not change:
- microfts2 API (all writes go through existing methods)
- fsnotify coordination (reconcileLoop already serializes via channel)
- Single-file operations (AddFile, RefreshFile still work as-is)

## Worker count

Default to `runtime.NumCPU()`. No flag needed — CPU-bound work
(chunking, trigram extraction) scales with cores. Index writes are
I/O but serialized, so they don't benefit from parallelism.

Under the per-file scan-actor model the bound is a **semaphore on the
per-file dispatch** (`startFileScan`): at most `runtime.NumCPU()` file
preparations run concurrently, and the DB actor never blocks to enforce
it. The walk uses the same default.

**A scan holds its slot until its store finishes**, not only while it prepares,
so at most `runtime.NumCPU()` prepared scans exist at once, waiting or storing.
Preparation is parallel and storing is serial, so when stores are slow a slot
released after preparing lets prepared scans pile up behind the writer, each
holding its file's bytes and trigram counts. Measured 2026-10-25, when a chunker
change made every chat store replace its chunks: about 31 files a minute stored,
and the backlog held 1.8 GB of file contents and 950 MB of trigram counts, with
the process at 5 to 6 GB. The slot is released once on every path out: an early
retire, a superseded scan, a store that succeeds or fails, and a write that
panics.

## Directory walk

The scan walk enumerates source directories concurrently with a bounded
worker pool (default `runtime.NumCPU()`), so filesystem latency is hidden
across directories instead of paid one `lstat` at a time. It takes **one
`lstat` per leaf** for type/size/mtime and reads **no file contents**;
staleness is decided by `mtime`+`size` against the stored record and a
content hash is taken only on a mismatch (classifyFile's existing rule).
Target: an ~8k-file corpus walked and classified well under 1s warm,
≤2s cold.

The walk enumerates in parallel but applies its stateful results — the
empty-file set, the already-indexed check, and directory-symlink
`Observe` — in a **serial merge** in deterministic walk order. So file
results stay a set (order-independent), and a symlink's classification
and its place in the reverse index do not depend on which worker reached
it first.

## Error handling

A worker error (file read failure, chunk failure) skips that file
and logs a warning. Does not abort the batch. The write actor collects
errors; RefreshStale returns the first N errors or a summary.

Missing files are still collected and returned as before.

## Ordering

No ordering guarantee on which files get indexed first. This is
fine — the index is a set, not a sequence. Results are the same
regardless of insertion order. The directory walk preserves this for
files and, additionally, Observes directory symlinks in a deterministic
walk order (see "Directory walk") so a parallel walk cannot change link
classification or the reverse-index order a sequential walk produced.
