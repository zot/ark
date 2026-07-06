# Luhmann — Go-side support for the orchestrator session

Language: Go. Environment: ark CLI binary; writes JSONL records under
`~/.ark/monitoring/luhmann.jsonl`.

This spec covers the **Go side** of the Luhmann orchestrator: the
`ark luhmann` CLI verbs the orchestrator session uses to record its
own supervisor lifecycle events **and to drain its work tube**
(`next`), and the `[luhmann]` `ark.toml` section that configures
restart policy. The orchestrator session
itself — its persona, its event handling, the lotto-tube subagent it
hosts — lives in a Claude Code skill (`.claude/skills/luhmann.md`)
and a companion agent definition (`.claude/agents/luhmann-researcher.md`),
neither of which is in this spec's scope. The skill / agent files
are Claude Code assets, not Go code.

See also: [monitor.md](monitor.md) (`ark monitor` reads the JSONL log
this CLI writes), [chimes.md](chimes.md) (the chime cadence the
orchestrator session subscribes to for cache warmth),
[simple-recall.md](simple-recall.md) (the recall lotto-tube subagent
the orchestrator supervises),
[bloodhound-cli.md](bloodhound-cli.md) (the external-CLI bloodhound,
whose secretary pool Luhmann supervises and whose hunts it curates off
the `next` tube).

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
ark luhmann next --session S [--first | --force] [--keepalive N]
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

### `next`

The orchestrator's **drain tube**: one blocking verb Luhmann pops in a
loop to receive everything the recall service pushes at it. It is
launched `run_in_background` so the orchestrator stays conversational and
keeps supervising while it waits. On completion Claude Code re-injects
the result; Luhmann then **re-invokes `next` first — before acting on the
work it carries** — so a stalled or garbled work turn can never leave the
seat undrained (the successor `next` is already blocking, ready to hand
back the following item once the work finishes). Loop continuity comes
from re-launching at the *front* of each turn, not the tail; the work
crank handles lead with this instruction and the `/luhmann` skill teaches
it (R3036). This is the established background-lotto-tube shape, the same
family as `ark connections recall next` and the `/ui` skill's `{cmd}
event` listener.

A single `next` return carries one of three **kinds**, told apart by the
returned body:

- **curation task** — a raw bloodhound finding the watcher handed back
  from an external-CLI hunt (see [bloodhound-cli.md](bloodhound-cli.md)).
  The body carries the CLI result-doc path (`tmp://BLOODHOUND-CLI/…`) and
  the raw finding. Luhmann applies its own discernment, calls the `ark
  bloodhound add` stencil one item per call, then tags the result doc to
  wake the waiting CLI. This curation is the strong parent's judgment
  layered on top of the sealed Haiku secretary's raw find.
- **supervisor directive** — a pool-control instruction, *stand up
  another secretary* or *stop one*. Luhmann executes it via the Task tool
  and records the lifecycle with `spawn-record` / `exit-record`. The
  CLI-bloodhound secretary pool is the managed class these directives
  drive.
- **keepalive** — when neither arrives within the keepalive window,
  `next` returns a keepalive crank-handle so Luhmann spends one cheap
  cached turn and re-invokes, holding the main-agent prompt cache warm
  across idle gaps.

#### Ownership lease — `--session`, `--first`, `--force`

Luhmann is a **singleton**: "it's the server" for the external-CLI
bloodhound. So `--session S` is not a tube *scope* (the tube is one
global queue of every CLI hunt and directive) but an **ownership
identity**, which lets the service detect a second Luhmann and keep
exactly one draining. The lease is **in-memory** server state with no
persistence, so a server bounce clears it.

- **`--first`** claims the role. It succeeds when the role is unowned;
  when a *different* session already owns it, it errors `you don't have
  ownership`.
- **plain `next --session S`** (no flag) **validates and never claims**.
  It proceeds only when S is the owner. When the role is unowned it
  errors `there are no sessions`; when a different session owns it, it
  errors `you don't have ownership`.
- **`--force`** reclaims the role unconditionally, the deliberate
  takeover for when a prior owner has died but its in-memory lease still
  lingers (the server itself never bounced).

The two error strings drive two orchestrator reflexes, and together they
make the protocol **self-converging** with no persistence and no human
arbitration:

- **`there are no sessions`** means the server was reborn by a bounce and
  is unowned. Luhmann re-invokes with `--first` to re-claim, then
  resumes.
