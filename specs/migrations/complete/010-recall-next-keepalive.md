# Migration: recall `next` — keepalive timeout, subscriber-gate, output access

**Language/environment:** Go (`ark` CLI + `ark serve`), the
`ark-recall-agent` PreToolUse guard, and the agent persona. Refines
migration 008 (`recall-next-crank-handle`) after live testing exposed
three operability gaps.

## Problem (state A)

008 made `ark connections recall next` a **pure lotto-tube**: infinite
block, never returns empty, the only clean exit is the context-gate.
That's correct for a client that can block for free. Live testing
surfaced three places where the real client — a Haiku subagent under a
hermetic seal — can't:

1. **Cache TTL vs. infinite block.** An LLM agent doesn't block for
   free: while it waits inside a backgrounded `next`, its Anthropic
   prompt cache ages out at the 5-minute TTL, so every wake after a
   long block pays a full uncached re-read — slow and expensive. A
   pure infinite-block lotto-tube is impractical for an agent client;
   the common case needs a bounded block. (The infinite-block variant
   is the *special* case — cheap-to-block clients like shell scripts.)

2. **Discarded work.** `next` dispatched a curation doc whenever one
   was pending, regardless of whether a result subscriber existed. The
   daemon judged, surfaced, recommended — and then `close` hit the
   subscriber-presence gate (R2807/R2808), found no consumer, and threw
   the result away (`no-subscriber`). Real work, discarded.

3. **No output access.** `next` blocks, so the harness backgrounds it;
   its output lands in a task file, not inline. The sealed agent
   (`tools: Bash`, guard allowing only the four recall verbs) had no
   way to read that file — no `cat`, no `BashOutput` tool — and burned
   a generation flailing (`sleep`, `tail`, Read, all denied) before
   stumbling onto a doc that happened to be pending so `next` returned
   inline.

## State B

- **Keepalive timeout.** `next` blocks for at most ~4 minutes. When no
  dispatchable doc appears in that window it returns a **keepalive
  directive** — exit status `0`, body "no curation doc yet, this is
  normal, run `next` again" — and the agent loops. This keeps the
  agent's wake cadence inside the 5-minute cache TTL and dodges any
  harness background-command kill. It consciously **reverses 008's
  "no timeout"**: the bug 008 deleted (timeout-read-as-stop) is held
  off not by construction now but by a **uniform crank handle** — every
  `next` return tells the caller to run `next` again; only the exit-`2`
  context-limit return says stop. There is no ambiguous "empty" to
  misread: a keepalive is just another "do this, then loop."

- **Subscriber-gate at dispatch.** `next` only returns a curation doc
  whose originating session has a result subscriber
  (`SubscriberCount("ark-recall-result", session) > 0`). Docs for
  sessions with no consumer are left pending — they pile up — and the
  daemon keeps blocking. Work is **deferred, never discarded**: the
  gate moves from "discard at `close`" to "don't dispatch in the first
  place." (Pile-up management is out of scope — a later concern.)

- **Output access + loop model.** The guard gains one allowance:
  `cat <file>` (single arg, no chaining/redirection) so the agent can
  read the backgrounded `next` output. The persona is rewritten to the
  real loop: run `next` (it backgrounds), **end the turn** — no `sleep`,
  no polling — and get re-invoked when it completes; then `cat` the
  output file and act (judge/surface/recommend/close for a doc, or just
  loop for a keepalive), then run `next` again.

## The pattern lesson

This is the [[lotto-tube]] pattern meeting a cache-bearing client. The
takeaway, folded back into the pattern: **a keepalive timeout is a
default ingredient of a lotto-tube, not an afterthought.** Any client
that pays to block (every LLM agent does, via the prompt-cache TTL)
needs the blocking pop to return on a bounded timer with a "nothing
yet, ask again" signal. The infinite-block form is the special case for
clients that block for free.

## What changes

- `recall_next.go` — `RecallNext` gains a keepalive deadline +
  `recallKeepalivePrompt`; `lowestPendingCuration` gains the
  result-subscriber gate.
- `.claude/skills/ark/recall-agent-guard.sh` — `cat <file>` allowance;
  denial runway updated.
- `.claude/agents/ark-recall-agent.md` — persona rewritten to the
  background → end-turn → woken → `cat` → act loop.
- R2857 (revised: keepalive + subscriber-gate, replaces no-timeout),
  R2859 (revised: `cat` allowance).
- Folds: `simple-recall.md` ("true lotto-tube, no timeout" → bounded
  keepalive; subscriber-gate; output-access loop), `crc-RecallAgentBuilder`,
  `crc-RecallAgent`, `seq-recall-agent`.

## What does not change

- The `surface` / `recommend` / `close` verbs and the result-doc shape.
- `close`'s subscriber-presence gate (R2807/R2808) stays as the final
  guard for the rare subscriber-vanished-mid-flight case.
- The watcher pipeline (it keeps writing curation docs; they pile up
  when unconsumed).
- The context-gate exit (R2777 vs `[luhmann].context_limit`) — still
  the only exit-`2` stop.
