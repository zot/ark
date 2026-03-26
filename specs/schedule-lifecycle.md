# Schedule Lifecycle

Wiring the schedule data layer (day buckets, log files, date parsing)
into a working lifecycle. Language: Go. Environment: ark server.

See also: specs/scheduling.md (data model, CLI commands),
specs/pubsub.md (event scheduler), .scratch/SCHEDULING.md (brainstorm).

## Schedule Filtering

The `[schedule]` section in ark.toml gains file and lifecycle filters:

```toml
[schedule]
tags = ["standup", "dentist", "birthday", "chime"]
filter_files = "~/notes/**"
exclude_files = ["~/notes/drafts/**"]
lifecycle_include = "*"
lifecycle_exclude = ["chime"]
```

`filter_files` and `exclude_files` control which files are scanned
for schedule tags. Same narrow/carve semantics as search: filter
sets the scope, exclude carves exceptions. Tilde expansion applies.
When both are absent, all indexed files are eligible.

`lifecycle_include` and `lifecycle_exclude` control which schedule
tags get the full lifecycle treatment: schedule log entries,
check-gap monitoring, gap detection. Tags outside the lifecycle
still fire through pubsub — they just don't get logged or
monitored. Default: `lifecycle_include = "*"` (all schedule tags
participate in the lifecycle).

Both pairs use glob patterns on their respective domains (file
paths, tag names).

## EnsureArkSource Scoping

The hardcoded `~/.ark` source should only index content directories:

```
include = ["ark.toml", "schedule/**", "apps/**", "storage/**"]
```

This keeps data.mdb, lock files, logs, and archive directories
out of the index. Archived schedule logs go to
`~/.ark/schedule-archive/` — outside the include list, unindexed,
still searchable with xzgrep on disk. Rotation is: compress old
log, move to archive dir, done.

## Log Writing on Event Fire

When the scheduler fires an event for a lifecycle tag:

1. Convert `@ark-event-upcoming: DATE` to `@ark-event-fired: DATE`
   in the schedule log file
2. Append `@check-gap: DATE` in the same paragraph — colocation
   means the markdown chunker keeps them in the same chunk
3. Compute next occurrence, append `@ark-event-upcoming: NEXT`
   if no exception (deleted upcoming line) exists for that date
4. Re-index the log file so day buckets update

The schedule log file is identified by the source file path using
the encoding from specs/scheduling.md (tilde contraction, then
underscore/hyphen/slash escaping).

For non-lifecycle tags (excluded by `lifecycle_exclude` or not
matched by `lifecycle_include`), the scheduler fires the event
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
re-materializes day buckets for affected files.

Detection: store the serialized `[schedule]` config in the LMDB
settings record. On config reload (startup, ark.toml fsnotify),
compare current vs stored:

- Tags added: scan files with the new tag via tag index, write
  schedule log entries and day buckets
- Tags removed: clear day buckets for files with that tag, remove
  schedule log chunks
- Defaults changed (durations): re-materialize with new durations
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
