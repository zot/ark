# Test Design: Client session identity
**Source:** crc-CLITree.md
**Refs:** specs/session-identity.md, specs/migrations/complete/020-client-session-identity.md

Covers R3313–R3320. Two layers, both cheap. `resolveClientSession` is a pure
function of one flag value and one environment variable, so every resolution
case is a table row with `t.Setenv`. The declaration is a property of the built
command tree, so `arkCommands()` + the existing `findCmd` helper reaches it with
no server, no session, and no fixtures.

**Why this is worth pinning despite being a few lines of logic.** The bug it
prevents is invisible at runtime: a consumer that subscribes under a stale
session id does not error, it blocks forever on a channel nobody publishes to,
which is indistinguishable from a quiet corpus. There is no failing call to
notice, so the test is the only thing that will notice.

**What changed from state A's design, and why the coverage got better.** State A
resolved a `--session` flag inside each `Action` body, so the one mistake that
mattered — applying the rule to `recall next` and redirecting every pool
secretary to its parent's tube — could only be guarded by *grepping ark's own
source* for `applyOwnSession(` call sites. That proxy passed while `luhmann
events` went unnoticed, because a verb that never calls the resolver is
invisible to a test that looks for calls. Under state B the need is declared on
the node, so the assertions run against the **real tree** and an undeclared
own-session verb is a missing entry rather than an absent call.

## Test: `--force-session` wins over a set environment
**Purpose:** R3314's override half. State A's flag was decorative — with the
variable set, no flag value could change the outcome — so this is the case that
proves the replacement actually accepts the input it advertises.
**Input:** `CLAUDE_CODE_SESSION_ID=live-1`, `--force-session other-2`.
**Expected:** resolves `other-2`, no error. Deliberate cross-session use, the
capability state A removed silently.

## Test: flag omitted resolves from the environment
**Purpose:** R3314's ordinary path, and the one every skill now takes.
**Input:** env `live-1`, no `--force-session`.
**Expected:** resolves `live-1`, no error.

## Test: `--force-session` with an empty environment
**Purpose:** R3314 outside an agent shell — a human or script invoking the verb
by hand has no variable to read, which is the case `--force-session` exists for.
**Input:** env unset, `--force-session manual-1`.
**Expected:** resolves `manual-1`, no error.

## Test: neither resolving is an error that names `--force-session`
**Purpose:** R3315. State A returned empty and let each body's own required-flag
message fire, which under B would name a `--session` flag the CLI no longer has.
A diagnostic pointing at a nonexistent flag is worse than none.
**Input:** env unset, no `--force-session`.
**Expected:** a non-nil error whose text contains `--force-session`. Asserting
the flag *name* appears, not just that it failed: the remedy is the whole point
of erroring rather than passing empty.

## Test: every own-session node declares the need
**Purpose:** R3313 — the assertion that replaces state A's source grep, and the
one that would have caught `luhmann events`.
**Input:** the tree from `arkCommands()`.
**Expected:** each of `bloodhound listen`, `connections recall listen`,
`luhmann next`, `luhmann events`, and `connections recall next` reports a
declared session need. A node dropping its declaration is a silent regression:
the verb keeps working in an ordinary session and fails only after a fork.

## Test: no declaring node carries a `--session` identity flag
**Purpose:** R3313/R3314's other half, and the migration's completion test in
executable form — the trap is a `--session` flag creeping back onto a declaring
node, where it would read as an accepted input and be ignored.
**Input:** the same five nodes.
**Expected:** none declares a flag named `session`. `connections recall next`
is the deliberate near-miss: it declares `tube`, which must not be spelled
`session` again.

## Test: `--force-session` is global, on the root
**Purpose:** R3316. A per-node flag would have to be added by hand to each
declaring node, which is R3311's enumeration one layer down.
**Input:** the root command's own flags.
**Expected:** `force-session` is present at the root, beside `dir`.

## Test: `recall next` declares `--tube` and not `--session`
**Purpose:** R3318. Replaces the state-A assertion that `--session` must exist.
Losing the flag entirely would leave a pool secretary with no way to name its
own tube; keeping it spelled `session` preserves the overload the rename ends.
**Input:** the `connections recall next` node's flags.
**Expected:** `tube` present, `session` absent, `pool` still absent (retired
T273).

## Test: `recall next`'s URL carries the tube and the identity separately
**Purpose:** R3317 + R3319 on the wire. Identity and tube must reach the server
as **distinct** parameters, or the field split exists only in the flag names and
the server cannot tell which value is which.
**Input:** the URL builder with nonce `7`, tube `luhmann-9-3`, client session
`live-1`; then again with an empty client session.
**Expected:** first form contains `nonce=7`, `tube=luhmann-9-3`, and
`client_session=live-1`, and contains no bare `session=` parameter. Second form
omits `client_session` entirely rather than sending it empty, so a CLI run
outside an agent shell registers nothing (R3319).

## Test: a declaring node's body never runs without an identity
**Purpose:** R3320. The structural property that makes R3311's exclusion list
unnecessary rather than merely shorter — under state A the rule was applied by
remembering to call the resolver in each body, so an omission was silent and
shaped exactly like the bug the rule prevents.
**Input:** a declaring node whose `Action` records that it ran, invoked with the
environment unset and no `--force-session`.
**Expected:** the run returns an error and the `Action` never executed. A body
that runs with an empty identity fails this case even if it later errors on its
own, because the guarantee is that resolution happens *before* the body.
