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
- [ ] **Item 8 — the config reload runs off the actor.** **OPEN (#188.)**
      The other direction: state written outside the actor that belongs on it.
      Found 2026-10-28 during windows-support 8.5 (#181). DECIDED: a leaf
      vending actor publishes the Config. `/mini-spec`. See Item 8.

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

## Item 8

Unlike Items 1-7, this is not work that should leave the actor; it is a write
that never entered it. `ReloadConfig` runs on the watcher's goroutine (the
settled-`ark.toml` branch of `watchLoop`) and on an HTTP goroutine
(`handleScheduleSuppress`). It publishes a new `Config` to every holder (the DB,
scanner, searcher, indexer, link table), rebuilds the link table, writes U and E
records, and applies config changes, all while the actor and the off-actor
readers are using the same holders.

Since 2026-10-28 (#181) the `Config` itself is copy-on-write (R3673-R3675): a
reload builds its new config completely and publishes it in one `installConfig`
call, and no published config is ever written. That makes a held config safe to
read, but the publication is still a set of plain pointer writes made off the
actor, which is a data race in Go however brief. The rest of the reload's work
has the same problem: the link-table rebuild and the record writes, which every
other path runs on the actor or through the write actor.

Measured 2026-10-28, before deciding: `ReloadConfig` re-enters no actor, and its
record writes are direct `bolt.Update`s, the same as the reconcile's apply step
on the actor, so wrapping it is deadlock-safe. The watcher's reload branch goes
on to `ScanScheduleLogs`, which reaches the actor through `WriteTmpLog`, so only
the `ReloadConfig` call itself can be wrapped. The reads are the larger surface.
The operation discipline already covers them in principle: `withFTS` (DB and
Searcher) captures `config` into the view it binds, and copy-on-write makes that
snapshot immutable. But several binds run off the actor (`handleContentView`,
`handleExtPending`, `expandSearchOp.init`, and the Librarian's `newRecallOp`,
`newSubstrateOp`, `BuildFetchPayload` and `FindConnections`, not each traced),
and each reads the `db.config` field that `installConfig` writes. `withFTS`
cannot hop to the DB actor itself, because some callers are already on it and a
synchronous call into one's own actor deadlocks. An AST scan of every actor
closure body found none that reaches `srv.db` or calls `Sync` directly (positive
control: both planted violations found), so the discipline holds where a body
scan can see; a helper called from a closure is beyond it.

DECIDED (Bill, 2026-10-28): **a vending actor.** A small separate actor holds the
published `Config`. `installConfig`, on the DB actor, hands it each new one
synchronously; `withFTS` asks it for the current one, from anywhere. A
synchronous call from inside the DB actor into a different actor is safe so long
as that actor never calls back, so the vending actor is a **leaf**: its closures
only read or replace the value, and it calls no other actor synchronously. That
rule is written where it is defined and checked. Semantically it is an atomic
pointer (a Java `synchronized` getter and setter, as Bill put it), built from the
codebase's one concurrency idiom instead of a mutex or an atomic.

The shape:

1. The vending actor holds the published `Config`; `installConfig` publishes to it.
2. `withFTS` reads the config from it. Whether `DB.Config()` itself becomes the
   vending call (every read safe by construction) waits on measuring how often
   it is called: 22 uses inside actor closures and 9 off the actor, some possibly
   in per-file loops, each would become a channel round trip.
3. `ReloadConfig`'s other work (the link-table rebuild, the U and E record writes,
   `ApplyConfigChanges`) moves onto the DB actor in a `SyncVoid` at both callers,
   with `ScanScheduleLogs` outside it.

