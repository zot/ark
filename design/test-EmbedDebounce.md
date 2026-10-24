# Test Design: EmbedDebounce
**Requirements:** R3579, R3580

**Source:** crc-Server.md

Covers when files stored between reconciles are embedded, and how the limited
pass shares the one embed-pass guard with the full pass. The timing is a pure
value (`embedDebounce`) driven with explicit times; the guard bookkeeping runs
on a real DB actor with a librarian that has nothing to embed, so each pass ends
at once.

## Test: the quiet window
**Purpose:** the set is embedded once 15 seconds pass with no new path, and
one outstanding timer is enough however many paths join (R3579).
**Input:** note `a` at t0 and `b` at t0+10s; fire at t0+15s and t0+25s; note `c`
at t0+30s.
**Expected:** only the first note arms (15s); the t0+15s fire re-arms for 10s;
the t0+25s fire yields `a` and `b`; the note after the flush arms a fresh 15s
timer.
**Refs:** crc-Server.md
**Code:** embed_debounce_test.go

## Test: the one-minute ceiling
**Purpose:** a file changing every ten seconds never lets the set go quiet, so
the ceiling is what embeds it (R3579).
**Input:** note the same path every ten seconds, firing each timer as it comes
due.
**Expected:** the set is taken exactly one minute after the first note.
**Refs:** crc-Server.md
**Code:** embed_debounce_test.go
**Fire alarm:** drop the ceiling from `due`; the set is never taken and the
test fails with "a file changing every ten seconds was never embedded".
**Inject:** embed_debounce.go:due
**Pulled:** 2026-10-25 — rang: "a file changing every ten seconds was never embedded"
**Alarm:** 1

## Test: a full pass clears the set
**Purpose:** paths stored before a full pass began are covered by it, and a
path noted after the clear is timed from its own arrival (R3580).
**Input:** note `a`, clear, note `b` 5s later, fire at t0+15s and t0+20s.
**Expected:** no second timer is armed after the clear; the t0+15s fire re-arms
for 5s; the t0+20s fire yields `b` alone.
**Refs:** crc-Server.md
**Code:** embed_debounce_test.go

## Test: waiting for the running pass
**Purpose:** while a pass runs, a due set is held and a full pass deferred; when
it ends the full pass goes first and drops the held files, and held files alone
start a limited pass (R3580).
**Input:** with `embedding` set, launch a limited pass for `a` and a full pass,
then end the pass; repeat with only a limited pass for `b`.
**Expected:** while busy both wait; after the first end the full pass runs with
nothing held or pending; after the second the limited pass runs and nothing is
held. Assertions are made off the actor, since a fatal inside an actor closure
would kill the actor.
**Refs:** crc-Server.md
**Code:** embed_debounce_test.go
**Fire alarm:** leave the held files in place when a full pass starts; the
first end reports `held=map[a:{}]`.
**Inject:** server.go:launchFullEmbed
**Pulled:** 2026-10-25 — rang: "after end: running=true fullPending=false held=map[a:{}]"
**Alarm:** 2
