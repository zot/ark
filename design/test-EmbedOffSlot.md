# Test Design: EmbedOffSlot
**Source:** crc-Librarian.md, crc-Server.md

Covers the post-reconcile embedding split: store writes route back onto the
write actor (never run directly off-slot), and only one embed pass runs at a
time. Both properties are invisible when violated — a direct off-slot write
still stores the record, and a missing guard still embeds — so each carries a
fire alarm.

## Test: enqueueStoreWrite serializes behind the single write slot
**Purpose:** Proves the embed goroutine's store write goes through the write
actor (R3415), not directly. A direct call would run the write immediately in
the caller's goroutine, breaking R1058 cache invalidation and R1067
serialization.
**Input:** A live DB actor (setupRecall). Occupy the single write slot with a
blocking write closure B (enqueued via `SyncVoid(db, enqueueWrite)`, body waits
on a channel). From another goroutine call `l.enqueueStoreWrite(func(){ ran = true })`.
**Expected:** While B holds the slot, `ran` stays false — the store write is
queued behind B. After the channel is closed and `db.WaitWritesIdle()` returns,
`ran` is true. If `enqueueStoreWrite` ran the write directly (off the actor),
`ran` would flip while B still blocks.
**Refs:** crc-Librarian.md
**Code:** embed_offslot_test.go
**Alarm:** 1
**Fire alarm:** In `enqueueStoreWrite`, replace the `SyncVoid(...enqueueWrite...)`
routing with a direct `write()` call. Red: `ran` observed true while the slot
is still held by B (assert fails before the channel is closed).
**Inject:** librarian.go:Librarian.enqueueStoreWrite
**Pulled:** 2026-09-17 — rang

## Test: beginEmbedPass admits one pass at a time
**Purpose:** Proves the re-entrancy guard (R3416): a second reconcile cannot
launch a concurrent embed goroutine while one is running.
**Input:** A zero-value `&Server{}`. Call `beginEmbedPass()` twice, then
`endEmbedPass()`, then `beginEmbedPass()` again.
**Expected:** First call returns true (slot claimed), second returns false
(already running), and after `endEmbedPass()` the next returns true again.
**Refs:** crc-Server.md
**Code:** embed_offslot_test.go
**Alarm:** 2
**Fire alarm:** Remove the `srv.embedding = true` assignment in
`beginEmbedPass` (the latch). Red: the second call returns true, so the assert
that it is false fails.
**Inject:** server.go:Server.beginEmbedPass
**Pulled:** 2026-09-17 — rang
