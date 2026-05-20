# NanoSessionStore
**Requirements:** R2504, R2505, R2506, R2507, R2527, R2528, R2529, R2530, R2531, R2532, R2533, R2535

Persistence boundary. Owns the on-disk JSON file: how sessions are
written, replaced, capped, and filtered. Package-level functions (not
methods on Nano) because the file format does not depend on agent
configuration.

## Knows
- The JSON shape: array of `NanoSession{label, cwd, ts, messages}`
- The 50-session cap
- The label-truncation length (80 characters, applied by callers when
  building a NanoSession)
- The file mode 0600
- The path is supplied by the caller (no default lookup here; Nano and
  the CLI compute the default)

## Does
- LoadNanoSessions(path) — read and unmarshal; return (nil, nil) for a
  missing file
- SaveNanoSession(path, s) — append, dedup by (label, cwd), cap at 50, write
  with mode 0600
- NanoSessionsInCwd(path, cwd) — load and filter to matching cwd, oldest first

## Collaborators
- Nano.REPL: persistence on every turn when KeepHistory is true
- CLI: `-c` reads via NanoSessionsInCwd; one-shot mode saves via SaveNanoSession
- NanoPicker: lists via NanoSessionsInCwd

## Sequences
- seq-nano-repl-turn.md
- seq-nano-session-resume.md
