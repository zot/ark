# Scheduling

Go layer for date-indexed events: parsing, index writes, scheduler
integration, acknowledgments. Language: Go. Environment: ark server
process, ark index.

See also: specs/pubsub.md (event scheduler mechanics, time value
grammar, recurring format), .scratch/SCHEDULING.md (design brainstorm).

## Schedule Tag Configuration

Schedule tags are declared via per-tag blocks in `ark.toml`. The mere
presence of a `[schedule.tag.X]` block declares X as a schedule tag.
Each block accepts per-tag knobs (lifecycle, log_cap,
default_duration, filter_files, exclude_files, suppress); all are
optional and take these defaults when unset:

- `lifecycle = "disk"` — audit fired events to a disk log. See
  [schedule-lifecycle.md](schedule-lifecycle.md) for the other values.
- `log_cap = 1000` — fired-entry lines kept per chunk before the oldest
  half is trimmed.
- `default_duration` unset — an untimed value gets no default span: a
  `DATE TIME` with no `..` is point-in-time, a bare `DATE` is all-day.
- `filter_files` / `exclude_files` unset — the tag inherits the top-level
  `[schedule]` scan scope.
- `suppress = false` — the tag arms normally.

```toml
[schedule]
exclude_files = ["*.jsonl", "~/.claude/**"]

[schedule.tag.dentist]
default_duration = "1h"
filter_files = ["~/notes/**"]

[schedule.tag.standup]
default_duration = "15m"
exclude_files = ["~/work/ark/specs/**"]

[schedule.tag.birthday]
default_duration = "all-day"

[schedule.tag.chime-1m]   # lifecycle defaults to "disk"
```

Tags declared via `[schedule.tag.X]` blocks are parsed for dates at
index time. Tags without a block are never date-parsed — zero
overhead. Default durations apply when a tag value has no explicit
`..` duration.

The legacy `tags = [...]` array and `[schedule.defaults]` table are
not parsed (retired by the schedule-record-only migration; see
[migrations/complete/006-schedule-record-only.md](migrations/complete/006-schedule-record-only.md)).

### Per-tag filtering

Each tag can override the global filter/exclude with `[schedule.tag.NAME]`.
A tag with no override inherits the global filter. Global excludes
always apply — per-tag filters narrow further, they don't bypass
global excludes.

### tmp:// schedule logs

Schedule tags in tmp:// files produce tmp:// schedule logs
(`tmp://schedule/HASH.md`) instead of disk logs. Server restart
kills both the tmp:// file and its schedule state — no orphaned
log files.

### Deferred schedule processing

Schedule item processing (EnsureUpcoming) is deferred outside the
DB closure actor. During indexing, schedule items are accumulated.
After scan/refresh completes, they are drained and processed in a
goroutine. This prevents file I/O from blocking the actor during
indexing. The `handleScan` and `handleRefresh` endpoints also drain.

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

1. Strip date keywords (see below) before parsing
2. Split on `..` first (range/duration detection)
3. Parse each side with itlightning/dateparse (token-trimming loop
   to separate date from description text)
4. Existing `computeNext` handles recurring specs
5. Remainder after date = description text (preserved on round-trip)

Use itlightning/dateparse — actively maintained fork of
araddon/dateparse. Handles dozens of date formats without specifying
which: ISO 8601, `Apr 15 2026`, `4/15/26`, etc.

### Malformed datetime handling

`dateparse` is permissive: for some malformed inputs it silently
returns a wrong-but-valid time rather than an error. Three cases are
caught so a typo never becomes a silently mis-scheduled event:

- **Dash-joined date and time.** `2026-05-28-13:45` is a common typo
  for `2026-05-28T13:45`. `dateparse` reads the `-13:45` as a
  *timezone offset*, not a time-of-day, and returns midnight. A date
  token of the shape `YYYY-MM-DD-HH` or `YYYY-MM-DD-HH:MM` is
  normalized — the separator hyphen is rewritten to `T` — and
  re-parsed, yielding the intended time. This is forgiving by design:
  an existing schedule entry written with the dash form starts firing
  at the right time instead of at midnight.

