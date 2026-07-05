# Test Design: LuhmannCLI

**Source:** crc-LuhmannCLI.md

Covers the bloodhound-CLI S1 drain tube: the in-memory ownership lease
(`claimLuhmann`) and the blocking drain (`LuhmannNext`). Both are exercised
against a bare `&Server{}` — the lease touches only `luhmannMu`/`luhmannOwner`
and the drain only those plus `nextQueue`, so no DB, pubsub, or socket is
needed. R3010, R3011, R3012, R3013, R3014, R3016.

## Test: ownership matrix
**Purpose:** `claimLuhmann` applies the lease correctly across all three modes ×
three owner-states, returning the right disposition + error string and mutating
`luhmannOwner` only when it claims (R3012, R3013, R3014).
**Input:** a `Server` with `luhmannOwner` preset to "", the calling session, or a
foreign session; call `claimLuhmann(session, mode)`.
**Expected:** the nine rows —

| mode  | owner before | session | disposition | error string           | owner after |
|-------|--------------|---------|-------------|------------------------|-------------|
| force | ""           | A       | OK          | ""                     | A           |
| force | A            | A       | OK          | ""                     | A           |
| force | A            | B       | OK          | ""                     | B (reclaimed) |
| first | ""           | A       | OK          | ""                     | A           |
| first | A            | A       | OK          | ""                     | A (self, idempotent) |
| first | A            | B       | Exit        | you don't have ownership | A (unchanged) |
| plain | ""           | A       | Reclaim     | there are no sessions  | "" (unchanged) |
| plain | A            | A       | OK          | ""                     | A           |
| plain | A            | B       | Exit        | you don't have ownership | A (unchanged) |

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
**Expected:** non-nil error (context cancelled).
**Refs:** crc-LuhmannCLI.md
