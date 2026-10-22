# Test Design: LuhmannCLI

**Source:** crc-LuhmannCLI.md

Covers the bloodhound-CLI S1 drain tube: the in-memory ownership lease
(`claimLuhmann`) and the blocking drain (`LuhmannNext`). Both are exercised
against a bare `&Server{}` — the lease touches only
`luhmannMu`/`luhmannOwner`/`luhmannActive`/`luhmannLiveUntil` and the drain only
those plus `nextQueue`, so no DB, pubsub, or socket is needed. R3010, R3011,
R3012, R3013, R3014, R3016, R3321.

## Test: ownership matrix
**Purpose:** `claimLuhmann` applies the lease correctly across all three modes ×
the owner-states, including whether a foreign incumbent's lease is live or stale,
returning the right disposition + error string and mutating `luhmannOwner` only
when it claims (R3012, R3013, R3014, R3321).
**Input:** a `Server` with `luhmannOwner` preset to "", the calling session, or a
foreign session, and `luhmannLiveUntil` set ahead of now for the live rows; call
`claimLuhmann(session, mode)`.
**Expected:** the eleven rows —

| mode  | owner before | session | disposition | error string           | owner after |
|-------|--------------|---------|-------------|------------------------|-------------|
| force | ""           | A       | OK          | ""                     | A           |
| force | A            | A       | OK          | ""                     | A           |
| force | A            | B       | OK          | ""                     | B (reclaimed) |
| first | ""           | A       | OK          | ""                     | A           |
| first | A            | A       | OK          | ""                     | A (self, idempotent) |
| first | A (live)     | B       | Exit        | you don't have ownership | A (unchanged) |
| first | A (stale)    | B       | OK          | ""                     | B (taken over) |
| plain | ""           | A       | Reclaim     | there are no sessions  | "" (unchanged) |
| plain | A            | A       | OK          | ""                     | A           |
| plain | A (live)     | B       | Exit        | you don't have ownership | A (unchanged) |
| plain | A (stale)    | B       | Exit        | you don't have ownership | A (unchanged) |

The last row is the load-bearing one: a stale incumbent is claimable by
`--first` and by nothing else, because validating is not claiming (R3013,
R3321). Without it, a future reader sees the takeover and generalizes it to
plain `next`, at which point two orchestrators both validate as owner.

**Refs:** crc-LuhmannCLI.md

## Test: lease liveness follows the drain
**Purpose:** the lease is live while an owner's `next` call is in flight and for
the grace window after the last one returns **normally**, stale beyond that, and
stale *immediately* when the last call was cancelled (R3321).
**Input:** a bare `Server`; raise and lower `luhmannActive` through the same
helper `LuhmannNext` uses, and preset `luhmannLiveUntil` to stand in for
elapsed time.
**Expected:**
- active > 0 → live, however old `luhmannLiveUntil` is. This is the case that
  matters: an owner parked in a 55-minute keepalive must never read as stale.
- active == 0 and `luhmannLiveUntil` within the grace window → live.
- active == 0 and the window elapsed → stale, and `claimLuhmann(B, first)` then
  takes the seat.
- a ctx-cancelled `LuhmannNext` leaves `luhmannActive` at 0 and zeroes
  `luhmannLiveUntil`, so the lease is stale at once rather than holding the
  window open for a caller that is gone. The count must not leak either, or the
  seat becomes permanently unclaimable — strictly worse than the stale seat
  R3321 exists to fix.
- a displaced owner's late return is ignored: after B force-reclaims, A's
  returning call must not decrement B's count, or B's lease would expire while
  B is alive.

**Refs:** crc-LuhmannCLI.md

## Test: keepalive on idle deadline
**Purpose:** with the seat owned and no work queued, `LuhmannNext` blocks until
the keepalive window elapses, then returns the keepalive crank-handle at
disposition OK (R3011, R3016).
**Input:** owned `Server`, empty `nextQueue`, a short `--keepalive` (ms scale).
**Expected:** `err == nil`, disposition OK, body contains "keep the seat warm".
**Refs:** crc-LuhmannCLI.md, seq-bloodhound-cli.md

