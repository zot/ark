# Test Design: absClientPath
**Source:** crc-CLI.md

Covers R3403 — the client absolutizes a filesystem path argument against
the client's cwd before it crosses the socket, so the server (which
resolves a relative path against its own cwd) never sees a relative path.

## Test: relative path resolves against the client cwd, not home
**Purpose:** the fetch case that proved the bug — `ark fetch requests/foo.md`
run from a project directory must send `<project>/requests/foo.md`, never
`<home>/requests/foo.md` (what the server produced by joining the relative
path to its own `~`).
**Input:** `t.Chdir` into a fresh temp dir (a non-home cwd); call
`absClientPath("requests/pending-reader-defects.md")`.
**Expected:** equals `filepath.Join(<cwd>, "requests/pending-reader-defects.md")`,
is absolute, and is NOT `filepath.Join(<home>, rel)`.
**Refs:** crc-CLI.md
**Code:** cmd/ark/absclientpath_test.go
**Alarm:** 1
**Fire alarm:** revert `absClientPath` to return its argument unchanged
(the pre-fix behavior — the relative path shipped as-is). The cwd-relative
assertion then fails because the raw relative string is not absolute.
**Inject:** cmd/ark/main.go:absClientPath
**Pulled:** 2026-09-16 — rang

## Test: tmp:// and empty are left unchanged
**Purpose:** a tmp:// argument is a virtual key, not a filesystem path;
"" would resolve to the cwd. Both must pass through untouched.
**Input:** `absClientPath("tmp://luhmann/launch.md")` and `absClientPath("")`.
**Expected:** returned unchanged.
**Refs:** crc-CLI.md
**Code:** cmd/ark/absclientpath_test.go
**Alarm:** 2
**Fire alarm:** drop the `strings.HasPrefix(p, "tmp://")` guard so a tmp://
path is fed to filepath.Abs; the tmp:// case then comes back as
`<cwd>/tmp:/luhmann/launch.md` and the equality assertion fails.
**Inject:** cmd/ark/main.go:absClientPath
**Pulled:** 2026-09-16 — rang

## Test: already-absolute path is idempotent
**Purpose:** the local-dispatch arm passes already-absolute paths; Abs must
be a no-op so that arm is unaffected.
**Input:** `absClientPath("/home/deck/work/mini-spec/requests/foo.md")`.
**Expected:** returned unchanged.
**Refs:** crc-CLI.md
**Code:** cmd/ark/absclientpath_test.go
**Alarm:** 3
**Fire alarm:** none distinct from the two above; documents the invariant.
