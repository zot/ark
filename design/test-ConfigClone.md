# Test Design: Config copy-on-write
**Source:** crc-Config.md, crc-DB.md

A published `Config` is never written (R3673), so everything rests on the copy
sharing nothing with its original (R3674). A field added later that `Clone`
missed would be shared, and written under a reader, with nothing failing.

## Test: Clone shares nothing with its original
**Purpose:** R3674: every slice, map and pointer reachable in a Config, unexported fields included, is fresh in the clone, and the clone equals the original
**Input:** a Config whose every nil slice, map and pointer is given one element, recursively, with unexported fields reached through `unsafe` (test only); the fill must reach at least 20 references or the test refuses to pass
**Expected:** `reflect.DeepEqual(c, clone)`; no slice, map or pointer at any path is shared
**Refs:** crc-Config.md, R3674
**Code:** config_clone_test.go
**Fire alarm:** drop the explicit clone of the unexported derivedStrategies
**Inject:** config.go:Config.Clone
**Pulled:** 2026-10-28 (local) — rang: `Clone shares Config.derivedStrategies with its original`
**Fire alarm:** deepCopy returns maps unchanged
**Inject:** config.go:deepCopy
**Pulled:** 2026-10-28 (local) — rang: `Clone shares Config.Strategies … Sources[].Strategies … Schedule.Tag … Luhmann.Classes`, naming each shared field

## Test: a config change does not race the reconcile walk
**Purpose:** R3673: configMutate changes a copy, so the off-actor walk holding the old Config is never written under
**Input:** the watch rig's tests (test-WatchCoverage.md) under `-race`, twenty runs: rapid add-source, remove-source and exclude changes while reconcile walks run
**Expected:** no data race
**Refs:** crc-DB.md, crc-Server.md, R3673
**Code:** watch_coverage_test.go
**Fire alarm:** MutateConfig applies fn to the live config instead of a Clone (the pre-fix behavior)
**Inject:** db.go:DB.MutateConfig
**Pulled:** 2026-10-28 (local) — rang under `-race`: four `DATA RACE` reports, `(*Config).AddSource` against `walkSources`

Not tested: the scheduler re-point after a mutation, and ReloadConfig's ordering (built, then installed), beyond the suites passing.
