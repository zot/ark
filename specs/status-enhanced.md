# Enhanced Status

`ark status` currently shows file counts (total, stale, missing,
unresolved) and whether the server is running. That's useful but
doesn't tell you how full the database is or what's in it.

## LMDB Map Usage

Status should report how much of the LMDB map is consumed. LMDB
pre-allocates a memory-mapped region (the "map size") and writes
pages into it. When it fills up, writes fail. Knowing current usage
vs capacity tells you when to rebuild with a larger map or run
`ark grow`.

Display: used bytes, total map size, and percentage. Human-readable
units (MB/GB).

## Index Composition

Status should show what's in the index:

- **Total chunks** — how many chunks are indexed across all files.
  More chunks = more granular search but larger DB.
- **Files by strategy** — how many files use each chunking strategy
  (e.g., 1200 lines, 73 chat-jsonl). Shows the mix of content types.
- **Sources configured** — how many source directories are in
  ark.toml. Quick sanity check that config is loaded.

## Output Format

All new fields appear after the existing ones. The output stays
plain text, one field per line, same as current status. Example:

```
files: 1273
stale: 0
missing: 12
unresolved: 3
chunks: 8451
sources: 5
strategies: lines=1200 chat-jsonl=73
map: 511 MB / 8 GB (6%)
server: not running
```

The `strategies` line lists each strategy with its file count,
space-separated. The `map` line shows used/total with percentage.

## Server Endpoint

`GET /status` returns the same data as JSON. New fields are added
to the existing StatusInfo response.
