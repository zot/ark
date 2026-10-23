# Actor discipline

Ark's rule (specs/architecture.md; R3419, R3433): chunking and gram computation
run off the DB actor, and only the database changes run on it (Bill,
2026-10-23: "chunking can work off-actor, it's the DB changes that need to be in
the write actor"). The file watcher, the reconcile's content files, rebuild's
scan and `ark refresh --force` already follow it, through `startFileScan`:
compute off-slot, store on-slot. The paths below do not. While one runs, every
other DB request waits behind it, `ark status` included.

Opened by Bill, 2026-10-23, from an inventory the same day (a delegated read of
the code, not a measurement; each part re-checks its own path before changing
it).

## Status

- [ ] **Item 1 — `ark add`.** **OPEN (not queued.)** `/mini-spec`. See Item 1.
- [ ] **Item 2 — `ark scan` and the scan after a schedule fires.** **OPEN (not
      queued.)** `/mini-spec`. See Item 2.
- [ ] **Item 3 — plain `ark refresh`.** **OPEN (not queued.)** `/mini-spec`. See
      Item 3.
- [ ] **Item 4 — saving a file from the UI.** **OPEN (not queued.)**
      `/mini-spec`. See Item 4.
- [ ] **Item 5 — `tmp://` writes.** **OPEN (not queued.)** Small documents; may
      be fine as they are. See Item 5.
- [ ] **Item 6 — staleness checks on the actor.** **OPEN (not queued.)** Not
      chunking, but hashing. See Item 6.
- [ ] **Item 7 — measure startup.** **OPEN (not queued.)** Per-phase timing. See
      Item 7.

## Item 1

`ark add` (`POST /add`) wraps `db.Add` in `SyncVoid`; `Add` calls
`indexer.AddFile` for a file, or per file for a directory, so every file added
is chunked on the DB actor. Move it to the scan path.

## Item 2

`ark scan` (`POST /scan`) and the post-schedule scan (`processScheduleItems`)
both run `db.Scan` inside `Sync`/`SyncVoid`, and `Scan` calls `AddFile` for each
new file. The startup reconcile already does the same job off-actor
(`startReconcileWalk`); these should share it.

## Item 3

`ark refresh` wraps `RefreshStale` in `SyncVoid`. `RefreshStale` prepares files
on worker goroutines, but the actor waits for all of them
(`workerWg.Wait()`, `writeWg.Wait()`), and a single stale file is refreshed
inline, so the actor is held for the whole job. `RefreshAsync`, used by the
reconcile, already dispatches each stale file as its own scan; plain refresh can
do the same, as `--force` now does.

## Item 4

`handleSave` (the UI editor's save) calls `indexer.RefreshFile` inside
`SyncVoid`. One file, but chunked on the actor while the person waits.

## Item 5

`tmp://` writes (`AddTmpFile`, `UpdateTmpFile`, `AppendTmpFile`) chunk on the DB
actor from HTTP handlers, Lua, the scheduler log, the watchdog document, and
about fifteen recall, routing and pty call sites; a few go through the write
actor. These are small generated documents, so the cost per write is low. Worth
measuring before moving, since moving them changes when a just-written document
becomes searchable.

## Item 6

`db.Files()` calls `fts.StaleFiles()`, which stats every indexed file and reads
and hashes any whose modification time changed. `lowestPendingBloodhound` calls
`db.Files()` through `Sync` on every secretary `next` poll, so each poll may hash
every chat transcript written since its last index, on the actor. The reconcile's
`RefreshAsync` also calls `StaleFiles()` on the actor (about 10 s of the
2026-10-23 17:00 startup). `fts.FileIDPaths()` lists indexed files without
classifying them, which is all `lowestPendingBloodhound` needs.

## Item 7

On 2026-10-23 the socket-to-reconcile-complete time was 27 s and 35 s on two
startups against 16-20 s before, and an `ark status` during startup took 59 s.
Of the 35 s, about 14 s was applying the scan and 10 s the staleness pass, both
on the actor. Whether #158's slower chat decode contributes is not established.
Log each phase's duration, then decide.
