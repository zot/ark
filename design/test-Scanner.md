# Test Design: Scanner (parallel walk)
**Source:** crc-Scanner.md

Cheap tests over the new parallel enumeration `walkSources(cfg, matcher, workers)`
— a temp directory tree plus a hand-built `*Config`/`*Matcher`, no `fts`, no
`*DB`. `walkSources` reads only the filesystem and the (mutation-free) Matcher,
so it is testable in isolation; the stateful serial-merge branch that calls
`fts.CheckFile` is covered end-to-end once #121's `*DB` harness lands (design.md
O214). The properties here are the ones a parallel rewrite can silently break:
a lost leaf under contention, an order that depends on goroutine scheduling, and
a data race on the shared result.

## Test: walk finds leaves and honors excluded dirs
**Purpose:** `walkSources` classifies included files, records unresolved leaves,
records a directory symlink as a candidate, and does NOT descend an excluded
directory. (R3437)
**Input:** a temp tree: `a/keep.md` (included), `a/skip/secret.md` under an
excluded `skip/`, `a/weird.xyz` (unresolved), and a dir symlink `a/link -> b`.
Run `walkSources(cfg, matcher, 8)`.
**Expected:** leaves contain `keep.md` (Included) and `weird.xyz` (Unresolved);
`skip/secret.md` is absent; link candidates contain `a/link`.
**Refs:** crc-Scanner.md
**Code:** scanner_test.go
**Alarm:** 1
**Fire alarm:** drop the `Excluded → skip subtree` branch in walkSources so it
descends excluded dirs — `skip/secret.md` then appears as a leaf and gets
indexed against the wrong patterns. Red: leaves include `secret.md`.
**Inject:** scanner.go:walkSources
**Pulled:** 2026-09-18 — rang (leaves differ at workers=1 and 8: excluded skip/ dir descended)

## Test: walk order is deterministic and worker-count invariant
**Purpose:** link candidates come back in the same DFS walk order a sequential
`filepath.WalkDir` produced, regardless of worker count, so the actor's later
`Observe` (and the reverse index it builds) is unchanged by parallelism. (R3437)
**Input:** a temp tree with several directory symlinks at different depths; run
`walkSources(cfg, matcher, 1)` and `walkSources(cfg, matcher, 8)`, and repeat the
8-worker run 20 times.
**Expected:** link-candidate order is identical across all runs and equals the
component-wise DFS order.
**Refs:** crc-Scanner.md
**Code:** scanner_test.go
**Alarm:** 2
**Fire alarm:** reverse the DFS sort comparator applied to link candidates in
walkSources — the order then no longer matches the sequential walk, changing the
`byTarget` reverse-index order a parallel walk must preserve. Red: order != the
expected DFS order.
**Inject:** scanner.go:walkSources
**Pulled:** 2026-09-18 — rang (link order != DFS order under a reversed comparator)

## Test: no data race on the shared result
**Purpose:** concurrent workers append leaves/links to the shared result under a
lock, so `-race` is clean over a multi-directory tree. (R3437)
**Input:** a temp tree wide enough to keep several workers busy; run
`walkSources(cfg, matcher, 8)` under `-race`; assert the leaf count.
**Expected:** no race reported; leaf count correct.
**Refs:** crc-Scanner.md
**Code:** scanner_test.go
**Alarm:** 3
**Fire alarm:** remove the mutex guarding the shared append in walkSources — the
race detector fires on concurrent slice appends. Red: `-race` reports a data race
(and counts flake low from lost appends).
**Inject:** scanner.go:walkSources
**Pulled:** 2026-09-18 — rang (DATA RACE on the leaves append under -race)

**Inject-past-the-list (2026-09-17):** removing `spawn`'s `default:` branch (making
the pool-slot acquire blocking) deadlocks a `workers=1` walk with a nested dir,
which `TestWalkSourcesMatchesSequentialWalk` runs — it times out rather than
passing, so the deadlock-avoidance invariant is guarded (an objection, not silence).
