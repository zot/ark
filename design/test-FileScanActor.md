# Test Design: FileScanActor
**Requirements:** R3612
**Source:** crc-FileScanActor.md

Cheap deterministic tests over the registry/changeNum decision logic on a
zero-value `*DB` (just the maps + counter — no bbolt, no server). The
properties here are all "invisible when violated": a wrong changeNum comparison,
a retire that fires when it shouldn't, or a content-staleness escape that skips
a real change all pass a naive suite while corrupting the index or starving a
file.

## Test: stamp monotonic + ABA-safe
**Purpose:** each startFileScan draws a strictly larger stamp from the global
counter, even across retire-and-recreate, so a stale compute can never match a
fresh entry. (R3421)
**Input:** stampFileScan("a") → s1; retire; stampFileScan("a") → s2.
**Expected:** s2 > s1; a fileScanCurrent("a", s1) after recreate is false.
**Refs:** crc-FileScanActor.md, seq-file-scan-actor.md#1.2
**Code:** filescan_test.go
**Alarm:** 1
**Fire alarm:** reset the counter per-entry (stamp from 1 each create) — then s1
== s2 and the stale check passes, letting a superseded compute overwrite a fresh
one. Red: s2 == s1 assertion fails.
**Inject:** db.go:stampFileScan
**Pulled:** 2026-09-17 — rang

## Test: changeNum redo-escape drops a superseded scan
**Purpose:** finishFileScan stores only the current stamp; a scan whose stamp
was bumped by a later event is dropped. This is also the guard that keeps
ErrAppendTailMoved from firing. (R3422, R3427)
**Input:** stamp("a")→my1; stamp("a")→my2 (a newer event); fileScanCurrent("a", my1).
**Expected:** fileScanCurrent("a", my1) == false; fileScanCurrent("a", my2) == true.
**Refs:** crc-FileScanActor.md, seq-file-scan-actor.md#1.7
**Code:** filescan_test.go
**Alarm:** 2
**Fire alarm:** make fileScanCurrent compare `>=` instead of `==` — then a stale
scan (my1) reads as current and both stores land (double index / stale
overwrite). Red: the my1==false assertion fails.
**Inject:** db.go:fileScanCurrent
**Pulled:** 2026-09-17 — rang

## Test: retire only when current
**Purpose:** retireFileScanIfCurrent removes the entry only if its changeNum
still matches; a newer event's entry survives. Closes the retire-vs-new-event
race. (R3424)
**Input:** stamp("a")→my1; stamp("a")→my2; retireFileScanIfCurrent("a", my1).
**Expected:** entry still present with changeNum == my2 (stale retire is a no-op).
**Refs:** crc-FileScanActor.md, seq-file-scan-actor.md#1.10
**Code:** filescan_test.go
**Alarm:** 3
**Fire alarm:** drop the changeNum check in retireFileScanIfCurrent (always
delete) — then a stale store's retire deletes the entry a newer in-flight event
just created, and the newer scan's store is dropped as "not current" (file left
unindexed). Red: entry-present assertion fails.
**Inject:** db.go:retireFileScanIfCurrent
**Pulled:** 2026-10-25 — rang ("stale retire(my1=1) deleted the entry newer scan my2=2 needs")

## Test: content-staleness escape, full-only, append-exempt
**Purpose:** the full-store escape skips only when committed length AND hash
already match the prepared file; an append is never escaped. (R3423)
**Input:** (a) committed (len,hash) == prep (len,hash), op=reindex → skip; (b)
same equality, op=append → do not skip; (c) hash differs → do not skip.
**Expected:** contentUnchangedForStore true only for (a).
**Refs:** crc-FileScanActor.md, seq-file-scan-actor.md#1.9.1
**Code:** filescan_test.go
**Alarm:** 4
**Fire alarm:** remove the `!isAppend` guard so an append with a coincidentally
matching stored hash gets skipped — an append that grows the file is silently
dropped, invisibly, because the happy path rarely hits a hash collision. Red:
case (b) expects false-skip but gets true.
**Inject:** indexer.go:contentUnchangedForStore
**Pulled:** 2026-09-17 — rang

## Test: store-failure re-emits exactly once, never inline reindex
**Purpose:** a failed store retires the entry and re-emits one change event; a
successful store retires without re-emitting. No inline executeFullRefresh
fallback. (R3425)
**Input:** storeResultAction(err) and storeResultAction(nil).
**Expected:** err → {retire:true, reEmit:true}; nil → {retire:true, reEmit:false}.
**Refs:** crc-FileScanActor.md, seq-file-scan-actor.md#1.10.1
**Code:** filescan_test.go
**Alarm:** 5
**Fire alarm:** make storeResultAction return reEmit:false on error — a transient
store failure then leaves the file unindexed with nothing to re-scan it until the
next periodic reconcile, which a green suite never notices. Red: err case expects
reEmit:true.
**Inject:** db.go:storeResultAction
**Pulled:** 2026-10-25 — rang ("store failure: got {retire:true reEmit:false force:false}, want {retire:true reEmit:true}")

