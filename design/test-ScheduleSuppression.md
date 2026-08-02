# Test Design: Schedule search — local arm and travelling suppression

**Source:** crc-EventScheduler.md

Guards R3396 and R3397. `ark schedule search` in its default output form could
not succeed in **either** state for seven weeks: with a server it proxied the
events and then opened the index to look suppression up, which the server was
holding; without a server it refused at the door. R1027 had claimed a
server-free path since the command was written, and the CLI had refused for just
as long — the query never needed a server, but the command had no arm to run it
in.

Both halves are cheap to pin because neither needs a DB, a socket or a
filesystem source tree: `QueryRange` reads a schedule directory, and suppression
is a config lookup.

## Test: suppression travels on the event

**Purpose:** R3397 — `MarkSuppressed` stamps each event from the config, and it
is the single place both arms get the answer, so the server and the local arm
cannot disagree about which tags are suppressed.
**Input:** a `*Config` declaring two schedule tags, one with `suppress = true`;
a slice of `ScheduleEvent` naming both tags plus a third the config does not
declare.
**Expected:** exactly the suppressed tag's events come back with `Suppressed ==
true`. The undeclared tag is false, not a panic — an event can name a tag whose
`[schedule.tag.X]` block was removed between the log write and the query.
**Fire alarm:** make `MarkSuppressed` a no-op and confirm red. Worth pulling
because the assertion is on a *false-by-default* boolean: a function that never
sets it passes every test that only checks the unsuppressed rows.
**Refs:** crc-EventScheduler.md, crc-Server.md, crc-CLI.md, specs/scheduling.md,
R3397, R2845
**Code:** scheduler_test.go

## Test: the query runs with no server around it

**Purpose:** R3396 — an `EventScheduler` built the way a CLI local arm builds
one (nil pubsub, nil error reporter, a schedule directory, a config) answers
`QueryRange` exactly as the server's does. This is what makes R1027 true of the
command rather than only of the computation.
**Input:** a temp directory holding one schedule log chunk with a recurrence
spec; `NewEventScheduler(nil, nil, dir, cfg)`; a date range covering two
occurrences.
**Expected:** both occurrences returned, with tag, source and times populated.
No panic — the point is that the query half touches neither collaborator.
**Fire alarm:** add a `es.pubsub.Publish(...)` call inside `QueryRange` and
confirm this goes red with a nil dereference. That is a Sleeping Sentry: the
guarantee being kept is *the absence of a dependency*, which nothing else in the
suite would notice being added, because the server always supplies a real
pubsub.
**Refs:** crc-EventScheduler.md, crc-CLI.md, specs/scheduling.md, R3396, R1027
**Code:** scheduler_test.go
