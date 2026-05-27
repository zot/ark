# Chimes — standard scheduling tags

Language: Go. Environment: ark CLI binary + server, running scheduler.

A small set of standard recurring-event tags that fire on fixed cadences
and carry the current timestamp as their value. The convention is
generic — any feature that needs a periodic tick can subscribe — and
subsumes the older hardcoded "Quarter Chimes" mechanism.

See also: [pubsub.md](pubsub.md) (publisher and event scheduler),
[scheduling.md](scheduling.md) (schedule logs and recurrence parser),
[config.md](config.md) (`[schedule]` configuration).

## The convention

Six standard cadences, named by their period in whole minutes:

| Tag           | Cadence  | Typical consumer                             |
|---------------|----------|----------------------------------------------|
| `@chime-1m:`  | 1 min    | UI clocks, fine-grained heartbeats           |
| `@chime-5m:`  | 5 min    | Cache warmth for short-TTL prompt caches     |
| `@chime-15m:` | 15 min   | Temporal awareness ("Quarter Chimes" role)   |
| `@chime-30m:` | 30 min   | Half-hour tick for low-frequency UI updates  |
| `@chime-45m:` | 45 min   | Cache warmth for the 1-hour prompt-cache TTL |
| `@chime-60m:` | 60 min   | Hourly heartbeat                             |

Tag value carries the current date and time when the chime fires, in
RFC 3339 format (e.g. `2026-05-27T14:30:00Z`). A subscriber listening
on a chime gets a usable "now" tick, not a content-free heartbeat.

## How they fire

Chimes are ordinary schedule-log entries — they ride the same indexer
→ EnsureUpcoming → priority queue path as user-authored schedule tags.
At fire time the value is rewritten to a current RFC 3339 timestamp
(the only chime-specific code; see `fire()`), so subscribers receive
a usable "now" tick instead of the source recurrence spec.

`ark.toml` ships the chime tag names in `[schedule].tags`:

```toml
[schedule]
tags = ["chime-1m", "chime-5m", "chime-15m", "chime-30m", "chime-45m", "chime-60m", ...]
```

A small ark-managed hosting file, `~/.ark/chimes.md`, carries the
recurrence specs:

```markdown
@chime-1m: every 1m
@chime-5m: every 5m
@chime-15m: every 15m
@chime-30m: every 30m
@chime-45m: every 45m
@chime-60m: every 60m
```

`~/.ark/chimes.md` is auto-created by ark on startup if missing, with
the canonical six entries above. The file is a regular ark file —
indexed, scanned, scheduled — so the same path that produces upcoming
entries for `@dentist:` and `@standup:` produces them for the chimes.
Users don't manage this file directly; ark owns it. If the user
deletes it, ark re-creates it on the next startup.

The auto-created file lives under `~/.ark/` and is indexed by the
default ark source. The standard `[schedule].tags` list ships in the
default `ark.toml` template so new installs pick up chimes without
user configuration.

If literal chime declarations appear in other indexed files (e.g. a
codebase that mentions `@chime-15m: every 15m` in source) — those
would arm duplicate events from a second source. Use
`[schedule].exclude_files` (or per-tag `[schedule.tag.NAME].exclude_files`)
to block such paths at config level; the indexer's
`MatchesScheduleFilterForTag` gate consults the exclude before
queuing the schedule item.

## How they're consumed

Subscribers attach with plain `ark subscribe`:

```bash
ark subscribe --session $ID --tag chime-45m
```

No `--scheduled` / `--recurring` flag — `[schedule].tags` declares
schedulability. When the scheduled tick fires, the value the
subscriber receives via `ark listen` is the RFC 3339 timestamp.

Subscribers pick the cadence that matches their need:

- A UI clock subscribes to `chime-1m` so it can update its
  displayed time once per minute.
- The Luhmann orchestrator subscribes to `chime-45m` so its prompt
  cache stays warm under the 1-hour TTL (the 15-minute buffer
  prevents drift at the boundary).
- A heartbeat logger that writes one line per hour to a
  monitoring log subscribes to `chime-60m`.

## Retirement of `AddChime()`

The pre-existing `EventScheduler.AddChime()` method (anchored at
R810 in the EventScheduler CRC) hardcoded a 15-minute recurring
event in code. The chime convention subsumes it: `@chime-15m:`
is one of the six standard cadences, routed through the normal
schedule-log path. `AddChime()` is removed; R810 retires with no
replacement of the same shape — the new requirement that anchors
the chime mechanism replaces it. The previous "Quarter Chimes"
section in `pubsub.md` is removed in favor of a pointer to this
spec.

## What this spec deliberately does not require

- A new `@chime-Xs` (seconds) or `@chime-Xh` (hours) family.
  Minute granularity covers every present use case and the six
  cadences above are the inventory. New cadences can be added by
  appending to `chimes.md` and `[schedule].tags`; no code change
  required.
- A way to disable chimes globally. A user who doesn't want any
  chimes can remove the tag names from `[schedule].tags` (or
  remove the entries from `chimes.md`); the schedule machinery
  ignores tags it isn't told to schedule.
- Subscriber-side throttling or coalescing. A subscriber that
  picks `chime-1m` gets a tick every minute. If that's too
  frequent, subscribe to a longer cadence instead.
