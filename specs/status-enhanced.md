# Enhanced Status

`ark status` currently shows file counts (total, stale, missing,
unresolved) and whether the server is running. That's useful but
doesn't tell you how full the database is or what's in it.

## Database File Size

Status should report the on-disk size of the database file
(`index.db`), read via `os.Stat`. Knowing how large the index has
grown tells you when to run `ark compact` to reclaim space from
deletions and overwrites.

Display: the database file size in human-readable units (MB/GB).

## Index Composition

Status should show what's in the index:

- **Total chunks** — how many chunks are indexed across all files.
  More chunks = more granular search but larger DB.
- **Files by strategy** — how many files use each chunking strategy
  (e.g., 1200 lines, 73 chat-jsonl). Shows the mix of content types.
- **Sources configured** — how many source directories are in
  ark.toml. Quick sanity check that config is loaded.

## Total Size

Status should report the total size of all indexed files. This is the
sum of file lengths as recorded in the index (not re-read from disk).
Displayed in human-readable units (KB/MB/GB), on the same line as
the file count.

## Staleness is as-of the last reconcile, never a fresh walk

`ark status` never stats or hashes the corpus. Determining whether a file
is stale means an `os.Stat` per indexed file (plus a read + hash on any
mtime mismatch), and on a large cold-cache index that is ~8k file reads
that would run on the DB actor and block every interleaving write for up
to a minute. Instead, the `stale`, `missing`, and `unresolved` counts are
what the **last reconcile** computed — the reconcile already walks the
corpus, so status just reports the number it left behind, tagged with the
wall-clock time it was computed:

```
stale: 211 (as of 12:47:03)
```

Before the first reconcile of the process completes there is no computed
count, so each of the three lines prints `-`:

```
stale: -
missing: -
unresolved: -
```

Because the counts are process-local (held by the running server), a cold
`ark status` opened directly against the index with no server running has
no reconcile behind it and prints `-`. When a server is running, `ark
status` proxies to it and reports the server's last-reconcile counts.

The remaining fields — file count, total size, chunk count, per-strategy
tally, source count, database file size — are a straight read over the
index's file records (no file I/O beyond one `os.Stat` of the database
file), and status reports them live.

## Output Format

All new fields appear after the existing ones. The output stays
plain text, one field per line, same as current status. Example:

```
files: 1273 (156 MB)
stale: 0 (as of 12:47:03)
missing: 12 (as of 12:47:03)
unresolved: 3 (as of 12:47:03) (uncovered=1 unindexable=1 symlink=1)
ext-unresolved: 2
chunks: 8451
sources: 5
strategies: lines=1200 chat-jsonl=73
db: 511 MB
server: not running
```

The total size appears parenthesized after the file count. The
`stale`/`missing`/`unresolved` lines carry `(as of HH:MM:SS)` (or `-`
before the first reconcile — see above). The `strategies` line lists each
strategy with its file count, space-separated. The `db` line shows the
database file size.

`unresolved` breaks down by reason ([symlinks.md](symlinks.md)) because
one reason normally dominates: `unindexable` files are usually the great
majority, and a bare total lets an actionable handful hide behind them.

`ext-unresolved` counts `@ext` tags whose target currently resolves to
nothing ([at-ext-storage.md](at-ext-storage.md)). It is surfaced here
because the failure is otherwise silent — a routing that never fires
looks exactly like one nobody wrote — and until now the only way to see
it was `ark inspect`. The count comes from the in-memory ExtMap, which
is re-derived on every load, so no record backs it.

## Server Endpoint

`GET /status` returns the same data as JSON. New fields are added
to the existing StatusInfo response.
