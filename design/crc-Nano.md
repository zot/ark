# Nano
**Requirements:** R2492, R2493, R2494, R2495, R2496, R2497, R2498, R2499, R2500, R2501, R2502, R2503, R2523, R2534, R2543, R2544, R2559, R2486, R2488, R2489, R2491, R2564, R2566

The orchestrator. Holds all configuration, applies defaults, and drives
the agent loop. Embedding programs construct a `Nano`, set the fields
they care about, and call `Run` or `REPL`.

## Knows
- Model, BaseURL, MaxSteps, MaxOutputBytes, SessionsPath: configurable
- ApproveAll, KeepHistory, TTY: behavior flags
- Stdin, Stdout, Stderr, HTTPClient, Cwd: injectable I/O and environment
- The system prompt (built per Run via NanoSystemPromptBuilder)

## Does
- Apply defaults on first use (init): BaseURL, MaxSteps, MaxOutputBytes,
  Stdin/Stdout/Stderr, HTTPClient, Cwd, SessionsPath
- Validate: Model is required; zero value returns an error
- Run(prompt, history) — append user message, drive the chat-tool loop
  up to MaxSteps, return the final assistant text and updated history
- REPL(history, label, readLine) — multi-turn loop: read, dispatch
  `:q`/`:reset`, call Run, optionally save session
- Fall back to bufio reader when REPL is called with a nil ReadLineFunc

## Collaborators
- NanoOllamaClient: each loop iteration delegates the HTTP exchange
- NanoShellTool: each tool call is dispatched here for validation + execution
- NanoSystemPromptBuilder: invoked once at the start of a fresh Run to seed
  history when none is provided
- NanoSessionStore: called from REPL when KeepHistory is true

## Sequences
- seq-nano-run-loop.md
- seq-nano-repl-turn.md
