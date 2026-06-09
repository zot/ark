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
