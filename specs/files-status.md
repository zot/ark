# Files Status Enhancement

Enhance `ark files` to show per-file statistics useful for understanding
chunk distribution and tuning embedding context windows.

## Filtering

`--filter-files GLOB` and `--exclude-files GLOB` (repeatable) set the
base file set, same semantics as search. Positional glob arguments
further narrow the result within the base set.

Example: `ark files --status --filter-files '~/.claude' '*.md'`
means "from everything under ~/.claude, show me the markdown files."

When neither `--filter-files` nor positional patterns are given,
all indexed files are included.

## --status Output

`--status` currently shows G/S/M status per file. Enhance it to also
show bytes and chunk count:

```
G  12340  23  /path/to/file.md
S   8921  15  /path/to/other.md
M      0   0  /path/to/gone.md
```

Columns: status, bytes, chunks, path. Right-aligned numeric columns.

For missing files, bytes and chunks are 0.

## Verbose Mode (--detail)

With `--detail`, show per-file chunk size statistics after the summary line.
Only files matching the positional patterns get verbose output (if no
positional patterns, all files get it).

```
G  12340  23  /path/to/file.md
  min: 124  max: 891  mean: 536  median: 512  p90: 780  p95: 850
G   8921  15  /path/to/other.md
  min: 200  max: 1200  mean: 594  median: 550  p90: 900  p95: 1100
```

The verbose line is indented two spaces, compact single-line format.

Missing and zero-chunk files skip the verbose line.

## Data Source

File bytes come from `os.Stat` (actual file size on disk).
Chunk count and chunk content come from `DB.AllChunks(path)`.
Chunk sizes are `len(chunk.Content)` in bytes.

## --tokenize

Same as `ark status --chunks`: load the embedding model tokenizer,
count tokens instead of bytes. Applies to both the chunk count
column (unchanged — it's a count not a size) and the verbose stats.

## Help Text

The command help should document the new flags and behavior.
