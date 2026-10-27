# Managed PTY Session — ark hosts a Claude Code session in a pty

Language: Go. Environment: `ark serve` (the long-running server process)
holds the pty master through a platform-neutral backend seam: `creack/pty` on
Unix, a ConPTY (pseudoconsole) on Windows, both pure Go so the CGO-free build is
preserved. The CLI `attach` client rides the existing unix-socket API.

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

## Platform backend — Unix pty and Windows ConPTY

The host runs on Windows as well as Unix, under ark's parity commitment: a
Windows install hosts its own Luhmann session, so the pty host is not Unix-only.
The mechanism that differs by OS is confined behind a backend seam, so the host,
the fan-out, the resize logic, and the teardown are all platform-neutral and the
platform code lives in one place.

The seam offers three operations to the host: spawn a child attached to a
terminal, resize that terminal, and read and write its byte stream. On Unix the
backend is `creack/pty` driving a real pty, and the child is stopped with process
signals (SIGTERM then SIGKILL). On Windows the backend is a ConPTY pseudoconsole
(`CreatePseudoConsole` / `ResizePseudoConsole`), and the child is stopped with
`TerminateProcess`. Both are pure Go, so the CGO-free build holds on both targets.

One behavior does not cross: the graceful flush window `stop` gives the child.
It comes from a Unix SIGTERM the child traps; the Windows pseudoconsole has no
equivalent, so a Windows stop terminates the child directly. This is the same
platform asymmetry the server's own shutdown accepts, and it is stated here
rather than papered over.

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
session is its own session, not the server's grandchild. The fork also **sets**
one marker of its own, `ARK_MANAGED_PTY=1`, so the hosted session can tell it was
started by `ark luhmann launch` — as opposed to a bare `/luhmann` invoked in some
other session — and surface the attach/detach hint to the user accordingly (the
`/luhmann` skill reads it; that greeting is a Claude Code asset, not this spec's).

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

`<cwd-encoded>` is Claude Code's own encoding of the working directory, and
ark must produce it exactly, or the check looks for a folder that never
exists. As measured in Claude Code 2.1.311: every character outside `A-Z`,
`a-z` and `0-9` becomes `-`, one for one, with no collapsing, so
`/home/deck/.ark/luhmann` is `-home-deck--ark-luhmann` and the Windows
`C:\Users\me\work` is `C--Users-me-work`. An encoding longer than 200
characters is cut to 200 and followed by `-` and the base-36 absolute value of
a 32-bit hash of the original path (`h = h*31 + c` over its UTF-16 code units).

The scan works because the current renderer emits the dialog in reading order,
the option number ahead of its "Yes … trust" label, once the ANSI escapes are
stripped. It is a heuristic and it fails safe: if the stream stops matching (say
a renderer that backtracks with cursor addressing to place text out of stream
order), the host sends no keystroke, and the launch fails visibly at the seat
claim, the same symptom as any other bootstrap that did not take. Recovering
from a backtracking renderer would mean maintaining a virtual screen — tracking
the cursor into a grid and reading the grid — which is out of scope until then.

**The accepter belongs to one launch, not to the host.** Two output readers can
briefly overlap: a teardown stops publishing the session immediately but leaves
the child's stream draining for a grace window, so a launch started in that
window runs a second reader alongside the first. Accepter state kept on the host
is therefore shared between them — two readers appending to one scan buffer, and
the departing one able to answer the *arriving* launch's dialog out of the dying
session's output. Giving each launch its own accepter removes the sharing
instead of guarding it, so the hazard cannot come back through a later caller
who does not know the rule.

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
  detaches, or resizes, then pushed to the child through the backend — a real
`SIGWINCH` on Unix, `ResizePseudoConsole` on Windows. A disconnect
  is not special-cased: it re-runs the same minimum, so the pty *grows* when the
  smallest client leaves, just as it shrinks when a smaller client joins. With a
  single client this degenerates to that client's size.
- **Attach/detach independence.** Clients attach and detach freely; zero
  attached clients leaves the session running (detached), and one client's
  detach never disturbs another.

## The output path: ownership instead of synchronization

The contract above says *what* the multiplexer guarantees. How it gets there is
a second commitment, because the mechanism is what the bugs were made of. Two
arrived on one afternoon: a scan buffer mutated outside the actor and justified
by a comment asserting sole ownership, and a synchronous actor round-trip paid
per chunk whether or not anyone was attached. Both taught the same lesson. An
invariant that lives in an argument decays as unrelated changes move the pieces
it rested on, while an invariant that lives in ownership does not, because you
cannot forget to take a baton that is identical to being the goroutine that
exists.

So the output path is built out of ownership hand-offs, and carries no lock, no
mutex, and no atomic anywhere along it. Two named patterns do the work, and the
names are the point rather than decoration: **baton** is an object with exactly
one holder by construction, and **sewing** is serialized control passed between
actors as posted continuations instead of calls. They are orthogonal — control
can travel with nothing, and an object can move with no control passing — and
here they compose, so it is worth keeping straight which one is carrying the
weight in each direction. `design/design.md` develops both under "Actor-Resident
I/O".

**Reading.** One goroutine owns the read buffer and blocks on the master. When
the read returns it hands that buffer to the actor and terminates. The actor
copies the bytes out to every attached connection, and only once it is finished
with the buffer does it spawn a fresh goroutine holding the same buffer, which
blocks on the next read. No two goroutines touch the buffer at any instant, and
the buffer is one allocation for the life of the session.

The read must not happen on the actor. It blocks indefinitely while the child is
quiet, and an idle orchestrator can go minutes without writing a byte, so a read
inside an actor closure would hold the actor for that whole time and stall every
attach, detach, resize, status and stop — worst exactly when the system looks
idlest. The goroutine does the waiting; the actor does the state changes.

The hand-off is a **post, not a call**: the reader never waits for the actor to
finish. Waiting is what the old path actually cost. A synchronous round-trip per
chunk tied the reader's rate to the actor's queue depth, and the workaround was a
lock-free mirror of the client-set size letting a detached session skip the trip
entirely. An asynchronous hand-off makes the mirror unnecessary, so the actor
goes back to being the only place the client set is known.

**Writing.** Each connection owns two buffers and a flag, all held by the actor.
Output is appended into the connection's pending buffer. If a write is already in
flight nothing more happens, since the writer in flight will pick the new bytes
up; otherwise the buffers are swapped, the connection is marked as writing, and a
write goroutine is spawned owning the filled buffer.

That goroutine writes the whole buffer to its transport, blocking, off the actor.
It then empties the buffer and posts a continuation back to the actor **carrying
the buffer with it**. If output accumulated meanwhile, the continuation swaps and
spawns again; if not, it clears the writing flag. The flag is set, cleared and
read only inside the actor, so it needs no atomic.

Three properties follow, and they are the reason for the shape:

- **No wakeup is lost.** Should the writer finish and more data arrive before its
  continuation runs, the append sees the connection already writing and does
  nothing, and the continuation then finds pending non-empty and spawns again.
  Should the continuation run first, it clears the flag and the next append
  spawns. The actor serializes both orders, so there is no interleaving to reason
  about.
- **At most one writer exists per connection.** The flag goes false→true in one
  place, inside the actor, and the only thing that can spawn a second writer is a
  continuation the first one posted after its write had completed.
- **Order is preserved.** Appends happen in read order, buffers leave in swap
  order, and one writer runs at a time. A terminal stream that reorders is
  garbage, so this is load-bearing rather than incidental.

**The bound is a byte cap on pending, and it belongs to the host.** R3118's
contract does not change: a connection that cannot keep up is dropped rather than
allowed to stall the fan-out. Where the bound lives does change. Each transport
used to carry its own bounded channel of undelivered chunks, its own drain
goroutine and its own flush timer, so the drop policy was implemented once per
transport and free to drift between them. The host now caps the pending buffer in
bytes on the append path and drops a connection that exceeds it. One number
replaces the per-transport machinery and states the same policy more directly.

Bytes are also a more honest bound than a chunk count. A count of undelivered
chunks bounds memory only when chunks have a bounded size, and a pty read ranges
from a few bytes to the full read size, so the old bound described a window
anywhere between kilobytes and megabytes depending on what the child happened to
be emitting.

**A transport writes; it does not buffer.** With the buffering in the host, a
client transport reduces to a blocking write of a byte slice plus a close. It
runs on the host's write goroutine, which makes blocking correct there rather
than merely tolerated: a stalled browser tab blocks its own writer, its pending
buffer grows, and it is dropped at the cap, while every other connection and the
child carry on untouched. Dropping closes the transport, and that is what
unblocks the stuck write.

Batching becomes adaptive as a side effect. Output accumulated while a write is
in flight leaves as one larger write when that write completes, so a fast client
gets small frequent writes and a slow one gets large coalesced ones, each tuned
to itself rather than to a constant. The fixed flush timer the socket transport
ran is deleted with the buffered writer whose latency it existed to bound.

**A continuation tolerates a connection that is already gone.** One arriving
after its connection was dropped, detached, or torn down must spawn nothing, or
every teardown racing an in-flight write leaks a goroutine.

**A read chain outliving its session keeps draining and publishes nothing.** A
teardown clears the host's master at once but hands the real one to a graceful
shutdown holding it open for a grace window, so reads keep returning the child's
flush-on-exit output after the session has stopped being the hosted one. That
output must still be drained, since an undrained pty blocks the child, and it
must not reach the clients of whatever launch came next. The chain reads and
discards until the master closes.

## The child command

What ark runs in the pty is configuration, not a literal. There are two knobs:
one for the everyday case, one for the escape hatch.

**The model is a launch parameter that falls back to config.** `launch` takes an
optional model. With none given it reads `[luhmann] model` from ark.toml; with
that unset it uses a compiled-in default. The three levels exist because the
three callers differ — a UI dropdown picks per launch, a user who always wants
the same model says so once in ark.toml, and a fresh install has to work with
neither. A parameter never writes config, so launching once at a different model
leaves the default alone. That is what makes a dropdown safe to experiment with.

ark pins a model rather than letting `claude` choose one, because the
orchestrator is a long-lived paid session and the choice is a spend decision. The
compiled-in default is conservative for the same reason.

**`[luhmann] command` replaces the whole argv.** When set it is run verbatim and
the model chain does not apply, since an override supplies its own flags. Its
purpose is to put a throwaway stand-in in the pty when the thing being exercised
is the pty machinery rather than the orchestrator — the reader, the fan-out, the
client multiplexing. A stand-in never claims the Luhmann seat, so the launch
fails at the confirmation gate once the seat timeout expires and the child is
torn down. The pty runs normally for that window, which is the point of the
substitution.

**An explicit model together with a `command` override is an error**, not a
silent preference. The two cannot both be honored, and discarding a parameter the
user typed is exactly the quiet wrong answer this project keeps paying for
elsewhere.

None of this weakens the spend-consent invariant above. Configuration changes
*what* a launch starts, never *when* one happens, and every launch still begins
with an explicit human act. A stand-in child is cheaper than the session it
replaces, never more expensive.

## Teardown

A hosted session goes down two ways. `ark luhmann stop` tears it down, or the
child exits on its own because the human quit it or the process died. Both take
the session's in-session pool secretaries with them, since those subagents live
inside the child process and have no existence apart from it.

So the teardown obligations belong to the going-down itself and run on **both**
paths: release the seat lease, and record an exit for every pool secretary the
session was running. Doing this on the `stop` verb alone is what leaves a
self-exit's secretaries behind as ghosts. A ghost is not merely untidy
bookkeeping; it holds a slot against `class.<NAME>.pool_max` and the supervisor
keeps routing work to a tube nobody is listening on, so the work is accepted and
then silently never done.

The exit record names which of the two happened. A teardown ark performed and
one the child chose are different events, and a later reader asking whether the
hosted session survived should be able to answer from the record rather than
infer it. The secretaries' records are often the only trace available: the
session's own exit record is written by the session, over the CLI, so a child
that dies abruptly writes nothing about itself.

## CLI

```
ark luhmann launch [--bootstrap INPUT] [--model MODEL]
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

Forks the pty with cwd `~/.ark/luhmann`, starts the child command (see The child
command above — `--model` names the model for this launch, falling back to
`[luhmann] model` and then the compiled-in default), and
sends the **bootstrap first input** — the string that loads the orchestrator
skill (default `load /luhmann`; overridable with `--bootstrap`). No project
`CLAUDE.md` is required: Claude Code acts only on its first input anyway, so
ark sends the bootstrap as that input rather than relying on a CLAUDE.md
auto-load. This verb is the **spend consent gate** — the only CLI door that
starts a paid session. Errors if a session is already hosted (one at a time).
Server required.

### `attach`

A raw-mode client over the unix socket: stdin → pty, pty → stdout, with a
tmux-style detach escape and resize propagation (`SIGWINCH` on Unix, a
window-rect poll on Windows, which has no `SIGWINCH`). Detaching leaves
the session running. On exit it restores the terminal **fully** — the termios
line discipline *and* a sanitizing sequence undoing the DEC private modes the
child set through its output (cursor visibility, bracketed paste, mouse
reporting), which `term.Restore` alone does not, so the shell returns clean
without a manual `reset`. Multiple `attach` clients may be connected at once
(fan-out). Server required.

### `status`

The single source of truth for whether a session is hosted: pty alive or not,
and — folding in [luhmann.md](luhmann.md)'s supervisor state — the pool
secretary roster count. `--json` for machine reads, human text otherwise. This
is what a UI lamp and the CLI test both read. Server required.

### `stop`

A **graceful teardown** of the hosted session, not a bare kill: the child is
asked to exit and given time to flush its transcript before it is killed
outright. On Windows there is no graceful-signal equivalent, so the child is
terminated directly (see Platform backend). The seat-lease release and the pool secretaries' exit records are not
this verb's own doing — they are the teardown obligations above, discharged
identically when the child exits on its own. What `stop` contributes is the
reason carried into those records, which says the teardown was ark's. (UI
label: "End session".) Server required.

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

## Launching from the UI, and how a launch announces itself

The ark app's Luhmann view has a Launch button, shown only when the terminal
reports `asleep` and gated behind a dialog confirming a paid session. It needs a
door into the launch, and that door is `sys.luhmannLaunch()` — an ark-only Lua
binding ([lua-api.md](lua-api.md)). The binding carries the **model** as well as
the bootstrap, so a model chooser in the view reaches the same parameter the CLI
`--model` flag does and needs no Go work of its own when it is built.

**The binding cannot block.** `launch` waits for the seat claim, and that wait is
generous by design because it covers Claude Code's startup plus a skill load. A
Lua binding that waited with it would hold the calling session's executor for
minutes, freezing the UI it was trying to serve. So the binding **starts** the
launch and returns at once, reporting only whether the launch was *accepted* —
that is, whether the host was free to take it. It does not report whether the
session came up, because it cannot know yet.

**The outcome arrives on the integration plane instead.** Every launch that
reaches its confirmation point publishes the result on a `tmp://` document,
success or failure alike, and any subscriber sees it — the UI, a test, a
monitor, symmetric subscribers all ([tmp-subscription.md](tmp-subscription.md)).
A failure publishes too: a launch that dies at the seat-claim gate would
otherwise leave a user who pressed the button with nothing at all, for as long
as the timeout runs.

**Publishing at the confirmation point, rather than in the binding, is what
makes the CLI case work for free.** A `ark luhmann launch` typed in a shell
passes through the same confirmation, so it publishes identically, so a UI with
the terminal on screen learns about it. That case is not a nicety: the terminal
element stops probing once it has announced `asleep`, so without the
announcement it would sit there asleep beside a session that is in fact running,
and stay that way until the user navigated away and back.

**None of this weakens the spend-consent invariant.** ark still never starts a
paid session on its own. The binding is reached only from a deliberate user
action behind the confirmation dialog; it is a second door for a human to open,
not a mechanism that opens by itself.

## Indexing the orchestrator's session

The orchestrator's Claude Code project directory
(`~/.claude/projects/<cwd-encoded>` for cwd `~/.ark/luhmann`) is added as an
**in-memory `chat-jsonl` source**, so the session's own conversation log is
indexed — searchable through recall, tappable by the watcher — with no user
configuration. This is the same principle as the hardcoded `~/.ark` source
(`EnsureArkSource`): the project directory exists **only because ark forked the
pty** with that cwd, so it is ark-managed content, not the user's, and ark
indexes it implicitly rather than asking. The global `*.jsonl` → `chat-jsonl`
strategy classifies it, and the standard `~/.claude/projects/**` search
exclusion keeps its chunks out of ordinary search results — present for recall,
absent from search noise. The whole project directory is indexed, not one
session file, so the orchestrator's memory spans launches, which the larger
orchestrator roles depend on.

Note this is distinct from how `ark luhmann send` (see [luhmann.md](luhmann.md))
**locates** the live session log: `send` globs `~/.claude/projects/*/<uuid>.jsonl`
directly on the filesystem, because it must read the log the instant a command is
enqueued — before the indexer, on its own clock, has picked the file up. Indexing
serves search and recall; the direct glob serves the synchronous read.

## Screen repaint

ark holds no virtual screen — the pty is a raw byte stream and ark never models
what is on it (the machine-opaque invariant above). A client that attaches
mid-session therefore receives only *subsequent* output; the full-screen paint
the child already emitted is gone, so a freshly attached — or re-attached —
client sees a blank or stale screen until the child next repaints on its own.

The host exposes a **forced repaint**: it toggles the pty size by one row —
shrink, hold a short beat, then restore — to raise a real `SIGWINCH`, which the
child (an Ink TUI) repaints the whole screen on. The hold is essential: a
synchronous shrink-then-restore coalesces (signals are not queued), so the child
reads the restored, unchanged size and skips the redraw; only an *observed*
intermediate size triggers a repaint. A same-size `SIGWINCH` will not do either —
the kernel drops `TIOCSWINSZ` when the size is unchanged. A repaint is **client-requested**, carried on a `repaint` frame over the
same client→host channel as input and resize, so one mechanism serves every
transport (CLI now, browser later) uniformly:

- **On attach.** The client requests a repaint immediately after connecting, so it
  sees the current screen at once instead of waiting for the child's next paint.
- **On a cancelled detach.** The `attach` client's detach escape (`Ctrl-]`) paints a
  transient one-line prompt so the escape is *acknowledged* — pressing `Ctrl-]`
  alone otherwise gives no feedback, which is what makes detaching feel
  untrustworthy. `d` detaches; any other key cancels — the client discards it (a
  mode key, not input for the child) and requests a repaint to wipe the prompt
  (again the child does the redraw, since ark has no screen of its own). On an
  actual detach the client clears the prompt itself before exiting — an instant
  local erase, since a child repaint would race the imminent disconnect, so the
  help text never lingers on the abandoned frame. The rest of the rendered frame
  is **deliberately left on screen** (not cleared) — useful for scrolling back or
  copying on-screen content after detaching; a future "clear on detach" would
  destroy that and is intentionally not done.

## Browser transport

The browser reaches the hosted session over ark's **own** websocket at `GET
/luhmann/pty`, served on the UI HTTP server (the same origin and port the
Frictionless UI uses) whenever the UI runtime is running. This is a separate
wire from ui-engine's `WebSocketEndpoint`, which is a structured view-diff
channel — the wrong shape for a raw terminal stream, and routing pty bytes
through its per-session view executor would tie terminal latency to view
rendering. So ark runs its own `gorilla/websocket` upgrade on that path.

Two message kinds carry the two directions:

- **Binary messages are raw pty bytes.** A browser→host binary message is input,
  merged onto the child like any client's keystrokes; a host→browser binary
  message is a chunk of the child's output. The message boundary is the chunk
  boundary, so there is no framing to parse.
- **Text messages are JSON control frames.** `{"t":"resize","cols":C,"rows":R}`
  reports the client's terminal size for the smallest-wins minimum, and
  `{"t":"repaint"}` requests the forced repaint (the browser's equivalent of the
  CLI client's on-connect repaint, carried the same client-requested way the
  Screen repaint section describes). The **first** message must be a resize, so
  the host knows the client's size before it attaches; a connection that opens
  with anything else is closed.

The websocket client is one implementation of the transport-agnostic client
interface the CLI `attach` also implements. It plugs into the identical fan-out
(broadcast, serialized input merge, smallest-wins resize, attach/detach
independence) with the same bounded-buffer drop, so a stalled browser tab is
dropped rather than allowed to stall the session, and the same
survive-zero-clients behaviour. This spec covers the Go endpoint and its wire;
the xterm.js terminal that drives it is `specs/luhmann-terminal-element.md`.

`GET /luhmann/pty-status` — the hosted/session/secretary-count report the CLI
`status` verb reads — is also served on the UI HTTP server, the same handler
mirrored on both muxes (as the curation endpoints are, for the content view). A
browser cannot see why a websocket handshake failed: the 409 above reaches the
page as an unexplained 1006 close, so a browser client has no way to tell "no
session is hosted" from "ark is restarting" without asking. The mirror is what
it asks.

## What this spec deliberately does not require

- **The xterm.js browser *client*.** This spec owns the websocket endpoint
  (`GET /luhmann/pty` on the UI HTTP server, ark's own `gorilla/websocket`) and
  its wire; the browser-side terminal that connects to it is owned by
  `specs/luhmann-terminal-element.md`, and that element's *placement* in the
  Frictionless app is a later `/ui-thorough` slice. The transport-agnostic
  client interface the fan-out uses (see Architecture) is what lets that client
  plug in without reworking the multiplexer.
- **A second concurrent hosted session.** One pty-hosted session at a time; a
  session pool is not in scope.
- **Content-based idle detection.** A precise "the agent finished its turn and
  is waiting for input" signal — derived by classifying JSONL record *content*
  (`origin.kind`, `turn_duration`, pending tool calls, the RecallWatcher
  classification) — is deferred until a consumer needs it: ark programmatically
  feeding the session input, a precise idle/working `status`, or the UI
  "stopped-up" case. Phase-1 launch confirmation is content-free and needs none
  of it.
- **Lua bindings for the rest of the lifecycle.** `sys.luhmannLaunch` exists
  (R3325) because the Launch button needed a door and the launch is
  asynchronous; `attach`, `status`, and `stop` have none, and the UI reaches
  status over HTTP instead. Bindings for those are a downstream `/ui-thorough`
  slice, added when a view actually authors them. (This bullet said "no Lua API"
  until 2026-07-30, which had been false since R3325 landed and contradicted
  Launching from the UI above.)
