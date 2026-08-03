# Test Design: Unresolved candidates — reason, proposer, liveness

**Source:** crc-Store.md

Guards R3356, R3357, R3358, R3366, R3370 and closes O197. A U record used to be
a bare path with a timestamp, and "unresolved" meant "seen during a scan and
matched no pattern". Widening it to "ark noticed this and did not index it"
brings in two kinds that behave differently from the original, and both break
the old assumptions rather than extending them.

## Test: a symlink proposal dies with its proposer

**Purpose:** R3357, and O197 — the defect this closes is not an approximation
but the *wrong question*. A symlink-reason record is keyed on the **target**: a
real directory outside every source, which is the one thing that has
emphatically not disappeared. What disappears is the proposal. `CleanUnresolved`
asked `os.Stat(rec.Path)`, so a withdrawn proposal survived every scan,
indistinguishable from a live one.
**Input:** a link proposing a target, then the same record after the link is
**repointed** at another directory, and again after it is **deleted**. Plus a
record with no proposer at all, and the two non-symlink reasons.
**Expected:** live while the link still resolves to the target; dead once
repointed; dead once deleted. A record with no proposer falls back to the path
test instead of being dropped, since it cannot be judged on its proposer. The
non-symlink reasons keep the ordinary test in both directions.
**Guard the guard:** after the deletion the test asserts the *target still
exists*. Without that, "dead" would be consistent with the old code having
simply found a missing path, and the case would prove nothing about which path
was tested.
**Fire alarm:** disable the symlink branch so liveness asks the target again —
the literal pre-fix logic — and confirm the repoint and delete cases go red
while the rest stay green.
**Refs:** crc-Store.md, specs/symlinks.md, R3357, R3355, O197
**Code:** store_test.go

## Test: the record keeps what it was told

**Purpose:** R3356, R3357 — `AddUnresolved(path, dir)` dropped the reason and
the proposing link on the floor, which is the root of both O197 and O202: a
candidate that cannot say why it exists cannot be explained to a user, and one
that does not name its link cannot be withdrawn or promoted.
**Input:** a symlink-reason record with a proposer, stored and read back.
**Expected:** reason and proposer survive the round trip; `FirstSeen` is
stamped by the writer so no caller has to remember.
**Fire alarm:** rebuild the record from path and dir alone before storing —
what the two-argument form did — and confirm both fields come back empty.
**Refs:** crc-Store.md, specs/record-formats.md, R3356, R3357
**Code:** store_test.go

## Test: the count is broken down by reason

**Purpose:** R3370 — one reason normally dominates, so a bare total lets the
handful of entries a user can act on hide behind thousands that no pattern
change will ever clear.
**Input:** the reason tallies, table-driven over the empty, single, full and
zero-bearing cases.
**Expected:** `` for nothing unresolved; otherwise
` (uncovered=a unindexable=b symlink=c)` in that **fixed** order — the order a
user works through them, patterns then chunkers then sources — with zero-count
reasons omitted, because a zero is noise rather than information. A map whose
counts are all zero reads as nothing at all.
**Fire alarm:** return the empty string unconditionally, which is the bare
total the line used to carry, and confirm every non-empty row goes red.
**Refs:** crc-CLI.md, specs/status-enhanced.md, R3370
**Code:** cmd/ark/main_test.go

## Not covered here, and deliberately

**The promotion and withdrawal pass in `ReloadConfig`** is not asserted. It
needs a whole `*DB` — the config-reload path, the scanner's link table, and the
store together — and no test in the repo builds one; the same gap O199 already
names. What *is* covered is the decision each half turns on: liveness above,
and the re-derivation itself in `test-LinkTable.md`.

**The `unindexable` determination** (R3358) is likewise unasserted. It fires
where `AddFile` returns `ErrNoChunks`, which needs a real index and a file that
no chunker will touch. Recorded rather than left unsaid: the failure mode is a
candidate silently *not* recorded, which looks exactly like a clean scan.
