# Schedule Lifecycle

Wiring the schedule data layer (priority queue, log files, date parsing)
into a working lifecycle. Language: Go. Environment: ark server.

See also: specs/scheduling.md (data model, CLI commands),
specs/pubsub.md (event scheduler), .scratch/SCHEDULING.md (brainstorm).

## Schedule Filtering

Top-level `[schedule]` carries cross-cutting filter knobs; per-tag
overrides and the lifecycle/log_cap/suppress knobs live in
per-tag `[schedule.tag.X]` blocks. See
[scheduling.md](scheduling.md) for the full per-tag schema.

```toml
[schedule]
filter_files = "~/notes/**"
exclude_files = ["~/notes/drafts/**"]

[schedule.tag.standup]
default_duration = "15m"

[schedule.tag.chime-1m]
lifecycle = "tmp"    # opt this chime into ephemeral audit
```

`filter_files` and `exclude_files` (top-level) control which files
are scanned for schedule tags. Same narrow/carve semantics as
search: filter sets the scope, exclude carves exceptions. Tilde
expansion applies. When both are absent, all indexed files are
eligible. Per-tag overrides go in `[schedule.tag.X] filter_files` /
`exclude_files`.

`[schedule.tag.X] lifecycle` selects audit destination per tag —
`"disk"` (default), `"tmp"` (ephemeral, evicted on restart, trimmed
to `log_cap` lines, default 1000), or `"none"` (fire-and-forget — no
audit anywhere). `lifecycle = "none"` tags still fire through pubsub.
Suppress (`suppress = true`, default `false`) stops firing without
dropping the declaration. See
[migrations/complete/006-schedule-record-only.md](migrations/complete/006-schedule-record-only.md) for the full record-only model.

## EnsureArkSource Scoping

The hardcoded `~/.ark` source's include list whitelists
ark-managed standard files at the top level and, under each
content directory, only the file extensions that carry
text content the indexer can chunk:

```
include = ["ark.toml", "chimes.md", "tags.md",
           "schedule/**/*.md",
           "apps/**/*.lua", "apps/**/*.js", "apps/**/*.html",
           "apps/**/*.css", "apps/**/*.md",
           "storage/**/*.md", "storage/**/*.pdf",
           "external/**/*.md", "skills/**/*.md"]
```

The top-level entries cover the standard files ark owns directly:
`ark.toml` (user-edited config), `chimes.md` (auto-created chime
declarations, see [chimes.md](chimes.md)), and `tags.md`
(starter tag bible, written from the install seed). Listing them
explicitly means ark-managed standard files are indexed
regardless of the user's `[[source]]` configuration — pulling
`~/.ark` out of `[[source]]` doesn't break chimes or tag
suggestions.

The directory entries are scoped per file extension, not as a
bare `**` recursion. Under each content directory ark indexes
only the extensions whose chunkers it ships:

- `schedule/**/*.md` — schedule log files (markdown chunker).
- `apps/**/*.{lua,js,html,css,md}` — Frictionless app sources
  (Lua handlers, web assets, in-app markdown).
- `storage/**/*.{md,pdf}` — app-managed user data ark can
  chunk (notes, PDFs handled by the pdf chunker).
- `external/**/*.md` — mirror chunks from external sources.
- `skills/**/*.md` — ark-managed agent skill files. The
  `~/.ark/skills/` entries are symlinks into the repo's skill
  sources; indexing them under the `~/.ark/skills/` path lets a
  hermetically-sealed subagent bootstrap by fetching its skill
  (`ark fetch ~/.ark/skills/<skill>.md`) — `fetch` serves only
  indexed content and does not resolve symlinks, so the
  symlink path must itself be indexed. The skill content is
  consequently indexed twice (once under the repo source, once
  under `~/.ark/skills/`); accepted as the cost of keeping the
  agent-facing path fetchable.

