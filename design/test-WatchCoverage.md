# Test Design: Watch Coverage

**Source:** crc-DB.md

Guards R2952 (F1): the live watcher descends exactly the directories the
Scanner walks. `DB.IsWatchableDir` is the shared descent predicate.

## Test: IsWatchableDir matches the Scanner's descent rule (Sleeping Sentry)
**Purpose:** A non-excluded dot-directory under a source is watchable when
`dotfiles=true` (the F1 bug: `.scratch/` edits went stale because the
recursive watch unconditionally skipped dot-dirs); a directory excluded as a
directory (`.git/`, `node_modules/`) is not watched; and the predicate equals
the Scanner's descent decision (`Classify` isDir=true != Excluded) for every
directory.
**Input:** Config with `dotfiles=true`, `default_exclude=[".git/",
"node_modules/"]`, one source at `/repo`. Probe `.scratch`, `.scratch/nested`,
`src`, `.git`, `node_modules`, and a path under no source.
**Expected:** `.scratch`, `.scratch/nested`, `src` → watchable; `.git`,
`node_modules`, out-of-source → not watchable; `IsWatchableDir(p) ==
(Classify(p, isDir=true) != Excluded)` for every probe.
**Refs:** crc-DB.md, seq-file-change.md, R2952
**Code:** watch_coverage_test.go

## Test: a link event survives the indexability filter and is never cached

**Purpose:** R3390's other half, and the one that is invisible when it
fails. A directory symlink matches no *file* pattern, so the filter that
guards the refresh drops the very event that repoints a tracked link — and
then writes the path into the negative cache, so every later event on it is
dropped without ever being classified. The single-hop tier (R3384) is
defined on that event, so the failure is silent in the wrong direction:
the association keeps returning its old, once-correct answer.
**Input:** a Server with an empty `ignoredPaths`, one source, includes
`*.md`. Classify: an indexable `a.md`; a directory symlink `link` (no
extension, matching nothing); a non-indexable regular file `notes.txt`; a
path that is gone.
**Expected:** `a.md` → index; `link` → link; `notes.txt` → ignore *and*
present in `ignoredPaths`; gone → link. Assert specifically that `link` is
**absent** from `ignoredPaths` afterward — caching it is what makes the
second and every subsequent event disappear.
**Alarm:** 1
**Fire alarm:** delete the `linkCandidate` arm of `classifyEvent`. The link
path then falls to the cache exactly as it does today, which is the state
this requirement exists to change. Confirm red.
**Inject:** watcher.go:classifyEvent
**Refs:** crc-Server.md, seq-file-change.md#1.3.5, R3390

## Test: an event overflow reconciles
**Purpose:** R3625. An overflow means the OS dropped change events, so the
files they named stay stale unless the watcher re-scans.
**Input:** `handleWatchError` with `fsnotify.ErrEventOverflow`, the same
sentinel wrapped by `fmt.Errorf("%w")`, and an unrelated error, each with a
counting reconcile.
**Expected:** one reconcile for each overflow case, none for the other error.
**Refs:** crc-Server.md, seq-file-change.md
**Code:** watch_coverage_test.go
**Fire alarm:** go back to logging only (drop the reconcile call); both
overflow cases fail "reconcile called 0 times … want 1"
**Inject:** watcher.go:handleWatchError
**Pulled:** 2026-10-26 — rang: with the call dropped, `overflow` fails "reconcile called 0 times for fsnotify: queue or buffer overflow, want 1" and `wrapped overflow` fails likewise

## The watch rig
The tests below share `newWatchRig`: a real database (Init + Open, so the DB actor runs), a real fsnotify watcher set up as `startWatching` sets it up (the `ark.toml` file, then every source), and the config routes `ark config` uses. Every mutation goes through its route, so it reaches the watcher the production way: configMutate, the coalesced reconcile on the actor, doReconcile, then the walk's apply step. A test ends when its watch condition holds, but the reconcile it started may still be running its tail, so the rig's cleanup waits for the reconcile to finish before the database closes. Without that wait the close lands under the reconcile and the panic surfaces outside any test (seen once, 2026-10-28).

