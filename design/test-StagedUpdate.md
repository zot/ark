# Test Design: Requested update (staging, startup step, UI bridge)
**Source:** crc-StagedUpdate.md, crc-UpdateBridge.md

StagedUpdate cases build `~/.ark` in `t.TempDir()` and inject every effect:
`versionOf` returns a scripted version or error, `stage` and `chain` record their
calls, `serverRunning` is a bool. No network, no process replacement. Stage and
Check run against the existing fake `ReleaseFetcher` from `update_test.go`.
Bridge cases inject `run` and read `Status()`.

## Test: stage writes ark.new and update.json, and nothing partial
**Purpose:** a verified download lands under the staged name with the request recorded (R3526, R3527)
**Input:** fake fetcher with latest `0.9.0`, a binary, its checksum; Stage(0); then a fetcher whose checksum mismatches
**Expected:** `ark.new` holds the binary, `update.json` is `{version: 0.9.0, downloadAttempts: 0}`, no `ark.new.tmp` left; with the bad checksum neither `ark.new` nor `update.json` exists
**Refs:** crc-StagedUpdate.md, seq-install.md
**Code:** cmd/ark/staged_update_test.go

## Test: stage and check are no-ops when current
**Purpose:** R3471's rule holds for the staged path; check reports without writing (R3526, R3528)
**Input:** running version `0.9.0`, latest `0.9.0`; Stage(0) and Check()
**Expected:** no `ark.new`, no `update.json`; Check returns latest `0.9.0`, newer false
**Refs:** crc-StagedUpdate.md
**Code:** cmd/ark/staged_update_test.go

## Test: startup ignores a stray ark.new, and a live server
**Purpose:** nothing happens without a request, and never under a running server (R3532)
**Input:** (a) `ark.new` present, no `update.json`; (b) a valid request with `serverRunning` true
**Expected:** in both, no rename, no chain, no stage call, files unchanged
**Refs:** crc-StagedUpdate.md, seq-install.md
**Code:** cmd/ark/staged_update_test.go
**Alarm:** 1
**Fire alarm:** drop the `serverRunning` check from Run. Red: case (b) chains.
**Inject:** cmd/ark/staged_update.go:StagedUpdate.Run
**Pulled:** 2026-10-19 — rang: `swapped or chained (1 chains, ark.old exists true)`

## Test: a later start sweeps ark.old with no request pending
**Purpose:** the aside copy from the last update does not wait for the next update to be removed (R3534)
**Input:** no `update.json`; a leftover `ark.old`
**Expected:** `ark.old` is gone after Run
**Refs:** crc-StagedUpdate.md, seq-install.md
**Code:** cmd/ark/staged_update_test.go
**Alarm:** 7
**Fire alarm:** move the sweep back after the request check (the shape the first Windows run caught). Red: `ark.old survived a start with no update pending`.
**Inject:** cmd/ark/staged_update.go:StagedUpdate.Run
**Pulled:** 2026-10-19 — rang: `ark.old survived a start with no update pending`

## Test: startup skips a developer install
**Purpose:** a symlinked `~/.ark/ark` is never swapped (R3533)
**Input:** `~/.ark/ark` a symlink, a valid request
**Expected:** no rename, no chain; the link is still a link
**Refs:** crc-StagedUpdate.md
**Code:** cmd/ark/staged_update_test.go

## Test: a valid staged binary is swapped and chained
**Purpose:** the success path, in order (R3534, R3537)
**Input:** request `0.9.0`, `versionOf(ark.new)` = `0.9.0`, a leftover `ark.old`
**Expected:** old `ark` bytes now at `ark.old`, the staged bytes at `ark`, no `ark.new`; chain called once with `~/.ark/ark` and the original args; the earlier `ark.old` was replaced
**Refs:** crc-StagedUpdate.md, seq-install.md
**Code:** cmd/ark/staged_update_test.go

