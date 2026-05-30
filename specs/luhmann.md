# Luhmann — Go-side support for the orchestrator session

Language: Go. Environment: ark CLI binary; writes JSONL records under
`~/.ark/monitoring/luhmann.jsonl`.

This spec covers the **Go side** of the Luhmann orchestrator: the
`ark luhmann` CLI verbs the orchestrator session uses to record its
own supervisor lifecycle events, and the `[luhmann]` `ark.toml`
section that configures restart policy. The orchestrator session
itself — its persona, its event handling, the lotto-tube subagent it
hosts — lives in a Claude Code skill (`.claude/skills/luhmann.md`)
and a companion agent definition (`.claude/agents/luhmann-researcher.md`),
neither of which is in this spec's scope. The skill / agent files
are Claude Code assets, not Go code.

See also: [monitor.md](monitor.md) (`ark monitor` reads the JSONL log
this CLI writes), [chimes.md](chimes.md) (the chime cadence the
orchestrator session subscribes to for cache warmth),
[simple-recall.md](simple-recall.md) (the recall lotto-tube subagent
the orchestrator supervises).

Design reference: [.scratch/LUHMANN-ORCHESTRATOR.md](.scratch/LUHMANN-ORCHESTRATOR.md),
[.scratch/LUHMANN-SKILL.md](.scratch/LUHMANN-SKILL.md).

## Supervisor log

`~/.ark/monitoring/luhmann.jsonl` is the orchestrator's append-only
supervisor log. Each line is one JSON object describing one lifecycle
event for a managed subagent class. The shipped class is `recall`; the
file format is class-neutral so additional classes (monitoring,
daydream, etc.) plug in later without record-shape changes.

Record fields:

| Field      | Type   | Meaning                                                                                                                                                                |
|------------|--------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `ts`       | string | RFC 3339 timestamp.                                                                                                                                                    |
| `kind`     | string | One of `spawn`, `exit`, `respawn`, `crash`, `quit-early`, `pause`, `resume`.                                                                                                         |
| `class`    | string | The managed subagent class (e.g. `recall`).                                                                                                                            |
| `nonce`    | int    | The nonce associated with the spawn this record describes. `pause` and `resume` records carry `nonce: 0` (the control record applies to the class, not a generation). |
| `task_id`  | string | The Claude Code Task identifier of the spawn. Present on `spawn` and `respawn`; empty otherwise.                                                                       |
| `reason`   | string | For `exit`, `crash`, and `quit-early`, a short classification string (`context-limit`, `quit-early`, `error`, etc.); for a storm `pause`, `crash-storm` / `quit-early-storm`. Empty otherwise.                                             |
| `crashes`  | int    | The consecutive-crash counter at the time of the record. Resets to 0 on a healthy `exit`; held across a `quit-early`. |
| `quit_early` | int   | The consecutive-quit-early counter. Resets to 0 on a healthy `exit`; held across a `crash`.                                                                              |
| `backoff`  | int    | For `crash` records, the seconds the supervisor waited before the next respawn (per the configured backoff schedule).                                                  |

The record format is forward-compatible: future fields slot in at the
end. Old readers ignore unknown fields.

## CLI

```
ark luhmann spawn-record --class C --nonce N --task-id T
ark luhmann exit-record  --class C --nonce N --reason R [--crashes K] [--quit-early K]
ark luhmann inspect-exit --nonce N [--json]
```

### `spawn-record`

Appends one record with `kind: "spawn"` to `luhmann.jsonl`. Called by
the orchestrator session immediately after launching a managed
subagent via the Task tool. Required flags: `--class`, `--nonce`,
`--task-id`. Server required: the write routes through the write
actor.

### `exit-record`

Appends one record to `luhmann.jsonl`. The `kind` is determined by
`--reason`, and **both** the `crashes` and `quit_early` counters
recompute from the previous record:

- `--reason context-limit` → `kind: "exit"` (healthy recycle). A
  success resets **both** counters to 0.
- `--reason quit-early` → `kind: "quit-early"`. `quit_early`
  increments; `crashes` is held. A quit-early is the loop-discipline
  exit class (the agent stopped before filling its context) — not a
  crash; the orchestrator respawns it like a healthy exit but counts
  it on its own streak.
- Any other reason → `kind: "crash"`. `crashes` increments;
  `quit_early` is held.

