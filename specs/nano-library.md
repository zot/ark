# Nano: library API

The library lives in `nano.go` as part of `package ark`. Other Go
programs import it via `github.com/zot/ark` and use the `Nano` struct.

## The Nano struct

Everything configurable hangs off one struct so a caller can populate
the fields they care about and leave the rest at zero values.

```go
type Nano struct {
    Model          string        // required, e.g. "qwen2.5-coder"
    BaseURL        string        // default "http://localhost:11434"
    MaxSteps       int           // default 200
    MaxOutputBytes int           // default 12000; clips each tool result
    ApproveAll     bool          // skip approval prompts
    KeepHistory    bool          // persist message log to SessionsPath
    SessionsPath   string        // default ~/.ark/nano-sessions.json
    TTY            bool          // enables ANSI color and spinner on Stderr
    Stdin          io.Reader     // approval input; defaults to os.Stdin
    Stdout         io.Writer     // final answer; defaults to os.Stdout
    Stderr         io.Writer     // prompts and spinner; defaults to os.Stderr
    Cwd            string        // working directory; defaults to os.Getwd
    HTTPClient     *http.Client  // injectable for tests; default &http.Client{}
}
```

The zero value is not usable: `Model` must be set or `Run`/`REPL`
will return an error.

## Public methods

```go
// Run executes one user prompt against Ollama, looping on tool calls
// until the model produces a final text answer or MaxSteps is reached.
// If history is nil, a fresh history is started with the system prompt.
func (n *Nano) Run(prompt string, history []Message) (string, []Message, error)

// REPL runs an interactive multi-turn session. readLine is the CLI's
// hook for plugging in chzyer/readline; passing nil falls back to a
// plain bufio reader with no editing or history.
func (n *Nano) REPL(history []Message, label string, readLine ReadLineFunc) error
```

The library does not depend on `chzyer/readline`. The CLI does, and
it passes a `ReadLineFunc` into `REPL`. Library users who want plain
stdin can pass nil.

## Session helpers (package-level)

```go
// LoadNanoSessions reads the sessions file at path. A missing file
// returns (nil, nil) rather than an error.
func LoadNanoSessions(path string) ([]NanoSession, error)

// SaveNanoSession appends s to the sessions file at path. If a
// session with the same label and cwd already exists it is replaced.
// The file is capped at the most recent 50 sessions.
func SaveNanoSession(path string, s NanoSession) error

// NanoSessionsInCwd returns sessions whose Cwd field matches,
// oldest first.
func NanoSessionsInCwd(path, cwd string) ([]NanoSession, error)
```

These are package-level functions, not methods, because they operate
on the file format and don't need a `Nano` instance. The CLI uses
them directly to implement `-c` and `-s` without going through
`Nano`.

## Naming note: NanoSession

Ark already has a `Session` type for closure-actor sessions
(`session.go`). To avoid colliding inside `package ark`, the chat
session type is named `NanoSession`, and the three session helpers
above carry the `Nano` prefix as well. The Nano struct itself, its
methods, and the supporting types (`Message`, `ToolCall`, `ShellArgs`,
…) keep their upstream names — none of them collide.

## Types

```go
type Message struct {
    Role      string     `json:"role"`     // "system", "user", "assistant", "tool"
    Content   string     `json:"content"`
    ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type ToolCall struct {
    Function ToolFunction `json:"function"`
}

type ToolFunction struct {
    Name      string          `json:"name"`
    Arguments json.RawMessage `json:"arguments"`
}

type ShellArgs struct {
    Command     string            `json:"command"`
    Description string            `json:"description"`
    Cwd         string            `json:"cwd,omitempty"`
    Timeout     int               `json:"timeout,omitempty"`
    Env         map[string]string `json:"env,omitempty"`
}

type NanoSession struct {
    Label    string    `json:"label"`
    Cwd      string    `json:"cwd"`
    Ts       int64     `json:"ts"`
    Messages []Message `json:"messages"`
}

type ReadLineFunc func(prompt string) (string, error)
```

`Message` mirrors Ollama's chat message shape directly so the library
can forward what it received without re-marshalling.
