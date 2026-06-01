# Sequence: Recall agent — daemon loop: curate → agent → result → assistant

**Requirements:** R2747, R2748, R2750, R2751, R2755, R2756, R2757, R2758, R2759, R2760, R2761, R2762, R2763, R2877, R2765, R2766, R2769, R2771, R2772, R2774, R2890, R2855, R2857, R2858, R2860

Picks up where `seq-recall-watcher.md` leaves off — after the
watcher writes `tmp://ARK-RECALL/curation-<session>-<fire>` and the
write actor publishes the matching pubsub event for the
`@ark-recall-curate` tag.

The recall agent is a **long-running daemon**, spawned once per
generation by the Luhmann orchestrator (respawn lifecycle in
`seq-luhmann-supervisor.md`). It is a Haiku subagent with a
hermetic-seal tool allowlist; its entire loop is one server verb,
`ark connections recall next <NONCE>`, which subscribes, dispatches
the lowest-fire curation doc whose session has a result subscriber
(blocking up to a ~90s keepalive otherwise), and context-gates. The
agent runs the verb in the **foreground** and loops in one continuous
turn — the short keepalive keeps `next` returning inline before the
harness's foreground auto-background threshold, so the subagent never
ends its turn mid-loop. It reads each return inline, judges the
candidates, and writes result docs through the `surface` / `recommend`
/ `close` builder verbs hosted in `ark serve`.
Step numbers 1, 2, 4, 5, 7 are pinned by Go code and keep their
meanings; the daemon framing lives in the actors and the loop-back at
step 7.7.

## Flow 1: curate event published; daemon spawned & subscribed

```
1. write actor commits tmp://ARK-RECALL/curation-<S>-<F>
         │  (the watcher minted <F>; no consumer allocates fires)
         │
         └── 1.1  publishes pubsub event matching
                  @ark-recall-curate (per write-actor
                  responsibilities — not the builder's job)

2. The session's own assistant spawns its secretary (via /recall) (R2890)
         │
         ├── 2.1  N = `ark connections recall reserve-nonce`
         │          → server's RecallAgentBuilder
         │            increments nonceCounter, returns N (R2755)
         │
         ├── 2.2  Task(                                         (R2890)
         │          subagent_type="ark-recall-agent",
         │          description="ark-recall secretary
         │                       loop nonce <N>",   (R2759)
         │          run_in_background=true,
         │          prompt="Start the recall secretary loop now.
         │                  Session: <S>. Nonce: <N>.")
         │          (session+nonce in prompt: the sealed agent
         │           cannot read its own description)
         │
         └── 2.3  secretary boots (Haiku, memory: local);      (R2769, R2860)
                  its persona says: run `ark connections recall
                  next <N>` and do what it returns. No separate
                  subscribe / fetch / skill-load — `next` carries
                  them (its subscribe is idempotent on first call).
```

## Flow 2: daemon loop iteration — `next` returns the doc

```
3. ark connections recall next <N>   (the whole loop)        (R2857, R2858)
         │  server-side, in one verb: idempotent subscribe on
         │  first call → context-gate (R2777 vs
         │  [luhmann].context_limit) → pick lowest-fire pending
         │  curation-<S>-<F> whose session has a result subscriber
         │  → block up to a keepalive window (~90s) if none
         │
         ├── 3.1  at/over context limit → returns EXIT          (R2857)
         │          directive (exit status 2); daemon stops, the
         │          orchestrator respawns (seq-luhmann-supervisor)
         │
         ├── 3.2  else, a dispatchable doc → returns the curation (R2857, R2858)
         │          doc CONTENT (# Source Chunk / ## Candidate)
         │          plus crank-handle prose "judge, surface/
         │          recommend, close <F> --nonce <N>, run next
         │          again" (exit status 0). The agent runs next in
         │          the foreground (≤~90s, under the harness's
         │          auto-background threshold) and reads the return
         │          inline, staying in one continuous turn (no
         │          ark fetch, no per-cycle completion beats).
         │
         ├── 3.3  no dispatchable doc within the keepalive window (R2857)
         │          → returns a KEEPALIVE (exit status 0, "no doc
         │          yet — run next again") so the agent's wake
         │          cadence stays inside the prompt-cache TTL. Docs
         │          for sessions with no result subscriber are left
         │          pending (pile up), never dispatched.
         │
         └── 3.4  daemon judges which candidates fit and which
                  proposed tags are worth recommending
```

## Flow 3: daemon writes result items

```
4. for each surface-worthy candidate:
         ark connections recall surface <F>             (R2756)
           -chunk <CID> -reason "<one-line>"
         │
         ├── 4.1  CLI POSTs to server's recall handler
         │
         └── 4.2  RecallAgentBuilder.SurfaceItem(           (R2751)
                    fire=<F>, chunkID=<CID>, reason)
                  - opens results[<F>] on first call
                  - chunkLocator(chunkID): server-side
                    ChunkInfo → path:range (+ size); the
                    daemon never passes the path
                  - appends "## Surface: <CID> (<size>)
                    <path>:<range>" H2 + "reason: ..." line

5. for each recommend-worthy tag candidate:
         ark connections recall recommend <F>           (R2757)
           -chunk <CID> -tag @<t>[:<v>]
           -reason "<one-line>"
         │
         └── 5.1  RecallAgentBuilder.RecommendItem(...)       (R2751)
                  - opens results[<F>] on first call if
                    not already open
                  - chunkLocator(chunkID) → path:range
                  - appends "## Recommend: @<t>[:<v>] on
                    <CID> <path>:<range>" H2 + "reason: ..."
                    line

6. on any malformed call:
         CLI parse rejects                                (R2772)
           → LogFumble(fire=<F>, nonce=<N>,
                       command, args, error)
                                                          [Fumble Log:
                                                          ~/.ark/monitoring/
                                                          recall-fumbles.jsonl]
         daemon's pipeline continues with remaining calls
```

