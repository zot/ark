# Nano: CLI

The CLI is the `nano` subcommand of `ark`, wired in `cmd/ark/main.go`.
Built as part of the standard ark `make` target.

## Synopsis

```
ark nano [-m model] [-c | -s] [prompt...]
```

- With a prompt: one-shot. The agent runs the prompt, prints the final
  answer to stdout, and exits.
- Without a prompt: interactive REPL.

`-m` may be combined with `-c` or `-s`. Flags must precede the prompt.

## Flags

- `-m <model>` — set the model name. **Required** — there is no
  environment-variable fallback.
- `--base-url <url>` — Ollama server base URL. Default
  `http://localhost:11434`.
- `--max-steps <N>` — maximum tool calls per task before the loop
  gives up. Default `200`. Non-integer argument exits with
  `--max-steps requires an integer`.
- `--approve-all` — auto-approve every shell command (boolean, no
  argument). Equivalent to typing `a` at the first approval prompt.
- `--stream` — print content tokens to stdout as Ollama emits them.
  Suppresses the thinking spinner; the live output is the progress
  signal. Default off (matches the standalone nano-go's
  non-streaming behavior).
- `-c` — continue the most recent session whose `cwd` matches the
  current working directory.
- `-s` — pick from up to ten recent sessions in this directory. Prints
  a numbered list, reads a digit from stdin, resumes that session.
- `-h`, `--help` — print usage and exit 0. Accepted at any position
  (including after `-m`).

Flags can appear in any order; they must precede the prompt.

If `-c` or `-s` is given and no matching sessions exist, the CLI exits
with `no sessions in this directory`.

If no model is provided, the CLI exits with `model not set: pass -m
<model>`.

The standalone `nano-go` CLI read these defaults from `OLLAMA_MODEL`,
`OLLAMA_BASE_URL`, `NANO_MAX_STEPS`, and `NANO_APPROVE`. Ark drops
those environment hooks — every knob is a flag.

## REPL

When the REPL is active:

- The prompt is `nano > ` with chzyer/readline line editing (arrow
  keys, history, ctrl-L to clear).
- `:q`, `quit`, `exit` end the session.
- `:reset`, `reset` clear the in-memory history and start over.
- Ctrl-C interrupts the current readline; ctrl-D / EOF exits.
- After every turn the session is saved.

## Session persistence

The CLI always runs with `KeepHistory=true`. After every turn (REPL
or one-shot), it writes the updated message log to the sessions file.
The default path is `~/.ark/nano-sessions.json` (inside ark's home
directory); see [`nano-sessions.md`](nano-sessions.md) for the file
format.

## Color and spinner

The CLI enables `TTY` (color escapes and the thinking spinner) only
when stderr is a character device. Piped output stays plain.

## Exit codes

- `0` — clean exit (one-shot completed, REPL exited normally)
- `1` — fatal error before or during the run (missing model, HTTP
  failure bubbled up from `Run`, invalid session pick, etc.)
