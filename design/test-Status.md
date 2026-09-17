# Test Design: off-actor, no-walk `ark status`
**Source:** crc-DB.md

The #132 status fix has two invisible-when-violated properties, one per fire
alarm. Both are cheap and deterministic: a real (temp) microfts2 index with one
indexed file, no server.

- **Off the actor (R3436).** `readStatusOffActor` binds the view in one trivial
  actor hop, then runs the read on the caller's goroutine. If the read ran
  *inside* the bind Sync, the actor would be held for the whole enumeration and
  an interleaving write would stall — but the records still land, so nothing but
  a timing test sees it.
- **No corpus walk (R3435).** `Status` reports the stored last-reconcile counts,
  not a live `StaleFiles` walk. If it walked, the number would still look
  plausible — only wrong, and only when the disk diverged from the last
  reconcile.

## Test: the status read runs off the DB actor
**Purpose:** R3436 — while a status read runs, the DB actor is free. The actor
closure that binds the view returns before the enumeration finishes, so an
interleaving actor hop is not blocked for the whole read.
**Input:** a real DB (testIndexer + newTestDB) with its actor started; a read
closure passed to `readStatusOffActor` that closes `readStarted` then blocks on
`readRelease`. While it blocks, a concurrent `Sync(db, trivial)` on another
goroutine.
**Expected:** the concurrent `Sync` completes (`hopDone`) while the read is still
blocking; after release the read returns.
**Refs:** crc-DB.md, seq-reconcile.md#3.1
**Code:** status_test.go
**Alarm:** 1
**Fire alarm:** in `readStatusOffActor`, run `read` INSIDE the bind Sync closure
(return its result from the Sync instead of after it). Then the blocking read
holds the single-slot actor, the concurrent `Sync` queues behind it, `hopDone`
never fires, and the test rings on the 2 s timeout.
**Inject:** db.go:readStatusOffActor
**Pulled:** 2026-09-17 — rang

## Test: status uses the stored counts, never a live file walk
**Purpose:** R3435 — `Status` reports the stale/missing/unresolved counts from
the passed-in reconcileCounts, and never stats or hashes a corpus file. A file
that is stale on disk does not change what status reports.
**Input:** index one file; then rewrite it on disk with different content and a
distinctly different mtime (os.Chtimes), so a live `StaleFiles` would classify it
`stale`. Call `db.Status(reconcileCounts{stale:7, missing:3, unresolved:5, at:t},
0)`.
**Expected:** `status.Stale == 7`, `status.Missing == 3`, `status.Unresolved ==
5` (the stored values), `status.ReconcileAt == t`; and a control `db.fts.StaleFiles()`
reports exactly one `stale` entry — proving 7 is the stored value, not the disk's.
**Refs:** crc-DB.md, seq-reconcile.md#3.2
**Code:** status_test.go
**Alarm:** 2
**Fire alarm:** in `Status`, replace `rc.stale` with a live walk — call
`db.fts.StaleFiles()` and count `Status=="stale"`. Then `status.Stale` becomes 1
(the disk truth), the assertion expecting 7 fails, and the test rings.
**Inject:** db.go:Status
**Pulled:** 2026-09-17 — rang
