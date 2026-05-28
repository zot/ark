# Schedule record-only migration

Rework the schedule subsystem around the principle that the **source
file owns the active spec** and the **schedule log records what
happened** (fires + spec-change audit). Drops `@ark-event-upcoming:`
from the log entirely; drops current-state `@ark-event-spec:` from
the log; replaces the `tags = [...]` + `[schedule.defaults]` config
shape with per-tag `[schedule.tag.X]` blocks; introduces a one-knob
`lifecycle` field selecting between disk/tmp/no audit; adds
`log_cap`-based trimming to both audit destinations; adds CLI
diagnostics for the new model.

Language: Go for the scheduler/config/CLI. Environment: ark server,
LMDB database, tmp:// overlay.

See also: [scheduling.md](../scheduling.md),
[schedule-lifecycle.md](../schedule-lifecycle.md),
[chimes.md](../chimes.md), [config.md](../config.md),
[cli-commands.md](../cli-commands.md).

## Problem

Three reinforcing problems surfaced after the chime fix slice
(R2778–R2812) landed:

1. **The schedule log lies about chime fires.** The log accumulates
   `@ark-event-fired:` entries at each server restart (from
   `ScanScheduleLogs` / `crankForward` converting past upcoming to
   fired), even though real chime fires bypass log mutation by
   design (R2778). The fired entries are restart artifacts, not
   actual fires.

