# Test Design: Bloodhound watermark → pool fallback
**Source:** crc-RecallWatcher.md

Covers the three server-selection branches (R3280) and their observable
effects: the pool route (R3281), the not-served report (R3282), and the
recognition gate that no longer requires a session secretary (R3279).

## Test: poolCanServe truth table
**Purpose:** validates the pool pre-check that decides between the pool route
and the not-served report (R3280). Pure decision logic over the roster — the
cheap deterministic case, so it is tested with a fake hub and no DB.
**Input:** `newFixerWatcher(&fakeLuhmannHub{...})` with the pool roster varied:
nil hub; seated owner with an empty roster (room to grow); a free secretary; all
secretaries busy with room; all busy at `pool_max`.
**Expected:** false when no orchestrator is seated; true when a secretary is
free; true when none is free but the roster is below `pool_max`; false when
none is free and the roster is at `pool_max`.
**Refs:** crc-RecallWatcher.md

## Test: watermarkHuntID cannot collide with a CLI id
**Purpose:** the two origins share one request-doc namespace, so a collision
would cross-deliver one hunt's findings to the other's waiting reader — a
silent, hard-to-diagnose failure. Pins the `wm-` prefix that keeps watermark ids
out of the CLI's integer-nonce space (R3280).
**Input:** `watermarkHuntID("sess-A", 7)`, and a representative CLI id (a bare
decimal nonce).
**Expected:** the watermark id carries the `wm-` prefix, embeds session and bid,
and does not parse as a bare integer.
**Refs:** crc-RecallWatcher.md

## Test: enqueueWatermarkHunt writes a Fixer-shaped request doc
**Purpose:** the pool path reuses the CLI pipeline's readers verbatim, so the
doc it writes must be recoverable by `scopeOf` and `clueOf` and must mark the
hunt raw (R3281). A body the Fixer can't parse would route a curated hunt to
Luhmann and deliver a watermark's findings to nobody.
**Input:** a watcher over `setupRecall(t)`'s DB with a seated fake hub; a
`bloodhoundReq` carrying a clue plus one `filter-files` and one `exclude-files`
glob.
**Expected:** `tmp://BLOODHOUND-CLI/wm-<session>-<bid>` exists; its body carries
the `@ark-bloodhound-cli` head tag, a `curate: false` line, one
`filter-files:`/`exclude-files:` line per glob, and the clue; `scopeOf` recovers
both globs and `clueOf` recovers the clue with the metadata stripped; the pool
records `origin`/`bid`/`clue` and `raw == true` for the path.
**Refs:** crc-RecallWatcher.md, seq-recall-watcher.md#4

## Test: the Fixer's request trigger leaves a watermark hunt as recorded
**Purpose:** writing the watermark's request doc fires the Fixer's request
trigger like any CLI request (R3552). Every earlier test called the handlers by
hand with a hand-built `cliRequest`, so none could see the trigger re-record the
hunt: origin dropped (the return then wakes the CLI path, R3281) and the path
queued twice (one hunt routed to two secretaries, its doc stacked with two
addresses). Measured live 2026-10-22 and 2026-07-31.
**Input:** a watcher over a live test DB with a seated fake hub and an empty
pool, the Fixer subscribed with production's own `subscribeFixer`; enqueue one
watermark hunt, then drain the Fixer's events and dispatch each with
production's `dispatchFixerEvent`.
**Expected:** at least one request event was delivered (else the test proves
nothing); the pool's record for the path still carries the origin, bid and
clue; the path is pending exactly once; exactly one stand-up directive was
pushed to the hub.
**Refs:** crc-RecallWatcher.md, seq-recall-watcher.md#4
**Code:** bloodhound_watermark_pool_test.go
**Alarm:** 1
**Fire alarm:** disable the request trigger's `wm-` id guard, so it records the
watermark as a CLI hunt; the origin comes back empty, pending holds the path
twice, and two stand-ups are pushed.
**Inject:** recall_watcher_cli.go:onBloodhoundCLIRequest
**Pulled:** 2026-10-22 — rang: origin empty and bid 0, the path pending 2 times, 2 stand-ups (the live signature)

