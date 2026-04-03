# Tag Value File Filtering

Add `--filter-files` and `--exclude-files` to `ark tag values`.
Language: Go. Environment: ark CLI.

## Problem

`ark tag values` shows all values for a tag across every indexed
file. When working with a specific subset of files (e.g., only
markdown files, or excluding JSONL logs), there's no way to scope
the output.

## Flags

Same pattern as `ark tag files` and `ark search`:

- `--filter-files GLOB` — only count values from files matching
  the glob (repeatable)
- `--exclude-files GLOB` — exclude files matching the glob
  (repeatable)

Both are composable: filter narrows first, exclude removes from
the result. Without either flag, behavior is unchanged.

## Behavior

When filtering is active, resolve fileids to paths for each value
and apply the glob filters. Recompute the count from matching files
only. Values that have zero matching files after filtering are
omitted from output.

The `-files` flag composes with filtering — it shows only the
files that passed the filter.

## Usage

```
ark tag values --filter-files '*.md' status
ark tag values --exclude-files '*.jsonl' --files from-project
```
