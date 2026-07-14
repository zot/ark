# Managed PTY Session — ark hosts a Claude Code session in a pty

Language: Go. Environment: `ark serve` (the long-running server process)
holds the pty master via `creack/pty` (pure Go, so the CGO-free build is
preserved); the CLI `attach` client rides the existing unix-socket API.

`ark serve` can host a Claude Code session inside a pty: it holds the pty
**master**, runs `claude` as a child process, and fans the byte stream out to
attached clients. The first hosted session is the **Luhmann orchestrator**
(cwd `~/.ark/luhmann`); the capability is written for that case but is not
Luhmann-specific. The orchestrator session's persona, skill, and event
handling remain Claude Code assets (see [luhmann.md](luhmann.md)); this spec
covers only the Go-side pty host and its CLI.

See also: [luhmann.md](luhmann.md) — the supervisor log, drain tube, and
`[luhmann]` config for the session this pty hosts. Its *process* lifecycle is
**this** spec's, superseding luhmann.md's former "no process daemon"
disclaimer. Also [simple-recall.md](simple-recall.md) — the JSONL record
classification this spec reuses for the readiness signal — and
[sessions.md](sessions.md).

Design reference: [.scratch/LUHMANN-MANAGED-SESSION-20260709.md](../.scratch/LUHMANN-MANAGED-SESSION-20260709.md).

## Load-bearing constraints

Two invariants shape every verb below.

**ark never proactively starts `claude`.** Running `ark serve` is free
infrastructure; a live Claude session costs money. Only an explicit `ark
luhmann launch` (or an explicit UI action) starts one. There is no autostart
on `serve`, no remembered "was running" intent that relaunches after a bounce,
and no auto-wake when a paid session is merely *needed*. The manual launch is
the consent gate for spend. (Memory: `project_ark-never-proactive-claude`.)

**The TUI is machine-opaque; the JSONL is the only machine-readable channel.**
Claude Code is a React app that addresses the terminal with control
characters. ark can *write* to the pty (send input) but must never *read* the
session's state by scraping that output stream. The pty's output has one kind
of consumer: human eyes, through a terminal emulator (the `attach` client's
own terminal, or a browser xterm.js later). ark's own reading of what the
session did comes from the **JSONL chat log** (the RecallWatcher tap). "Send
and receive" are therefore two different wires: send is the pty write side,
receive is the JSONL read side.

## Architecture

`ark serve` holds the pty **master**. `claude` is a child of `ark serve`: it
dies on `ark stop`, and per the invariant it is **not** auto-relaunched, so a
restart leaves the session down until a human re-launches it.

The child launches as a **fresh, top-level `claude`**, indistinguishable from
one a human started in a clean shell. This matters because `ark serve` may
itself be running inside a Claude Code session (an agent started it from its
shell), and that parent environment carries session-identity markers:
`CLAUDECODE`, `CLAUDE_CODE_*`, and `AI_AGENT`. A child `claude` that inherits
them concludes it is a nested sub-session and will not complete an interactive
turn. The bootstrap never submits, so no JSONL is ever written and the launch
dies waiting at the second-record gate (see the confirmation protocol below).
So the fork strips those markers from the child's environment before starting
it; credentials (`ANTHROPIC*`) and unrelated config are left intact. The hosted
session is its own session, not the server's grandchild.

A directory Claude Code has never opened triggers a one-time **"trust this
folder"** dialog at startup, a numbered menu (`1. Yes, I trust this folder` /
`2. No, exit`). For a managed launch that is a hazard: the dialog intercepts the
bootstrap keystrokes, so the session comes up but never loads `/luhmann` and the
launch fails. So when the launch targets a directory new to Claude Code (its
`~/.claude/projects/<cwd-encoded>/` does not exist yet), the host watches the
child's early output and answers the dialog before sending the bootstrap. It
selects the trust-accepting option by the *number it reads from the stream*,
never by pressing Enter on whatever is highlighted. The default is "Yes" today,
but a future build could default to "No, exit", and a blind Enter would then
kill the session. Reading the number also answers a reordered menu correctly.

The scan works because the current renderer emits the dialog in reading order,
the option number ahead of its "Yes … trust" label, once the ANSI escapes are
stripped. It is a heuristic and it fails safe: if the stream stops matching (say
a renderer that backtracks with cursor addressing to place text out of stream
order), the host sends no keystroke, and the launch fails visibly at the seat
claim, the same symptom as any other bootstrap that did not take. Recovering
from a backtracking renderer would mean maintaining a virtual screen — tracking
the cursor into a grid and reading the grid — which is out of scope until then.

The master's byte stream fans out to a set of attached **clients**. A client
is transport-agnostic: anything that can receive the output stream, send input,
and report a terminal size. Phase 1 ships one client transport, the CLI
`attach` over the unix socket; the browser xterm.js client (a later slice) is
another implementation of the *same* client interface, not a rework of the
fan-out. **Any number of clients, in any mix, may be attached at once** —
several CLI `attach`es (a terminal tab, a second tab, an ssh session) as
readily as one CLI plus one browser. The multiplexer is built for concurrent
clients from the start (multiple clients per transport, and heterogeneous
transports together), so simultaneity is supported rather than a punted edge
case.

