# Sequence: REPL turn

One turn of the interactive REPL. The CLI builds the ReadLineFunc out
of chzyer/readline; the library uses it without knowing where the line
came from.

**Participants:** CLI, Nano, NanoSessionStore

## Steps

- 1. **Read.** Call readLine("nano > ") → (line, err).
  - 1.1. If err is io.EOF (incl. ErrInterrupt translated by the CLI),
    return cleanly from REPL (R2523).
  - 1.2. On other errors, return the error to the caller.
  - 1.3. Trim whitespace. Empty line → continue loop.
- 2. **Dispatch keywords.**
  - 2.1. If line lowercases to `:q`/`quit`/`exit`, return (R2521).
  - 2.2. If line lowercases to `:reset`/`reset`, set history=nil,
    label="", print "reset", continue loop (R2522).
- 3. **Run.** Call Nano.Run(prompt, history) → (answer, newHistory, err).
  - 3.1. On error, print "error: <msg>" to Stderr; do not save; continue.
  - 3.2. Replace history with newHistory.
  - 3.3. If label is empty, label = prompt.
- 4. **Persist.** If KeepHistory is true, call NanoSessionStore.SaveNanoSession
  with {label: truncate(label, 80), cwd: Nano.Cwd, ts: now, messages:
  history} (R2524, R2529, R2530, R2531, R2532).
  - 4.1. On save error, print warning to Stderr; do not abort the REPL.
- 5. **Print.** Write answer to Stdout. Continue loop.
