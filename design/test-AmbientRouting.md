# Test Design: Ambient routing to a secretary
**Source:** crc-RecallWatcher.md, crc-RecallAgentBuilder.md
**Refs:** seq-recall-watcher.md#6, specs/secretary-pools.md, specs/simple-recall.md

Covers R3298–R3304. Every case here is pure decision logic over a roster and a
subscriber count, so all of it runs against a zero-value watcher with a fake
pool and a fake pubsub — no DB, no server, no live Luhmann. That is deliberate:
the defect these tests exist to prevent (#90) was invisible precisely because
nothing asserted the decisions, only that the pieces parsed.

## Test: local secretary wins and consumes no pool slot
**Purpose:** R3303 rule 1 — a session with its own secretary is served by it,
and the pool is untouched. Guards the dispatch race the pull model allowed,
where a pool secretary could take a session's own work.
**Input:** `ark-secretary-work=<S>` has one subscriber; `recall` roster holds
one free secretary; route a doc with origin `S`.
**Expected:** the doc's address is `S`; the roster secretary is still free and
its `idleSince` unchanged.

## Test: no local secretary routes to a free recall-class secretary
**Purpose:** R3303 rule 2 — the pool serves a session that has no secretary.
**Input:** no `ark-secretary-work=<S>` subscriber; Luhmann seated; `recall`
roster holds free secretary nonce 7.
**Expected:** the doc's address is `<luhmann-session>-7`; that secretary is
marked busy with `idleSince` cleared; no stand-up directive pushed.

## Test: occupancy is stamped, so prune spares a curating secretary
**Purpose:** R3300 — the bug itself. A secretary serving ambient must not read
as idle.
**Input:** secretary nonce 7 routed a curation doc 700s ago;
`class.recall.cooldown_seconds` = 600; run `prune`.
**Expected:** no stop directive for nonce 7. Contrast case in the same test: a
secretary that never held work, idle 700s, **does** draw a stop directive.

## Test: close frees the holder into cooldown
**Purpose:** R3300 — the other half of occupancy; without it a secretary is
busy forever and the pool wedges.
**Input:** nonce 7 holds the doc for origin `S` fire 3; `recall close` arrives
for the `S-3` token.
**Expected:** nonce 7 is not busy, `idleSince` is set, and its in-flight entry
is dropped.

## Test: no free secretary with room pushes a stand-up for the recall class
**Purpose:** R3301 — ambient staffs its own class, rather than waiting for an
unrelated hunt to staff it.
**Input:** no free `recall` secretary; roster size 1; `class.recall.pool_max`
= 3.
**Expected:** exactly one *stand up another* directive naming class `recall`
(not `bloodhound`); the doc is left pending and is **not** reported.

## Test: a full pool defers without reporting
**Purpose:** R3304's boundary — deferral is not unservability. Reporting here
would train the assistant to treat a busy pool as a failure.
**Input:** `recall` roster at `pool_max` with every secretary busy.
**Expected:** the doc stays pending; no result is written to
`ark-recall-result=<S>`; no stand-up directive.

## Test: no seated Luhmann reports unservable
**Purpose:** R3304 — the state that must never be silent, since the remedy
belongs to the requesting session.
**Input:** no `ark-secretary-work=<S>` subscriber; `luhmannOwner` empty;
`ark-recall-result=<S>` subscribed.
**Expected:** one result written to `ark-recall-result=<S>` whose body names
the failed condition; the curation doc is dropped rather than left pending.

## Test: classes do not share slots
**Purpose:** R3298 — the structural claim. If this fails, the two workloads can
starve each other and separate classes bought nothing.
**Input:** `bloodhound` roster at its `pool_max` with all secretaries busy;
`recall` roster empty; route an ambient doc.
**Expected:** a stand-up directive for class `recall` is pushed; the bloodhound
roster is untouched and no bloodhound secretary is considered.

## Test: dispatch matches the address, not the path
**Purpose:** R3302 — the distinction the whole change rests on. A pool
secretary must receive a doc whose path names a different session.
**Input:** a curation doc at `curation-<S>-3` addressed
`@ark-secretary-work: <L>-7`; `lowestPendingCuration` called for `<L>-7`.
**Expected:** the doc is returned; its reported origin is `S`. Negative case:
the same lookup for `S` does **not** return it, so a session's own secretary
cannot take back work the watcher routed elsewhere.

## Test: a routed doc carries the origin's conversation, not the curator's
**Purpose:** R3302 / R2891 — a pool secretary's composite identity has no
conversation, and handing the curator none (or the wrong one) is worst exactly
when judging fit matters most.
**Input:** doc origin `S` addressed to `<L>-7`; session `S` has recent turns,
`<L>-7` has none.
**Expected:** the returned body is prefixed with `S`'s turns.
