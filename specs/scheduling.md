# Scheduling

Go layer for date-indexed events: parsing, LMDB indexing, scheduler
integration, acknowledgments. Language: Go. Environment: ark server
process, LMDB database.

See also: specs/pubsub.md (event scheduler mechanics, time value
grammar, recurring format), .scratch/SCHEDULING.md (design brainstorm).

## Schedule Tag Configuration

ark.toml declares which tags carry dates:

```toml
[schedule]
tags = ["dentist", "standup", "birthday", "release", "review"]

[schedule.defaults]
dentist = "1h"
standup = "15m"
birthday = "all-day"
```

Tags listed in `[schedule]` are parsed for dates at index time. Tags
not listed are never date-parsed — zero overhead. Default durations
apply when a tag value has no explicit `..` duration.

Adding or removing a tag from the schedule config triggers
re-materialization of day-bucket entries for all files containing
that tag.

## Date and Duration Parsing

The `..` range operator expresses durations, consistent with `@ack:`
ranges:

```
@dentist: 2026-04-15 09:00..10:30 cleaning
@meeting: 2026-04-15 09:00..2026-04-17 17:00 offsite retreat
@standup: every Monday at 09:00..09:15
```

Parser distinguishes:
- `TIME..TIME` — same-day duration (09:00..10:30)
- `TIME..DATE TIME` — multi-day span
- `DATE TIME` with no `..` — point-in-time, use default duration
- `DATE` with no time — all-day event

The `..` is unambiguous: no spaces around it, timestamps on both
sides. Everything after the time portion is description text,
preserved on edits.

### Parsing strategy

1. Split on `..` first (range/duration detection)
2. Parse each side with itlightning/dateparse (token-trimming loop
   to separate date from description text)
3. Existing `computeNext` handles recurring specs
4. Remainder after date = description text (preserved on round-trip)

Use itlightning/dateparse — actively maintained fork of
araddon/dateparse. Handles dozens of date formats without specifying
which: ISO 8601, `Apr 15 2026`, `4/15/26`, etc.

### Anchored-only natural language

Relative expressions are only allowed after an absolute anchor:

```
@retreat: Feb 2 2026..one week later offsite planning
@sprint: 2026-04-01..two weeks later
```

"One week later" is relative to `Feb 2`, not "now" — re-indexing
always resolves to the same date. Bare relative dates (`next
Tuesday`) are not supported because they shift on re-index.

Small vocabulary: `N days/weeks/months later`. Implementable as
arithmetic on the parsed start date — no NLP library needed.

## Schedule Log

`~/.ark/schedule/` directory holds log files — the on-disk source of
truth for event instances. One log file per source file that contains
schedule tags. Rotatable without touching the zettelkasten.

### Log format

Each event definition gets a chunk in its log file:

```markdown
@ark-event: standup
@ark-event-source: ~/notes/schedule.md
@ark-event-spec: every Monday at 09:00..09:15

@ark-event-fired: 2026-03-10 09:00
@ark-event-fired: 2026-03-17 09:00
@ark-event-upcoming: 2026-03-24 09:00
@ark-event-upcoming: 2026-03-31 09:00
```

The log file is a regular ark file — tagged, indexed, searchable.

### Lifecycle

- **Index time** — when a source file with a schedule tag is indexed,
  the scheduler ensures a log chunk exists with `@ark-event-upcoming:` entries
  through the forward window (default 6 months).
- **Fire** — convert `@ark-event-upcoming:` → `@ark-event-fired:`, append next
  `@ark-event-upcoming:` if there isn't already one for that date.
- **Crank-forward on startup** — scan log for `@ark-event-upcoming:` entries
  in the past, convert to `@ark-event-fired:`, add new `@ark-event-upcoming:` forward.
- **Scheduling exceptions** — delete an `@ark-event-upcoming:` line to skip
  that occurrence. Edit the date to move it. Just file edits,
  indexed normally. Crank-forward checks before adding — no
  duplicate `@ark-event-upcoming:` entries.

### Source of truth chain

- **Source file** — the pattern (`@standup: every Monday at 09:00`)
- **Log file** — the concrete instances (`@ark-event-upcoming:`, `@ark-event-fired:`)
- **Day buckets** — derived from log entries, rebuildable
- **`@ack:` in source file** — human record

Files all the way down. Rebuilding the index loses nothing.

### Log rotation

Old `@ark-event-fired:` entries accumulate. The log directory is designed for
rotation — archive or delete old log files. The source file's `@ack:`
entries are the durable human record; the log is the machine record.