## Test: per-chunk changeNum preemption abandons between chunks
**Purpose:** when the reuse gate reports supersession, buildEntries returns
errScanSuperseded and does NOT compute the abandoned chunks — a superseded scan
neither recomputes nor stores, and preemption lands strictly between chunks.
(R3430)
**Input:** three split chunks; a fake gate superseding from the second reuse()
call; a counting PrepareChunk.
**Expected:** err is errScanSuperseded; PrepareChunk ran exactly once (chunk 0).
**Refs:** crc-FileScanActor.md, seq-file-scan-actor.md#1.5.3.3
**Code:** filescan_test.go
**Alarm:** 6
**Fire alarm:** drop the `if superseded { return errScanSuperseded }` early return
in buildEntries — the superseded scan then runs to the end, recomputing chunks a
newer scan owns and building a full entry set that a stale store could land
(silent: the write still happens, just the wrong one). Red: err is nil and
PrepareChunk ran 3 times instead of 1.
**Inject:** indexer.go:buildEntries
**Pulled:** 2026-09-17 — rang

## Test: per-chunk hash reuse skips recompute
**Purpose:** buildEntries computes grams once per unique chunk hash and reuses the
cached grams pointer — intra-scan (a repeated chunk) and cross-scan (a prior
scan's deposits), which is the convergence a rapidly-edited large file relies on.
(R3428, R3429)
**Input:** intra-scan [A,B,A] with an empty gate; cross-scan [A,B] with both
hashes preloaded; a counting PrepareChunk.
**Expected:** intra-scan computes 2 (A reused, and entries[0].Grams ==
entries[2].Grams); cross-scan computes 0.
**Refs:** crc-FileScanActor.md, seq-file-scan-actor.md#1.5.3.4
**Code:** filescan_test.go
**Alarm:** 7
**Fire alarm:** make the loop ignore the gate's cached grams (always-miss) — every
chunk then re-runs PrepareChunk, so a large file with a one-chunk edit recomputes
every chunk's grams on every scan. Silent: the index is correct, only slow, so no
naive test notices. Red: intra-scan computes 3 (want 2); cross-scan computes 2
(want 0).
**Inject:** indexer.go:buildEntries
**Pulled:** 2026-09-17 — rang

## Test: per-file dispatch is bounded to the worker cap
**Purpose:** startFileScan's dispatch admits at most `cap(scanSem)` concurrent
file preparations — the semaphore, not the number of stale files, sets the
ceiling. A cap violation is invisible: the index still ends up correct, only a
burst spawns unbounded compute and memory. (R3438)
**Input:** a zero-value `*DB` with `scanSem` cap N; launch M >> N goroutines each
calling `runScanBounded(fn)`, where fn increments a live-counter on entry (record
peak), blocks on a release channel, decrements on exit.
**Expected:** observed peak concurrency <= N; after release all M complete.
**Refs:** crc-FileScanActor.md, seq-file-scan-actor.md#1.2.2
**Code:** filescan_test.go
**Alarm:** 8
**Fire alarm:** make `runScanBounded` run fn without acquiring `scanSem` — then all
M preparations run at once and a reconcile of thousands of stale files spawns
thousands of concurrent computes, the exact unboundedness Item 6 removes. Silent:
the index is still correct, the burst just melts the box. Red: peak == M (>> N).
**Inject:** db.go:runScanBounded
**Pulled:** 2026-09-18 — rang (peak concurrency exceeded cap=3)

## Test: a scan's slot is held until it is released
**Purpose:** a prepared scan keeps its dispatch slot after preparing, so prepared scans cannot pile up behind the writer; a double release frees one slot (R3612)
**Input:** a one-slot DB; a scan that hands its release on; a second scan
**Expected:** the second waits until the first's release; no slot held after both
**Refs:** crc-FileScanActor.md
**Code:** filescan_test.go
**Fire alarm:** release the slot when the prepare closure returns, as before R3612; red is "a second scan ran while the first still held its slot"
**Inject:** db.go:runScanBounded
**Pulled:** 2026-10-25 — rang ("a second scan ran while the first still held its slot")
**Fire alarm:** drop the once-guard, so a double release takes a slot it does not hold; red is "the slot was never released"
**Inject:** db.go:runScanBounded
**Pulled:** 2026-10-25 — rang ("the slot was never released")
