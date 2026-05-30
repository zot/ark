# LuhmannCLI
**Requirements:** R2791, R2792, R2793, R2794, R2795, R2796, R2861

Three CLI verbs the orchestrator session uses to record its own
supervisor lifecycle into `~/.ark/monitoring/luhmann.jsonl`.
`spawn-record` and `exit-record` are server-required because they
route through the write actor; `inspect-exit` is cold-start because
it only reads subagent JSONLs.

The orchestrator session itself — the persona, the supervisor logic,
the Task-spawning of managed subagents — lives in a Claude Code
skill (`.claude/skills/luhmann.md`) and is **not** Go code. This
card covers only the Go surface the skill calls into.

## Knows
- arkDir: string — `~/.ark/`
- logPath: string — `~/.ark/monitoring/luhmann.jsonl`

## Does

### `luhmann spawn-record --class C --nonce N --task-id T`
- Server-required. All three flags are required; missing-flag exit
  is non-zero before any state change.
- Read the existing log tail to find the most recent record for
  the named class. `crashes` and `quit_early` both carry forward
  from that record's fields (or `0` if no prior record exists). (R2861)
- Append one record:
  ```
  {ts: now, kind: "spawn", class: C, nonce: N, task_id: T,
   reason: "", crashes: <carried>, quit_early: <carried>, backoff: 0}
  ```
  via the write actor. (R2794)

### `luhmann exit-record --class C --nonce N --reason R [--crashes K] [--quit-early K]`
- Server-required. `--class`, `--nonce`, `--reason` required.
- Look at the tail to find the previous record for this class and
  read both `crashes` and `quit_early`. Reason → kind, then
  recompute both counters (R2795, R2861):
  - `context-limit` → kind `exit`; `crashes := 0`, `quit_early := 0`
    (a healthy recycle is a success — resets both).
  - `quit-early` → kind `quit-early`; `quit_early := prev + 1`,
    `crashes := prev` (held).
  - any other reason → kind `crash`; `crashes := prev + 1`,
    `quit_early := prev` (held).
  - `--crashes K` / `--quit-early K` override the respective
    computed value when the caller has already classified.
- Append one record:
  ```
  {ts: now, kind: <"exit"|"quit-early"|"crash">, class: C, nonce: N,
   task_id: "", reason: R, crashes: <computed>,
   quit_early: <computed>, backoff: <see below>}
  ```
  For `crash`, `backoff` is the seconds the supervisor will wait
  before respawn (orchestrator chooses, passes as `--backoff`).
  For `exit` and `quit-early`, `backoff` is zero. (R2795)

### `luhmann inspect-exit --nonce N [--json]`
- Cold-start.
- Reuse the nonce → `.meta.json` discovery from R2760
  (`discoverSubagentJSONL(nonce)`). On lookup failure, label is
  `unknown`.
- Read the subagent's JSONL backwards. Classify (R2796):
  - **healthy** requires `tokens_at_close` (the R2777 value) at or
    over `[luhmann].context_limit` — the generation filled and
    recycled as designed. With `ark connections recall next` the
    only clean exit is its context-gate directive, so a real
    recycle always reaches the limit.
  - **quit-early** when the most recent record is a clean turn
    boundary (a `tool_result` for `ark connections recall next` /
    `close`, or a `recall.jsonl` outcome ∈ {`result-emitted`,
    `silent-close`, `no-subscriber`}) but `tokens_at_close` is
    *below* the limit — the agent stopped before filling. The
    orchestrator respawns it (fresh nonce) like a healthy exit but
    does not count it as a crash; the distinct label keeps the early
    stop visible instead of masquerading as healthy.
  - **crash** when the most recent record is an error tail
    (`isError: true`) or the JSONL ends mid-turn (last record is
    not a complete tool_result / assistant turn).
  - **unknown** when the JSONL is empty or discovery failed.
- Default output: the label string on stdout. `--json`: object
  with `label`, `last_record_kind` (e.g. `tool_result`, `assistant`,
  `user`), `last_error` (string or null), and `tokens_at_close`
  (the R2777 value, or `0` when unmeasured).

## Collaborators
- Monitor (crc-Monitor.md): reads back the log this card writes.
- RecallAgentBuilder (crc-RecallAgentBuilder.md): provides the
  `discoverSubagentJSONL` helper (R2760) and the same `.meta.json`
  scanning behavior; LuhmannCLI calls into it for `inspect-exit`.
  No new lookup code on this card.
- Server (crc-Server.md): hosts the HTTP handlers for the two
  write-actor verbs.
- CLI (crc-CLI.md): dispatches the three subcommands.

## Sequences
- seq-luhmann-supervisor.md