## Day-Bucket LMDB Indexing

A 1D quadtree at day granularity. Day buckets are derived from
`@ark-event-upcoming:` and `@ark-event-fired:` entries in the schedule log (plus
one-shot events directly from source files).

```
Key:   TD|20260415|fileid|tag
Value: {start, end, tag, summary, path, allDay}
```

A 3-day event spanning Apr 15-17 gets 3 entries. Calendar query for
March = seek `TD|20260301`, scan to `TD|20260331`. No post-filtering.

Worst case is bounded by human scheduling density — a week-long
vacation is 7 entries, a year of weekly standups is ~52 in the
materialization window.

### Reverse index for deletion

On re-index, old day-bucket entries must be cleaned up (tags removed,
dates changed, file deleted). Single reverse-index key per file:

```
Key:   TF|fileid
Value: [20260415, 20260416, 20260417]
```

Re-index flow:
1. Get `TF|fileid` — one read, all dates
2. For each date, delete `TD|date|fileid|*`
3. Delete `TF|fileid`
4. Write new TD + TF from current file content

## Scheduler Integration

The scheduler reads schedule log files — not subscriptions, not LMDB
registries. The log files are the source of truth for what's upcoming.

### Startup scan

On server start, scan `~/.ark/schedule/` for log files. Read
`@ark-event-upcoming:` entries, populate the scheduler priority queue. Any
`@ark-event-upcoming:` in the past gets converted to `@ark-event-fired:` and the next
occurrence is computed and appended.

### Crank-forward on fire

When a recurring event fires:
1. Convert `@ark-event-upcoming:` → `@ark-event-fired:` in the log file
2. Compute next occurrence
3. Append `@ark-event-upcoming:` if none exists for that date (exception check)
4. Re-index the log file (new day-bucket entries written)
5. Re-enqueue in the priority queue

### Event payload

Events delivered through the publisher carry their nature: whether
this is a scheduled event firing at its time vs a tag-change
notification. The receiver needs to distinguish "your dentist
appointment is now" from "someone moved your dentist appointment."

## Remove Scheduling from Subscriptions

The `--scheduled` and `--recurring` flags are removed from the
subscribe CLI and API. The `ScheduleMode` type, `ScheduleNone`,
`ScheduleOneShot`, `ScheduleRecurring` constants, and the `Schedule`
field on `TagSub` are removed from pubsub.go. `ScanForSub` is
removed from scheduler.go — replaced by the log-based startup scan.

Subscriptions retain: `--tag`, `--value`, `--filter-files`,
`--except-files`, `--cancel`, `--list`, `--stats`.

## Acknowledgments

`@ack:` tags in the same chunk as an event in the source file:

```markdown
@standup: every Monday at 09:00
@ack: ..Mar 10 2026
@ack: Mar 17 2026 Bill was out
@ack: Mar 24 2026..Mar 31 2026 discussed scheduling
```

Syntax:
- `@ack: ..DATE [text]` — open start, first ack only
- `@ack: DATE [text]` — single date
- `@ack: DATE..DATE [text]` — closed range
- Open ends (`DATE..`) never allowed
- Multiple `@ack:` tags per chunk, no blank lines between them

Gaps between acknowledged dates = missed/unacknowledged occurrences.
This is the staleness signal — no separate state file needed.

## Gap Detection

Compare `@ark-event-fired:` entries in the log against `@ack:` entries in the
source file. Unacknowledged fired dates within a lookback window
(default 7 days) are surfaced as recent misses. Franklin's morning
briefing data — "You had a dentist appointment Saturday, did it
happen?"

## Lua APIs

```lua
-- Query: items overlapping a date range
local items = mcp:scheduled("2026-03-01", "2026-03-31")
-- returns: [{date, endDate, tag, summary, path, recurring, allDay}]

-- Mutate: change a scheduled item's date (preserves trailing text)
mcp:reschedule(path, tag, newDate, newEndDate)

-- Completion: tag names and values from the index
mcp:tagComplete(prefix)

-- File info: indexed? what tags? what schedule?
mcp:fileStatus(path)

-- Subscribe: UI-side tag-change subscription with callback
mcp:subscribe({tag="status", value="open|accepted"}, callback)
mcp:subscribe({tag="dentist", filterFiles="~/notes/**"}, callback)
```

`mcp:subscribe` has full parity with the CLI subscribe flags (minus
the removed scheduled/recurring). Callback fires on tag changes so
views refresh automatically.
