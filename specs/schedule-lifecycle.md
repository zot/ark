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

`~/.ark` is a hardcoded source whose include list is an explicit
allowlist — schedule logs are indexed because `schedule/**/*.md` is one
of its entries, not because the source is recursive.

**The allowlist and its per-entry rationale moved to
[source-monitoring.md](source-monitoring.md) §"What inside ~/.ark is
indexed"** (2026-07-27), which is the spec that owns "~/.ark is always a
source". It lived here only because `schedule/**` happened to be the
first entry, which meant nobody looking for "what does ark index in its
own home" could find it.

What stays schedule's business: archived logs go to
`~/.ark/schedule-archive/`, which is deliberately **outside** the
allowlist — rotated logs leave the index automatically and stay
searchable on disk with xzgrep. Rotation is: compress old log, move to
archive dir, done (R963).

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

Only the next occurrence of a recurring event is live at any time. It
is held in the in-memory priority queue, not written into the schedule
log (the log no longer carries an upcoming marker; the queue is the
authoritative "what's next"). On startup `ScanScheduleLogs` re-arms the
queue from each surviving chunk's recurrence spec via
`crankForwardAndEnqueue`, and separately scans the audit logs for
unresolved `@check-gap:` entries within the lookback window, appending
those to `tmp://watchdog/missed-events`. Missed firings surface through
that check-gap audit rather than being recomputed from recurrence math.

The calendar UI computes virtual recurring items on the fly from
the recurrence spec for display purposes — this is Lua-side work,
deferred until the calendar view is built.
