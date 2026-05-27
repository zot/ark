# Sequence: Recall agent — curate → agent → result → assistant

**Requirements:** R2747, R2748, R2750, R2751, R2755, R2756, R2757,
R2758, R2759, R2760, R2761, R2762, R2763, R2764, R2765, R2766,
R2769, R2770, R2771, R2772, R2773, R2774, R2775, R2776

Picks up where `seq-recall-watcher.md` leaves off — after the
watcher writes `tmp://ARK-RECALL/curation-<session>-<fire>`
and the write actor publishes the matching pubsub event for
the `@ark-recall-curate` tag.

The assistant is responsible for spawning the recall agent
one-shot per fire via the Task tool; the agent is a Haiku
subagent with a hermetic-seal tool allowlist and writes the
result doc through the agent-builder CLI verbs hosted in
`ark serve`.

## Flow 1: curate event → agent spawn

```
1. write actor commits tmp://ARK-RECALL/curation-<S>-<F>
         │
         └── 1.1  publishes pubsub event matching
                  @ark-recall-curate (per write-actor
                  responsibilities — not the builder's job)

2. assistant's ark listen pops the event
         │ (assistant runs:
         │   ark subscribe --session <CC-SID>
         │     --tag ark-recall-curate
         │   ark subscribe --session <CC-SID>
         │     --tag ark-recall-result=<CC-SID>
         │   ark listen --session <CC-SID>)            (R2775, R2776)
         │
         ├── 2.1  if event.@ark-recall-curate != <CC-SID>:
         │          drop event (not for this assistant) (R2775)
         │
         ├── 2.2  N = `ark connections recall reserve-nonce`
         │          → server's RecallAgentBuilder
         │            increments nonceCounter, returns N (R2755)
         │
         ├── 2.3  description = "ark-recall fire <F> nonce <N>"  (R2759)
         │
         └── 2.4  Task(
                    subagent_type="ark-recall-agent",
                    description=description,
                    prompt=<curation-doc-path,
                           fire, nonce briefing>)
```

## Flow 2: agent reads curation doc

```
3. recall agent boots (Haiku, memory: local)            (R2769)
         │
         ├── 3.1  first tool attempt: Read tmp://...
         │          → PreToolUse guard denies              (R2770, R2771)
         │          → denial stderr carries:
         │            "Use `ark fetch tmp://ARK-RECALL/
         │              curation-<S>-<F>` instead."
         │
         ├── 3.2  agent retries with ark fetch:           (R2770)
         │          ark fetch tmp://ARK-RECALL/
         │                    curation-<S>-<F>
         │          → returns the curation doc body
         │
         └── 3.3  agent reads # Source Chunk / ## Candidate
                  H1/H2 blocks; decides which candidates
                  fit and which proposed tags are worth
                  recommending
```

## Flow 3: agent writes result items

```
4. for each surface-worthy candidate:
         ark connections recall surface <F>             (R2756)
           -chunk <CID> -range <PATH>:<RANGE>
           -reason "<one-line>"
         │
         ├── 4.1  CLI POSTs to server's recall handler
         │
         └── 4.2  RecallAgentBuilder.SurfaceItem(
                    fire=<F>, chunkID=<CID>,
                    rangeLabel, reason)
                  - opens results[<F>] on first call
                  - appends "## Surface: ..." H2 +
                    "reason: ..." line

5. for each recommend-worthy tag candidate:
         ark connections recall recommend <F>           (R2757)
           -chunk <CID> -tag @<t>[:<v>]
           -reason "<one-line>"
         │
         └── 5.1  RecallAgentBuilder.RecommendItem(...)
                  - opens results[<F>] on first call if
                    not already open
                  - appends "## Recommend: ..." H2 +
                    "reason: ..." line

6. on any malformed call:
         CLI parse rejects                                (R2772)
           → LogFumble(fire=<F>, nonce=<N>,
                       command, args, error)
                                                          [Fumble Log:
                                                          ~/.ark/monitoring/
                                                          recall-fumbles.jsonl]
         agent's pipeline continues with remaining calls
```

## Flow 4: close → result doc + monitor log

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
         └── 7.6  append one JSON line to                  (R2763)
                  ~/.ark/monitoring/recall.jsonl:
                  {
                    fire, session, nonce,
                    in_tokens, out_tokens,
                    latency_ms, surfaced, recommended,
                    outcome, timestamp
                  }
                  drop results[<F>] from in-flight map
```

## Flow 5: assistant reads result doc

```
8. write actor publishes pubsub event matching
   @ark-recall-result=<CC-SID>

9. assistant's ark listen pops the event
         │ (same listener as Flow 1)                       (R2775, R2776)
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
                  → Store.RejectDerived increments RJ
                  counter (varint counter + 8-byte BE
                  nanos) (R2764, R2774)
```

## Notes

- The recall agent **never subscribes** to pubsub and **never
  writes RJ records** (R2774). Permanent rejection state stays
  user-controlled through the assistant.
- The same `(fire, nonce)` pair is the cookie that joins three
  identifier layers: the curation doc path (via fire), the
  result doc path (via fire), and the subagent's
  `.meta.json` (via nonce). One assistant Task call binds them.
- `close` is idempotent in the sense that it never errors on
  the "no result builder was opened" path — silent-close is a
  valid outcome (R2758).
