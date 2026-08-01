# Test Design: ChanSvc
**Source:** crc-ChanSvc.md

The ordering guarantee is the reason these exist. It was assumed for a long
time, was in fact absent, and nothing failed — so the tests below are written to
fail if the delivery path ever loses it again, not merely to demonstrate that it
holds today.

## Test: posts from one goroutine run in post order
**Purpose:** R3344, R3345 — the core guarantee. A spawned sender per post makes this fail non-deterministically, which is exactly the regression to catch.
**Input:** one goroutine posts N closures that each append their index to a shared slice (appended inside the closure, so the actor serializes the writes); wait for the last to run.
**Expected:** the recorded sequence is 0..N-1 in order. N large enough that a racing sender fails reliably rather than occasionally.
**Refs:** crc-ChanSvc.md

## Test: ordering spans fire-and-forget and synchronous posts
**Purpose:** R3344 — both post kinds enter by the same door, so a `Svc` followed by a `SvcSync` from one goroutine must run in that order. A design that ordered only one kind would pass the test above and still be wrong.
**Input:** from one goroutine, interleave fire-and-forget and synchronous posts that each record their index.
**Expected:** the recorded sequence matches the post sequence.
**Refs:** crc-ChanSvc.md

## Test: exactly one closure runs at a time
**Purpose:** R3346 — work moved off the loop goroutine, so serialization is now maintained by the in-flight flag rather than by the loop being busy. That is the property most easily lost by a refactor of `accept`/`launch`/`next`.
**Input:** many concurrent posters; each closure increments a counter on entry, records the maximum observed, sleeps briefly, and decrements on exit.
**Expected:** the maximum observed concurrency is 1.
**Refs:** crc-ChanSvc.md

## Test: a closure may post fire-and-forget without deadlocking
**Purpose:** R3349 — the property the old spawned-sender arrangement was buying, which the redesign must keep. A closure posting from inside itself must queue behind itself and run afterwards.
**Input:** a closure that posts another closure and returns.
**Expected:** both run, the inner one after the outer, with no deadlock.
**Refs:** crc-ChanSvc.md

## Test: a burst larger than the channel is absorbed in order
**Purpose:** R3347 — the queue behind the channel absorbs bursts, and doing so must not reorder them. A backlog that drains out of order is the failure the previous arrangement actually had.
**Input:** one goroutine posts many more closures than the channel's capacity, while the first closure is held blocked; release it.
**Expected:** all closures run, in post order.
**Refs:** crc-ChanSvc.md

## Not tested, deliberately
**The panic policy (R3348).** A closure that panics terminates the process, so
exercising it in-process would take the test binary down with it. Testing it
properly means a subprocess harness, which is disproportionate for a policy whose
implementation is the *absence* of a recover — there is no code to regress, only
code that could be added. If a central recover is ever introduced, this entry is
the record that its absence was intentional.

**Synchronous call from inside a closure (R3349).** The correct behavior is a
permanent deadlock, so a test asserting it would hang rather than fail. The rule
is enforced by the operation-object audit surface (R3350), not by a test.
