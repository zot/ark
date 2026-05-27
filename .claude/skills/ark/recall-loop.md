<!-- CRC: crc-RecallAgent.md | Seq: seq-recall-agent.md | R2773, R2777 -->
@knowledge: ark
@from-service: ARK-RECALL

# Recall lotto-tube loop

The outer loop a long-running recall subagent runs. One persistent
process pops `@ark-recall-curate` events one at a time, processes
each per [ark-recall.md](ark-recall.md), and exits when its own
context fills past a configurable limit so the parent can recycle
it.

This skill is the META skill. The WORK skill — what to do with a
single curation doc, when to surface, when to recommend, when to
silent-close — lives in `ark-recall.md`. Each loop iteration
follows that workflow inline; this skill never says "what does
close do."

## Starting

The parent (Luhmann, or any orchestrator) launches the loop as a
hermetically-sealed Task subagent:

```
Agent(
  subagent_type="ark-recall-agent",
  description="ark-recall lotto-tube loop nonce <N>",
  run_in_background=true,
  prompt="Start the recall loop now. Context limit: <LIMIT>."
)
```

The `nonce <N>` in the description is the same nonce mechanism
v2 uses for one-shot agents — it lets `ark connections recall
context --nonce N` and `close --nonce N` find this subagent's
JSONL via the source-enumeration lookup.

## How It Works

The loop is **inline-blocking** — each cycle is one blocking Bash
call to `ark listen`, then the per-event work, then the next
blocking listen. No `run_in_background=true`; the subagent has
no other interactive surface to preserve and the inline form
trivially satisfies "always read output" (the output is the next
Bash result, never abandoned).

### Start

1. **Subscribe** once:

   ```
   ~/.ark/ark subscribe --session recall-loop-<NONCE> --tag ark-recall-curate
   ```

   The subscription session ID is per-spawn (`recall-loop-<NONCE>`).
   Listener-uniqueness — only one listener may pop events from a
   given subscription session at a time — falls out automatically
   because each spawn has its own ID.

   Bare tag subscription. The loop sees every curate event from
   every assistant session; filter by `@ark-recall-curate` value
   at receipt if scoping per-session.

### Loop

2. **Pop** (blocking; the lotto tube):

   ```
   ~/.ark/ark listen --session recall-loop-<NONCE> --timeout 300
   ```

   Three outcomes:
   - **Event(s) returned** (one or more crank-handle blocks
     separated by `---`): proceed to step 3.
   - **Empty output, exit 0** (timeout, no events in 300s): just
     loop back to step 2. Quiet idle.
   - **Non-zero exit**: server is gone or otherwise unhealthy.
     Exit the subagent; parent restarts when ark serve recovers.

3. **Per event, per [ark-recall.md](ark-recall.md):** parse the
   crank-handle's `File:` line for the curation doc path; the path
   ends in `-<fire>` so the fire number is the trailing integer.
   Fetch via `ark fetch`, decide, call `surface` / `recommend` /
   `close` against the fire. The work skill is the contract; this
   skill never restates it.

4. **Check context** after each `close`:

   ```
   ~/.ark/ark connections recall context --nonce <NONCE> --limit <LIMIT>
   ```

   - **Exit 0** (under limit): loop back to step 2.
   - **Exit 1** (≥ limit): exit the subagent. The parent watches
     for termination and re-spawns with a fresh nonce. State on
     disk survives the cycle — only the agent's working context
     is recycled.

## CLI Reference

| Command | Description |
|---------|-------------|
| `ark subscribe --session SID --tag ark-recall-curate` | Register subscription (once at start) |
| `ark listen --session SID --timeout N` | Block for one event (drains any queued) — the lotto tube |
| `ark fetch tmp://ARK-RECALL/curation-...` | Read a curation doc (the work skill uses this) |
| `ark connections recall surface FIRE -chunk N -reason TEXT` | Per-fire work (see ark-recall.md) |
| `ark connections recall recommend FIRE -chunk N -tag @t[:v] -reason TEXT` | Per-fire work |
| `ark connections recall close FIRE --nonce N` | Per-fire cleanup |
| `ark connections recall context --nonce N --limit M` | Self-check — exit 1 if context ≥ limit |

## Architecture

```
parent (Luhmann or orchestrator)
   │
   └─ Task subagent_type=ark-recall-agent, nonce N, limit L
        │
        ▼
   recall-loop subagent (Haiku, hermetic seal, memory: local)
        │
        ├─ subscribe --tag ark-recall-curate  (once)
        │
        └─ loop:
            ├─ ark listen --session recall-loop-<N>
            │     (lotto tube — blocks for event)
            │
            ├─ per popped event:
            │     → per ark-recall.md:
            │         ark fetch curation-<S>-<F>
            │         decide
            │         surface / recommend / close
            │
            └─ ark connections recall context --nonce <N> --limit <L>
                  │
                  ├─ exit 0: keep looping
                  └─ exit 1: exit — parent restarts fresh
```

## Self-Recycle Limit

The `context_tokens` field in `~/.ark/monitoring/recall.jsonl`
shows the curve over a run. Set the loop's `--limit` so the
parent restart happens before Haiku's hard context ceiling —
leave headroom for one final close to flush state. Typical
limit: 150K tokens (Haiku 4.5's effective ceiling is ~200K).

State on disk (`recall.jsonl`, RC/RD/RJ/RF records, tmp:// docs)
survives the cycle — only the agent's working context is
recycled. New nonce, new transcript, same accumulated knowledge.

## Hermetic Seal

Same `recall-agent-guard.sh` as the one-shot agent — only the
narrow set of `ark` commands above is allowed. `Read`, `Edit`,
`Write`, network: all denied.

The skill's responsibility is the loop structure. The work
inside each iteration delegates to [ark-recall.md](ark-recall.md);
the bar for surfacing, the format of items, the close
mechanics all live there.