## Flow 4: close → result doc + monitor log → self-recycle check

```
7. ark connections recall close <F> --nonce <N>          (R2758)
   [-preserve-curation]
         │
         ├── 7.1  RecallAgentBuilder.Close(fire=<F>,
         │          nonce=<N>, preserveCuration)
         │
         ├── 7.2  if results[<F>] has items:
         │          write tmp://ARK-RECALL/
         │            result-<S>-<F> via write actor:    (R2750, R2751)
         │            @ark-recall-result: <S>
         │            (body: ## Surface: / ## Recommend:
         │             H2 sequence)
         │          outcome = "result-emitted"
         │        else:
         │          no result doc written
         │          outcome = "silent-close"
         │
         ├── 7.3  remove tmp://ARK-RECALL/                (R2758)
         │          curation-<S>-<F> via write actor
         │          (unless -preserve-curation);
         │          also sweep orphan curation docs for
         │          the same session whose fire < <F>
         │          (other sessions' orphans untouched)
         │
         ├── 7.4  discoverSubagentJSONL(nonce=<N>)         (R2759, R2760)
         │          cwd_encoded = replace_slashes(cwd)
         │          parent_session = $CLAUDE_CODE_SESSION_ID
         │          dir = ~/.claude/projects/
         │                 <cwd_encoded>/<parent_session>/
         │                 subagents
         │          scan *.meta.json for description
         │            containing "nonce <N>"
         │          → agent-<id>.jsonl
         │
         ├── 7.5  sumSubagentTokens(jsonlPath)             (R2761, R2762)
         │          for each "type":"assistant" record:
         │            inTokens += usage.input_tokens
         │            outTokens += usage.output_tokens
         │
         ├── 7.6  append one JSON line to                  (R2763)
         │          ~/.ark/monitoring/recall.jsonl:
         │          { fire, session, nonce,
         │            in_tokens, out_tokens,
         │            latency_ms, surfaced, recommended,
         │            outcome, timestamp }
         │          drop results[<F>] from in-flight map
         │
         └── 7.7  daemon loops back to step 3:             (R2857)
                  ark connections recall next <N>
                  - the context-gate now lives inside `next`
                    (3.1): when the nonce's fill reaches
                    [luhmann].context_limit, the next call
                    returns EXIT and the daemon stops; the
                    orchestrator respawns with a fresh nonce
                    (seq-luhmann-supervisor.md). The agent no
                    longer calls `recall context` itself.
```

## Flow 5: assistant reads result doc

```
8. write actor publishes pubsub event matching
   @ark-recall-result=<CC-SID>

9. assistant's ark listen pops the event
         │ (assistant runs ONLY the value-scoped result        (R2855)
         │  subscription:
         │   ark subscribe --session <CC-SID>
         │     --tag ark-recall-result=<CC-SID>
         │   ark listen --session <CC-SID>
         │  it does NOT subscribe to curate, reserve
         │  nonces, or spawn the agent — the daemon owns
         │  the curate side)
         │
         ├── 9.1  ark fetch tmp://ARK-RECALL/
         │           result-<S>-<F>
         │          → returns result doc body
         │
         ├── 9.2  parse ## Surface: / ## Recommend: H2s
         │
         ├── 9.3  for each Recommend item, consult RJ
         │          counter via Store.HasDerivedRejection: (R2765, R2766)
         │          - counter >= reject_mention_ceiling:
         │            drop silently
         │          - counter >= reject_propose_ceiling:
         │            suppress per-record specifics; may
         │            include in aggregate count
         │          - below propose ceiling: full mention
         │
         └── 9.4  decide whether to surface to user;
                  on user reject, assistant calls
                  ark connections recall reject-derived
                  → Store.RejectDerived applies a -1
                  judgment delta (signed score + 8-byte
                  BE nanos) (R2877, R2774)
```

## Notes

- The daemon **subscribes to curate** (bare, `recall-loop-<N>`) and
  loops; the user-facing assistant **subscribes only to results**
  (value-scoped) and consumes them (R2852, R2855). The curate
  subscription moved from the assistant (retired one-shot path) to
  the daemon.
- The daemon **never mints fires** (the watcher does, R2752) and
  **never writes RJ records** (R2774). Permanent rejection state
  stays user-controlled through the assistant.
- The `(fire, nonce)` pair joins three identifier layers: the
  curation doc path (via fire), the result doc path (via fire), and
  the subagent's `.meta.json` (via nonce). The fire varies per loop
  iteration; the nonce is stable for the daemon's whole generation.
- `close` is idempotent in the sense that it never errors on the
  "no result builder was opened" path — silent-close is a valid
  outcome (R2758).