## Test: a source added while the server runs is watched
**Purpose:** R3670 (R351): a concrete source added at runtime is watched, not only indexed. Before the fix only glob expansion started a watch after startup
**Input:** the rig; `POST /config/add-source` for a new directory holding a subdirectory
**Expected:** both directories appear in the watcher's WatchList
**Refs:** crc-Server.md, seq-reconcile.md, R351, R3670
**Code:** watch_coverage_test.go
Written before the fix, it failed against the unfixed code (`…/proj is not watched after add-source; watched: []`), the Linux measurement the carve lacked. Its alarm, dropping watchNewSources, was pulled 2026-10-28 and did **not** ring: the walk's fill-in (R3672) watches the source once the walk returns, so this test sees *that* the source is watched but not *when*. The ordering test below holds that alarm.

## Test: a new source is watched before its scan
**Purpose:** R3670: a new source is watched before its scan runs, so an edit made during the scan is caught, as at startup (R359). Only the ordering tells watchNewSources from the walk's fill-in
**Input:** the rig; inside one actor closure, add the source through MutateConfig, call doReconcile, and read the WatchList. The walk's apply step needs the actor, so it cannot run before the closure ends
**Expected:** the source and its subdirectory are watched when doReconcile returns
**Refs:** crc-Server.md, seq-reconcile.md, R3670
**Code:** watch_coverage_test.go
**Fire alarm:** drop the watchNewSources call from doReconcile
**Inject:** server.go:doReconcile
**Pulled:** 2026-10-28 (local) — rang: `the new source was not watched when doReconcile returned, so its scan ran unwatched; watch list: […/ark.toml]`

## Test: a removed source is unwatched, and the ark.toml watch is not
**Purpose:** R3671 (R351): removing a source stops its watches, one entry at a time, and the sweep never takes the watch on `ark.toml`, a file under no source
**Input:** the rig; add-source of a directory with a subdirectory, then remove-source
**Expected:** both directories leave the WatchList; `ark.toml` stays on it
**Refs:** crc-Server.md, seq-reconcile.md, R351, R3671
**Code:** watch_coverage_test.go
**Fire alarm:** drop the unwatchStaleDirs call from doReconcile
**Inject:** server.go:doReconcile
**Pulled:** 2026-10-28 (local) — rang: `after remove-source: watched(…/gone) = true, want false`
**Fire alarm:** drop the ark.toml guard from unwatchStaleDirs
**Inject:** watcher.go:unwatchStaleDirs
**Pulled:** 2026-10-28 (local) — rang: `the ark.toml watch was removed; watch list: []`

## Test: removing a nested source keeps the outer source's watches
**Purpose:** R3671: a source removed from inside a source that remains leaves the shared directories watched. A recursive unwatch of the inner source, which the old orphan path did, would take them
**Input:** the rig; sources `outer/`, `outer/inner/` (holding `deep/`) and an unrelated `other/`; remove-source of `inner/`, then of `other/`, whose unwatching marks that the first removal's reconcile has run
**Expected:** `outer/`, `inner/` and `inner/deep/` all still watched
**Refs:** crc-Server.md, R3671
**Code:** watch_coverage_test.go
**Fire alarm:** a recursive unwatch of the removed source's subtree in handleConfigRemoveSource
**Inject:** server.go:handleConfigRemoveSource
**Pulled:** 2026-10-28 (local) — rang: `…/outer/inner lost its watch when a source nested in outer/ was removed`

## Test: an exclude change moves the watches
**Purpose:** R3671, R3672: an exclude stops the watches on the subtree it covers, and removing it watches that subtree again. The second half is the scan walk's fill-in: the source root is still watched, so only the walked directory list can show the subtree is missing
**Input:** the rig; a source with `skip/` and `keep/`; add-exclude `skip/` on it, then remove-pattern `skip/`
**Expected:** `skip/` unwatched after the exclude while `keep/` stays watched; `skip/` watched again after the exclude is removed
**Refs:** crc-Server.md, crc-Scanner.md, seq-reconcile.md, R3671, R3672
**Code:** watch_coverage_test.go
**Fire alarm:** drop the watchWalkedDirs call from the walk's apply step
**Inject:** server.go:startReconcileWalk
**Pulled:** 2026-10-28 (local) — rang: `after removing the exclude: watched(…/src/skip) = false, want true`

These tests are also the copy-on-write config's race guard (R3673): under `-race` they failed with four data races in `walkSources` against `AddSource`/`RemoveSource` before it, and pass twenty runs in a row with it. See test-ConfigClone.md.

Not driven separately: the `ark.toml` reload, which ends in the same `srv.reconcile()`. Not tested: that a watched source is not walked again, which WatchList makes a lookup; and that an orphaned glob source stays watched.