This keeps non-text artifacts out of the index by construction:
Fossil checkout files (`.fslckout`, `*.fossil`), binary office
documents (`*.docx`), undo-tree dumps, lock files, build outputs,
index pages, and the like. Each was previously surfaced as an
`fts add ...: chunk "1-1" contains invalid UTF-8` line on every
startup — extension-scoped includes drop them before they reach
the chunker. Archived schedule logs go to
`~/.ark/schedule-archive/` — outside the include list,
unindexed, still searchable with xzgrep on disk. Rotation is:
compress old log, move to archive dir, done.

Adding a new chunkable extension under one of these directories
means appending the matching `dir/**/*.ext` pattern to
`arkSourceIncludePatterns` — not flipping to `**` and excluding
binaries afterward.

### Override path — commented in `install/ark.toml`

The synthetic source is the default behavior, but a user may
need to override it (add a new extension under `apps/`, add a
top-level ark-managed file, etc.). `install/ark.toml` carries
the same include list as a commented `[[source]] dir = "~/.ark"`
block, in UNIX-config style — uncomment and edit to replace the
synthetic defaults. `EnsureArkSource` skips its synthetic
addition when any `[[source]]` already targets the database
directory, so a user-defined block wins.

The commented example in `install/ark.toml` and the
`arkSourceIncludePatterns` constant in `config.go` must stay in
sync — both express the same default. Edits to one are edits to
the other.

## Log Writing on Event Fire

When the scheduler fires an event for a lifecycle tag:

1. Convert `@ark-event-upcoming: DATE` to `@ark-event-fired: DATE`
   in the schedule log file
2. Append `@check-gap: DATE` in the same paragraph — colocation
   means the markdown chunker keeps them in the same chunk
3. Compute next occurrence, append `@ark-event-upcoming: NEXT`
   if no exception (deleted upcoming line) exists for that date
4. Re-index the log file so the priority queue updates (EnsureUpcoming)

The schedule log file is identified by the source file path using
the encoding from specs/scheduling.md (tilde contraction, then
underscore/hyphen/slash escaping).

For tags with `lifecycle = "none"`, the scheduler fires the event
through pubsub but skips steps 1-4. No log entry, no check-gap.

## Check-Gap and Ack Resolution

`@check-gap: DATE` in a schedule log chunk means "this event
fired but hasn't been acknowledged." The lifecycle subscribes to
`@ack:` tag changes in source files. When an ack arrives that
covers a fired date:

1. Find the corresponding schedule log chunk (by tag name and
   source path)
2. Remove the `@check-gap:` line for that date
3. Re-index the log file

On startup, scan schedule logs for unresolved `@check-gap:` entries.
These are events that fired but were never acknowledged. For entries
within the lookback window (default 7 days), append to
`tmp://watchdog/missed-events`. Franklin subscribes to `@watchdog:`
and surfaces them.

No polling — ack resolution is subscription-driven. The check-gap
is a marker, not a timer. Its presence means unresolved; its
absence means handled.

## Config Change Detection

When ark.toml's `[schedule]` section changes, the server
re-evaluates affected files and re-arms the in-memory priority queue.

Detection: store the serialized `[schedule]` config in the
settings record. On config reload (startup, ark.toml fsnotify),
compare current vs stored:

- Tags added: scan files with the new tag via tag index, write
  schedule log entries
- Tags removed: remove schedule log chunks for files with that tag
- Defaults changed (durations): re-evaluate affected entries and re-arm the queue with new durations
- Filter changes: re-evaluate which files participate, add/remove
  schedule log entries accordingly

The comparison is on the serialized config — any change triggers
a re-scan of affected tags, not a full rebuild.

## Materialization Strategy

Only the next occurrence of a recurring event is materialized in
the schedule log. On startup, compute missed occurrences between
last-fired and now, surface them as missed events, then materialize
just the next one.

The calendar UI computes virtual recurring items on the fly from
the recurrence spec for display purposes — this is Lua-side work,
deferred until the calendar view is built.