## Test: the Fixer's request trigger ignores a watermark it has not seen recorded
**Purpose:** in production the Fixer drains on its own goroutine, so its request
trigger can run before `enqueueWatermarkHunt` records the hunt (R3552). The guard
must hold then too. Found by a past-the-list injection 2026-10-22: a first
version of the fix relied on recording before the write, and moving the record
back after the write turned nothing red, because the event-path test only
delivers after enqueue returns.
**Input:** a watcher with a seated fake hub and an empty pool; dispatch a request
event for a `wm-` path through `dispatchFixerEvent` with nothing recorded.
**Expected:** no record, nothing pending, no directive pushed.
**Refs:** crc-RecallWatcher.md, seq-recall-watcher.md#4
**Code:** bloodhound_watermark_pool_test.go
**Alarm:** 2
**Fire alarm:** disable the `wm-` id guard; the trigger records the path as a CLI
hunt, queues it and pushes a stand-up.
**Inject:** recall_watcher_cli.go:onBloodhoundCLIRequest
**Pulled:** 2026-10-22 — rang: a CLI-shaped record (raw false, no origin) created for the unrecorded path

## Test: relayRawResult routes by origin
**Purpose:** the destination branch is the whole point of the fallback (R3281).
A watermark-origin hunt must be delivered to the emitting session's finding
channel in a doc indistinguishable from one its own secretary would write, and a
CLI-origin hunt must be unaffected by the new field.
**Input:** a request doc carrying a `## Raw findings` body; relayed twice — once
with `cliRequest{origin: "sess-A", bid: 3, clue: …}` and once with a zero
`origin` — with an `ark-bloodhound-result=sess-A` subscriber present.
**Expected:** origin set → `bloodhoundFindingPath("sess-A", 3)` exists, headed
`@ark-bloodhound-result: sess-A` and `## Finding: <clue>`, carrying the raw
findings; no CLI result doc written. Origin empty → the CLI result doc at
`bloodhoundCLIResultPath(id)` tagged `@ark-bloodhound-cli-result: <id>`,
unchanged from before. Both branches drop the request doc.
**Refs:** crc-RecallWatcher.md

## Test: relayRawResult with no subscriber writes nothing but still cleans up
**Purpose:** R2808 parity — a consumer that dropped mid-hunt must not leave an
orphaned request doc, and must not get a doc written to a dead channel.
**Input:** a watermark-origin relay with no `ark-bloodhound-result` subscriber.
**Expected:** no finding doc; the request doc is removed.
**Refs:** crc-RecallWatcher.md

## Test: proposesTags is origin-dependent
**Purpose:** pins the R3283 decision table in one place: session-delegated hunts
propose, CLI-delegated ones never do, and a watermark's own `notags` overrides.
Pure predicate — no DB.
**Input:** `cliRequest` values covering nil, CLI origin (empty), watermark
origin, and watermark origin with `notags` set.
**Expected:** true only for a watermark origin without `notags`.
**Refs:** crc-RecallWatcher.md

## Test: a request-doc recommend survives close
**Purpose:** the regression that made proposals unusable on this path (R3283).
`RecommendItem` on a request-doc cookie must accumulate where `closeCLIHunt`
flushes; routed to the session-result stream it fails with `unknown fire …
(curation doc not in flight)` and the proposal is lost. **Verified to fail
without the fix** (branch reverted, test went red), so it is a live sentry
rather than a test that passes for its own reasons.
**Input:** a live request doc; `FindingItem` then `RecommendItem` against its
bare id; `closeCLIHunt`.
**Expected:** the request doc's findings section carries both the finding and
the `## Recommend:` item. Guards against a silent regression, which is the
failure mode this path had before.
**Refs:** crc-RecallAgentBuilder.md

## Test: emitNotServed reports rather than dropping
**Purpose:** the negative case is the defect this item exists to fix (R3282).
Silence is indistinguishable from "still running" or "found nothing", so the
assertion is that a doc *appears* and names the failed condition.
**Input:** a watcher whose session has no secretary, with (a) no orchestrator
seated and (b) a seated orchestrator whose pool is full.
**Expected:** a finding doc at `bloodhoundFindingPath(session, bid)` tagged
`@ark-bloodhound-result: <session>`, whose body contains "Not served", echoes
the clue in its `## Finding:` header, names the distinguishing reason
(orchestrator unavailable vs pool full), and points at
`ark bloodhound search --raw`.
**Refs:** crc-RecallWatcher.md, seq-recall-watcher.md#4

## Test: the reap deadline depends on who is still waiting
**Purpose:** R3041, R3293 — a CLI request's client hit `--timeout` and exited, so
it gets the generous configured TTL; a watermark's emitter is blocked on
`listen`, so it learns sooner. Also pins the two ordering invariants the choice
rests on: the strand deadline must exceed the re-issue threshold (or the sweep
reaps before the stand-up it just pushed could land, turning a slow orchestrator
into an absent one), and a configured TTL shorter than the strand deadline wins
(a watermark must never outlive a CLI request submitted at the same moment).
**Input:** `reapDeadline` over a zero-value `cliRequest` (CLI) and one carrying
`origin`, at a 900s and a 30s configured TTL.
**Expected:** CLI → the configured TTL; watermark → `bloodhoundStrandTTL`, unless
the configured TTL is shorter, in which case that. Constants ordered as above.
**Refs:** crc-RecallWatcher.md

