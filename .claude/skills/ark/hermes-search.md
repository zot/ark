# Hermes Search Reference

Your tools are `~/.ark/ark` commands. Do not use grep, awk, wc, find,
or shell loops when an ark command exists.

The database is at `~/.ark`. The ark command is at `~/.ark/ark`.
If the server is not running, start it with `~/.ark/ark serve`.

## Search

```bash
# combined FTS + vector
~/.ark/ark search <query>

# exact text match
~/.ark/ark search --contains <text>

# regex search
~/.ark/ark search --regex <pattern>

# semantic search only
~/.ark/ark search --about <query>

# combine: FTS drives, regex post-filters
~/.ark/ark search --contains 'chunk' --regex '@to-project:.*\bark\b'
```

### Search Flags

```
--k N                     max results (default 20)
--scores                  show scores
--after YYYY-MM-DD        date filter
--chunks                  emit chunk text as JSONL
--files                   emit full file content as JSONL
--preview N               with --chunks: N-char preview window
--wrap NAME               wrap output in XML tags
--tags                    output tag names from matching chunks
--like-file <path>        find similar files via FTS density
--regex <pattern>         repeatable, all must match (AND)
--except-regex <pattern>  repeatable, any match rejects
--filter <query>          content-based positive filter (FTS, repeatable)
--except <query>          content-based negative filter (FTS, repeatable)
--filter-files <glob>     restrict to matching paths
--exclude-files <glob>    reject matching paths
--filter-file-tags <tag>  restrict to files with tag (fast, uses index)
--exclude-file-tags <tag> reject files with tag (fast, uses index)
```

Note: `--chunks` and `--files` are mutually exclusive.

### Output Formats

- Default: `path:startLine-endLine`
- `--scores`: appends score columns
- `--chunks`: JSONL `{"path","startLine","endLine","score","text"}`
- `--files`: JSONL `{"path","score","text"}`
- `--wrap NAME`: XML `<NAME source="path" lines="start-end">content</NAME>`
  Convention: `knowledge` for notes/docs/code, `memory` for conversation logs

JSONL conversation logs contain only human-readable content — user
messages, assistant responses, thinking blocks. Tool blocks and
metadata are stripped.

## Retrieval

```bash
# retrieve full file contents (any indexed project)
~/.ark/ark fetch --wrap knowledge <path>...

# expand context around a search hit
~/.ark/ark chunks <path> <range> [-before N] [-after N]
```

## File Discovery

```bash
# locate files by name pattern across all projects
~/.ark/ark files '**/requests/*.md'
~/.ark/ark files '**/*chunker*'

# with status (G=good, S=stale, M=missing)
~/.ark/ark files --status '**/pattern*'
```

## Tags

```bash
# all known tags with counts
~/.ark/ark tag list

# tag definitions (fast, from LMDB)
~/.ark/ark tag defs [TAG...]

# files containing tags (with sizes)
~/.ark/ark tag files <tag>...

# tag occurrences with context lines
~/.ark/ark tag files --context <tag>
```

### Tag Output Formats

- `ark tag list`: `tag\tcount`
- `ark tag files`: `path\tsize\ttag\tcount`
- `ark tag files --context`: `path\t@tag: context line`

Tags are `@word:` patterns in indexed files. The colon is required.
Tags are extracted per-line — keep tag values on a single line.

## Status

```bash
~/.ark/ark status                          # file/stale/missing counts
~/.ark/ark stale                           # files needing re-index
```

## Guidelines

- **Always use `--wrap` when retrieving content**
- **Always exclude jsonls:** `--exclude-files '*.jsonl'`
- Use `ark fetch --wrap knowledge` to load files, not the Read tool
- Return results concisely — summarize, don't dump raw output unless asked
