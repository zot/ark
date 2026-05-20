# Nano: overview

## What it is

Nano is the embedded shell-agent loop in ark, callable as `ark nano`. The
agent reads a user prompt, asks a language model what to do, executes
shell commands with the user's approval, feeds the output back to the
model, and loops until the model produces a final answer. Nano talks to
a local Ollama server.

Two faces in one ark binary: a Go library (the `Nano` struct and its
methods in `nano.go`, package `ark`) that callers can wire into their own
programs, and a thin CLI wrapper hung off the existing ark CLI dispatch
in `cmd/ark/main.go`. Either is usable on its own; the CLI is built into
the ark binary as a subcommand.

## Provenance

Nano is a Go port of nano.py (Parham Negahdar, MIT) by way of nano-go
(Bill Burdick, MIT). The full attribution chain and MIT license text
live in [`readme-nano.md`](../readme-nano.md). The architectural shape —
one shell tool, one loop, human-in-the-loop approval, locally persisted
session memory — is inherited from the upstream.

## Why a Go port, why embedded

The Python original is a single 199-line file with zero dependencies.
It's the right shape for read-the-whole-thing-over-lunch auditing but it
can't be embedded in other Go programs without subprocess gymnastics.
The Go port keeps the same loop and the same one-tool philosophy but
exposes everything through a struct so callers can wire it into their
own programs. Folding it into ark makes the loop available as a
subcommand alongside ark's other zettelkasten operations.

## Language and environment

- Language: Go (module `github.com/zot/ark`)
- Package: `ark` (the same package that owns the rest of the ark
  surface; nano.go is a sibling top-level file)
- Backend: Ollama `/api/chat` over HTTP, default base URL
  `http://localhost:11434`
- One non-stdlib dependency added to ark's go.mod by the integration:
  `github.com/chzyer/readline`, used by the REPL line editor only
- Operating systems: Linux, macOS, anywhere `sh -c` exists; not
  designed for Windows out of the box

## Inherited behavior from nano.py

Anything nano.py did that isn't called out as changed below, nano in
ark still does. In particular: the single `execute_shell` tool, the
5–10-word command description requirement, the 200-step loop cap, the
12 KB output cap, the y/a/n approval flow with optional auto-approve,
the REPL with `:q` and `:reset`, and the system prompt that lists the
project's documentation files and discovered skill files.

## What changed in the port (vs. nano.py)

- **Backend.** Ollama, not OpenAI. No API key. No `previous_response_id`.
- **Sessions.** Ollama is stateless, so to keep `-c` / `-s` working,
  nano persists the full message log per session. The CLI defaults to
  this behavior; the library defaults to off.
- **Object shape.** All options are fields on a `Nano` struct;
  library calls are methods on it.
- **No default model.** A model name must be supplied — either as a
  field on the `Nano` struct or via `-m` / `OLLAMA_MODEL` for the CLI.

## What changed in the integration (vs. standalone nano-go)

- **Package.** `package nano` → `package ark`. Library import path
  becomes `github.com/zot/ark`; callers use `ark.Nano`,
  `ark.NanoSession`, etc.
- **`Session` rename.** Ark already has a `Session` type (closure-actor
  session in `session.go`). Nano's `Session` is renamed `NanoSession`,
  and the package-level helpers become `LoadNanoSessions`,
  `SaveNanoSession`, `NanoSessionsInCwd`.
- **CLI invocation.** `nano-go [flags] [prompt]` → `ark nano [flags]
  [prompt]`.
- **Sessions file path.** Default moves from `~/.nano-go_sessions.json`
  to `~/.ark/nano-sessions.json` so the embedded copy keeps its state
  inside the ark home directory and does not collide with a leftover
  standalone `nano-go` install on the same machine.
