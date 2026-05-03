# `ark serve -compact`

LMDB grows monotonically: free pages from deletions and overwrites
are reused for new writes, but the file itself never shrinks. After
months of indexing, reindexing, and removal the on-disk size drifts
well above what the live data needs.

`mdb_env_copy2` with the `MDB_CP_COMPACT` flag writes a fresh copy
of an LMDB environment containing only live pages. The result is a
defragmented database byte-equivalent to the original from the
caller's perspective.

`ark serve -compact` runs that compaction as a startup step before
the server begins handling requests. When the flag is absent,
startup is unchanged.

## Behavior

When `-compact` is passed:

1. Acquire the file lock on `~/.ark/` (the same one that prevents
   two `ark serve` instances from running simultaneously). If
   another process holds it, fail with the usual error.
2. Open the existing LMDB environment read-only.
3. Copy via `mdb_env_copy2` into a sibling path (`<dbpath>.compact`).
4. On success: atomic rename of the compacted file into the
   live database path, replacing the original. The original is
   removed.
5. On failure: leave the original in place, remove the partial
   copy, log the error, and continue startup with the
   uncompacted DB. Compaction is best-effort — a failure here
   must not block service.
6. Reopen the environment and proceed with normal startup.

The compaction window is single-process: server is not yet
listening, no clients are connected. No write transactions can be
in flight. Read-only is sufficient for the source environment.

Both microfts2 and ark databases are compacted (each is a separate
LMDB environment under `~/.ark/`).

## Why startup, not on-demand

Compaction is non-trivial — it copies every live page. Doing it
mid-session would require quiescing writes and is not worth the
complexity. Startup is the natural moment: the user has already
chosen to restart, and the additional cost is bounded.

## Reporting

Stdout messages, before the normal `serving on …` line:

```
compacting microfts2: 2.1 GB → 380 MB
compacting ark: 188 KB → 188 KB
```

When the post-compaction size is within 5% of the original, log
"already compact" and skip the rename for that environment. Avoids
unnecessary I/O on a fresh DB or one compacted recently.

## `auto_compact` in ark.toml

`ark.toml` accepts a top-level `auto_compact = true|false` boolean.
When set, it determines whether `ark serve` runs the compaction step
on startup, *unless* the CLI flag is supplied.

Resolution:

1. If the user supplied `-compact` or `-compact=false` on the
   command line, the flag value wins.
2. Otherwise, use `auto_compact` from ark.toml.
3. If `auto_compact` is not set in the toml, the default is
   `false` — preserves today's opt-in semantics.

The CLI `-compact` flag remains a bool so users can still type the
short form. Distinguishing "user supplied the flag" from "default
zero value" uses `fs.Visit` after `fs.Parse` to enumerate the flags
that were actually set.

`ark.toml` example:

```toml
auto_compact = true
```

**Placement matters.** TOML attaches every key after a `[table]`
header to that table until the next header. `auto_compact` is a
top-level field, so it must appear *before* any `[strategies]`,
`[[source]]`, `[schedule]`, etc. — otherwise the parser silently
reads it as `schedule.auto_compact` (or whatever the most recent
table was) and the value is ignored.

## Out of scope

- Scheduled compaction (cron-style). The toml setting is binary.
- Compaction of any non-LMDB storage (the file tree itself, blob
  files, etc.).
- Online compaction during a running session.
