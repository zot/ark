# Filename case

Language: Go. Environment: the ark server and CLI, on every platform; the
behaviour below is active on Windows and a no-op elsewhere.

Windows filesystems are case-insensitive: `Casey.md`, `CASEY.md` and
`casey.MD` name one file. Ark keeps one name per file (see the Path identity
theme in [index.md](index.md)), and without care a case-insensitive filesystem
gives a file several. Measured 2026-10-26: renaming `Casey.md` to `CASEY.md`
left the file indexed under both names, both came back from search, and no
refresh or scan dropped the old one, because a stat of the old spelling still
succeeds.

## One spelling: the one on disk

On Windows, every path arriving from outside is rewritten to its **on-disk
spelling** before ark uses it: each component takes the case the filesystem
stores for it. This happens at the same places link resolution happens
([symlinks.md](symlinks.md)), so a CLI argument, an `@ext` target, a content
URL and the wildcard-free prefix of a search filter all arrive spelled the
way the disk spells them. Watcher events and each `[[source]].dir` are
rewritten too: those do not pass through link resolution, and everything a
scan or watch produces descends from them.

The rewrite stops at the first component that does not exist, and at the
first component holding a glob character (`*`, `?`, `[`), keeping the rest as
written. A missing path stays what it was, so it is reported as not found in
the usual way, and a glob is never mistaken for the file it happens to match
first.

Because every path inside ark is then spelled one way, ark's own path-keyed
state (records keyed by path, in-memory path sets, source-root matching,
the link table) needs no case handling of its own.

The cost is one filesystem lookup per path component at entry, on Windows
only. Elsewhere the rewrite returns the path unchanged.

## A respelled path is a gone path

A path is live only if it exists **under that spelling**. On a
case-insensitive filesystem a stat of the old spelling of a renamed file still
succeeds, so the usual existence test would keep the old name forever. So an
unresolved-file record or a recorded directory link whose stored spelling no
longer matches the disk is treated exactly as if its file had vanished, and
dropped the same way.

## The index folds what it keys on

A database ark creates on Windows sets microfts2's case-insensitive filenames
option (microfts2 `specs/filename-case.md`, Bill, 2026-10-26). microfts2 then
keys each file on its path uppercased and keeps the spelling as given, so a
case-only rename reaches the same file record, keeping its chunks, tags and
embeddings, and updates its spelling (the last spelling written wins). With
the on-disk rewrite, the spelling written is always the current one, so an
event for the old name cannot restore it.

A case-only rename changes neither a file's content nor its modification time,
so ark's skip for unchanged content would never reach the index to respell it.
That skip therefore also requires the stored spelling to match: a file whose
spelling changed is re-indexed, which stores the new spelling and keeps the
ids of its unchanged chunks. Measured 2026-10-26: without this the rename left
one entry, still under the old spelling.

A refresh of a stale file starts from the path the index stored, so that path
is spelled as the disk spells it before the refresh begins. Otherwise a renamed
file that was also re-saved would refresh, and stay, under its old name.

The option is fixed when the database is created. A Windows index created
before it keeps the old behaviour until it is rebuilt.
