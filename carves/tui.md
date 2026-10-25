# A TUI runner: Playwright for the terminal, scripted in Lua

**Problem.** Ark has a Playwright for its browser surfaces and nothing like it for
the terminal. A terminal program can be checked today only by hand, or by a
throwaway probe that reads raw bytes. Two pieces of upcoming work need more than
that: the ark TUI ([ark-tui.md](ark-tui.md), a Go+Charm client), and the Emacs
work (ark's editing support driven inside `emacs -nw`). Both are screens, not byte
streams, and checking one means launching it in a pty, typing at it, waiting for
something to appear, and reading what is on the screen.

So the ask (Bill, 2026-10-25): a runner that drives a terminal program the way
Playwright drives a browser, with its scripts written in Lua.

Opened 2026-10-25.

## Status

Nothing is built. The substrate exists (below); the first fork, the screen model,
is open.

- [ ] **Item 1 — the screen model.** **OPEN (not queued.)** Turn the pty's byte
      stream into the grid a person sees. DECIDED: a Go emulator, checked
      against xterm.js. See Item 1.
- [ ] **Item 2 — the runner core.** **OPEN (not queued.)** Launch, answer the
      startup queries, send input, wait, resize, snapshot. `/mini-spec`. See
      Item 2.
- [ ] **Item 3 — the Lua surface.** **OPEN (not queued.)** The script
      vocabulary. See Item 3.
- [ ] **Item 4 — the CLI verb and its output.** **OPEN (not queued.)** See
      Item 4.
- [ ] **Item 5 — first consumers.** **OPEN (not queued.)** The ark TUI and
      Emacs. See Item 5.

## What ark already has

Bill's observation that ark has the basic tools is right, and they are worth
naming, because the runner is mostly assembly:

- **A pty that works on both platforms.** `pty_backend.go` with its unix and
  Windows (ConPTY) halves launches a child in a pty and carries input, output and
  resize. `ptyhost.go` builds the managed Luhmann session on top of it; the runner
  wants the backend, not the host, since the host is about seats and fan-out.
- **Answering a TUI's startup questions.** A TUI asks its terminal things at
  startup (device attributes, cursor position, colours) and some block until
  answered. `ptyprobe/` found this with headless `claude` and carries the list of
  queries and xterm-flavoured replies. A runner that does not answer them hangs
  exactly the way that probe was written to show.
- **Watching output and answering it.** `trustAccepter` in `ptyhost.go` watches a
  launch's early output for the "trust this folder" dialog and answers it. That
  is a one-step expect script already in production.
- **Lua.** `gopher-lua` is in `go.mod` and ark already registers Go functions
  into it (`specs/lua-api.md`).
- **Markdown out.** Every ark surface is agent-readable markdown by default, so a
  screen snapshot rendered as a fenced text grid is the native output shape, not
  a new one.

- **A terminal emulator, in the browser.** `luhmann-terminal/` is a custom
  element over `@xterm/xterm` v6 (built by the Makefile, R3160), and the
  browser's Luhmann view renders the hosted session through it, websocket-fed by
  `pty_browser.go`. xterm.js is a complete parser and screen buffer. (A first
  draft of this carve said ark had no emulator; it searched `go.mod` only.)
- **A detach key in the CLI attach.** `cmd/ark/luhmann_attach.go` reads raw
  stdin and intercepts `Ctrl-]` then `d` (R3123). On that path the emulator is the
  user's own terminal: ark passes bytes through.

What the runner lacks is an emulator *it* can read: its Lua lives in Go, and Go
cannot run xterm.js.

## Item 1

A screen model: feed it the child's output, ask it for the grid of characters
(and, later, attributes and cursor position). Every wait-for-text and every
snapshot reads this, so it is the foundation.

The fork, not decided, is where the emulator runs:

- **(a) In the browser, reusing `<luhmann-terminal>`.** Run the target under
  ark's pty transport, which already feeds that element over a websocket, and read
  xterm's buffer (`term.buffer.active`) through the page. Same parser as the
  Luhmann view, so the runner and the view cannot disagree, and no new emulator.
  But every run needs a browser, headless at least, and Lua reaching the page goes
  through the Frictionless bridge, which mixes the UI workflow into a Go tool.
- **(b) `@xterm/headless`.** xterm.js publishes its parser and buffer without the
  DOM, same version family. It needs a JS runtime: Node beside ark, or an
  embedded engine in Go, either of which breaks the single-binary rule.
- **(c) A Go emulator, a dependency or our own.** One binary, no browser. A
  second parser that can disagree with what the Luhmann view shows, and real work
  if written: Bubbletea and Emacs exercise a wide swath of escape codes. For a
  dependency, check the licence first (ark had to replace a GPL PDF library).

DECIDED (Bill, 2026-10-25): **(c), with xterm.js as the oracle.** The candidate
is `github.com/charmbracelet/x/vt`, Charm's VT emulator package: MIT (read from
its LICENSE, as is Bubbletea's, 2026-10-25), and young (v0.1.0, tagged
2026-10-12), which is exactly the case the oracle is for. Bubbletea itself is a
framework for writing TUIs, not an emulator of other programs' output, so its
licence settles the ark TUI's toolkit ([ark-tui.md](ark-tui.md)) rather than this
item; the two come from the same house, which helps when the TUI is the first
thing the runner drives.

