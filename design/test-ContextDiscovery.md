# Test Design: Subagent context discovery
**Source:** crc-RecallAgentBuilder.md, crc-Monitor.md
**Refs:** seq-recall-agent.md#7.4, specs/simple-recall.md §Subagent JSONL discovery

Covers R3305–R3308. All of it runs against a temp `$HOME` with planted
`.meta.json` / `.jsonl` pairs — no server, no live agent, no Claude Code.

**Why this suite exists, stated plainly so it does not get deleted as
redundant.** The previous implementation was correct in every layer except the
one that decides whether it runs at all: it read `$CLAUDE_CODE_SESSION_ID` from
the process environment, and the process that reads it is `ark serve`, which
never has one. The old tests passed throughout, because a test binary inherits
the developer's shell — including that variable. **The fixture was more like
production's parent than production was.** Every case below therefore either
unsets the variable or sets it to a deliberately wrong value.

## Test: discovery works with no CLAUDE_CODE_SESSION_ID (the sentry)
**Purpose:** R3306 — the production condition. This is the one that was
silently false for weeks.
**Input:** `CLAUDE_CODE_SESSION_ID` **unset** in the test process. A planted
transcript for nonce 7 under `<project>/<parent>/subagents/`, and nonce 7
registered to `<parent>`.
**Expected:** discovery returns the planted path and `ContextTokens` reports a
non-zero count with `ok == true`.

## Test: a registered parent beats a wrong inherited environment
**Purpose:** R3306's sharper half. An inherited variable is worse than a missing
one, because it resolves *some* sessions and silently misses the rest — so it is
not enough to work when the environment is empty; the environment must not be
consulted at all.
**Input:** `CLAUDE_CODE_SESSION_ID` set to `wrong-session`, which has its own
planted `subagents/` directory containing a **different** transcript also
matching `nonce 7`. Nonce 7 registered to `right-session`.
**Expected:** discovery returns `right-session`'s transcript. If it returns the
other one, the environment is still winning.

## Test: an unregistered nonce is unmeasured, not zero
**Purpose:** R3307 — the distinction R2760 explicitly got wrong by mandating
that a failed lookup log zeros.
**Input:** nothing registered for nonce 9.
**Expected:** `ContextTokens(9)` returns `ok == false`, and the monitor record
written for that fire is marked unmeasured rather than carrying `0` token
fields.

## Test: registration survives to lookup across the verbs that need it
**Purpose:** R3305 — one registration serves `close`, `context`, and the `next`
context gate; each is a separate call site and any one of them reverting to the
environment reintroduces the bug.
**Input:** register nonce 7, then resolve through each of the three paths.
**Expected:** all three resolve to the same transcript.

## Test: the context-limit gate fires again
**Purpose:** R3306's payoff. With discovery dead, `RecallNext`'s gate is
`ok && tokens >= limit` with `ok` permanently false, so the designed clean exit
(R2857) never happened in production — secretaries ran until the harness ended
them, and it looked healthy from outside because Luhmann classified exits
through the working *cold* path.
**Input:** registered nonce whose planted transcript exceeds `context_limit`.
**Expected:** `RecallNext` returns the exit directive. Negative case: a
transcript below the limit does not exit.

## Test: monitor averages exclude unmeasured records
**Purpose:** R3308 — the diagnostic that hid the defect.
**Input:** 20 recall records, 17 unmeasured and 3 measured at 40,000 /
44,000 / 46,000 context tokens.
**Expected:** the reported average is the mean of the three (43,333), not the
mean over 20 (6,500), and the count averaged over is reported as 3. The real
values behind this case were `avg_context_tokens: 6495` against a measured mean
of 43,304.

## Test: reserve-nonce outside an agent shell registers nothing, and says so
**Purpose:** R3305's edge — a human running the CLI by hand has no session.
**Input:** `reserve-nonce` with `CLAUDE_CODE_SESSION_ID` unset.
**Expected:** the reservation succeeds and returns a nonce; no parent is
registered; a later lookup for that nonce reports unmeasured rather than
erroring or logging zero.
