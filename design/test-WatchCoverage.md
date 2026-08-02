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
**Fire alarm:** delete the `linkCandidate` arm of `classifyEvent`. The link
path then falls to the cache exactly as it does today, which is the state
this requirement exists to change. Confirm red.
**Refs:** crc-Server.md, seq-file-change.md#1.3.5, R3390
