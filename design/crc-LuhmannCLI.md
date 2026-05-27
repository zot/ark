# LuhmannCLI
**Requirements:** R2791, R2792, R2793, R2794, R2795, R2796

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
  the named class. `crashes` carries forward from that record's
  `crashes` field (or `0` if no prior record exists).
- Append one record:
  ```
  {ts: now, kind: "spawn", class: C, nonce: N, task_id: T,
   reason: "", crashes: <carried>, backoff: 0}
  ```
  via the write actor. (R2794)

### `luhmann exit-record --class C --nonce N --reason R [--crashes K]`
- Server-required. `--class`, `--nonce`, `--reason` required.
- Look at the tail to find the previous spawn / exit / crash for
  this class and read `crashes`. Compute the new value (R2795):
  - `--reason context-limit` → kind `exit`, `crashes := 0` (healthy
    recycle resets the counter).
  - any other reason → kind `crash`, `crashes := prev + 1`.
  - `--crashes K` overrides the computed value when the caller
    has already classified.
- Append one record:
  ```
  {ts: now, kind: <"exit"|"crash">, class: C, nonce: N, task_id: "",
   reason: R, crashes: <computed>, backoff: <see below>}
  ```
  For `crash`, `backoff` is the seconds the supervisor will wait
  before respawn (orchestrator chooses, passes as `--backoff`).
  For `exit`, `backoff` is zero. (R2795)

### `luhmann inspect-exit --nonce N [--json]`
- Cold-start.
- Reuse the nonce → `.meta.json` discovery from R2760
  (`discoverSubagentJSONL(nonce)`). On lookup failure, label is
  `unknown`.
- Read the subagent's JSONL backwards. Classify (R2796):
  - **healthy** when the most recent record is a `tool_result` for
    `ark connections recall close`, OR when the most recent
    `recall.jsonl` line for the same nonce shows `outcome ∈
    {"result-emitted", "silent-close", "no-subscriber"}`.
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
