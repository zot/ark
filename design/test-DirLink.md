# Test Design: DirLink
**Source:** crc-DirLink.md

Pure filesystem logic over a temp dir — no server, no DB. The refusal is the
property that is invisible when violated: a helper that deletes a real
directory reports success and destroys data.

## Test: refuse to remove a real directory
**Purpose:** RemoveDirLink deletes only a link, never a real directory at the destination (R3454)
**Input:** a temp dir with a real (non-link) subdirectory `dst/` holding a file
**Expected:** RemoveDirLink("dst") returns an error, and both `dst/` and its file still exist
**Refs:** crc-DirLink.md
**Code:** dirlink_test.go
**Fire alarm:** drop the is-a-real-directory guard so RemoveDirLink calls os.RemoveAll unconditionally; the test goes red because the real dir and its file vanish (and, before the guard, the assertion that the file survives fails)
**Inject:** dirlink.go:RemoveDirLink

## Test: recognize an already-owned link (idempotent)
**Purpose:** IsOurLink / MakeDirLink treat a re-link to the same target as a no-op, not an error (R3455)
**Input:** MakeDirLink(link, target) once, then again with the same target
**Expected:** the second call succeeds, the link still resolves to target, and it was not recreated (no error, target unchanged)
**Refs:** crc-DirLink.md
**Code:** dirlink_test.go
**Fire alarm:** make IsOurLink always return false so MakeDirLink re-creates over an existing link; the test goes red when the second call errors on an existing path (or churns the link)
**Inject:** dirlink.go:IsOurLink

## Test: make and read a directory link
**Purpose:** happy path — MakeDirLink creates a link readable through to the target (R3453)
**Input:** a target dir with a marker file; MakeDirLink(link, target)
**Expected:** reading `link/marker` returns the target's content; os.Readlink(link) reports the target
**Refs:** crc-DirLink.md
**Code:** dirlink_test.go