2. **`@ark-event-upcoming:` rots.** Chime fires bypass log mutation,
   so the upcoming entry written at one startup stays in the file
   forever (or until the next restart's reconcile rewrites it).
   Anyone reading the file sees a stale value.

3. **Spec is duplicated.** The active spec lives in the source file
   (`@standup: every Monday at 09:00` in `~/notes/schedule.md`) and
   is also mirrored in the log chunk's `@ark-event-spec:` field for
   restart-time queue population. If the user edits the source, the
   log's mirror updates on next indexer pass — but the *old* value
   is lost, since neither the source file nor the log preserves
   history.

The underlying cause is that the log file is trying to do three
incompatible jobs at once: current-state cache, restart queue, and
audit trail. R2810 (startup reconcile) showed that the restart queue
can be reconstructed from spec + now; this migration finishes the
move by making the log a pure audit trail.

Two adjacent issues fold in:

4. **`tags = [...]` + `[schedule.defaults]` + `[schedule.tag.X]` is
   three overlapping config surfaces.** A single per-tag block
   collapses them.

5. **`ark schedule change` doesn't work for tmp:// sources.** The
   handler uses `os.ReadFile`/`os.WriteFile` directly. A schedule
   tag in any tmp:// doc (watchdog notes, agent-emitted state)
   can't be rescheduled. Surfaced during the design conversation;
   folded in because it's the same code path.

## Behavior preserved

- Source-file schedule tag syntax: `@standup: every Monday at 09:00`,
  `@dentist: 2026-04-15 09:00..10:30 cleaning`, etc. The recurring-
  event grammar (`every Xm`, `every WEEKDAY HH:MM`, etc.) is
  unchanged.
- `@remove:` / `@add:` / `@ack:` exceptions in the source file:
  unchanged.
- `@check-gap:` / `tmp://watchdog/missed-events` flow: unchanged.
- Fire-time pubsub publish for all categories: every armed event
  still publishes through pubsub when its time arrives. Audit
  destination is orthogonal.
- The R2810 reconcile contract (drop chunks no longer matching
  current schedule filter; delete log files with no surviving
  chunks): unchanged in spirit, extended to also drop chunks whose
  source file is unreadable/missing.
- The R2812 invariant (`Recurring` propagated so `fire()` can
  re-enqueue): unchanged, but the propagation source shifts —
  `EnsureUpcoming` now carries the spec value from the indexer
  directly rather than reading `c.Spec` from a stored field.
- `ark schedule search`, `ark schedule parse`, `ark schedule tags`:
  remain. Output adjusts for the new model (suppressed marker, no
  upcoming column for tags without one).
- Month buckets / `QueryRange`: unchanged — already cranks from spec
  + bounds + exceptions.
- The synthetic `~/.ark` source with per-extension whitelist
  (R2811): unchanged.

## Log chunk schema

A schedule log chunk now carries:

```
@ark-event: standup
@ark-event-source: ~/notes/schedule.md
@ark-event-spec-initial: 2026-04-01 14:32 — every Monday at 09:00
@ark-event-fired: 2026-04-06 09:00
@ark-event-fired: 2026-04-13 09:00
@ark-event-spec-changed: 2026-04-15 14:00 — every Tuesday at 09:00
@ark-event-fired: 2026-04-21 09:00
@ark-event-fired: 2026-04-28 09:00
```

Tag inventory:

- `@ark-event:` — tag name. Required. One per chunk.
- `@ark-event-source:` — source file path (or `tmp://...` URI).
  Required. One per chunk.
- `@ark-event-spec-initial: TIMESTAMP — SPECVALUE` — chunk birth
  marker. Required. Exactly one per chunk. Records when the chunk
  was created and what the spec was at that moment. The timestamp
  parses as a date; the trailing text after ` — ` is the verbatim
  spec value (read as-is, not passed through the recurrence parser
  again at read time).
- `@ark-event-spec-changed: TIMESTAMP — SPECVALUE` — spec change
  marker. Zero or more per chunk. Appended each time the indexer
  notices the source file's `@TAG:` value differs from the most-
  recent spec-marker's value.
- `@ark-event-fired: TIMESTAMP` — fire audit entry. Zero or more.
  Appended at fire time for tags with `lifecycle = "disk"` or
  `lifecycle = "tmp"`.
- `@ark-event-start:` / `@ark-event-end:` — bounds (existing).
  Optional. Mirrored from the source file's spec for fast restart
  computation; rewritten on spec-change.

Tags removed from the chunk:

- `@ark-event-spec:` — current-state spec, removed. Active spec is
  always read from the source file's `@TAG:` value. The verbatim
  spec value is preserved in spec-marker trailing text for history.
- `@ark-event-upcoming:` — removed. The priority queue is the
  authoritative "what's next" source.

### Reader contract

- **Active spec** for `(tag, source)`: read the source file, parse
  its `@TAG:` value. The log never speaks for the current spec.
- **Historical spec** at time T: walk the chunk's spec markers in
  document order; the most-recent marker with `timestamp ≤ T`
  gives the spec value active at T.
- **Fire history**: walk `@ark-event-fired:` entries.
- **Era attribution** (which spec produced fire F): the most-recent
  spec marker before F in document order.

### Spec-marker grammar detail

The spec-marker tag value is `TIMESTAMP — SPECVALUE`. The separator
is ` — ` (space, em-dash, space). The timestamp uses the standard
`scheduleDateFmt` (`2006-01-02 15:04`). The spec value is the
verbatim text the user wrote in the source file's `@TAG:` line,
unprocessed — read it back into a `string`, not into a `DateRange`.

The em-dash separator avoids collision with the recurrence parser's
keywords (`starting`, `from`, `until`, etc.) — the recurrence parser
never sees the timestamp half of the marker because the log-reader
code splits on ` — ` before passing the trailing text anywhere.

## Source-as-truth: queue population moves to the indexer

`ScanScheduleLogs` becomes audit-log hygiene + startup queue arming.
Its responsibilities are:

1. Drop chunks whose tag is no longer in `[schedule.tag.X]` (R2810).
2. Drop chunks whose source no longer passes
   `MatchesScheduleFilterForTag` (R2810).
3. *New:* drop chunks whose `@ark-event-source:` file is missing or
   unreadable (covers explicit deletion of a scheduled source file,
   not just config-driven retirement).
4. Delete log files with no surviving chunks (R2810).
5. Scan for unresolved `@check-gap:` entries within the lookback
   window (existing R972, R973, R979).
6. *New:* arm the priority queue from each surviving chunk's current
   spec marker via `crankForwardAndEnqueue`. The indexer's reconcile
   only re-indexes stale or changed files, so schedules whose source
   hasn't changed since the previous startup would otherwise sit
   un-armed until the next source-file edit — the arm-from-chunk
   pass closes that gap. `Add` is idempotent per-ID (R808, R809,
   R2809), so a later `EnsureUpcoming` from the indexer replaces
   rather than duplicates.

Beyond startup, the priority queue is updated by the indexer's
`EnsureUpcoming` pass on each indexer notification:

- Indexer parses a source file, sees `@standup: every Monday at 09:00`.
- Calls `EnsureUpcoming(tag, value, sourcePath)`.
- `EnsureUpcoming` reads the corresponding log chunk (if any), compares
  the incoming `value` against the most-recent spec-marker's value:
  - If different: append `@ark-event-spec-changed: NOW — value`. Rewrite
    bounds tags if extracted bounds changed.
  - If same: no log mutation.
- `EnsureUpcoming` computes `next = ComputeNext(value, max(fired, now),
  bounds, exceptions)` and enqueues a `ScheduledEvent` with `Recurring =
  value`. The Add is idempotent per-ID (R808, R809, R2809), so re-arming
  on each indexer notification replaces rather than duplicates.

The `Recurring` field on `ScheduledEvent` continues to be populated
(R2812), but its source is now the value the indexer passed, not a
stored `c.Spec` field.

## Lifecycle categories

A schedule tag's `lifecycle` knob takes one of three values:

| `lifecycle = "disk"` | Default. Audit to `~/.ark/schedule/HASH.md`. Persists across restarts. Trimmed to `log_cap` lines. |
| `lifecycle = "tmp"`  | Audit to `tmp://schedule/TAG/SOURCE-ENCODED`. Vanishes on restart. Trimmed to `log_cap` lines. |
| `lifecycle = "none"`  | No audit anywhere. Pure pubsub fire-and-forget. |

`lifecycle = "none"` implies no log file, no spec-change history, no
fire history. Real-time consumers receive events via subscribe/listen
only; no diagnostic trail.

All three categories arm normally — the priority queue holds events
regardless of audit destination. The category only affects what gets
written when (or whether anything is written).

## Audit log trim

When a chunk's `@ark-event-fired:` count would push the chunk past
its `log_cap` line allowance, the older half of the fired entries
are dropped. Spec markers are preserved (they are timeline anchors;
losing them breaks era attribution). The trim runs at fire time on
the append that would exceed the cap.

Default `log_cap = 1000` per tag, overridable per `[schedule.tag.X]
log_cap = N`. Applies identically to disk and tmp:// audit. For a
weekly standup, 1000 entries is ~19 years. For chime-1m, 1000 entries
is ~16 hours.

Trim is per-chunk, not per-file. A log file containing chunks for
several tags (rare — usually one chunk per tag per source) trims each
chunk independently.

Disk log files are also subject to the existing archive convention
(`~/.ark/schedule-archive/`) per `schedule-lifecycle.md` for users
who want longer history with rotation; `log_cap` is the bounded
default for users who don't.

## Config schema: `[schedule.tag.X]` blocks

The `[schedule]` section gains per-tag blocks. The mere presence of
a `[schedule.tag.X]` block declares X as a schedule tag.

```toml
[schedule]
# Top-level [schedule] still carries cross-cutting knobs:
exclude_files = ["*.jsonl", "/home/deck/.claude/**"]
# (filter_files, lifecycle_include, lifecycle_exclude removed —
# lifecycle is per-tag now.)

[schedule.tag.standup]
# Defaults: lifecycle = "disk", log_cap = 1000, suppress = false.

[schedule.tag.dentist]
default_duration = "1h"

[schedule.tag.chime-1m]
# Defaults — explicit block declares the tag.

[schedule.tag.heartbeat]
lifecycle = "tmp"
log_cap = 500

[schedule.tag.noisy-tick]
lifecycle = "none"  # fires through pubsub only; no audit

[schedule.tag.standup-suppressed-during-summer]
suppress = true     # tag is declared but does not arm
```

Per-tag keys:

- `lifecycle` — `"disk"` (default), `"tmp"`, or `"none"`.
- `log_cap` — int, default 1000. Maximum fired entries per chunk
  before older-half trim.
- `default_duration` — string. Replaces `[schedule.defaults]`
  entries.
- `filter_files` — array, optional. Per-tag override.
- `exclude_files` — array, optional. Per-tag override.
- `suppress` — bool, default false. When true: tag is declared and
  visible in CLI commands but `EnsureUpcoming` becomes a no-op for
  it; on config reload the priority queue drains matching events;
  past audit history preserved. Re-enabling restores firing.

The hard switch — `tags = [...]` and `[schedule.defaults]` are not
parsed in the new code. Existing users (Bill) edit `ark.toml`
manually to convert. `WriteDefault` ships only the new shape.

## Default chimes

`WriteDefault` ships per-chime blocks for each of the six standard
cadences:

```toml
[schedule.tag.chime-1m]
[schedule.tag.chime-5m]
[schedule.tag.chime-15m]
[schedule.tag.chime-30m]
[schedule.tag.chime-45m]
[schedule.tag.chime-60m]
```

All default to `lifecycle = "disk"` (D1). A user who wants chime
fires kept ephemeral edits one block to `lifecycle = "tmp"`; one who
wants no chime audit edits to `lifecycle = "none"`; one who wants a
chime silenced edits to `suppress = true`.

The matching `~/.ark/chimes.md` content (auto-created by
`EnsureChimesFile`) is unchanged — it contains the six
`@chime-Nm: every Nm` lines that act as the source-file specs.
`install/ark.toml` carries the per-chime blocks in the same template
that already carries the commented `[[source]] dir = "~/.ark"`
example.

## New `ark schedule` subcommands

For diagnostics + management of the new model:

### `ark schedule upcoming TAG [--all]`

Prints the next fire for TAG from the in-memory priority queue.
Without `--all`, just the head entry for TAG. With `--all`, every
queued tag's next fire, sorted by NextFire.

Output (markdown, one line per entry):

```
@standup: 2026-05-04 09:00  ~/notes/schedule.md
@chime-1m: 2026-05-28 10:31  ~/.ark/chimes.md
```

Server-required. Works for any lifecycle category — the queue holds
them all.

### `ark schedule logs TAG [SOURCE] [-n N] [--json]`

Reads the audit log for TAG. Without SOURCE, lists all sources
that have a log chunk for TAG. With SOURCE, prints the chunk's
fire history (most recent N, default 50) and spec markers.

Output (markdown):

```
@standup  ~/notes/schedule.md  (lifecycle=disk, 47 fires)
  spec history:
    initial 2026-04-01 14:32 — every Monday at 09:00
    changed 2026-04-15 14:00 — every Tuesday at 09:00
  recent fires:
    2026-05-25 09:00
    2026-05-18 09:00
    ...
```

`--json` emits a single object: `{tag, source, lifecycle, fired:
[...], specs: [{kind, ts, value}, ...]}`.

For `lifecycle = "none"` tags: prints `(no log — lifecycle = "none")`.
For `lifecycle = "tmp"` tags: reads the tmp:// doc (server-required).
For `lifecycle = "disk"` tags: cold (reads the disk log file
directly, no server needed).

### `ark schedule suppress TAG`

Sets `[schedule.tag.TAG] suppress = true` in `ark.toml` via the
existing config-mutation path. Server reloads config; `EnsureUpcoming`
becomes no-op for the tag; queue drains matching entries on reload.
Past audit history preserved.

If the tag has no `[schedule.tag.TAG]` block, exits non-zero with a
diagnostic ("tag is not declared in [schedule.tag.*]; add the block
first"). Suppress doesn't declare; it only modifies an existing
declaration.

Server-required.

### `ark schedule unsuppress TAG`

Sets `suppress = false` (or removes the key) for the tag. Server
reloads; tag resumes firing per its current spec.

Server-required.

## `ark schedule change` tmp:// support

The existing `ark schedule change PATH TAG NEWSTART [NEWEND]`
handler reads/writes the source file via `os.ReadFile` /
`os.WriteFile` directly. Update the handler:

1. If `PATH` is a `tmp://` URI: read content from the tmp:: overlay
   (via existing `db.tmpContent` access — exposed through a getter
   if not already), apply the same in-memory transformation, write
   back via `db.UpdateTmpFile` (routes through the write actor per
   the all-mutation-through-write-actor invariant).
2. Otherwise: existing disk path.

Pubsub change notification fires either way — the existing
`UpdateTmpFile` path already publishes through pubsub per R2281.

Verification: `ark schedule change tmp://test/sched.md dentist
'2026-05-01 09:00'` rewrites the value and triggers a fire
notification through pubsub.

## Existing CLI surface updates

- `ark schedule tags`: marks suppressed tags with `[suppressed]`.
  Reads `lifecycle` per tag and shows `[lifecycle=tmp]` or
  `[lifecycle=none]` as appropriate (no marker for the default
  disk).
- `ark schedule search`: events whose tag is suppressed render with
  a `[suppressed]` prefix on the output line. The search still
  computes from spec (suppressed tags' specs are valid — they just
  don't arm).

## What this deliberately does not do

- **No automatic disk-to-archive rotation.** `log_cap` trim is the
  bounded-history default. Users who want longer history with
  rotation manage `~/.ark/schedule-archive/` manually per
  `schedule-lifecycle.md` (existing convention).
- **No suppression of `lifecycle = "none"` events from the priority
  queue.** Even with no audit, the queue arms them — they just fire
  silently. To stop firing, use `suppress = true`.
- **No per-spec-era queries via CLI yet.** `ark schedule logs --era T`
  could be added later; for now, the JSON output gives a reader
  enough material to reconstruct era attribution client-side.
- **No source-file history reconstruction from git/fossil.** The log
  preserves the spec values it captured; if a spec was edited and
  then edited back before the indexer noticed, intermediate values
  are not preserved. Acceptable given the indexer's typical
  sub-second response to file changes.
- **No backward-compat parsing of `tags = [...]`.** Per D2 (hard
  switch), the old shape is gone in the new code. Bill manually
  updates `ark.toml`; future users start from the new
  `WriteDefault` template.
- **No new lifecycle category beyond `disk`/`tmp`/`none`.** The
  three-value enum is the inventory. Adding a fourth (e.g. "binary"
  for compact storage) is not in scope.
- **No per-tag `lifecycle_include`/`lifecycle_exclude` glob patterns.**
  Per-tag config is explicit-block-only; no glob-match fallback.
  Removes the `lifecycle_include`/`lifecycle_exclude` keys from
  `[schedule]`.
