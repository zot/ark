# Scheduling

Ark can schedule: tags declared in `ark.toml` carry dates and recurrences, an
in-memory queue fires them through pubsub, chimes tick on fixed cadences, and
`ark schedule` has eight subcommands. It has gone through several iterations
since March 2026. **Nobody has used it.** Its author has never put a real
appointment in it or tested it personally, and the only schedule source on the
dev box is a test fixture.

So this carve does two things. It establishes what actually exists and clears
away the specs and requirements left over from earlier iterations. And it
finds out what it would take for Bill to use it.

Opened by Bill, 2026-10-21.

## Status

- [ ] **Item 1 — what exists.** **MEASURED (2026-10-21), see [What exists](#exists).**
      Re-check before relying on any line of it.
- [ ] **Item 2 — clear the old iterations out of the docs.** **OPEN (not
      queued.)** Superseded specs and design files, contradictions, stale and
      never-built requirements. `/mini-spec`. See Item 2.
- [ ] **Item 3 — defects found in the survey.** **OPEN (not queued), NOT
      VERIFIED beyond a spot check.** See Item 3.
- [ ] **Item 4 — chimes nobody hears.** **OPEN (not queued.)** See Item 4.
- [ ] **Item 5 — what it takes for Bill to use it.** **OPEN (not queued.)** The
      point of the carve. See Item 5.
- [ ] **Item 6 — who scheduling is for.** **OPEN (not queued.)** See Item 6.

<a id="exists"></a>
## What exists

Measured 2026-10-21 by reading the code and running the CLI against the live
server. A survey, not an audit: each line is a claim to re-check before it is
built on.

**The engine.** `EventScheduler` (`scheduler.go`) keeps a heap of
`ScheduledEvent`s and one `time.AfterFunc`. The indexer finds tags declared
under `[schedule.tag.X]`, and `server.processScheduleItems` hands them to
`EnsureUpcoming`, which arms the next occurrence only. When an event fires,
`fire()` publishes the tag through pubsub and, unless the tag's lifecycle is
`none`, appends `@ark-event-fired:` to an audit log (`fireLogMutate`), trimmed
to `log_cap`, plus `@check-gap:` when the tag has a `default_duration`. **Publishing
is the only effect of a fire.** No message, notification, launch or UI change
follows unless something has subscribed. Fires missed while the server was down
are not backfilled. At startup `ScanCheckGaps(7)` writes unresolved gaps to
`tmp://watchdog/missed-events`.

**State.** The queue is memory only. Audit logs are on disk at
`~/.ark/schedule/<hash>.md` (lifecycle `disk`) or at `tmp://schedule/…`
(lifecycle `tmp`). The index holds only the `schedule` and `schedule_config`
I-fields ([record-formats.md](../specs/record-formats.md)).

**Chimes.** Six `@chime-Nm:` cadences (1, 5, 15, 30, 45, 60 minutes) in
`~/.ark/chimes.md`, auto-declared with lifecycle `none`. A fire carries an
RFC 3339 timestamp. Consumed with `ark subscribe --tag chime-Nm` and `ark listen`;
there is no chime CLI.

**CLI.** `ark schedule search | change | tags | parse | upcoming | logs |
suppress | unsuppress` ([cli-commands.md](../specs/cli-commands.md)).

**Lua.** `mcp.scheduled` and `mcp.reschedule` exist
([lua-api.md](../specs/lua-api.md)); no app calls them. **No UI** shows
schedules or chimes.

**Specs.** [scheduling.md](../specs/scheduling.md) (data model),
[schedule-lifecycle.md](../specs/schedule-lifecycle.md),
[chimes.md](../specs/chimes.md),
[006-schedule-record-only.md](../specs/migrations/complete/006-schedule-record-only.md)
(the current model: logs are audit only, the queue is the authority),
[pubsub.md](../specs/pubsub.md), and the `[schedule]` keys in
[config.md](../specs/config.md). [features.md](../specs/features.md) has no
scheduling row; chimes appear only under Luhmann.

**Tests.** `scheduler_test.go` (8 tests, parsing and bounds) and
`reschedule_test.go` (1). Nothing tests `fire`, `EnsureUpcoming`, log scanning,
chimes or lifecycles; `design.md` records these as gaps (O29, O33, O38, O39).

**Live on the dev box.** Declared: the six chimes, `@deadline` (all day),
`@reminder` (30m), `@standup` (15m). The only real source is a test fixture.
One audit log, last written 2026-10-19: 19 reminder fires, 15 standup fires, 16
unresolved `@check-gap:` lines. Nothing subscribes to chimes, so they publish to
no one. The server log has no record of fires.

**Where it came from.** Private planning notes (March 2026) framed scheduling as
the "nervous system" for Franklin, the planned personal-assistant agent: a sense
of "today", stale inboxes noticed, missed appointments raised the next morning
("damage control, not replay"). Also planned and never built: a calendar view in
the ark app (month/week/agenda), date-picker and ack widgets in the editor,
an agent that dispatches schedule events to other agents, holidays (R813), and
`@ended:` to stop a recurrence (R825).

## Parts

**Item 2.** Clear the earlier iterations out of the docs, so a reader meets
only the current model.

- **Superseded, by the survey:** `design/seq-scheduling.md` (the forward window,
  `@ark-event-upcoming:`, `IsScheduledFire`); `specs/scheduling.md` §Log file
  naming and §Lifecycle; `schedule-lifecycle.md` §Log Writing on Event Fire;
  `pubsub.md` §Scheduling as a Subscription Property (`--scheduled` and
  `--recurring` were retired); the `AddChime()` step in `seq-pubsub.md`.
- **Mixed:** `seq-chimes.md` (steps 1.5 and 2.5), `crc-EventScheduler.md`
  (`monthBuckets`, `IsScheduledFire`). **Unchecked:** `crc-PubSub.md`.
- **Contradictions with the code:** log names are documented as reversible
  path encodings in three places, while the code hashes; `chimes.md` still cites
  the retired `[schedule].tags` key; `cli-commands.md` says `change` updates the
  retired `@ark-event-upcoming:` marker.
- **Requirements:** about 19 with no code anchor. Some describe the retired
  upcoming-marker model (R877, R964, R966, R1006, R1008), some were never built
  (R825, R813, R878, R892, R912, R913, R980), some have the wrong path (R1013
  against R2824). Fourteen were already set aside in June for a retire, reword or
  gap decision that is Bill's.
- **features.md** gets a scheduling row once Item 5 says what scheduling is for.

**Item 3.** Defects the survey found. The first two were spot-checked.

- **A `tmp` lifecycle drops its fire audits.** `processScheduleItems` installs
  `WriteTmpLog` and `ReadTmpLog` and sets them back to nil when it returns
  (`server.go` 1089-1109; again around 428-446). A later fire of a tmp-lifecycle
  tag finds them nil and writes nothing. Assigning those fields outside any
  actor is also a likely data race, against
  [architecture.md](../specs/architecture.md).
- **`fire()` only publishes** (confirmed). Not a defect in itself, but it is why
  a fire is invisible to a person.
- `ark schedule --help` says "(requires server)", but `search`, `tags` and
  `parse` run without one.
- Comments still say chimes default to lifecycle `disk` (`fire()`, `isChimeTag`);
  the code says `none`.
- Malformed schedule values are skipped silently (O118). Every error here
  should be baby food.

**Item 4.** Chimes publish every minute to nobody. They were built to keep a
Luhmann session's prompt cache warm, and the keepalive in `luhmann next`
replaced that use in July 2026. Decide: keep them as a clock other features can
subscribe to, make them lazy (arm a cadence only while it has a subscriber), or
retire them. Chime ticks also flooded `ark schedule search` (about 24 entries in
a 15-minute window), the one usability complaint on record.

**Item 5.** What it takes for Bill to use it. The survey suggests the main
reason it has gone unused: **a fire reaches no person.** It publishes, and
nothing a person looks at is subscribed. Before building anything, find out
from Bill:

- What he would schedule first, and where he would write it (a daily note, a
  project file, a dedicated file).
- How a fire should reach him: a desktop notification, a line in the ark UI, a
  Franklin morning briefing, Emacs, the terminal of a running Claude session.
- What the "damage control" view looks like: missed and unacknowledged events,
  next morning.
- What stays out: the Franklin design's anti-nag rule ("not today" is not
  "overdue") pulls against reminder-style scheduling.

Then the smallest end-to-end path he will actually use, tried on his real notes
before anything is generalised. [observability.md](observability.md)'s generated
paths fit here: the queue and the missed events are natural inspectors.

**Item 6.** Who scheduling is for. Franklin's persona (March 2026) says "not a
calendar"; the scheduling design two weeks later makes scheduling Franklin's
nervous system. The agent files never mention schedules. Item 5's answers
probably settle this; record the decision either way.

## Open questions

@undecided: whether chimes stay, go lazy, or retire; Item 4.
@undecided: how a fire reaches Bill; Item 5, Bill's answer.
@undecided: whether Franklin owns scheduling; Item 6.
