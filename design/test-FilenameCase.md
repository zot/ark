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

## Test: a drive prefix is defined once
**Purpose:** R3635
**Input:** `C:\x.md`, `c:/x.md`, `Z:\`, and the near misses `C:x.md`, `1:\x`,
`/x/y.md`, `C:`, empty, `~/x.md`, `%uuid:"a"`.
**Expected:** 3 for the first three, 0 for the rest (Windows); 0 for
`C:/x.md` off Windows.
**Refs:** crc-FilenameCase.md
**Code:** drive_windows_test.go, pathcase_test.go

## Test: an `@ext` target keeps its drive
**Purpose:** R3636
**Input:** `C:\notes\a.md`, the same with `:"hello"`, and `C:/notes/a.md:3-5`;
anchorSkip of a drive target with a quoted anchor holding an `@tag:` shape.
**Expected:** the base keeps its drive; the narrower is the next colon; the
anchor is skipped whole.
**Refs:** crc-FilenameCase.md
**Code:** drive_windows_test.go
**Fire alarm:** search for the narrower colon from offset 0
**Inject:** ext.go:indexNarrowerColon
**Pulled:** 2026-10-26 (windows-gpu) — rang: `ParseExtTargetParts("C:\\notes\\a.md")` gave base `C` and range anchor `\notes\a.md`

## Test: a locator with no range keeps its drive
**Purpose:** R3637, recall's splitLoc.
**Input:** `C:\x.md` and `C:\x.md:3-5`.
**Expected:** the whole path with no range; path and `3-5`.
**Refs:** crc-FilenameCase.md
**Code:** drive_windows_test.go
**Fire alarm:** accept any colon as the range separator
**Inject:** recall_agent_builder.go:splitLoc
**Pulled:** 2026-10-26 (windows-gpu) — rang: `splitLoc(no range) = ("C", "\\x.md")`

## Test: the CLI's locator split keeps its drive
**Purpose:** R3637, the CLI half.
**Input:** `C:\x.md:3-5` and `C:\x.md`.
**Expected:** a locator for the first; not a locator for the second.
**Refs:** crc-FilenameCase.md
**Code:** cmd/ark/locator_windows_test.go
**Fire alarm:** drop the drive guard from splitPathLocator
**Inject:** cmd/ark/main.go:splitPathLocator
**Pulled:** 2026-10-26 (windows-gpu) — rang: `splitPathLocator(no range) = ("C", "\\x.md", true)`

## Test: the content view accepts a drive path
**Purpose:** R3638. Guarded by the existing content-view tests, which failed
on Windows before this ("absolute path required").
**Input:** the `TestContentView_*` suite on Windows.
**Expected:** passes.
**Refs:** crc-FilenameCase.md, crc-Server.md
**Code:** contentview_test.go
**Fire alarm:** require a leading `/` again
**Inject:** server.go:Server.contentPath
**Pulled:** 2026-10-26 (windows-gpu) — rang: `TestContentView_MarkdownFullFile` and the rest fail

## Test: PATH keeps its Windows list
**Purpose:** R3639
**Input:** PATH `C:\Windows\system32;C:\Program Files\Git\cmd`; injectPath of
`C:\Users\me\.ark`. Off Windows: `/opt/bin:/usr/bin` and `/home/me/.ark`.
**Expected:** three `;`-separated entries, intact; off Windows ark's dir just
before `/usr/bin`.
**Refs:** crc-FilenameCase.md
**Code:** drive_windows_test.go, pathcase_test.go
**Fire alarm:** split the list at `:`
**Inject:** db.go:injectPath
**Pulled:** 2026-10-26 (windows-gpu) — rang: `PATH = "C;\\Windows\\system32;C;\\Program Files\\Git\\cmd;C:\\Users\\me\\.ark"`

## Test: a mirror slug drops the drive colon
**Purpose:** R3640
**Input:** extMirrorPath of `C:\Users\me\notes\a.md` under `C:\Users\me\notes`.
**Expected:** `external\C-Users-me-notes\a.md.md`, no colon past the drive.
**Refs:** crc-FilenameCase.md, crc-DB.md
**Code:** drive_windows_test.go
**Fire alarm:** keep the colon in the slug
**Inject:** db.go:extMirrorPath
**Pulled:** 2026-10-26 (windows-gpu) — rang: `external\C:-Users-me-notes\a.md.md`

## Test: a drive glob is absolute
**Purpose:** R3641
**Input:** AnchorGlobToDir of `C:\notes\**`, `c:/notes/**` and `\notes\**`
against `C:\proj`; of `specs\**` against `C:\proj`. Off Windows: `C:/notes/**`
against `/p`.
**Expected:** the three absolute globs unchanged, `specs\**` joined; off
Windows the drive shape is joined.
**Refs:** crc-Searcher.md
**Code:** drive_windows_test.go, pathcase_test.go
**Fire alarm:** drop the drive passthrough
**Inject:** search.go:AnchorGlobToDir
**Pulled:** 2026-10-27 (windows-gpu) — rang: `AnchorGlobToDir("C:\\notes\\**") = "C:\\proj\\C:\\notes\\**"`

## Test: a `/`-written glob resolves its link
**Purpose:** R3642
**Input:** a link `s2\link` → `s1`; ResolvePattern of the native link path
followed by `/**/*.md`.
**Expected:** `s1/**/*.md`: the link resolved, the remainder unchanged.
**Refs:** crc-LinkTable.md
**Code:** drive_windows_test.go
**Fire alarm:** cut at the platform separator only
**Inject:** links.go:ResolvePattern
**Pulled:** 2026-10-27 (windows-gpu) — rang: `ResolvePattern = "…\\s2\\link/**/*.md", want "…\\s1/**/*.md"`

## Test: a native BIBLE address is recognized
**Purpose:** R3643
**Input:** bibleVirtualTarget of `C:\bibles\esv\BIBLE\1 Samuel`, and of
`C:\bibles\esv\BIBLE\a\b`.
**Expected:** source `C:\bibles\esv`, book `1 Samuel`; the nested book refused.
**Refs:** crc-BibleChunker.md
**Code:** drive_windows_test.go
**Fire alarm:** search for a literal `/BIBLE/`
**Inject:** biblechunker.go:bibleVirtualTarget
**Pulled:** 2026-10-27 (windows-gpu) — rang: `native address → ("", "", false)`
