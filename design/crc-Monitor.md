# Monitor
**Requirements:** R2784, R2785, R2786, R2787, R2788, R2789, R2790

CLI surface for reading and lightly mutating the per-class JSONL
supervisory logs under `~/.ark/monitoring/`. Read-only by intent —
`status` and `recent` open and tail the log files directly; `pause`
and `resume` route a single control record through the write actor.

The shipped classes are `recall` (per-fire records written by
`close` per R2763) and `luhmann` (per-event records written by the
orchestrator's `ark luhmann spawn-record` / `exit-record` per
crc-LuhmannCLI.md). The CLI's class enumeration is hardcoded; a
future supervisor that adds its own JSONL is wired in by extending
this card and the dispatch table.

## Knows
- arkDir: string — `~/.ark/` (from the `--dir` global flag)
- classes: []string — hardcoded `[]string{"recall", "luhmann"}`
- freshnessWindow: time.Duration — 90 minutes (R2785), used to
  classify `recall` as `active` vs `idle` from the latest record's
  timestamp

## Does

### `monitor status`
- Run cold (no server). For each class:
  - Read `~/.ark/monitoring/<class>.jsonl` in reverse, stopping at
    the first state-defining record. For `luhmann`, that is the
    most recent of `spawn`, `exit`, `respawn`, `crash`, `pause`,
    `resume`; the derived state is `running` / `paused` /
    `crashed` per R2785. For `recall`, every record is a fire
    completion; state is `active` if `now - latest.timestamp <
    freshnessWindow`, else `idle`.
  - Derive counters: for `luhmann`, the current `crashes` counter
    and the current nonce (taken from the most recent record that
    carries them). For `recall`, the count of records in the last
    window and the average `in_tokens` / `out_tokens` across
    those records.
- Render the per-class block as a small markdown table.
  `--json` emits one object per class on stdout. (R2784)

### `monitor recent`
- Run cold. Default `n=20`. With a `CLASS` positional, restrict to
  that file; otherwise read each file's tail and interleave by
  timestamp.
- Print records oldest-first within the selected window. Default
  output: one bullet per record with timestamp, kind, and the most
  informative identifying fields for the kind (e.g. `nonce`,
  `class`, `reason`, `fire`). `--json` emits raw JSONL records
  one per line. (R2786)

### `monitor pause CLASS` / `monitor resume CLASS`
- Server-required. Read the tail of `<class>.jsonl` to verify the
  state guard (R2789): `pause` exits non-zero if the most recent
  state-defining record was already a `pause`; `resume` exits
  non-zero if state is currently `running`.
- Append one record `{ts, kind: "pause", class: CLASS, nonce: 0}`
  (or `"resume"`) to `<class>.jsonl` via the write actor (R2787,
  R2788). The state-machine guard is best-effort — a race with a
  concurrent writer can produce a duplicate, which the consumer
  treats as idempotent.
- `monitor` does not implement the pause/resume effect on the
  consumer. It only writes the signal record; the orchestrator (or
  any future class consumer) reads its own log to decide what to
  do.

## Collaborators
- LuhmannCLI (crc-LuhmannCLI.md): the writer for `luhmann.jsonl`.
  Reads back through `monitor`.
- RecallAgentBuilder (crc-RecallAgentBuilder.md): the writer for
  `recall.jsonl` via `close` (R2763).
- Server (crc-Server.md): hosts the HTTP handler for `pause` /
  `resume`'s write-actor append; `status` / `recent` do not call
  the server.
- CLI (crc-CLI.md): dispatches the four subcommands.

## Sequences
- seq-luhmann-supervisor.md
