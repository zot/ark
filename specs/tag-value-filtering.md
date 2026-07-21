# Tag Value File Filtering

Add path-glob filtering to `ark tag values`.
Language: Go. Environment: ark CLI.

## Problem

`ark tag values` shows all values for a tag across every indexed
file. When working with a specific subset of files (e.g., only
markdown files, or excluding JSONL logs), there's no way to scope
the output.

## Flags

Same `-files` filter stack as `ark tag files` and `ark search`:

- `-files GLOB` — only count values from files matching the glob
  (repeatable; `-with` polarity is the default)
- `-without -files GLOB` — exclude files matching the glob
  (repeatable)

Both are composable: positive rows narrow first, `-without` rows remove
from the result. With no row, behavior is unchanged. Globs follow the
project-wide rules in [main.md](main.md#glob-patterns) — anchored
CLI-side to the current directory, so `/**/*.md` is the explicit
"markdown anywhere" form.

**The boolean is `--show-files`, not `--files`.** `parseFilterStack`
runs before `flag.Parse` and normalizes `--files` to `-files`, so a
boolean named `files` would be swallowed as a filter row and would eat
the following TAG as its glob — a silent misparse rather than an error.
`ark search` hit the same collision and resolved it the same way, by
renaming its boolean to `--file-content`.

## Behavior

When filtering is active, resolve fileids to paths for each value
and apply the glob filters. Recompute the count from matching files
only. Values that have zero matching files after filtering are
omitted from output.

`--show-files` composes with filtering — it lists only the files that
passed the filter.

## Usage

```
ark tag values -files '/**/*.md' status
ark tag values -without -files '/**/*.jsonl' --show-files from-project
```