`--crashes K` / `--quit-early K` override the respective computed
counter when the caller has already classified. Called by the
orchestrator session when it observes a managed subagent's Task
complete. The companion `respawn` record (if the supervisor decides
to respawn) is written by the next `spawn-record` call (which carries
both counters forward). Server required.

### `inspect-exit`

Reads the subagent's own JSONL (located via the
`nonce → .meta.json` lookup defined in
[simple-recall.md](simple-recall.md) R2760) and classifies the exit as
`healthy`, `quit-early`, `crash`, or `unknown`. Output:

- Default: one of the four labels on stdout.
- `--json`: a JSON object with `label`, `last_record_kind`,
  `last_error` (string or null), and `tokens_at_close` (the value
  R2777 returns).

The classification rule turns on the context fill at close
(`tokens_at_close`) against `[luhmann].context_limit`:

- `healthy` — `tokens_at_close` is at or over the limit: the
  generation filled and recycled as designed. With `ark connections
  recall next` the daemon's only clean exit is its context-gate
  directive, so a real recycle always reaches the limit.
- `quit-early` — the most recent record is a clean turn boundary (a
  `tool_result` for `ark connections recall next` / `close`, or a
  `recall.jsonl` outcome of `result-emitted` / `silent-close` /
  `no-subscriber`) but `tokens_at_close` is *below* the limit: the
  agent stopped looping before filling. A distinct error class, with
  its own streak and escalation (see the supervisor decision tree);
  the label keeps the early stop visible instead of masquerading as
  healthy.
- `crash` — the most recent record is an error tail or the file
  ends mid-turn.
- `unknown` — the lookup couldn't find the JSONL (no `.meta.json`
  match or stale subagent state).

Cold-start. No server required.

## `[luhmann]` configuration

`ark.toml`'s `[luhmann]` section configures the orchestrator's
restart policy. Every key is read by the orchestrator session (via
`ark config` or by reading `ark.toml` directly); this Go-side spec
defines the schema and the loader contract.

```toml
[luhmann]
context_limit = 150000
crash_pause_after = 3
quit_early_pause_after = 3
backoff_seconds = [1, 5, 30]

[luhmann.class.recall]
enabled = true
```

| Key                              | Type     | Default       | Meaning                                                                                                                                              |
|----------------------------------|----------|---------------|------------------------------------------------------------------------------------------------------------------------------------------------------|
| `context_limit`                  | int      | `150000`      | The token ceiling the orchestrator passes to each spawned subagent (used by the subagent's self-recycle check via R2777).                            |
| `crash_pause_after`              | int      | `3`           | After this many consecutive crashes for a class, the supervisor stops respawning and writes a `crash-storm` `pause` record. User clears with `ark monitor resume`. |
| `quit_early_pause_after`         | int      | `3`           | After this many consecutive quit-earlies for a class (independent counter), the supervisor stops respawning and writes a `quit-early-storm` `pause` record. User clears with `ark monitor resume`. |
| `backoff_seconds`                | []int    | `[1, 5, 30]`  | The seconds to wait between successive crash respawns. Last value is used for any further attempts up to `crash_pause_after`.                        |
| `class.<NAME>.enabled`           | bool     | `true`        | Whether the orchestrator should host this class. Setting to `false` disables it without removing supervisor state from the log.                      |

Live reload: `[luhmann]` follows the same `ark.toml` reload path as
the rest of the config. Changes take effect on the next supervisor
decision; no restart required. The orchestrator session re-reads
the section when it acts on a subagent completion event.

## What this spec deliberately does not require

- An `ark luhmann start` or `stop` daemon command. The orchestrator
  is a Claude Code session, not a process under `systemd` — its
  lifecycle is the session's lifecycle. The Go-side surface is
  purely the supervisor log and the config schema.
- Inter-class coordination (e.g. "pause recall when monitoring is
  active"). One class at a time per the firmness scope; future
  multi-class supervisors will earn their own coordination spec.
- A web UI. The monitor view in Frictionless is downstream
  (ARK-STATE item 2, `/ui-thorough`) and reads the same JSONL log
  that `ark monitor` does.
- A Lua API. The orchestrator session is the only writer to
  `luhmann.jsonl`; no Frictionless feature needs to author records
  yet. When one does, the Lua bridge is a follow-on.
