# V2.5 Enhancements: Glob Sources, Strategy Mapping, File Logging

## Glob Sources

Config is intent, the database is state. Source directories in
ark.toml can be glob patterns — `~/.claude/projects/*/memory` means
"every project memory directory." The glob is a directive: it says
what *should* exist. The database only ever contains concrete
directories.

`ark sources check` is a lightweight reconciliation command. It
expands globs from config, diffs against what's already in the
database. New directories are added as sources. Directories that
no longer exist are flagged as MIA. No file scanning or indexing
happens — just directory listing against glob patterns. Cheap enough
to run on every `ark serve` startup.

A concrete source that was created by a glob cannot be removed
directly — that's an error ("managed by glob pattern X"). To change
what a glob manages, change the glob in ark.toml. When a glob is
removed from config, its formerly-resolved sources become orphans.
`ark sources check` detects and reports orphans for cleanup.

Glob expansion uses Go's `filepath.Glob` after `~` expansion on
each pattern.

### CLI

- `ark sources check` — expand globs, report new/MIA/orphan
  directories. Output is human-readable: lines like
  `+ /home/deck/.claude/projects/-foo/memory` (new),
  `- /home/deck/.claude/projects/-gone/memory` (MIA),
  `? /home/deck/old-notes` (orphan — no glob owns it).
  New sources are added to config automatically.
  MIA sources are flagged but not removed.
- `ark config add-source` accepts glob patterns. If the dir contains
  glob characters (`*`, `?`, `[`), it's stored as a glob source
  instead of validated with os.Stat.

### Config Format

```toml
# Concrete source
[[source]]
dir = "~/work/daneel"
strategy = "lines"
include = ["*.md"]

# Glob source — expands to multiple concrete sources
[[source]]
dir = "~/.claude/projects/*/memory"
strategy = "markdown"
```

No separate `[[glob_source]]` table. A source with glob characters
in its `dir` is a glob source. Keeps the config format flat and
simple.

### Server API

- `POST /config/sources-check` — run glob reconciliation, return
  JSON with new/MIA/orphan arrays

## Global Strategy Mapping

A top-level `strategies` table in ark.toml maps file glob patterns
to chunking strategy names. When scanning, the scanner checks each
file against the strategy map before falling back to the source's
default strategy.

Longest pattern wins — `docs/**/*.md` beats `*.md` because it's more
specific. Pattern length (in characters) is the tiebreaker. This is
poor-man's specificity: simple, predictable, no CSS-style priority
rules.

### Config Format

```toml
[strategies]
"*.md" = "markdown"
"*.jsonl" = "chat-jsonl"
"docs/**/*.md" = "markdown"
```

### Per-source strategies

A source may include its own `strategies` map that amends the global
one for files in that source. The per-source entries take precedence
over global entries for the same pattern — they overlay, not replace.

```toml
[[source]]
dir = "~/work/logs"
strategies = {"*.jsonl" = "chat-jsonl"}
```

### Behavior

- During scan, for each file: merge the source's strategies over the
  global strategies, then find the longest matching pattern
- If no pattern matches in the merged map, the file has no strategy
  and is chunked with the default (`lines`)
- Strategy names must be registered in microfts2 — error at scan
  time if a strategy name is unknown

## File Logging

The server logs to `~/.ark/logs/ark.log` in addition to stderr.
CLI commands that cold-start do not log to file — only the
long-running server needs persistent logs.

### Behavior

- Server creates `~/.ark/logs/` directory on startup if it doesn't
  exist
- Uses Go's `log.SetOutput` with an `io.MultiWriter` for both
  stderr and the log file
- Simple size cap: on startup, if the log file exceeds 10MB,
  truncate it (keep the last 1MB). No rotation, no external
  dependencies.
- Log format is Go's default: date time message
