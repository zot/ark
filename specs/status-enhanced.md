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

## Output Format

All new fields appear after the existing ones. The output stays
plain text, one field per line, same as current status. Example:

```
files: 1273 (156 MB)
stale: 0
missing: 12
unresolved: 3
chunks: 8451
sources: 5
strategies: lines=1200 chat-jsonl=73
db: 511 MB
server: not running
```

The total size appears parenthesized after the file count. The
`strategies` line lists each strategy with its file count,
space-separated. The `db` line shows the database file size.

## Server Endpoint

`GET /status` returns the same data as JSON. New fields are added
to the existing StatusInfo response.