## Test: a reaped watermark reports rather than vanishing
**Purpose:** R3293 — the #84 defect. R3282 covered only the dispatch-time
refusal, so a hunt accepted into the pool and never served still disappeared
quietly; the silence had moved from the gate to the reaper, where it is harder to
spot because the hunt appeared to be accepted. Asserts the reason names the
*strand* and not a dispatch-time condition, since all of those held when the hunt
was accepted and naming one would misdescribe what happened.
**Input:** a watermark-origin `cliRequest` submitted `2 × bloodhoundStrandTTL`
ago, with an orchestrator seated; `sweepRequests`.
**Expected:** the request is reaped, and a `## Finding:` doc appears on
`@ark-bloodhound-result=<S>` carrying the clue, "Not served", "no secretary ever
became available", and the `--raw` fallback — and containing neither
"no orchestrator seated" nor "full with no room to grow".
**Refs:** crc-RecallWatcher.md

## Test: a reaped CLI request reports to nobody
**Purpose:** R3293's other half. The discriminator is who is still waiting, so
the CLI path must stay silent — writing a finding for every abandoned hunt would
leave unread tmp docs behind. Guards against "fix the silence" being
over-applied to the case where silence is correct.
**Input:** a CLI-origin `cliRequest` (empty `origin`) older than the configured
TTL; `sweepRequests`.
**Expected:** reaped, and no finding doc written.
**Refs:** crc-RecallWatcher.md

## Test: both capability gates ignore the session secretary
**Purpose:** R3279, R3294 — the assertion **O133** was opened for and could not
be written until a pubsub harness existed. Pins the two gates together: neither
consults `secretaryPresent`, so each is on exactly while its own result tag is
subscribed, and a session with no secretary of its own still arms ambient
(borrowing the pool). Guards the revert direction specifically — restoring the
conjunct is a one-line change that would look harmless.
**Input:** `newPoolWatcher` (live PubSub — the nil-pubsub short-circuit returns
true in both helpers, so a nil one would make this pass vacuously), no secretary
ever registered, across the four subscription combinations.
**Expected:** `bloodhoundEnabled` tracks the bloodhound-result sub alone;
`ambientEnabled` tracks the recall-result sub alone. Verified red by restoring
the conjunct: the `recall only` and `both` cases fail.
**Refs:** crc-RecallWatcher.md

## Test: every watermark route is logged
**Purpose:** R3623. A lost hunt and a slow one are the same silence to a session
blocked on `listen`; one default-verbosity line per recognized watermark names
where it went.
**Input:** the real `dispatchBloodhound`, with a librarian-backed watcher and a
builder (HOME pointed at a temp dir), over four routes: own secretary (both
subs), pool (seated hub, no secretary), not served (no orchestrator), listener
gone (no bloodhound-result sub at dispatch).
**Expected:** exactly one `recall-watcher: watermark session=… B=7 clue="…"
route=…` line per case, naming that case's route.
**Refs:** crc-RecallWatcher.md
**Code:** bloodhound_watermark_pool_test.go
**Fire alarm:** drop the route line on the pool branch's success, the shape the
silence had before; the `pool` case fails naming the missing line
**Inject:** recall_watcher.go:dispatchBloodhound
**Pulled:** 2026-10-26 — rang: without the pool line, the `pool` case fails "want exactly one route line containing … route=Luhmann's pool" with an empty log

## Test: a watermark nobody hears is logged, not recognized
**Purpose:** R3624. The likeliest lost hunt is a `listen` never started or
already gone, and the gate used to return before anything looked.
**Input:** `OnAppend` on a qualifying chat path, a real empty PubSub (the nil
short-circuit would read as subscribed), first an assistant line with no
watermark, then one with a watermark.
**Expected:** nothing logged for the first; for the second, one line with the
session, the clue, and "no listener", no `B=`, and no hunt counter allocated.
**Refs:** crc-RecallWatcher.md, seq-recall-watcher.md
**Code:** bloodhound_watermark_pool_test.go
**Fire alarm:** restore the combined early return ahead of the log call, so a
session with neither sub returns before looking; red reports the watermark not
logged
**Inject:** recall_watcher.go:OnAppend
**Pulled:** 2026-10-26 — rang: the restored combined return fails "unheard watermark not logged with session and clue"