- **Date with a timezone but no time-of-day is an error.** A value
  that parses to a date carrying a timezone offset but no clock time
  (e.g. `2026-05-28Z`, or a dash form that normalization did not
  rescue) is rejected rather than interpreted as midnight on that
  date. A bare date with no timezone is still a normal all-day event —
  only the date-plus-timezone shape is meaningless and errors.

- **Ambiguous month/day ordering is an error.** A value like
  `3/1/2014` could mean March 1 or January 3. After a permissive parse
  succeeds, the same token is re-checked with `dateparse.ParseStrict`;
  if it reports an ambiguous mm/dd vs dd/mm format, the value is
  rejected rather than guessed. ISO `YYYY-MM-DD` and spelled-out
  months (`Apr 15 2026`) are unambiguous and unaffected.

These checks apply wherever a schedule tag value is parsed
(`ParseDateValue`): `ark schedule add`/`change`, the source-file scan,
and `ark schedule search`'s DATE argument.

### Date keyword stripping

`dateparse` handles date formats but not English prepositions.
Before passing to dateparse, recognized keywords are stripped from
the front of a date expression:

- start keywords: `from`, `starting`, `beginning`, `after`, `on`
- end keywords: `to`, `until`, `through`, `ending`, `before`, `by`

This benefits all date parsing, not just bounded recurrences:

```
@dentist: on April 15 at 9am cleaning
@vacation: from June 1..June 14
@review: by March 30 submit feedback
```

Keywords are only stripped when followed by a parseable date —
`"on time"` doesn't lose its `on`. The stripping is a single
pass before `parseDateTrimming`, not embedded in the trimming
loop.

### Bounded recurring events

Recurring events can have start bounds, end bounds, or both.
Either order is accepted — bounds can appear before or after the
recurrence spec:

```
@standup: every Sat at 9:30am starting Mar 2 2026
@standup: every Monday at 5pm until May 30
@standup: from March 1 to May 30 every Monday at 5pm
@standup: every Monday at 5pm from March 1 to May 30
```

The `..` range form also works for bounds in either order:

```
@standup: 2026-03-01..2026-05-30 every Monday at 09:00
@standup: every Monday at 09:00 2026-03-01..2026-05-30
```

Semantics:
- Start-only = no end bound, materialize through forward window
- End-only = start from first occurrence after now
- Both = bounded window, either can be omitted independently
- Materialization stops at `min(endDate, now + forwardWindow)`

`computeNext` gains `notBefore` and `notAfter` parameters. It
returns zero time when the next occurrence exceeds `notAfter`.

The schedule log records parsed bounds as explicit tags so the
scheduler reads them directly on startup without re-parsing
natural language from the source file:

```markdown
@ark-event-spec: every Monday at 09:00
@ark-event-start: 2026-03-01
@ark-event-end: 2026-05-30
```

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

### Log file naming

Log file name is derived from the source file's path (first path
name in the fileid entry's list, after tilde contraction):

- Replace each `_` with `__`
- Replace each `-` with `_-`
- Replace each `/` with `-`

So `~/notes/schedule.md` → `~-notes-schedule.md`,
`~/work/my-project/cal.md` → `~-work-my_-project-cal.md`.

Reversible: `__` is always a literal underscore, `_-` is always
a literal hyphen, bare `-` is always a path separator. Tilde
contraction (`/home/user/` → `~/`) happens before encoding,
keeping the names compact and readable.

### Log format

Each event definition gets a chunk in its log file. The chunk is a
pure audit record — fires and spec-change history. The active spec
lives in the source file at `@ark-event-source:`; the chunk's
spec-history markers preserve what it *was* at each change. There is
no `@ark-event-upcoming:` — the in-memory priority queue is the
authoritative "what's next" source.

```markdown
@ark-event: standup
@ark-event-source: ~/notes/schedule.md

@ark-event-spec-initial: 2026-03-01 14:32 — every Monday at 09:00..09:15
@ark-event-fired: 2026-03-10 09:00
@ark-event-fired: 2026-03-17 09:00
@ark-event-spec-changed: 2026-03-20 11:00 — every Tuesday at 09:00..09:15
@ark-event-fired: 2026-03-24 09:00
```