## Test: curation work delivery
**Purpose:** a queued curation task is drained ahead of the keepalive and rendered
as a curation crank-handle pointing at the request doc (R3011).
**Input:** owned `Server`; push `LuhmannWork{Kind:"curation", Path:"tmp://BLOODHOUND-CLI/xyz"}`;
call with a keepalive long enough that the work, not the timer, wins.
**Expected:** body contains the request-doc path and the `bloodhound add` instruction.
**Refs:** crc-LuhmannCLI.md, seq-bloodhound-cli.md#1.5

## Test: directive work delivery (stand-up)
**Purpose:** a queued stand-up directive is rendered as the spawn crank-handle
naming the class and the `reserve-nonce --luhmann` + spawn-record steps (R3011).
**Input:** owned `Server`; push `LuhmannWork{Kind:"directive", Directive:"stand-up", Class:"bloodhound"}`.
**Expected:** body says "stand up another" and names "bloodhound".
**Refs:** crc-LuhmannCLI.md

## Test: directive work delivery (stop)
**Purpose:** a stop directive names the specific pool secretary's nonce and the
`exit-record` command (R3011, R3019).
**Input:** owned `Server`; push `LuhmannWork{Kind:"directive", Directive:"stop", Class:"bloodhound", Nonce:99}`.
**Expected:** body names "stop", the nonce "99", and "exit-record".
**Refs:** crc-LuhmannCLI.md

## Test: stand-down returns immediately
**Purpose:** a non-owner's call short-circuits on the lease and never enters the
blocking select, so the losing side of a race steps aside at once (R3013, R3014).
**Input:** `Server` owned by A; call `LuhmannNext(ctx, "B", plain, 1h)`.
**Expected:** disposition Exit, body contains "Stand down", returns well under the
keepalive window (no block).
**Refs:** crc-LuhmannCLI.md

## Test: reclaim on unowned
**Purpose:** a plain call against an unowned (post-bounce) server returns the
reclaim crank-handle without claiming or blocking (R3013, R3014).
**Input:** unowned `Server`; call `LuhmannNext(ctx, "A", plain, 1h)`.
**Expected:** disposition Reclaim, body instructs re-running with `--first`, owner
still "".
**Refs:** crc-LuhmannCLI.md

## Test: context cancellation
**Purpose:** a cancelled request context unblocks the drain with the context error
rather than a spurious work/keepalive return (R3010).
**Input:** owned `Server`, empty `nextQueue`, an already-cancelled ctx, long keepalive.
**Expected:** non-nil error (context cancelled), `luhmannActive` back to 0, and
the lease no longer live (R3321).
**Refs:** crc-LuhmannCLI.md

## Test: work crank handles lead with re-launch-first
**Purpose:** R3036 — every work crank handle (curation, stand-up, stop) must LEAD
with the backgrounded re-launch instruction, ahead of the work-specific content, so
a mid-work drift can't kill the loop.
**Input:** `luhmannWorkPrompt("S", w, "raw results")` for each of curation, stand-up,
and stop work items.
**Expected:** each body contains "re-launch the seat" + "backgrounded", and that
instruction's index precedes the work marker ("bloodhound add" / "stand up another" /
"exit-record").
**Refs:** crc-LuhmannCLI.md

## Test: inspect-exit reads the transcript Claude Code writes
**Purpose:** R2796 classifies a secretary's exit from its transcript. Claude Code
nests a tool call as a `tool_use` block in an assistant record's
`message.content`, and usage as `message.usage`. Measured 2026-10-22: the
classifier read a top-level `tool_result` and top-level `usage`, which never
exist, so it saw no close and 0 tokens, and every bloodhound pool exit was a
crash; three ordinary hunts would trip the crash-storm pause.
**Input:** a transcript written in that shape, with or without a
`recall close` call and with a given context fill; an empty ark dir.
**Expected:** closed below the limit → `quit-early`; closed at the limit →
`healthy`; never closed → `crash`; `tokens_at_close` is the fill written (the
R2777 value).
**Refs:** crc-LuhmannCLI.md, R2796, R2777
**Code:** cmd/ark/inspect_exit_test.go
**Fire alarm:** restore HEAD's `classifySubagentExit`; every case reads `crash`
with `tokens_at_close = 0`.
**Inject:** cmd/ark/main.go:classifySubagentExit
**Pulled:** 2026-10-22 — rang: label "crash" and tokens_at_close 0 in all three cases