## Test: a staged binary reporting another version is rejected
**Purpose:** validation means "runs and is the version requested", not just "runs" (R3535)
**Input:** request `0.9.0`, `versionOf(ark.new)` = `0.8.0` (exits fine)
**Expected:** no chain, `ark.new` deleted, `downloadAttempts` 1, stage called with 1
**Refs:** crc-StagedUpdate.md
**Code:** cmd/ark/staged_update_test.go
**Alarm:** 2
**Fire alarm:** accept any `versionOf` that returns no error. Red: it chains.
**Inject:** cmd/ark/staged_update.go:StagedUpdate.Run
**Pulled:** 2026-10-19 — rang: `swapped or chained (1 chains …)`, `downloadAttempts = 0, want 1`

## Test: three failed validations abandon the request
**Purpose:** a binary broken for this machine is not re-downloaded forever (R3535, R3536)
**Input:** request with `downloadAttempts` 2, `versionOf` failing
**Expected:** `update.json` removed, `ark.new` deleted, stage not called, a log line naming the abandoned version
**Refs:** crc-StagedUpdate.md
**Code:** cmd/ark/staged_update_test.go
**Alarm:** 3
**Fire alarm:** compare against the count before incrementing (`attempts < max` on the old value). Red: stage is called a third time.
**Inject:** cmd/ark/staged_update.go:StagedUpdate.Run
**Pulled:** 2026-10-19 — rang: `update.json or ark.new survived the third failure`, `downloaded a fourth time`

## Test: the chained binary finishes instead of swapping again
**Purpose:** a request for the running version means "refresh and clear" (R3539)
**Input:** request `0.9.0`, running version `0.9.0`, no `ark.new`
**Expected:** refresh called once, `update.json` removed, no chain, no stage
**Refs:** crc-StagedUpdate.md, seq-install.md
**Code:** cmd/ark/staged_update_test.go
**Alarm:** 4
**Fire alarm:** remove the own-version branch. Red: refresh is never called and `update.json` survives (the step falls through to validating a missing `ark.new` and counts an attempt).
**Inject:** cmd/ark/staged_update.go:StagedUpdate.Run
**Pulled:** 2026-10-19 — rang: `refresh called 0 times, want 1`

## Test: a failed second rename restores ark
**Purpose:** a half-done swap never leaves `~/.ark/ark` missing (R3537)
**Input:** a request whose `versionOf` reports the right version while `ark.new` does not exist, so the first rename succeeds and the second fails (a directory at `ark.new` would not do: Linux renames a directory onto a missing path)
**Expected:** Run returns an error, `~/.ark/ark` holds the original bytes, no chain
**Refs:** crc-StagedUpdate.md
**Code:** cmd/ark/staged_update_test.go
**Alarm:** 5
**Fire alarm:** drop the rollback rename. Red: `~/.ark/ark` does not exist.
**Inject:** cmd/ark/staged_update.go:swapStaged
**Pulled:** 2026-10-19 — rang: reading `~/.ark/ark` fails with no such file (the missing binary is the property itself)

## Test: the lock keeps a second starter out
**Purpose:** two simultaneous starts cannot both rename (R3534)
**Input:** hold `update.lock` from the test, then Run with a valid request
**Expected:** no rename, no chain; after releasing, Run proceeds
**Refs:** crc-StagedUpdate.md
**Code:** cmd/ark/staged_update_test.go

## Test: the bridge reports check, download and staged states
**Purpose:** the UI sees in-flight work and the outcome; a stage from an earlier session reads as staged (R3540, R3541, R3542)
**Input:** `run` blocking on a channel; CheckForUpdate, read Status, release with `0.9.0\nnewer than 0.5.0\n`; then RequestUpdate with `run` failing; then files `update.json` + `ark.new` on disk with a fresh bridge
**Expected:** `checking` while blocked, then `idle` with latest `0.9.0`, newer true; `failed` with the error; the fresh bridge reports `staged`
**Refs:** crc-UpdateBridge.md
**Code:** update_bridge_test.go
**Alarm:** 6
**Fire alarm:** run the child synchronously inside CheckForUpdate. Red: `CheckForUpdate blocked until the check finished` (the test calls it from a goroutine and gives it a second, so red is an assertion, not a hung suite).
**Inject:** update_bridge.go:UpdateBridge.CheckForUpdate
**Pulled:** 2026-10-19 — rang: `CheckForUpdate blocked until the check finished; it must return at once`