- **`you don't have ownership`** means another session now holds the
  seat, so this Luhmann **exits**. After a bounce where two orchestrators
  both see `there are no sessions` and both fire `--first`, one wins and
  the loser's `--first` returns `you don't have ownership`, so it steps
  down. Exactly one Luhmann survives.

A server bounce is a **wait condition, not an error** (Stubborn
Plumbing, as with `ark connections recall next`): `next` redials with
backoff across the bounce, and on reconnect the `there are no sessions`
case routes into the re-`--first` path above rather than failing the
loop.

#### Keepalive cadence

`--keepalive N` overrides the idle window; the default is ≈ 45 minutes.
The window must stay under the cache TTL that governs where the loop
runs. `next` is drained by Luhmann, the *main conversational agent*,
whose subscription prompt cache has a **1-hour TTL**, so it runs
backgrounded and its keepalive sits at about 45 minutes, comfortably
under the hour. This is a **backgrounded-loop** clock and must not be
confused with the recall secretary's 90-second *foreground* keepalive on
`ark connections recall next`: that number is an artifact of the
harness's ~120 s foreground-Bash auto-background threshold on a dedicated
subagent. Same "stay warm" instinct, two different clocks.

This drain tube **subsumes** the earlier standalone `ark heartbeat`
keepalive design: one tube carries curation tasks, supervisor
directives, and the keepalive chime on a single sub-1-hour clock, so no
separate heartbeat command is needed.

Server required.

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
```

**Recall is no longer a Luhmann class.** As of the secretary-pipeline
migration (seam 3a), the per-session recall secretary is spawned and
supervised by each session's own assistant via `/recall` (see
`simple-recall.md`); the orchestrator no longer hosts a `recall` class. The
supervisor *mechanism* — the `ark luhmann` verbs, the crash/quit-early
streak machine, and `class.<NAME>.enabled` — remains for a future managed
class and for Luhmann's promotion to the user-side majordomo role.

The **CLI-bloodhound secretary pool** ([bloodhound-cli.md](bloodhound-cli.md))
is the first use of that retained mechanism. On the watcher's `next`
directives Luhmann stands up and stops pool secretaries (recording each with
`spawn-record` / `exit-record`), and their crashes and quit-earlies feed the
same streak machine and `[luhmann]` policy the recall class once used. Unlike
recall, this pool *is* Luhmann-hosted: pool secretaries serve every session's
CLI hunts, so no single session's assistant can own them.

| Key                              | Type     | Default       | Meaning                                                                                                                                              |
|----------------------------------|----------|---------------|------------------------------------------------------------------------------------------------------------------------------------------------------|
| `context_limit`                  | int      | `150000`      | The token ceiling the orchestrator passes to each spawned subagent (used by the subagent's self-recycle check via R2777).                            |
| `crash_pause_after`              | int      | `3`           | After this many consecutive crashes for a class, the supervisor stops respawning and writes a `crash-storm` `pause` record. User clears with `ark monitor resume`. |
| `quit_early_pause_after`         | int      | `3`           | After this many consecutive quit-earlies for a class (independent counter), the supervisor stops respawning and writes a `quit-early-storm` `pause` record. User clears with `ark monitor resume`. |
| `backoff_seconds`                | []int    | `[1, 5, 30]`  | The seconds to wait between successive crash respawns. Last value is used for any further attempts up to `crash_pause_after`.                        |
| `class.<NAME>.enabled`           | bool     | `true`        | Whether the orchestrator should host this class. Setting to `false` disables it without removing supervisor state from the log.                      |
| `class.<NAME>.pool_max`          | int      | `3`           | For a pooled class (the CLI-bloodhound pool), the maximum concurrent secretaries Luhmann stands up on `stand up another` directives. See [bloodhound-cli.md](bloodhound-cli.md).             |
| `class.<NAME>.cooldown_seconds`  | int      | `600`         | For a pooled class, how long a secretary that has returned to idle stays warm before it is eligible for pruning (damps spawn/stop churn; warm enough for interactive-burst reuse).           |
| `class.<NAME>.request_ttl_seconds` | int    | `900`         | For the CLI-bloodhound pool: the watcher reaps a stranded request (a client that hit `--timeout` and exited) older than this. Read by the watcher, not Luhmann. See [bloodhound-cli.md](bloodhound-cli.md).           |

Live reload: `[luhmann]` follows the same `ark.toml` reload path as
the rest of the config. Changes take effect on the next supervisor
decision; no restart required. The orchestrator session re-reads
the section when it acts on a subagent completion event.

## What this spec deliberately does not require

- An `ark luhmann start` or `stop` **process** daemon. The
  orchestrator is a Claude Code session, not a process under
  `systemd`: its lifecycle is the session's lifecycle. `next` is a
  blocking verb the session itself backgrounds and re-invokes (the
  drain tube above), not a detached server process. The Go-side
  surface is the supervisor log, the drain tube, and the config
  schema.
- Inter-class coordination (e.g. "pause recall when monitoring is
  active"). One class at a time per the firmness scope; future
  multi-class supervisors will earn their own coordination spec.
- A web UI. The monitor view in Frictionless is downstream
  (ARK-STATE item 2, `/ui-thorough`) and reads the same JSONL log
  that `ark monitor` does.
- A Lua API. The orchestrator session is the only writer to
  `luhmann.jsonl`; no Frictionless feature needs to author records
  yet. When one does, the Lua bridge is a follow-on.
