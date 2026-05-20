# Sequence: NanoSession resume

Handling of `-c` and `-s` in the CLI before any prompt is processed.

**Participants:** CLI, NanoSessionStore, NanoPicker

## Steps

- 1. **`-c` path.** Continue the most recent session in cwd.
  - 1.1. NanoSessionStore.NanoSessionsInCwd(path, cwd) → []NanoSession.
  - 1.2. If empty, die "no sessions in this directory" (R2514).
  - 1.3. Pick the last element (oldest-first list, so the last is the
    most recent).
  - 1.4. Set history = last.Messages, label = last.Label.
  - 1.5. Print "continuing: <label>" to Stderr (dim when TTY).
- 2. **`-s` path.** Interactive pick.
  - 2.1. NanoPicker.pick(path, cwd) → NanoSession (delegates to NanoSessionsInCwd).
  - 2.2. Inside NanoPicker: truncate to the last ten, render numbered list,
    read one digit from Stdin, return the chosen NanoSession or an error
    (R2513).
  - 2.3. On error, die with the error message.
  - 2.4. Set history = chosen.Messages, label = chosen.Label.
  - 2.5. Print "resuming: <label>" to Stderr (dim when TTY).
- 3. **Continue.** With history and label populated, dispatch to one-shot
  (if a prompt argument was given) or REPL.
