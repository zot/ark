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

## Test: CLI-hunt finding routes by namespace (DB integration)
**Purpose:** the pool secretary's `finding`/`close` cookie is the bare request
id (no `<session>-b<B>` kind-marker); `FindingItem` accepts it only when a live
`tmp://BLOODHOUND-CLI/<id>` request doc matches, else rejects (R3025).
**Input:** `setupConnections` DB; `BloodhoundCLIOpen` → the id; `FindingItem(id, …)`;
then `FindingItem("999", …)` with no matching doc.
**Expected:** the first succeeds; the second returns "not a bloodhound cookie".
**Refs:** crc-RecallAgentBuilder.md, seq-bloodhound-cli.md#1.3.2

## Test: CLI-hunt close re-tags the request doc (DB integration)
**Purpose:** `closeCLIHunt` appends the raw findings to the request doc and flips
its head tag to `@ark-bloodhound-cli-return: <id>` (WITH COLON — the S3 regression
guard) in one write; it writes no finding- doc and drops the secretary-facing seed
+ crank handle (R3025, R3031).
**Input:** `setupConnections` DB; `BloodhoundCLIOpen` → `enhanceRequestDoc` (so the
doc holds a `## Recall seed` + crank handle); one `FindingItem`; `CloseResult(id)`.
**Expected:** the doc's first line is `@ark-bloodhound-cli-return: <id>`; the body
contains `## Raw findings` and the finding loc; it no longer contains
`You are the bloodhound` (the crank handle) or `## Recall seed`.
**Refs:** crc-RecallAgentBuilder.md, seq-bloodhound-cli.md#1.3.2

## Test: bloodhound add stencil accumulates and the terminal flips the tag (DB integration)
**Purpose:** `BloodhoundCLIAdd` appends one JSON line per call to the result
accumulator; `BloodhoundCLIAddDone` writes `tmp://BLOODHOUND-CLI-RESULT/<id>`
tagged `@ark-bloodhound-cli-result: <id>` (WITH COLON), removes the request doc,
and drops the accumulator (R3027, R3028).
**Input:** `setupConnections` DB; two `BloodhoundCLIAdd(id, loc, note, chunk)`; then
`BloodhoundCLIAddDone(id)`.
**Expected:** the result doc's first line is `@ark-bloodhound-cli-result: <id>`; its
body is two JSONL lines each parsing to `{path,range,note}`; the request doc is gone.
**Refs:** crc-RecallAgentBuilder.md, seq-bloodhound-cli.md#1.5.2, #1.5.3

## Test: CLI result path strips the head tag end-to-end (DB integration)
**Purpose:** `BloodhoundCLIResult` returns pure JSONL — the
`@ark-bloodhound-cli-result` head tag is doc metadata, not output (R3029) — driven
through the real subscribe→publish path.
**Input:** `setupConnections` DB; `BloodhoundCLIOpen` (subscribes) → `BloodhoundCLIAdd`
→ `BloodhoundCLIAddDone`; then `BloodhoundCLIResult(ctx, id, timeout)`.
**Expected:** ok is true; the returned text is the JSONL with no leading `@`-tag line;
each line parses as a finding.
**Refs:** crc-RecallAgentBuilder.md, seq-bloodhound-cli.md#1.6

## Test: empty hunt yields an empty result (DB integration)
**Purpose:** a `--done` with no prior `add` writes an empty-body result doc, so the
CLI prints no lines and exits 0 (R3029).
**Input:** `setupConnections` DB; `BloodhoundCLIOpen` → `BloodhoundCLIAddDone(id)` with
no adds; then `BloodhoundCLIResult`.
**Expected:** ok is true; the returned JSONL body is empty (no lines).
**Refs:** crc-RecallAgentBuilder.md, seq-bloodhound-cli.md#1.6.1