The active spec is read from the source file. Reader contract for the
chunk: walk in document order; the most-recent `@ark-event-spec-initial:`
or `@ark-event-spec-changed:` marker before any fired entry was the
spec in effect when that fire happened.

See [migrations/complete/006-schedule-record-only.md](migrations/complete/006-schedule-record-only.md)
for the full migration that introduced this shape.

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

## Month Buckets (in-memory)

Replace day buckets (TD/TF records) with in-memory month
buckets. The scheduler fires from log file upcoming entries.
Calendar and CLI compute events from recurrence specs. Month
buckets are the skip list for fast range queries.

Computed on startup from schedule log specs. One entry per month
per recurring event — the first occurrence in that month. Query
flow:

1. Find the month bucket at or before the range start
2. Crank forward from that point to generate all events in range
3. Apply scheduling exceptions (@remove:, @add:)
4. Merge ack status from source file @ack: tags

A 10-year overview is 120 buckets per event, milliseconds to
compute. In-memory only — derived from specs, recomputable on
restart. No index storage needed.

Enables `ark schedule search` without a running server: open the
DB, read schedule log files, build month buckets, compute the
query. Same code path, no server dependency.

Remove: Store.WriteDayBuckets, QueryDayBuckets, ClearDayBuckets,
WriteDayBucketsForFile, dayBucketsFromLogFile, TD/TF index records.

## Scheduling Exceptions

Cancel or reschedule individual occurrences without changing the
recurrence spec. Exception tags live in the same chunk as the
event in the source file:

```
@standup: every Monday at 09:00
@ack: ..Mar 24 2026
@remove: 2026-03-31 snow day
@add: 2026-04-01 moved after snow day
```

Short names (`@remove:`, `@add:`, `@ack:`) are scoped by the
event chunk — no `@ark-event-` prefix needed in source files.
See tags.md "Tag scoping" for the naming convention.

Exception tags carry a date and optional trailing description text,
same format as schedule tag values. Parsed at index time and stored
in the event struct alongside the recurrence spec.

When computing occurrences (month bucket generation, schedule
search, crank-forward):
- `@remove: DATE` — skip that date, don't generate an occurrence
- `@add: DATE` — include an extra occurrence on that date

The source file is the authority. The schedule log reflects the
computed result — its upcoming entry accounts for exceptions.

## Scheduler Integration

The scheduler reads schedule log files — not subscriptions, not index
registries. The log files are the source of truth for what's upcoming.

### Startup scan

On server start, scan `~/.ark/schedule/` for log files. Read
`@ark-event-upcoming:` entries, populate the scheduler priority queue. Any
`@ark-event-upcoming:` in the past gets converted to `@ark-event-fired:` and the next
occurrence is computed and appended.

### Startup reconcile against current config

The startup scan treats each `@ark-event-upcoming:` entry as
joint state with `ark.toml` and the recurrence spec — it can be
correct, incorrect, or missing — and reconciles per chunk against
the current `[schedule]` configuration. For each chunk in each log
file:

- If the chunk's tag is no longer in `[schedule].tags`, or the
  source path no longer passes the schedule filter for that tag
  (`MatchesScheduleFilterForTag(source, tag)` is false), **drop
  the chunk**. The tag has been retired or the source excluded
  since the log was written; the entry no longer reflects user
  intent.
- Otherwise, validate the upcoming entries normally: past
  upcomings convert to fired; if no future upcoming exists for a
  recurring spec, compute the next occurrence from
  `@ark-event-spec:` + now and append one
  `@ark-event-upcoming:`.

If a log file's chunks are all dropped, **delete the log file**.
Avoids ghost log shells once their last source is excluded.

The dropped-chunk policy is a config-driven retirement: tightening
`[schedule].tags` or `[schedule].exclude_files` retroactively
prunes the matching log entries on the next startup. The
source-removal check is the cheap config check, not "is the
schedule tag still present in the source file content" — re-
parsing the source file is the indexer's job on its next refresh.

### Single upcoming entry

The schedule log maintains exactly one `@ark-event-upcoming:` entry per
recurring event — the next occurrence. The calendar UI computes future
dates from `@ark-event-spec:`. No forward window materialization.

After server downtime, crank-forward converts all past upcoming
entries to fired, then writes one new upcoming for the next
occurrence. Multiple fired entries may be written (catch-up), but
only one upcoming exists at a time.

