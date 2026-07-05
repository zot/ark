# Test Design: Bloodhound CLI Fixer (S2)

**Source:** crc-RecallWatcher.md

Covers the watcher-as-Fixer pool state machine and the S1↔S2 bridge. The pool
bookkeeping is exercised against a bare `RecallWatcher` with a **fake
`luhmannHub`** and `db == nil`, so no DB/pubsub/socket is needed — the scenarios
are chosen so `enhanceRequestDoc` (the only DB-touching path) is never reached
(a hunt routes only when a pending path *and* a free secretary coincide, which
each test avoids). R3017, R3018, R3020, R3023, R3024, R3033.

## Test: pool config defaults and overrides
**Purpose:** `EffectivePoolMax` / `EffectiveCooldownSeconds` return 3 / 120 with
no config and the configured value when set (R3017, R3018).
**Input:** a `LuhmannConfig` with no `class` entry, then one with
`class.bloodhound.pool_max` / `cooldown_seconds` set.
**Expected:** defaults 3 / 120; overrides honored.
**Refs:** crc-Config.md

## Test: EnqueueLuhmann is non-blocking when full
**Purpose:** the nextQueue producer returns false instead of blocking when the
queue is full, so the Fixer can leave a hunt pending (R3011, R3024).
**Input:** a `Server` with a capacity-1 `nextQueue`; enqueue twice.
**Expected:** first returns true, second returns false.
**Refs:** crc-LuhmannCLI.md

## Test: request gated without a Luhmann owner
**Purpose:** with no orchestrator, a request is not routed and no work is queued
(R3020).
**Input:** fake hub with `owner == ""`; `onBloodhoundCLIRequest(path)`.
**Expected:** no queued work; pending stays empty.
**Refs:** crc-RecallWatcher.md, seq-bloodhound-cli.md#1.2.1

## Test: request with room stands a secretary up
**Purpose:** a request with a live owner but no free secretary and room in the
pool enqueues the hunt and pushes a stand-up directive (R3023).
**Input:** fake hub with `owner == "S"`, empty pool; `onBloodhoundCLIRequest`.
**Expected:** pending holds the path; one `stand-up` directive queued for the
`bloodhound` class.
**Refs:** crc-RecallWatcher.md, seq-bloodhound-cli.md#1.2.3

## Test: return frees the secretary and routes to curation
**Purpose:** on a return, the secretary is freed into cooldown *before* curation
and the request-doc path is pushed to Luhmann as a curation task (R3024, R3025).
**Input:** a busy secretary with `inflight[path]=nonce`, empty pending;
`onBloodhoundCLIReturn(path)`.
**Expected:** the secretary is not busy; one `curation` task with the path is
queued; `inflight` no longer holds the path.
**Refs:** crc-RecallWatcher.md, seq-bloodhound-cli.md#1.4

## Test: reserve-nonce --luhmann registers a pool secretary
**Purpose:** `RegisterPoolSecretary(nonce)` adds a free roster entry, idempotently
(R3033).
**Input:** `RegisterPoolSecretary(42)` twice on an empty pool.
**Expected:** one entry keyed by 42, not busy.
**Refs:** crc-RecallWatcher.md

## Test: prune retires only idle-past-cooldown secretaries
**Purpose:** `prune` (autoscale down, R3019) stops a not-busy secretary idle past
`cooldown_seconds`, but not a within-cooldown or a busy one; it pushes one stop
directive naming the nonce and drops it from the roster.
**Input:** three secretaries — one idle 200s (default cooldown 120s), one idle 0s,
one busy 200s; `prune()`.
**Expected:** the first is removed and a `stop`/`bloodhound`/nonce-1 directive is
queued; the other two remain.
**Refs:** crc-RecallWatcher.md, seq-bloodhound-cli.md

## Test: PoolBusy snapshot
**Purpose:** `PoolBusy` (the CLI's fail-fast-without-`--wait` gate, R3022) is true
only when no secretary is free *and* the pool is at pool_max.
**Input:** empty pool; then 3 busy (pool_max default 3); then free one.
**Expected:** not busy → busy → not busy.
**Refs:** crc-RecallWatcher.md

## Test: request doc head tag carries a colon (DB integration)
**Purpose:** guard the colon regression — `BloodhoundCLIOpen` must write
`@ark-bloodhound-cli: <id>`, since a colon-less tag is never extracted/published
and the watcher never wakes (R3021, R3028). Also that the result-tag subscription
lands before the doc.
**Input:** `setupConnections` DB; `BloodhoundCLIOpen(payload)`.
**Expected:** the doc's first line starts `@ark-bloodhound-cli: `; a subscriber to
`ark-bloodhound-cli-result=<id>` exists.
**Refs:** crc-RecallAgentBuilder.md, seq-bloodhound-cli.md#1.1

## Test: enhance re-tags in place (DB integration)
**Purpose:** guard the add-vs-update regression — the enhance must `UpdateTmpFile`
the existing request doc, not `AddTmpFile` it (which fails "file already indexed");
the baton flips the head tag to `@ark-secretary-work=<composite>` (R3031, R3032).
**Input:** `setupConnections` DB; `BloodhoundCLIOpen` then `enhanceRequestDoc(path, "L-9")`.
**Expected:** no error; the doc's first line is `@ark-secretary-work: L-9`.
**Refs:** crc-RecallWatcher.md, seq-bloodhound-cli.md#1.2.3

## Test: prune no-ops without a live Luhmann
**Purpose:** with no orchestrator to receive a stop, `prune` retires nothing and
queues nothing (R3019).
**Input:** fake hub with `owner == ""`, one idle-past-cooldown secretary; `prune()`.
**Expected:** the secretary remains; no work queued.
**Refs:** crc-RecallWatcher.md
