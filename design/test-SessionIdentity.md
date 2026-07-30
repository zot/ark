# Test Design: Own-session identity resolution
**Source:** crc-CLITree.md
**Refs:** specs/session-identity.md

Covers R3309–R3311. `resolveOwnSession` is a pure function of one string and one
environment variable, so every case is a table row with `t.Setenv` — no server,
no session, no fixtures.

**Why this is worth pinning despite being four lines of logic.** The bug it
prevents is invisible at runtime: a consumer that subscribes under a stale
session id does not error, it blocks forever on a channel nobody publishes to,
which is indistinguishable from a quiet corpus. There is no failing call to
notice, so the test is the only thing that will notice.

## Test: flag omitted resolves from the environment
**Purpose:** R3309, the ordinary path once the skills stop passing the flag.
**Input:** `CLAUDE_CODE_SESSION_ID=live-1`, flag `""`.
**Expected:** returns `live-1`, no report.

## Test: flag agreeing is used unchanged and reported silently
**Purpose:** R3309 — an explicit flag that matches must not produce noise, or
every legitimate call grows a warning.
**Input:** env `live-1`, flag `live-1`.
**Expected:** returns `live-1`, empty report.

## Test: flag disagreeing loses to the environment, and says so
**Purpose:** R3309 + R3310 together — the fork case. This is the test that
matters; the others guard it from regressing into noise or silence.
**Input:** env `live-2`, flag `stale-1` (the shape a `--fork-session --resume`
replay produces).
**Expected:** returns `live-2`; report is non-empty and names **both** values, so
an operator can see which id was replayed and which is live. A correction with an
empty report fails this case as surely as no correction at all.

## Test: empty environment falls back to the flag
**Purpose:** R3309's outside-an-agent-shell path — a human or script invoking the
verb by hand has no session to read.
**Input:** env unset, flag `manual-1`.
**Expected:** returns `manual-1`, no report. Not an error, and no attempt to
correct what cannot be checked.

## Test: neither flag nor environment resolves to empty
**Purpose:** the resolver reports nothing and decides nothing; the caller keeps
its existing required-flag error rather than the resolver inventing one.
**Input:** env unset, flag `""`.
**Expected:** returns `""`, no report.

## Test: the composite routing flag is left alone
**Purpose:** R3311 — the exclusion, and the case that would break every pool
secretary if the rule were applied uniformly.
**Input:** the `connections recall next` action with env `live-1` and
`--session luhmann-9-3`.
**Expected:** the value reaching `RecallNext` is `luhmann-9-3`. A pool
secretary's `--session` is its own tube, not its parent's identity; resolving it
from the environment would silently redirect it to the parent's tube, where it
would drain another agent's work.