The fan-out contract, all of it multi-client by design:

- **Output broadcast.** The child's output goes to every attached client. A
  slow or stalled client (a frozen browser tab) must not stall the others or
  the session: each client has bounded output buffering, and a client that
  cannot keep up is dropped rather than allowed to block the fan-out.
- **Input merge.** Input from any client goes to the child, serialized so two
  clients' keystrokes cannot interleave mid-sequence. Two people typing at once
  is the user's business (tmux behaviour), not an error to arbitrate.
- **Resize, smallest-wins.** The pty size is the minimum of all attached
  clients' sizes (tmux behaviour), recomputed whenever a client attaches,
  detaches, or resizes, then pushed to the child via `SIGWINCH`. A disconnect
  is not special-cased: it re-runs the same minimum, so the pty *grows* when the
  smallest client leaves, just as it shrinks when a smaller client joins. With a
  single client this degenerates to that client's size.
- **Attach/detach independence.** Clients attach and detach freely; zero
  attached clients leaves the session running (detached), and one client's
  detach never disturbs another.

## CLI

```
ark luhmann launch [--bootstrap INPUT]
ark luhmann attach
ark luhmann status [--json]
ark luhmann stop
```

These four **pty-lifecycle** verbs live in the `ark luhmann` namespace
alongside the supervisor/drain verbs [luhmann.md](luhmann.md) owns
(`spawn-record`, `exit-record`, `inspect-exit`, `next`). The split is
deliberate: luhmann.md owns supervision and the drain tube; this spec owns the
process/terminal lifecycle. [cli-commands.md](cli-commands.md) is the unified
inventory.

### `launch`

Forks the pty with cwd `~/.ark/luhmann`, starts `claude` as the child, and
sends the **bootstrap first input** — the string that loads the orchestrator
skill (default `load /luhmann`; overridable with `--bootstrap`). No project
`CLAUDE.md` is required: Claude Code acts only on its first input anyway, so
ark sends the bootstrap as that input rather than relying on a CLAUDE.md
auto-load. This verb is the **spend consent gate** — the only CLI door that
starts a paid session. Errors if a session is already hosted (one at a time).
Server required.

### `attach`

A raw-mode client over the unix socket: stdin → pty, pty → stdout, with a
tmux-style detach escape and `SIGWINCH` (resize) propagation. Detaching leaves
the session running. Multiple `attach` clients may be connected at once
(fan-out). Server required.

### `status`

The single source of truth for whether a session is hosted: pty alive or not,
and — folding in [luhmann.md](luhmann.md)'s supervisor state — the pool
secretary roster count. `--json` for machine reads, human text otherwise. This
is what a UI lamp and the CLI test both read. Server required.

### `stop`

A **graceful teardown** of the hosted session, not a bare kill. Stopping the
pty takes the session's in-session subagents (its pool secretaries) down with
it, so `stop` also releases the seat lease and records the secretaries' exits,
leaving the monitoring log truthful instead of showing ghosts. (UI label: "End
session".) Server required.

## Launch confirmation — content-free

`launch` confirms the session came up without parsing any JSON record content,
using one event ark already owns — the seat claim — after clearing the way for
it:

1. **Clear a stale seat.** Before forking, unconditionally clear any prior seat
   claim (`ForceReleaseSeat`). A prior session that died without releasing the
   in-memory lease would otherwise block the new session's `--first` claim; the
   managed launch is the authoritative start, so it takes the seat.
2. **Send the bootstrap.** Write `load /luhmann` to the pty. Claude Code buffers
   input typed at any time, so ordering against startup is safe.
3. **Seat claim.** Wait for the launched session to claim the Luhmann seat via
   `ark luhmann next --first` — the authoritative confirmation that `/luhmann`
   loaded and attached, and the event that teaches ark the session's id (from the
   claim's `--session`). A timeout on the wait fails the launch.

Nothing here reads the *content* of a record: the sole confirmation is ark's own
seat lease (step 3).

## What this spec deliberately does not require

- **The browser transport *wiring*.** The websocket endpoint itself (`GET
  /luhmann/pty` on the UI HTTP server, ark's own `gorilla/websocket`) and the
  xterm.js client are a later slice. What phase 1 *does* commit is the
  transport-agnostic client interface the fan-out uses (see Architecture), so
  that slice plugs in a browser client without reworking the multiplexer.
- **A second concurrent hosted session.** One pty-hosted session at a time; a
  session pool is not in scope.
- **Content-based idle detection.** A precise "the agent finished its turn and
  is waiting for input" signal — derived by classifying JSONL record *content*
  (`origin.kind`, `turn_duration`, pending tool calls, the RecallWatcher
  classification) — is deferred until a consumer needs it: ark programmatically
  feeding the session input, a precise idle/working `status`, or the UI
  "stopped-up" case. Phase-1 launch confirmation is content-free and needs none
  of it.
- **A Lua API.** No Frictionless feature authors pty lifecycle yet; the UI
  desk-lamp reads `status` and calls these verbs, a downstream `/ui-thorough`
  slice.
