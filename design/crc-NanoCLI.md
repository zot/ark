# NanoCLI
**Requirements:** R2508, R2509, R2510, R2511, R2514, R2519, R2520, R2521, R2522, R2524, R2525, R2526, R2490, R2512, R2560, R2561, R2562, R2563, R2565

The thin shell around the Nano library. Reads env vars and argv, builds
a Nano, dispatches to `Run` (one-shot) or `REPL` (interactive), and
wires up chzyer/readline.

## Knows
- The env vars: OLLAMA_MODEL, OLLAMA_BASE_URL, NANO_MAX_STEPS, NANO_APPROVE
- The flag grammar: `[-m model] [-c | -s] [prompt...]` with `-m` and
  `-c`/`-s` orderable
- Sessions live at `~/.ark/nano-sessions.json`
- TTY detection via stderr's stat ModeCharDevice bit
- The exit-code contract: 0 clean, 1 fatal

## Does
- Build a Nano with KeepHistory=true and TTY set from stderr's ModeCharDevice
- Seed Nano.Model from `-m` (preferred) or OLLAMA_MODEL; die with
  `model not set: pass -m <model> or set OLLAMA_MODEL` otherwise
- Parse leading flags: `-m`, `-c`, `-s` in any combination
- Handle `-c` by loading the last session in cwd via NanoSessionsInCwd
- Handle `-s` by delegating to NanoPicker
- Print `no sessions in this directory` and exit 1 when -c/-s find none
- For non-empty prompt: call Nano.Run, save the session, print the
  answer to stdout, exit 0
- For empty prompt: construct a readline.Instance, build a ReadLineFunc
  that translates ErrInterrupt to io.EOF, and call Nano.REPL
- Translate `:q`/`quit`/`exit` to a clean REPL exit (handled by Nano)
- Print fatal errors to stderr and exit 1

## Collaborators
- Nano: the engine the CLI configures and invokes
- NanoSessionStore: for `-c` lookups and one-shot save
- NanoPicker: for `-s` interactive selection
- chzyer/readline: line editing for the REPL prompt

## Sequences
- seq-nano-repl-turn.md
- seq-nano-session-resume.md
