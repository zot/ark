# Test Design: Bounded local index open

**Source:** crc-CLI.md

Guards R3391. The defect it exists for was invisible to every other kind of
test: `withDB` called `ark.Open` (zero timeout), which was *correct* under
LMDB — several reader processes could hold the index — and became an unbounded
`flock` wait when the bbolt port made the lock exclusive. No test failed, no
symptom appeared, because the hazard needs a CLI racing a server for the lock.
It surfaced months later as `ark status --chunks` hanging forever against a
running server.

A behavioural test would need a held lock, a live server and a subprocess (the
failure path calls `fatal`, i.e. `os.Exit`). The invariant that actually
regressed is narrower and cheaper to assert, and it is the one a future
LMDB-shaped change would break again: **no CLI code path opens the index
unbounded.**

## Test: the CLI never calls the unbounded ark.Open (Sleeping Sentry)

**Purpose:** R3391 — the bound belongs to the act of a CLI opening the index,
not to one dispatcher. A new command added next year gets this wrong by
copying whichever helper it finds; this is what tells it.
**Input:** every `.go` file under `cmd/ark/`, source-scanned for a call to
`ark.Open(`. The bounded forms (`ark.OpenWithTimeout`, `openLocalBounded`,
`withDB`) are the permitted ones.
**Expected:** zero occurrences. A hit names the file and line and says why the
unbounded form is the server's alone.
**Alarm:** 1
**Fire alarm:** put `ark.Open(arkDir)` back into `withDB` and confirm this goes
red. That is the literal pre-2026-08-01 code, so the alarm re-creates the
defect exactly rather than approximating it.
**Inject:** cmd/ark/main.go:withDB
**Refs:** crc-CLI.md, specs/cli-dispatch.md, R3391, R2994
**Code:** cmd/ark/main_test.go

## Not covered here, and deliberately

The *behaviour* on timeout — bounded wait, liveness recheck, fail-fast when a
server is already answering — is verified by hand against a live server
(`ark status --chunks`: infinite hang before, `error: a server is running and
holds the index` in 4.5 s after) and not by an automated test, because the
failure path calls `fatal` and a real assertion needs a held lock plus a
subprocess harness. Banked as gap O198 rather than left unsaid.
