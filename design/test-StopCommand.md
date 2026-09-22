# Test Design: StopCommand
**Source:** crc-CLI.md, crc-Server.md

Covers `ark stop`'s portable graceful-shutdown path (windows-support Part 2):
socket-dial liveness/release, the POST-/shutdown-then-poll flow, and the
platform-split timeout fallback.

## Test: socket-dial liveness — alive then released
**Purpose:** `socketReleasedWithin` reads a live unix socket as NOT released and
a closed/absent one as released — the portable liveness check that replaces the
`proc.Signal(0)` probe (R3443).
**Input:** a temp-dir `ark.sock` with a live `net.Listen("unix", …)`; then the
listener closed.
**Expected:** returns `false` while the listener accepts; returns `true` once the
listener is closed (socket unlinked / dial refused).
**Refs:** crc-CLI.md
**Code:** cmd/ark/stop_test.go
**Fire alarm:** invert the dial sense in `socketReleasedWithin` (treat a
successful dial as released) — the "alive" assertion goes red because a running
server would be reported down.
**Inject:** cmd/ark/main.go:socketReleasedWithin
**Pulled:** 2026-09-22 — rang: "socket reported released while server is up"

## Test: refused dial reads as released
**Purpose:** with no socket present, `socketReleasedWithin` returns immediately
true (R3443).
**Input:** a temp-dir path with no listener.
**Expected:** `true` without waiting the full timeout.
**Refs:** crc-CLI.md
**Code:** cmd/ark/stop_test.go
**Fire alarm:** same injection as above (invert dial sense) makes this hang to
the timeout and return false.
**Inject:** cmd/ark/main.go:socketReleasedWithin
**Pulled:** 2026-09-22 — rang

## Test: graceful stop releases the socket
**Purpose:** POSTing `/shutdown` over the socket (via the real `serverClient` +
`proxyRaw`) and then polling by dial observes the server release the socket
(R3441, R3442, R3443). The double responds then closes the listener, mirroring
`handleShutdown` minus its `os.Exit` (which a unit test cannot survive).
**Input:** a temp-dir `ark.sock` served by an `http.Server` whose `POST
/shutdown` handler writes+flushes a 200 then closes the listener.
**Expected:** `socketReleasedWithin` is `false` before the POST and `true` after.
**Refs:** crc-CLI.md, crc-Server.md
**Code:** cmd/ark/stop_test.go
**Fire alarm:** drop the `ln.Close()` from the handler double (respond but never
tear down) — the post-POST release assertion goes red.
**Inject:** cmd/ark/stop_test.go:TestGracefulStopReleasesSocket
**Pulled:** 2026-09-22 — rang: "socket not released after graceful stop"

## Test: Linux timeout fallback force-kills
**Purpose:** on Unix, `timeoutKill` force-kills the target PID and returns nil
once the socket is released (R3444).
**Input:** a real child (`sleep 60`) plus a socket path with no listener
(releases immediately).
**Expected:** `timeoutKill` returns nil; `cmd.Wait()` returns within 2s (the
child was killed), not blocking the full sleep.
**Refs:** crc-CLI.md
**Code:** cmd/ark/stop_fallback_unix_test.go
**Fire alarm:** comment out `proc.Kill()` in `timeoutKill` — the child survives,
`cmd.Wait()` never returns inside 2s, the select times out red.
**Inject:** cmd/ark/stop_fallback_unix.go:timeoutKill
**Pulled:** 2026-09-22 — rang: "child still alive 2s after timeoutKill"

## Test: serve-loop error is clean under the shutdown latch
**Purpose:** `serveExitError` returns nil when the `shuttingDown` latch is set —
so a graceful shutdown's closed-listener `Accept` error is not propagated as a
fatal that would race the teardown's `os.Exit(0)` and skip `db.Close()` (R3446).
Pure decision logic, tested on a zero-value `Server` with no socket, DB, or
server: latch set swallows any serve error (and nil); latch clear swallows only
`net.ErrClosed` (wrapped or bare) and propagates every other error unchanged.
**Input:** a zero-value `Server`; `shuttingDown` toggled via `Store(true)`;
sample errors (a plain accept error, `net.ErrClosed`, a `%w`-wrapped
`net.ErrClosed`, and nil).
**Expected:** latched → nil for all inputs; unlatched → nil for `net.ErrClosed`
(bare or wrapped) and nil, the original error otherwise.
**Refs:** crc-Server.md
**Code:** shutdown_race_test.go
**Fire alarm:** replace `serveExitError`'s body with `return err` (the
pre-fix behavior) — the latched cases and the `net.ErrClosed` cases go red
("serveExitError = … , want nil").
**Inject:** server.go:serveExitError
**Pulled:** 2026-09-22 — rang: latched-swallow and ErrClosed cases returned the error where nil was wanted

## Test: Windows timeout fallback errors without killing
**Purpose:** on Windows, `timeoutKill` returns an error (no TerminateProcess in
the default path) pointing the user at `ark stop -f` (R3444).
**Input:** any PID and socket path (the Windows body ignores them).
**Expected:** non-nil error whose text names `-f`.
**Refs:** crc-CLI.md
**Code:** cmd/ark/stop_fallback_windows_test.go
**Fire alarm:** make the Windows `timeoutKill` return nil — the error assertion
goes red. This is a prescription, not yet run: a Linux dev box cannot execute a
GOOS=windows test binary. It compiles under `GOOS=windows go test -c`, verified
2026-09-22. No `**Pulled:**` line until it is run on Windows.
**Inject:** cmd/ark/stop_fallback_windows.go:timeoutKill
