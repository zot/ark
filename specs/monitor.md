# Monitor — `ark monitor` CLI

Language: Go. Environment: ark CLI binary; reads JSONL monitoring
logs under `~/.ark/monitoring/`.

The `ark monitor` subcommand inspects ark's internal supervisory
JSONL logs — the per-fire records the recall watcher writes
(`recall.jsonl`, see [simple-recall.md](simple-recall.md) R2763)
and the per-event records the Luhmann supervisor writes
(`luhmann.jsonl`, see [luhmann.md](luhmann.md)). It is a read-only
tool: it never writes log files itself, never mutates DB state.

## Subcommands

```
ark monitor status [--json]
ark monitor recent [-n N] [CLASS] [--json]
ark monitor pause CLASS
ark monitor resume CLASS
```

### `status`

Reports a one-screen overview of every monitored class. A *class* is
a kind of monitored worker that writes JSONL records under
`~/.ark/monitoring/<class>.jsonl`. The shipped classes are `recall`
(populated by the recall watcher) and `luhmann` (populated by the
Luhmann supervisor).

For each class, the command reads the latest records from the JSONL
log and reports:

- The current lifecycle state, derived from the most recent
  state-defining record. For `luhmann.jsonl`, the states are
  `running`, `paused`, and `crashed` (definitions in
  [luhmann.md](luhmann.md)). For `recall.jsonl`, which records
  fire outcomes rather than lifecycle, the state is `active`
  when the most recent record's timestamp is within a small
  freshness window (default 90 minutes) and `idle` otherwise.
- The most recent record's timestamp.
- A small set of class-specific counters derived from the log
  tail: for `luhmann`, the current crash counter and the current
  nonce; for `recall`, the recent fire count and the average
  in/out tokens across the last N records.

The default output is a small markdown table. `--json` emits one
object per class on stdout.

The command runs cold — no server required. Reading the JSONL files
is sufficient; the DB is not consulted.

### `recent`

Prints the tail of one or all monitoring logs.

```
ark monitor recent             # all classes, last 20 records
ark monitor recent -n 50       # all classes, last 50 records
ark monitor recent recall      # just recall, last 20
ark monitor recent recall -n 5 # just recall, last 5
```

Default `-n` is 20. Records are printed in original order (oldest of
the selected window first). With `--json`, each line of output is the
raw JSONL record; without `--json`, each record is rendered as a
short markdown bullet with timestamp, kind, and the most relevant
identifying fields.

Cold-start. No server required.

### `pause CLASS` and `resume CLASS`

Append a control record to the named class's monitoring log. The
record carries `"kind": "pause"` or `"kind": "resume"` and a
timestamp. The consumer of the log (e.g. the Luhmann supervisor for
`luhmann`) checks the most recent control record at decision time
and acts accordingly — `monitor` itself does not implement the
pause/resume effect, it only signals it.

`pause` exits non-zero with a diagnostic if the class is already
paused (the most recent state-defining record was a `pause` not
followed by a `resume`); `resume` exits non-zero if the class is
already running. The check is best-effort — a race with a concurrent
writer can produce a duplicate, which the consumer treats as
idempotent.

`pause` and `resume` are the only `monitor` subcommands that write.
They append to the JSONL file via the standard write actor path (no
direct file write from the CLI handler — see [db-write-actor.md](db-write-actor.md)).

## Output formats

All subcommands accept `--json`. The default human-facing output is
small markdown — one block per class for `status`, one bullet per
record for `recent`. `--help` on any subcommand prints usage and exits
zero.

## Exit codes

- `0` — success
- non-zero — invalid usage, no such class, or (for `pause`/`resume`)
  the state-already-set guard tripped.

## What this spec deliberately does not require

- A way to register new monitoring classes from outside ark. The
  shipped classes (`recall`, `luhmann`) are hardcoded in the CLI.
  When a future supervisor adds its own JSONL, this spec gets a new
  class entry; until then there are two.
- An aggregate summary across classes. `status` lists each class
  separately. Cross-class reasoning is the user's job.
- A `tail -f` mode. `recent` is one-shot. Live streaming belongs in
  a future enhancement once a concrete consumer earns it.
- Log rotation. Monitoring logs grow unbounded until rotation lands
  as a separate workstream — see the rolling notes in
  [.scratch/LUHMANN-ORCHESTRATOR.md](.scratch/LUHMANN-ORCHESTRATOR.md).