The corpus test is part of the decision rather than an afterthought: record the
raw bytes of real sessions (ptyspy-style), replay them through both emulators,
and compare the grids. The xterm.js side runs only at test time, as
`@xterm/headless` under the Node that already builds `luhmann-terminal/`, so
nothing of it ships in the binary and the single-binary rule holds. A
disagreement is a finding about one emulator or the other, and the recorded
session is its reproduction.

## Item 2

The runner core in Go, over `pty_backend`: launch a command at a size, answer the
startup queries (from `ptyprobe`'s list), send keys and text, wait for a
condition (text or a pattern on screen, or the screen going quiet), resize, take
a snapshot, and tear down cleanly. It owns the child, so it is a natural closure
actor, as the pty host is.

Open: whether it runs in-process from the CLI (self-contained, no server) or
inside `ark serve`. In-process is the default reading, since a test runner should
not need a server to exist; the server only matters if a script wants ark itself
running, which is a separate process the script can start.

## Item 3

The script vocabulary, in Lua: something like `spawn`, `type`, `press`,
`wait_for`, `screen`, `expect`, `resize`. Playwright's lesson worth copying is
auto-waiting: an assertion about the screen retries until a timeout instead of
racing the program. Its other lesson, the locator, maps to "a region or a line
matching a pattern" rather than a DOM node.

Open: which Lua state runs it (a fresh gopher-lua state per script is the
simplest), and how much of `lua-api.md`'s vocabulary is shared rather than
parallel.

## Item 4

A verb (shape to settle: `ark tui run SCRIPT.lua`) whose output is baby food: per
step, what was sent, what was waited for, and the screen when it mattered, as
fenced text grids, with a failing step showing the last screen and what it was
waiting for. That makes the runner usable by an agent exactly as Playwright's
snapshots are, which is half its point.

## Item 5

The first consumers, which are also the acceptance test: a smoke script for the
ark TUI's first screen once ark-tui Item 1 has one, and an Emacs script that
opens a file through ark's Emacs support and checks what the buffer shows. Neither
consumer belongs to this carve; the scripts do.

## Overlaps

- **[ark-tui.md](ark-tui.md)** owns the TUI. This carve owns the runner that tests
  it, and the TUI's screens are its first real target.
- **The Emacs work** (tmp:// editing in Emacs, a private working note) is the
  other first target.
- **[windows-support.md](windows-support.md).** The runner inherits ConPTY through
  `pty_backend_windows.go`, so a script that passes on Linux is a candidate
  Windows check too, which the ssh-session junction quirks make valuable.
