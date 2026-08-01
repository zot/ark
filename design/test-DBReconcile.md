# Test Design: DB reconcile coalescing
**Source:** crc-DB.md

The gate is a deterministic state machine over one actor-owned bool, so it needs
no server, no sources and no filesystem: a zero-value `DB` with a running actor
and a fake scan reaches all of it. Verified to fail without the gate — removing
it turns the first test's expected 2 scans into 6.

## Test: many requests during one run collapse into a single further scan
**Purpose:** R346, R3351 — the point of coalescing. One scan is running and five more are requested; the first waiter runs and the rest merge into it, so two scans happen rather than six.
**Input:** a held scan that blocks until released, then five further requests, then release.
**Expected:** exactly two scans, with a pause afterwards so a wrongly-queued sixth would have room to appear.
**Refs:** crc-DB.md

## Test: the pending mark clears when the scan begins, so a later request scans again
**Purpose:** R3351 — the load-bearing half, and the one that is easy to get backwards. Clearing at the *end* of a scan (or not at all) would silently swallow a request made after everything quiesced, which is how changes get lost rather than merely delayed.
**Input:** run and release a scan and its merged follower, let the actor drain, then request once more.
**Expected:** a third scan runs.
**Refs:** crc-DB.md

## Test: with nothing in flight, a request scans immediately
**Purpose:** R346 — the gate must not turn the ordinary idle case into a no-op. A coalescer that drops the *first* request is a plausible off-by-one and would be invisible under load.
**Input:** a request against an idle DB.
**Expected:** one scan runs.
**Refs:** crc-DB.md

## Not tested here
**`Server.reconcile`'s body** — the wiring of `onWriteComplete`, `doReconcile`,
and the schedule drain. That needs real sources, watches and a filesystem, and
is exercised by the reconcile paths elsewhere; what these tests own is the
scheduling rule around it, which is the part that changed.
