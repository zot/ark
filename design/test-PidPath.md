# Test Design: PidPath
**Source:** crc-Server.md

Tests for `PidFilePath` / `LegacyPidFilePath` (server.go). Pure functions, no
collaborators — a temp dir and an env var.

## Test: PID path is in the ark home, TMPDIR-independent
**Purpose:** R3417 — the PID file lives at `<home>/ark.pid`, not in `os.TempDir()`,
so changing `TMPDIR` never moves it (the bug: server and `ark stop` under
different TMPDIRs disagreed on the location).
**Input:** `PidFilePath(home)` with and without a `TMPDIR` override.
**Expected:** always `<home>/ark.pid`.
**Refs:** crc-Server.md
**Code:** pid_path_test.go
**Alarm:** 1
**Fire alarm:** revert `PidFilePath` to the `filepath.Join(os.TempDir(), "ark"+name+".pid")`
form. The test goes red: the path is no longer `<home>/ark.pid` and moves with TMPDIR.
**Inject:** server.go:PidFilePath
**Pulled:** 2026-09-17 — rang (path returned under the temp dir, TMPDIR-dependent)

## Test: legacy path stays temp-derived for the stop fallback
**Purpose:** R3418 — `LegacyPidFilePath` keeps the old TMPDIR-derived flat name so
`ark stop` can still stop a server started by the pre-move binary.
**Input:** `LegacyPidFilePath(home)`.
**Expected:** not in the home dir; flat name, not `ark.pid`.
**Refs:** crc-Server.md
**Code:** pid_path_test.go
**Alarm:** 2
**Fire alarm:** point `LegacyPidFilePath` at `<home>/ark.pid`. The test goes red:
the fallback would then equal the new path and stop the old server's file lookup.
**Inject:** server.go:LegacyPidFilePath
**Pulled:** 2026-09-17 — rang (legacy path landed in the home dir)
