# Test Design: FilenameCase
**Source:** crc-FilenameCase.md

The behaviour exists only on a case-insensitive filesystem, so the Windows
tests run on the VM as a compiled test binary
(`GOOS=windows go test -c`); the Linux test pins the identity.

## Test: off Windows nothing is respelled
**Purpose:** R3626, R3629, R3630 on case-sensitive filesystems: two spellings
are two files.
**Input:** mixed-case absolute, relative and tmp:// paths.
**Expected:** `DiskSpelling` returns each unchanged, `respelled` is false,
`foldFilenames` is false.
**Refs:** crc-FilenameCase.md
**Code:** pathcase_test.go

## Test: DiskSpelling rewrites case only
**Purpose:** R3626
**Input:** a temp `Src\Casey.md`, given lowercased throughout; with a missing
tail; with a glob last component; with a lowercase drive letter; a tmp:// path.
**Expected:** the disk's spelling for what exists; the missing tail and the glob
kept as written; `C:`; tmp:// unchanged.
**Refs:** crc-FilenameCase.md
**Code:** pathcase_windows_test.go
**Fire alarm:** drop the glob guard; FindFirstFile expands the wildcard and the
glob case comes back as the first file it matched
**Inject:** pathcase_windows.go:DiskSpelling
**Pulled:** 2026-10-26 (windows-gpu) — rang: `src\*.MD` came back as `Src\Casey.md`

## Test: a case-renamed path is not live
**Purpose:** R3629
**Input:** `Casey.md` renamed to `CASEY.md`; the old spelling still stats.
**Expected:** `respelled(old)` true, `respelled(new)` false; an unresolved
record under the old spelling is not live, under the new one it is.
**Refs:** crc-FilenameCase.md, crc-Store.md
**Code:** pathcase_windows_test.go
**Fire alarm:** drop the respelled clause from unresolvedIsLive; the old
record stays live
**Inject:** store.go:unresolvedIsLive
**Pulled:** 2026-10-26 (windows-gpu) — rang: TestCaseRenamedPathIsNotLive fails

## Test: link resolution returns the disk's spelling
**Purpose:** R3627
**Input:** an empty LinkTable; `ResolveVia` of an existing file's path uppercased.
**Expected:** the file's on-disk spelling.
**Refs:** crc-FilenameCase.md, crc-LinkTable.md
**Code:** pathcase_windows_test.go
**Fire alarm:** resolve without DiskSpelling; red shows the uppercased path back
**Inject:** links.go:LinkTable.ResolveVia
**Pulled:** 2026-10-26 (windows-gpu) — rang: ResolveVia returned the input uppercased

## Test: a source dir takes the disk's spelling
**Purpose:** R3628 (config half; the watcher half is the live probe in the
windows-support carve, 8.3)
**Input:** an ark.toml whose `[[source]].dir` is an existing dir uppercased.
**Expected:** `LoadConfig` yields the dir in on-disk spelling.
**Refs:** crc-FilenameCase.md, crc-Config.md
**Code:** pathcase_windows_test.go
**Fire alarm:** load the dir with tilde expansion only; red shows the uppercased dir
**Inject:** config.go:LoadConfig
**Pulled:** 2026-10-26 (windows-gpu) — rang: the source dir stayed uppercased

## Test: a case rename keeps one indexed file
**Purpose:** R3630, through the real Init and Open.
**Input:** add `Casey.md`; rename it `CASEY.md`; add the old name spelled by
DiskSpelling, as the watcher would.
**Expected:** the new spelling is indexed under the same file id, and its stored
name is the new spelling.
**Refs:** crc-FilenameCase.md, crc-DB.md
**Code:** pathcase_windows_test.go
**Fire alarm:** create the database without folding (foldFilenames false); the
rename indexes a second file
**Inject:** pathcase_windows.go:foldFilenames
**Pulled:** 2026-10-26 (windows-gpu) — rang: "file id 2 after the rename, want 1: the rename indexed a second file"

## Test: a case rename respells through the watcher's scan
**Purpose:** R3631, the scan store's unchanged skip. A case-only rename
changes neither content nor mtime; measured live 2026-10-26, the skip kept the
old spelling.
**Input:** add `Casey.md`, rename it `CASEY.md`, then `IndexPathsAsync` of the
old name spelled by DiskSpelling (the watcher's path), polled to a deadline.
**Expected:** the stored spelling becomes `CASEY.md`; the chunk ids are kept;
the old file id is not left indexed beside the new one (a full reindex takes a
new file id, as any edit does).
**Refs:** crc-FilenameCase.md, crc-FileScanActor.md
**Code:** pathcase_windows_test.go
**Fire alarm:** drop spelledAsStored from storeContentUnchanged; the skip keeps
the old name until the deadline
**Inject:** indexer.go:Indexer.storeContentUnchanged
**Pulled:** 2026-10-26 (windows-gpu) — rang: "stored spelling after the watcher's scan [...Casey.md] … the unchanged skip kept the old name". The same clause on the refresh path's skip did not ring with any test: a same-bytes rename is never stale (staleness is by content), so that clause was removed as unreachable.

## Test: a stale refresh runs under the current spelling
**Purpose:** R3631, RefreshStale. A rename plus an edit is stale, and its path
comes from the index in the old spelling.
**Input:** add, rename, edit, then `Refresh` with no pattern (on Windows the
Matcher cannot yet match an absolute path, 8.4).
**Expected:** fresh, stored as `CASEY.md`.
**Refs:** crc-FilenameCase.md, crc-Indexer.md
**Code:** pathcase_windows_test.go
**Fire alarm:** take the stale path as stored; the refresh reindexes and keeps
the old name
**Inject:** indexer.go:Indexer.RefreshStale
**Pulled:** 2026-10-26 (windows-gpu) — rang: stored spelling stayed `Casey.md`

## Test: a reconcile scans under the current spelling
**Purpose:** R3631, RefreshAsync.
**Input:** add, rename, edit, then `RefreshAsync` through the actor, polled.
**Expected:** fresh, stored as `CASEY.md`.
**Refs:** crc-FilenameCase.md, crc-DB.md
**Code:** pathcase_windows_test.go
**Fire alarm:** scan the stale path as stored; the old name is kept
**Inject:** db.go:DB.RefreshAsync
**Pulled:** 2026-10-26 (windows-gpu) — rang: "stored spelling after the reconcile [...Casey.md]"

## Test: a case-renamed link is unresolvable
**Purpose:** R3629, the link half.
**Input:** a directory symlink `Linky` inside a source, observed; renamed `LINKY`.
**Expected:** Observe of the old spelling is LinkUnresolvable; of the new, not.
Skips without the symlink privilege.
**Refs:** crc-FilenameCase.md, crc-LinkTable.md
**Code:** pathcase_windows_test.go
**Fire alarm:** drop the respelled clause from Observe; the old spelling still
resolves
**Inject:** links.go:LinkTable.Observe
**Pulled:** 2026-10-26 (windows-gpu) — rang: "Observe(old spelling) = 0, want LinkUnresolvable"
