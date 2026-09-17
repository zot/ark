# Test Design: off-actor reconcile walk
**Source:** crc-DB.md

`launchReconcileWalk` is the testable core of the enumeration-off-actor change
(R3433, R3434): it kicks the walk off the DB actor and coalesces concurrent
kicks. Both properties are invisible when violated — an enumeration wrongly run
on the actor still lands the same records (only the UI stalls), and a second walk
launched during the first still indexes the same files (only work is
duplicated) — so each gets a fire alarm. Both run on a zero-value `DB` with just
its actor (`newChanSvc`), a fake walk that blocks on a channel, no server, no
sources, no filesystem.

## Test: the walk runs off the DB actor
**Purpose:** R3433 — while the walk runs, the actor is free. The actor closure
that launches the walk returns before the walk finishes, so an interleaving
tmp:// write is not blocked for the whole enumeration.
**Input:** a fake walk that closes `walkStarted` then blocks on `walkRelease`,
launched from inside a `SyncVoid` closure on the actor.
**Expected:** the launching closure returns (`launchDone`) while the walk is
still blocking; after release, `reconcileWalking` clears.
**Refs:** crc-DB.md, seq-reconcile.md#2.2
**Code:** reconcile_walk_test.go
**Alarm:** 1
**Fire alarm:** in `launchReconcileWalk`, run the walk synchronously on the actor
— replace `go func(){ walk(); SyncVoid(db, apply…) }()` with an inline `walk();
apply(); reconcileWalking=false; …`. Then the launching closure blocks for the
whole walk, `launchDone` never fires while the walk blocks, and the test rings on
the 2 s timeout ("the actor stayed blocked while the walk ran").
**Inject:** db.go:launchReconcileWalk
**Pulled:** 2026-09-17 — rang (FAIL: the actor stayed blocked while the walk ran, at the timeout; compiles and runs)

## Test: a reconcile during a walk coalesces
**Purpose:** R3434 — a second `launchReconcileWalk` while a walk is in flight does
NOT start a second walk. It sets `reconcileWalkPending`, and the first walk's
completion re-triggers exactly one reconcile (relaunch).
**Input:** launch 1 with a blocking walk; while it blocks, launch 2 with a walk
that increments `walk2Called`; then release launch 1.
**Expected:** `reconcileWalkPending` is set and `walk2Called == 0` while the walk
is in flight; after release, `relaunchCalled == 1` and `reconcileWalkPending`
clears.
**Refs:** crc-DB.md, seq-reconcile.md#2.2, seq-reconcile.md#2.5
**Code:** reconcile_walk_test.go
**Alarm:** 2
**Fire alarm:** drop the `if db.reconcileWalking { reconcileWalkPending = true;
return }` guard in `launchReconcileWalk`. Then launch 2 kicks a second walk
goroutine: the coalescing invariant breaks (the pending mark is not durably set —
the second walk's completion clears it — and `walk2Called` becomes 1), and the
test rings.
**Inject:** db.go:launchReconcileWalk
**Pulled:** 2026-09-17 — rang (FAIL: a reconcile during an in-flight walk did not set reconcileWalkPending — not coalesced)