### Crank-forward on fire

When a recurring event fires:
1. Convert `@ark-event-upcoming:` → `@ark-event-fired:` in the log file
2. Compute next occurrence
3. Write one `@ark-event-upcoming:` if the event hasn't ended
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

Subscriptions retain: `--tag` (sigil-form, see
[file-tag-filter.md](file-tag-filter.md)), `--file-tag`,
`--filter-files`, `--exclude-files`, `--cancel`, `--list`,
`--stats`. `--value` is retired (T61–T63); the value-match piece
is encoded in the `--tag` sigil.

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

## Schedule CLI

`ark schedule` subcommand exposes scheduling to agents and the
console. Franklin shells out to these; the calendar UI calls the
equivalent Lua APIs.

### ark schedule search

```
ark schedule search DATE [--tag TAG] [--gaps] [--json]
```

Query day buckets for events. DATE uses the same format as schedule
tag values: single date, range with `..`, keyword prefixes. Trailing
text is ignored.

```
ark schedule search 2026-04-15
ark schedule search 2026-04-01..2026-06-30
ark schedule search "April 2026"
ark schedule search 2026-04-01..2026-06-30 --tag standup
```

Output is markdown by default (crank-handle style), JSON with
`--json`. Events are computed from recurrence specs and month
buckets — no day buckets needed. Works without a running
server.

Each event renders as a bullet. The time range collapses when it
carries no extra information:

- Start and end differ, with a summary: `- 09:00–10:30: dentist`
- Start equals end (a point in time): `- 13:45: ping` — the
  `–END` half is dropped.
- No summary: the trailing `: ` is dropped — `- 09:00–10:30` or
  `- 13:45`.

This keeps zero-duration, no-summary events (chime ticks landing in
the window) from rendering as `- 13:45–13:45:` with a dangling
colon. All-day events keep the `- all day: SUMMARY` form, with the
same trailing-colon drop when the summary is empty.

`--tag` filters to a specific schedule tag. `--gaps` shows only
events with unacknowledged occurrences — computed by comparing
the recurrence spec against `@ack:` dates in the source file.

### ark schedule parse

```
ark schedule parse DATE
```

Parse a date expression and show the result. Diagnostic tool for
verifying how schedule tag values are interpreted. Shows start, end,
description text, and for recurring specs: the recurrence pattern,
bounds, and next occurrence.

### ark schedule tags

```
ark schedule tags [--values]
```

Show configured schedule tags, default durations, lifecycle status,
and per-tag filter/exclude patterns.

With `--values`: also show each tag's current values from source
files and next upcoming date from schedule logs. Reads log files
directly — no server needed.

### ark schedule change

```
ark schedule change PATH TAG NEWSTART [NEWEND] [--dry-run]
```

Rewrite the date in a schedule tag value, preserving trailing
description text. Re-indexes the file after modification. PATH is
the source file, TAG is the schedule tag name, NEWSTART/NEWEND are
the new dates.

`--dry-run` shows what would change without writing.

For recurring events in the schedule log, updates the corresponding
`@ark-event-upcoming:` entry. For one-shot events, rewrites the tag
value directly.

## Config Change Detection

When ark.toml's `[schedule]` section changes (tags added/removed,
defaults changed), the server must re-materialize day buckets for
affected files.

Detection: store the serialized `[schedule]` section in the
settings record (I prefix). On config reload (startup, ark.toml
fsnotify), compare current vs stored. If different:
- Tags added: scan files with the new tag, write schedule log entries
- Tags removed: remove schedule log chunks for that tag
- Defaults changed: re-compute month buckets with new durations

## Gap Detection

Gaps are computed from the recurrence spec and `@ack:` dates in the
source file — no fired records needed. Compare what *should* have
happened (crank the spec from the last ack date to now) against
what *was* acknowledged. Unacknowledged past occurrences within a
lookback window (default 7 days) are recent misses.

Franklin's morning briefing: "You had a dentist appointment
Saturday, did it happen?"

`ark schedule search --gaps` computes this: for each event in the
query range, check if an `@ack:` in the source file covers that
date. Past events without acks are gaps.

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
