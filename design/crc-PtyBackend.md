# PtyBackend
**Requirements:** R3447, R3448, R3449, R3450

The platform-neutral seam between PtyHost and the operating system's terminal
primitive. It spawns the `claude` child attached to a terminal, carries the
child's byte stream, resizes the terminal, and stops the child, so the host, the
fan-out, the resize logic, and the teardown never name an OS (R3447, R3448). Two
build-tagged implementations satisfy it: a Unix pty via `creack/pty` and a
Windows ConPTY. Both are pure Go, so the CGO-free build holds on both targets
(R3449).

## Knows
- ptyMaster: the hosted terminal's master side, exposing **read, write, close,
  and resize** as distinct operations rather than a single OS file (R3448). A
  ConPTY splits the byte stream (two pipes) from the resize control (the
  pseudoconsole handle), so the host cannot treat the master as one `*os.File`;
  the interface is what lets `masterPtr` hold a backend value instead of a
  concrete file. On Unix it wraps the creack/pty master (read, write, and close
  pass through; resize is `pty.Setsize`); on Windows it wraps the ConPTY (read
  drains the output pipe, write fills the input pipe, resize is
  `ResizePseudoConsole`, close tears the pseudoconsole down) (R3449).
- ptyChild: the spawned child's process handle, exposing **wait**, a **graceful
  signal**, and a **kill** (R3450). On Unix it wraps `*exec.Cmd`; the graceful
  signal is SIGTERM and the kill is SIGKILL. On Windows it wraps the ConPTY's
  process; there is no graceful signal, and the kill is `TerminateProcess`.
- (no host state): the backend knows nothing of clients, buffers, or the session
  lifecycle; those stay in PtyHost. Its whole surface is the terminal primitive,
  which is what keeps the platform code in one place (R3448).

## Does
- startPty: build-tag-split constructor. Given the child argv and an initial
  size, spawn the child attached to a fresh terminal and return a `ptyMaster`
  and a `ptyChild` (R3448, R3449). Unix wraps `exec.Command` plus `creack/pty`;
  Windows drives ConPTY (`CreatePseudoConsole`), which on a pre-1809 build
  degrades to an explicit unsupported error rather than a silent failure.
- resize (on ptyMaster): apply a new size, a real `SIGWINCH` via `pty.Setsize`
  on Unix and `ResizePseudoConsole` on Windows, so the host's smallest-wins
  logic (R3120) reaches the child identically on both platforms (R3449).
- stop primitives (on ptyChild): a graceful signal that exists only on Unix
  (SIGTERM) and a kill that is SIGKILL on Unix and `TerminateProcess` on Windows.
  The host's gracefulShutdown owns the *timing* (signal, wait the grace window,
  then kill); the backend owns only the platform-correct primitives (R3450).

## Collaborators
- PtyHost: the sole consumer. It holds the returned `ptyMaster`/`ptyChild`, reads
  and writes through the master, resizes on the smallest-wins rule, and hands
  both to the graceful shutdown on teardown (R3448). Connected-to.
- creack/pty: the Unix terminal primitive (R3449). Part-of the Unix build.
- ConPTY (UserExistsError/conpty): the Windows pseudoconsole primitive; it spawns
  the child, carries its I/O, resizes it, and terminates it (R3449, R3450).
  Part-of the Windows build.

## Sequences
- seq-pty-launch.md
