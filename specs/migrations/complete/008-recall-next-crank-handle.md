# Migration: recall loop → `ark connections recall next` (batteries-included crank handle)

**Language/environment:** Go (`ark` CLI + `ark serve`), a bash
PreToolUse guard, and a Claude Code agent definition. Builds on the
just-landed daemon (migration 007, `retire-oneshot-recall`).

## Problem (state A)

The recall daemon's loop is a multi-step choreography the Haiku agent
has to remember and execute in the right order, every generation:

```
subscribe --tag ark-recall-curate         # before the first poll, or events fire into the void
ark files 'tmp://ARK-RECALL/curation-*'    # catch docs that predate the subscription
sort by fire — numerically, not lexically  # filesystem order is wrong; uuid+int defeats lexical sort
ark fetch <curation-doc>                    # read the content
surface / recommend / close <F> --nonce N   # the actual judgment
ark connections recall context --limit L    # am I full? loop or exit?
ark listen --timeout N → loop back          # wait for more — and don't read the timeout as "stop"
```

Every line is a battery the agent can drop as its context rots over a
long generation. Two failures we hit live prove it:

- The agent read the `ark listen` **timeout as a stop condition** —
  did one fire, blocked, timed out, narrated "I'll wait for the
  notification," and ended its turn. In a background subagent the turn
  ending *is* the exit, so "one long generation per 150K" collapsed to
  "one fire per ~5 minutes."
- `inspect-exit` then labelled that early quit **healthy**, because its
  only signal was "last record was a clean assistant turn" — which is
  true of a quit-early *and* a real 150K recycle.

Asking the [[batteries-included]] rubric — *"are all the batteries
included?"* — of this loop, the answer is plainly no: the client is
choreographing six steps it shouldn't have to know about.

## State B — one verb

The agent calls exactly one command and does what it says:

```
ark connections recall next <NONCE>
```

`next` carries all the batteries inside:

- **Subscribe (idempotent).** On first call for `<NONCE>` it
  establishes the `@ark-recall-curate` subscription under session
  `recall-loop-<NONCE>`; subsequent calls are no-ops. The agent never
  runs `ark subscribe`.
- **Pick the next doc, in fire order.** It selects the lowest-fire
  pending `tmp://ARK-RECALL/curation-<session>-<fire>` doc across all
  sessions — ordering decided server-side, numerically, so neither the
  lexical-sort trap nor a client-side `ark files` sweep is needed.
  Processing lowest-fire-first keeps `close`'s same-session orphan
  sweep safe (it only ever reaps already-handled fires).
- **Return its content.** The response body *is* the curation doc
  (`# Source Chunk` / `## Candidate` blocks), so the agent never runs
  `ark fetch`.
- **Block — true lotto-tube, no timeout.** When no doc is pending,
  `next` blocks until one arrives. It never returns an empty/timeout
  result, so there is no blank output for the agent to misread as
  "stop" — the timeout-as-stop failure is deleted by construction. A
  dead server breaks the blocking call (non-zero exit), which the
  orchestrator handles like any crash.
- **Context-gate.** Before blocking, `next` checks the nonce's context
  fill (the R2777 token sum) against `[luhmann].context_limit`. When
  it's reached, `next` returns an **exit** directive instead of a doc —
  so the agent stops *because it was told to at the limit*, a clean
  recorded event rather than an ambiguous turn-end.
- **Dual output (the [[anlp]] move).** The response carries
  crank-handle **prose** for the agent ("judge these, `surface` /
  `recommend` the worthy ones, `close <F> --nonce <N>`, then run `next`
  again" / "you're full — stop") *and* a **meaningful exit status** for
  machine clients (`0` = handed you a doc, `2` = exit/done). A
  hand-written `while ark connections recall next "$N"; do …; done`
  works as well as the agent — the agent and a script are
  interchangeable clients.

The agent's entire loop:

```
loop:
  ark connections recall next <N>
    → curation doc  → judge; surface/recommend worthy; close <F> --nonce <N>; loop
    → exit          → stop (orchestrator recycles)
```

## Bug A — inspect-exit gets its second signal

Because the only legitimate way the daemon exits is now `next`
returning **exit** at the context limit, `inspect-exit` (R2796) can
stop treating "clean assistant turn boundary" as healthy. It uses the
context fill at close (`tokens_at_close`, already computed) against
`[luhmann].context_limit`: **healthy** = reached the limit
(filled-and-recycled); a clean turn boundary *below* the limit is
**quit-early**, not healthy. The supervisor no longer waves a 19K
one-fire stop through as a clean recycle.

## What collapses

- **Guard** (`recall-agent-guard.sh`) shrinks to `ark connections
  recall next | surface | recommend | close`. Dropped: `subscribe`,
  `listen`, `files`, `fetch tmp://…curation-*`, `context` — all
  absorbed by `next`.
- **Skill.** The loop is small enough to live in the agent persona
  ("run `ark connections recall next <your nonce>` and do what it
  says"). `recall-loop.md` is retired as the loop driver; the
  **surfacing bar** (when a candidate genuinely fits) moves into the
  agent persona, since that judgment is the agent's core identity.
- `ark-recall.md` **stays** as the standalone one-shot work skill —
  the preserved one-shot capability from migration 007 — untouched.

## What does not change

- The watcher pipeline (curation-doc production, fire minting,
  subscriber-presence gate).
- The result-doc shape (R2750/R2751, including the `path:range` just
  added) and the `surface`/`recommend`/`close` verbs.
- The Luhmann supervisor lifecycle (spawn-record / exit-record /
  respawn), other than `inspect-exit`'s sharper healthy test.
- The assistant's result-only subscription (R2855's surviving half).
