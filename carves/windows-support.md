# Windows: the Unix primitives ark assumed were free

Ark grew up on Unix and leans on three operating-system primitives as if they
cost nothing: the process signal, the symbolic link, and the AF_UNIX socket.
Each is load-bearing somewhere. Signals stop the server and resize the attached
terminal. Symlinks wire skills, agents, and whole Frictionless apps into the
places the runtime looks. The socket carries the entire CLI-to-server API.
Windows honours the first two differently from Unix and the third only on recent
releases and with caveats, so once the release sweep began producing a
`windows-amd64.exe` the binary was born broken in exactly the spots those
primitives touch.

The coherent question here is not "fix bug X." It is which Unix assumptions ark
can keep making behind a build tag and which need a portable seam through the
middle of a feature. That is why the pieces belong together: the answer to
`ark stop` (reach the server over its own socket instead of a signal) is the
same shape as the answer to install (do the work without a symlink), and both
are cheaper to decide once than five times.

Scope note: this carve is about the **shipped release binary** on end-user
Windows. The developer-only rigs (`ptyprobe/`, `ptyspy/`) and the dev-box
inner loop are out of scope except where they overlap a shipped path.

**Deployment model: single-machine, single-OS, full feature parity.** ark is a
self-contained install: one machine runs the server and the CLI clients that
talk to it, the shape it already has on the Steam Deck. The user base splits
into all-Linux installs and all-Windows installs, neither crossing the OS
boundary, so Windows is a peer target to Linux, not a port hung off it. The
commitment is parity: every ark capability runs entirely on Windows and entirely
on Linux, Luhmann and its managed pty included, with nothing Unix-only by
design. That rules out the tempting escape hatch of narrowing a feature to Unix
and no-opping it on Windows; a no-op is a compile stopgap, never a finished
part. It is also why the server-side parts are in scope at all rather than being
someone else's Linux problem: the unix-socket *listener* (Part 5), the file
watcher (Part 8), embedding provisioning (Part 6), and the server's own shutdown
(Part 2) all have to run on Windows, because on a Windows install the server is
on Windows.

A corollary worth stating plainly: parity flattens priority. Every part must
ship for "ark runs on Windows" to be true, so none is optional or low-priority
as a goal. The only ranking this carve admits is implementation *order*, a
question of what comes first because something else depends on it, not of
whether a part gets done. Dependency sets that order more than taste does: the
socket transport (Part 5) is the gate, since nothing else can even be exercised
until it is verified. Read the per-part state markers (OPEN, NOT VERIFIED,
UNKNOWN) as "where the work stands," never as "whether the work happens."

The cross-OS alternative (a Windows CLI dialing a Linux-hosted server) is set
aside because no user runs that way. It also cannot use the transport ark has:
`~/.ark/ark.sock` is an AF_UNIX path local to one machine, so reaching a Linux
server from Windows would mean TCP and a reworked trust model (Part 5).

The remote-access need that alternative seems to serve is already met, on both
platforms, by SSH: log in to the box ark runs on and use it there. The CLI and
server stay co-located, the socket stays local, and SSH owns the network hop and
its own mature trust model, and forwards the terminal (including window-size
changes, so resize never arises on that path). This is a reason ark should not
grow a cross-machine transport, not a reason to relax the native parity work: an
all-Windows user has no SSH hop and still needs the local socket (Part 5), the
ConPTY host (Part 4), and Windows resize (Part 1). The one soft spot is
direction: SSH out of Windows uses the solid built-in client, while SSH into
Windows (hosting a remotely-reached ark on a Windows box) leans on OpenSSH
server, a less-trodden path there.

## Status

What is still open, in **implementation order** (testability first: what unblocks
fast, trustworthy verification), so the part numbers run out of sequence; detail
is keyed by bare number in the sections below, which stay in numeric order. The
checkbox is authoritative where the three markings disagree. Verification-only
and unknown items carry the negative marker rather than a false green.

- [x] **Part 5 — the AF_UNIX transport under Windows.** **VERIFIED WORKING,
      2026-09-20** (dockur Windows VM, `ark.exe` from 09-19). `ark.exe serve`
      bound `ark.sock`, a second `ui.sock`, and HTTP `:57801`; clients dialed and
      round-tripped (`luhmann status` → "asleep", `status` → "server: running");
      and it re-bound cleanly over the stale `ark.sock` left on disk. `sun_path`
      was 26 bytes, far under the ceiling. The gate is open — the sequence below
      is unblocked.
- [x] **Test part 5 on Windows** — 2026-09-20 serve run: listen + dial
      round-trip verified on the dockur VM.
- [x] **Part 2 — `ark stop` speaks only Unix signals.** **Was the do-second item: the
      testing-rate multiplier.** A live server, `ps` shows it, stop reports "not
      running." The fallback is a manual kill, and kill-then-restart is how the
      old binary ends up still serving while you believe you restarted, so a
      broken stop makes every Windows test result untrustworthy, not merely slow.
      **CONFIRMED on Windows 2026-09-20**: `ark stop` printed `process 3792 not
      running: not supported by windows` while `tasklist` showed PID 3792 alive;
      `taskkill /F` was needed. **DESIGN DECIDED 2026-09-22 (A):** graceful
      `POST /shutdown` on every platform, socket-dial liveness, timeout then
      Linux-kills / Windows-errors. **IMPLEMENTED 2026-09-22 (R3440–R3445)**,
      compiles Linux + Windows and stop tests pass; validate failures are
      pre-existing debt, not Part 2. Committed 8bfea51 (2026-09-22).
- [x] **Test part 2 on Windows** — 2026-09-22 live on the dockur VM: `ark stop`
      cleanly stopped a running server (reported "ark server stopped (pid 5220)",
      process gone, server logged `received POST /shutdown, shutting down`), where
      the 09-19 binary said "not running." Surfaced a pre-existing shutdown-race
      (process exits 1 on the closed-listener error, racing gracefulShutdown's
      exit 0; safe under bbolt). **Fixed by R3446** (atomic shutdown latch, committed 1e4643c); re-verified live
      2026-09-22: graceful stop now exits 0 with no `error: accept` line.
- [x] **Part 4 — the managed-pty path assumes a Unix terminal.** **VERIFIED on
      Windows 2026-09-23. Was the gate before Part 1's test.** Required under parity: Luhmann must host on Windows (ConPTY
      child, `ResizePseudoConsole`, `TerminateProcess`, VT-mode attach). Proving
      Part 1 end to end needs a Windows-hosted session to attach to, so this
      precedes it. **DESIGN DECIDED 2026-09-23 (Bill, after a live Windows spike):
      (b) — creack/pty on Unix + UserExistsError/conpty on Windows, behind a
      `ptyMaster`/`ptyChild` seam** (a type substitution for the `*os.File`
      master, not a three-syscall swap). The spike ruled out both unified
      libraries: go-pty drags in x/crypto+u-root, and phuslu/pty mis-binds its
      ConPTY child on Windows 26200. **IMPLEMENTED 2026-09-23 (R3447–R3450):**
      `pty_backend.go` (the seam), `pty_backend_unix.go` (creack),
      `pty_backend_windows.go` (conpty), and the `ptyhost.go` rework; compiles
      Linux + Windows, the 31 Unix PtyHost tests pass, and the Windows path is
      **VERIFIED live 2026-09-23** (dockur VM, build 26200): ConPTY spawn, hosted
      session, `stop` then TerminateProcess, and self-exit then finalize all
      confirmed. See Part 4.
- [x] **Test part 4 on Windows** — 2026-09-23 live on the dockur VM (build
      26200). A `[luhmann] command` stand-in launched a ConPTY session (`status`
      reported awake, the master installed); `luhmann stop` terminated it
      (TerminateProcess) and status returned to asleep; and a self-exiting
      stand-in (`ping -n 3`) self-cleared awake→asleep on its own, the watcher
      closing the master so the read chain EOF'd and finalize fired, with the
      launch reporting `claude exited during startup`.
- [ ] **Part 1 — SIGWINCH.** Compile break **FIXED** (committed 2026-09-20);
      **Windows resize detection still OPEN** (the Windows body is a no-op, a
      stopgap not a finish). The detector is unit-testable alone (resize the
      console, assert a send); its end-to-end test waits on Part 4.
- [ ] **Test part 1 on Windows**
- [ ] **Part 3 — `os.Symlink` needs privilege on Windows.** **OPEN. Independent
      track** once the binary runs. Install, linkapp, and bundle extraction all
      create symlinks.
- [ ] **Test part 3 on Windows**
- [ ] **Part 6 — llama.cpp embedding provisioning on Windows.** **UNKNOWN.
      Independent track.** Whether `ark embed install` has Windows artifacts at
      all is unexamined.
- [ ] **Part 8 — filesystem change events under Windows.** **NOT VERIFIED.
      Independent track.** The mechanism is portable (fsnotify); conduct under
      Windows' quirks, and recovery from dropped events, are unchecked.
- [ ] **Part 7 — the audit past the grep seed.** **OPEN. Runs alongside, not
      last.** Parts 1–6 came from one afternoon's greps, not a sweep.

Not a defect, recorded so nobody repairs it: the `/tmp/add`, `/tmp/append`, and
`/tmp/remove` strings in `cmd/ark/main.go` and `message_cli.go` are HTTP route
paths for `tmp://` documents. They are URLs, not filesystem paths, and mean
nothing to the Windows filesystem.

<a id="1"></a>

## Part 1

`cmd/ark/luhmann_attach.go` referenced `syscall.SIGWINCH`, which the Windows
`syscall` package does not define, so the whole `cmd/ark` package failed to
compile for `GOOS=windows` and the release sweep died at the Windows step. The
**compile break** is fixed with the standard build-tag split:
`notifyWinch(chan<- os.Signal)` lives in `cmd/ark/luhmann_winch_unix.go`
(`//go:build !windows`, the real `signal.Notify(ch, syscall.SIGWINCH)`) and
`cmd/ark/luhmann_winch_windows.go` (`//go:build windows`), so the shared attach
code never names the constant. That split is the template every other signal
split should follow, and it is written down here rather than left to be
rediscovered.

What is **not** yet done is resize detection itself. The Windows body is a no-op
today, so an attached client never re-reports its size. Under the parity
commitment that is a stopgap, not the design: R3120 is owed on Windows, not
narrowed to Unix. The seam is kind to a real implementation, because the attach
consumer ignores the signal value and only re-reads the size (which `term.GetSize`
already reads cross-platform) on any send. Two ways to fill it: poll
`GetConsoleScreenBufferInfo` on a short ticker and send when the window rect
changes (contained, no input-path rewrite, and it dodges the legacy-conhost
buffer quirk where `WINDOW_BUFFER_SIZE_EVENT` misses window-only resizes), or own
the console input stream and translate `WINDOW_BUFFER_SIZE_EVENT` (the true
SIGWINCH analog, but it shares the input queue with keystroke reads, so it forces
a single-reader rewrite of `pipeInput`). Polling is the lean. Note the host-side
half: on a resize the Windows *host* must call `ResizePseudoConsole` where Unix
resizes the pty (Part 4), so R3120 is met on both ends of the wire, not only the
client.

<a id="2"></a>

## Part 2

**IMPLEMENTED and committed: 8bfea51, 2026-09-22 (R3440–R3445).** The analysis
below is the record of why; the code is in the tree.

`cmd/ark/main.go` `cmdStop` (near 3776) is Unix signals end to end, and there
are three separate failure points, not one:

1. **The liveness probe.** `proc.Signal(syscall.Signal(0))` at 3810. On Windows
   `os.Process.Signal` returns the error string `"not supported by windows"` for
   any signal but `os.Kill`, so the probe fails and stop reports the process
   "not running" although `ps` shows it alive. This is the exact line the user
   hit (pid 1644).
2. **The send.** `proc.Signal(SIGTERM/SIGKILL)` at 3816–3820. Same restriction;
   only `proc.Kill()` (which calls `TerminateProcess`) works on Windows.
3. **The exit poll.** `proc.Signal(syscall.Signal(0))` again at 3828.

And the far end matches: the server's only shutdown trigger is
`signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)` at `server.go:374`
(R175). `SIGINT` maps to console Ctrl+C on Windows, but nothing external
delivers `SIGTERM` to a Go process there, so even a corrected client cannot ask
the server to shut down *gracefully* by signal. The graceful teardown itself is
a self-contained goroutine (`server.go` ~375–392: stop scheduler, close watcher,
shut the UI runtime, close listener, close DB, exit 0), which is convenient — it
extracts cleanly into a function an endpoint could also call.

**DECIDED (Bill, 2026-09-22): A, graceful over the socket, on every platform.**

B (the platform-split kill) is rejected: an ungraceful `TerminateProcess` risks
leaving the socket or the DB lock held, which is the exact clean-release
guarantee Part 2 exists to give the test loop, so the "small" fix undercuts the
reason the part is a gate at all. Routing the graceful teardown through an
endpoint on every platform, not Windows-only, is deliberate too: a graceful path
only Windows exercises is the config-nobody-runs that rots, the same shape as
O212 and the `withFTS` dead-code bug.

The shape:

- Extract the teardown goroutine (`server.go` ~375–392) into a function.
- Add `POST /shutdown`, which responds and then runs that teardown, the way
  `POST /luhmann/stop` (`server.go:557`) already does.
- Rework `cmdStop` to POST then poll for release, replacing every
  `proc.Signal(...)`. Liveness and the release-poll are both a socket dial now: a
  response means alive, a refused dial means down. That is portable (Part 5
  proved it on Windows) and it retires the `proc.Signal(0)` probe that is failure
  point 1.
- Bound the poll with a timeout, and split the timeout fallback by platform,
  because that fallback is the ungraceful kill A set out to avoid:
  - **Linux** kills (`SIGKILL` / `proc.Kill`), then errors if the kill fails or
    the process still will not release. The OS frees the lock and the socket on
    death, so a kill is a safe-enough last resort here.
  - **Windows** errors, no kill. `TerminateProcess` is the very operation A avoids
    on Windows, so a failed graceful stop surfaces as an error for the user to
    resolve rather than a silent, risky kill.

This fixes Part 2's far end for free: the server stops depending on a `SIGTERM`
that nothing external delivers on Windows, because the endpoint drives the same
teardown everywhere. The Unix `SIGTERM`/`SIGINT` handlers stay for Ctrl+C but are
no longer the only graceful path. All Go, so mini-spec: `server.go` (extract plus
endpoint) and `cmd/ark/main.go` (`cmdStop`).

Open detail for implementation: `ark stop -f` today maps to the OS kill. The
natural reading here is that `-f` becomes the explicit opt-in to the ungraceful
kill, Windows included (the one sanctioned way to `TerminateProcess`), so the
default errors safely and `-f` is the escape hatch the user owns. Confirm when we
implement.

<a id="3"></a>

## Part 3

**The `linkapp`/Frictionless-app half of this part is redesigned in
[app-activation.md](app-activation.md).** That carve replaces the per-file
symlink aggregation with one directory link per app (a junction on Windows) and
moves collision handling into the runtime, so the copy-or-manifest question
below is retired for it. What stays here is the Claude Code integration: skills
and agents into `.claude/`, where we do not own the reader and must place files
where it looks.

`os.Symlink` on Windows needs SeCreateSymbolicLinkPrivilege — Developer Mode or
an elevated process — and returns `"A required privilege is not held by the
client"` otherwise. The shipped paths that call it:

- **Project install**, `cmdUIInstall` (`main.go` ~3925 skills, ~3946 agents).
  The agent side was just reworked to glob every `~/.ark/agents/*.md`
  (**DECIDED (Bill, 2026-09-19)**: install the full bundled set, not a hardcoded
  name), so the symlink loop is a natural single place to add a Windows branch.
- **linkapp**, `runLinkapp` and its sibling (`main.go` ~626/632/641 and
  ~4117/4124/4134): the `app.lua`, app directory, and per-viewdef symlinks under
  `~/.ark/`. This is how new viewdefs become visible to the runtime.
- **Bundle extraction** (`main.go` ~4737) and **llama lib extraction**
  (`llamalibs.go:283`), the latter also entangled with Part 6.

A second symlink mechanism, missed by the `os.Symlink` grep and found live on
2026-09-20: bundled Frictionless Lua shells out to the Unix `ln` binary —
`os.execute('ln -sfn "target" "link"')` at `apps/mcp/app.lua:46` (grafted into
the binary) and `frictionless/apps/job-tracker/app.lua:101`. On Windows this
prints `'ln' is not recognized` twice during `serve` startup and is non-fatal, so
whatever those links wire silently does not. It is worse than the `os.Symlink`
sites in one respect: `ln` is a Unix executable absent from Windows entirely,
where `os.Symlink` is at least a portable API gated only on privilege. This is
Part 7's "os/exec of Unix-only helpers" class caught in the act, and the fix
belongs to a Frictionless (Lua) pass, not the mini-spec Go side.

The user's own Windows box did not hit the skills-symlink error, only the
agent-not-found one, which means Developer Mode is probably on there — so this
part is invisible to the reporter and real for everyone else. State the negative
loudly: a repaired agent glob looks like a fixed install, and is not, on an
unprivileged Windows account.

**The fork (open).** (a) Require Developer Mode and document it — zero code, a
real adoption wall. (b) Copy instead of symlink on Windows for the install
skill/agent files, and copy or use a directory junction for the linkapp trees.
More code and a platform-split, but it works for a normal user. (c) Teach the
runtime to read from the bundle or a manifest so nothing needs linking — the
largest, and it reopens decisions Part 5 and the whole `~/.ark` layout depend
on. The install files (small, static) are the easy case for (b); the linkapp
viewdef farm is the hard case, because its whole point on the dev box is
edit-source-see-it-live, and that loop is a Unix dev-box concern that a Windows
end user does not have.

<a id="4"></a>

## Part 4

The managed-pty feature (`ark luhmann`) assumes a Unix terminal in more than the
SIGWINCH spot Part 1 touched, and under the parity commitment every one of those
assumptions is now in scope: a Windows user hosts a Luhmann session on their own
box, so this is a required part, not a dev-box luxury. (The earlier draft marked
it low priority on the reasoning that a Windows user is only a client; parity
retires that reasoning.) The Unix-only spots, now each confirmed against the code:

- **Spawning the child in a terminal.** `ptyhost.go:372` calls
  `pty.StartWithSize(cmd, …)`. creack/pty's `start_windows.go` is a compile-only
  stub returning `ErrUnsupported` (confirmed 2026-09-23), so the feature links on
  Windows and then dies at the first launch with no master. The equivalent is a
  pseudoconsole (ConPTY, `CreatePseudoConsole`, Windows 10 1809+). A Go ConPTY
  wrapper exists, so this is known-shape work, not a research unknown.
- **Resizing the child.** `setSizeLocked` (`ptyhost.go:727`) calls
  `pty.Setsize(master, …)`, the same Windows stub returning `ErrUnsupported`
  (`winsize_unsupported.go`). Its analog is `ResizePseudoConsole`. This is the
  host end of Part 1; R3120 is met on both ends of the wire or on neither.
- **Stopping the child.** `gracefulShutdown` (`ptyhost.go:890`) sends
  `syscall.SIGTERM`. The escalation `child.Process.Kill()` at 896 already maps to
  `TerminateProcess` and is portable, but the graceful first step is not. The
  Windows analog is a console `CTRL_BREAK` to the process group, the platform
  split Part 2 already settled for the server.
- **Raw-mode attach.** The client hijacks the socket into raw terminal mode and
  reads a byte stream. On Windows that means enabling the console's VT
  input/output modes and settling the single-reader `pipeInput` question Part 1
  raised.

**What the seam actually is.** The instinct is to read this as three missing
syscalls behind three build tags. The code says otherwise. `PtyHost` stores the
master as a concrete `*os.File` (`ptyhost.go:185`) and mirrors it in an
`atomic.Pointer[os.File]` (177) for the lock-free input path. That one handle
does three jobs at once: the output stream the reader chain blocks on (`m.Read`,
478), the input stream `inputLoop` writes (`m.Write`, 631), and the object
`pty.Setsize` resizes (727). A Unix pty master is all three because the kernel
makes one fd carry them. ConPTY does not. It hands back an input pipe, an output
pipe, and a separate `HPCON` that `ResizePseudoConsole` acts on. The Unix
assumption at the center of Part 4 is a *type*, "the master is one file," rather
than a call.

That makes the honest seam a pair of small interfaces, not a `//go:build` swap of
three functions:

- `ptyMaster`: `io.ReadWriteCloser` plus `Resize(cols, rows uint16) error`. The
  Unix build wraps the creack/pty `*os.File`, where read, write, and close pass
  through and `Resize` calls `pty.Setsize`. The Windows build wraps the ConPTY
  object, where `Read` drains the output pipe, `Write` fills the input pipe,
  `Resize` calls `ResizePseudoConsole`, and `Close` tears down the pseudoconsole
  and its pipes.
- `ptyChild`: `Wait()` plus a graceful `Stop()` and a `Kill()`. The Unix build
  wraps `*exec.Cmd` (SIGTERM, then the existing Kill). The Windows build wraps
  whatever the ConPTY spawn returns (`CTRL_BREAK`, then `TerminateProcess`).

A construction seam ties them together: `startPty(argv, size) (ptyMaster,
ptyChild, error)`, build-tag split, replacing the bare `exec.Command` plus
`pty.StartWithSize` pair at `ptyhost.go:369`. One cost surfaces here rather than
mid-implementation: `atomic.Pointer[os.File]` cannot hold an interface, so
`masterPtr` becomes `atomic.Pointer[T]` over the concrete wrapper struct (or an
`atomic.Value`), and that is the most load-bearing line in the concurrency model.

**The library fork (open).** Four ways to get a real ConPTY behind that seam, and
the choice is a principle question as much as a code one.

(a) **One cross-platform pty library** (`aymanbagabas/go-pty` or similar): a
single API wrapping creack/pty on Unix and ConPTY on Windows, so `ptyMaster` and
`ptyChild` have one implementation each and both platforms run the same
integration code. This is the shape Part 2 chose deliberately, a unified path
both platforms exercise, so a bug surfaces on Linux where it is cheap and no
Windows-only branch is left to rot the way O212 and the `withFTS` dead code did.
The catch: it reworks the verified Steam Deck path to gain Windows, and it adds a
dependency in the concurrency-critical spot.

(b) **Keep creack/pty on Unix, add a Windows-only ConPTY**
(`UserExistsError/conpty` behind the seam): the verified Linux path stays
untouched byte-for-byte and Windows gets its own implementation. This is
don't-poke-the-bear applied to the path that is load-bearing on the Steam Deck.
The catch is the mirror of (a)'s virtue: the Windows pty code becomes a config
only Windows users ever run, the rot-shape Part 2 argued against, kept honest
only by the Windows test loop.

(c) **A creack/pty fork with ConPTY** (`photostorm/pty` or similar): smallest diff
if the fork truly presents ConPTY under creack's existing API, since the import
path changes and little else. Two doubts hold it back until a spike checks them:
a fork's maintenance and trust, and whether a drop-in `Setsize(*os.File)` can
honestly carry a resize that is fundamentally an `HPCON` operation.

(d) **Hand-roll ConPTY on `x/sys/windows`**: no dependency beyond what is already
transitive, maximum control, most code. The carve's own "a Go ConPTY wrapper
exists" is the argument against reinventing it.

The tension that decides this is (a) versus (b). Part 2's precedent points at the
unified path (a); the don't-poke-the-bear rule points at the isolated one (b).
The pull toward (a) is stronger than it first looks, because go-pty wraps creack
on Unix, so the bear being poked is ark's integration code, not the pty syscalls.
The tension that first decided this was (a) versus (b), and (a) won on the
unified-path argument. A verification spike overturned it. Run 2026-09-23 against
the dockur VM (Windows 10.0.26200), it disqualified both unified libraries and
validated the isolated one:

- **go-pty (a): rejected.** Its `ssh.go` is unconditionally in `package pty` and
  imports `x/crypto/ssh` and `u-root/pkg/termios`, so importing it forces two new
  modules into ark for a feature ark never calls, with no opt-out.
- **phuslu/pty (a-variant): rejected on the evidence, reluctantly.** Zero
  dependencies, and its Unix path is flawless (the spike drove a real bash from
  24×80 to 40×120 and the child observed it). But on Windows 26200 its ConPTY
  child fails to bind to the pseudoconsole: the child renders to the *parent*
  console and the read pipe carries only conhost's control stream, so the child's
  real output is lost. phuslu's own test suite reproduces it (four round-trip
  tests fail), so it is a genuine library bug on this build rather than a harness
  artifact. The likely mechanism is a `CreateProcess` isolation gap, fixable in
  principle, but not a bet worth making on an untagged single-author library for a
  shipped binary.
- **UserExistsError/conpty (b): validated.** It binds the child reliably in every
  console state, and the full-job probe passed: interactive input round-trips,
  `Resize` drove the child's real dimensions (`mode con` reported the new 40×120),
  and Wait+Close gave a clean EOF teardown. It needs no new module, since x/sys is
  already present. Its one quirk, an output pipe that stays open until Close
  rather than EOF-ing on child exit, is documented behavior and matches ark's
  existing finalize-then-close flow. (The symptom class and this EOF behavior are
  the ones openai/codex #45504 and egarim/telekinesis #49 describe.)

**DECIDED (Bill, 2026-09-23): (b) — creack/pty on Unix, UserExistsError/conpty on
Windows, behind the `ptyMaster`/`ptyChild` seam.** The unified-path virtue that
first favoured (a) turned out to be unreachable: one candidate is too heavy, the
other mis-binds on the target build. (b)'s cost is the two implementations ark
maintains, the rot risk Part 2 named, held in check by the Windows test loop this
spike established.

Implementation notes the seam already absorbs: conpty takes a command-line string
instead of an `*exec.Cmd`, so the Windows `ptyChild` wraps a `*conpty.ConPty`
(Pid, Wait, Close) and composes argv into a command line; there is no
`cmd.Process` on Windows; the child's output EOF arrives from Wait+Close, not from
the read itself; and the Windows stop is a terminate rather than a graceful
`CTRL_BREAK`, the same platform asymmetry Part 2 already accepts.

**Landed 2026-09-23.** The seam is `pty_backend.go` (the `ptyMaster`/`ptyChild`
interfaces plus a `ptyMasterRef` so `masterPtr` stays an `atomic.Pointer` that can
clear to nil), `pty_backend_unix.go` (creack + `*exec.Cmd`, byte-for-byte the old
behavior), and `pty_backend_windows.go` (conpty). One Windows-specific wrinkle the
VM test confirmed live 2026-09-23: conpty's output pipe does not EOF on child exit until Close,
so the Windows backend runs a watcher that closes the master when the child exits,
which is what makes the read chain's self-exit finalize fire (R3343). The graceful
flush window is Unix-only (Windows `SignalGraceful` is a no-op), so a Windows stop
elapses the grace then `TerminateProcess`es the child.

Kept as its own part so "ark works on Windows" is never claimed while a whole
verb group silently does not, now as work owed rather than work waived.

<a id="5"></a>

## Part 5

The entire CLI talks to the server over `net.Listen("unix", …)` /
`net.Dial("unix", …)` on `~/.ark/ark.sock` (`server.go:170–188`,
`cmd/ark/main.go:401–411`, `luhmann_attach.go:118`). AF_UNIX exists on Windows
10 1803+ and Go supports it, and as of 2026-09-20 this is **verified working** on
a dockur Windows VM: `ark.exe serve` bound `ark.sock` and the UI's `ui.sock`,
clients dialed and round-tripped, and it re-bound over a stale `ark.sock` left on
disk. `sun_path` measured 26 bytes there. Two caveats survive the verification
because they turn on the deployment, not the code: the `sun_path` limit still
bites if the user profile path is long (the socket lives under it), and a
pre-1803 Windows has no AF_UNIX at all, which would force a TCP-loopback fallback
and change the trust model. The transport is proven on a modern box; only the
edges are environmental.

<a id="6"></a>

## Part 6

Embedding provisions llama.cpp shared libraries at runtime via
`ark embed install` (they are deliberately not bundled). On Windows the artefacts
are `.dll`, the download URLs and archive shapes differ, and `llamalibs.go:283`
extracts tar symlinks (Part 3). Whether any of this has a Windows path is
unexamined. Flagged as UNKNOWN rather than worked, so the carve does not imply an
analysis that has not happened. A Windows binary that builds and serves but
cannot embed is a real and partial "works," and the release should say which.

<a id="7"></a>

## Part 7

Parts 1–6 are what a single afternoon of targeted greps surfaced (signals,
`os.Symlink`, unix sockets, leading-slash paths). A real sweep should also look
at file-mode assumptions, `os/exec` of Unix-only helpers, path separators
crossing the socket as data, case-sensitivity, and CRLF. Named as its own part
so the depth of the audit is honest: shallow, deliberately, for now.

First hits already in hand (2026-09-22, found while grounding
[app-activation.md](app-activation.md)): the mcp app shells out to Unix binaries
at run time — `ln -sfn` (`apps/mcp/app.lua:46`, activation, owned by
app-activation.md), `mkdir -p` (`app.lua:86`, `762`), and `base64 -d`
(`772`). The `os/exec`-of-Unix-helper class is not hypothetical; those three
break on Windows today and want Go-provided portable primitives the apps call
instead.

<a id="8"></a>

## Part 8

Unlike Parts 1–4, file-change notification is the primitive ark already routed
through a portable seam: `github.com/fsnotify/fsnotify v1.9.0`, which carries a
real Windows backend (`ReadDirectoryChangesW`). It compiles and runs there, and
`watcher.go` uses it the way that survives the platform. It watches each source
directory rather than leaning on a recursive watch that no OS's fsnotify offers,
re-watches a newly created subdirectory on its `Create` event
(`watchDirRecursive`, ~140), debounces the three-to-seven-event burst one editor
save fires, and drains the `Errors` channel so it cannot deadlock. So "should it
work?" is yes at the level of the mechanism, which lands this in the same
untested-but-mechanically-sound tier Part 5 occupied until it was verified on
2026-09-20, not the born-broken one.

What is unverified is conduct under Windows' particular quirks:

- **Overflow is logged, not recovered.** `watcher.go:210` logs an `Errors`
  event and does nothing more. On Windows a `ReadDirectoryChangesW` buffer
  overflow under a burst — a git checkout, a bulk edit — delivers
  `ErrEventOverflow` and drops the events behind it, so those files change and
  are never re-indexed. Silent index staleness with one log line. The Unix
  buffer is larger and the throttle absorbs more, so this bites Windows first.
  The repair is small and helps both platforms: on overflow, kick the reconcile
  scan that already exists rather than only logging.
- **Atomic-save ordering.** Windows editors save through a temp file plus
  `MoveFileEx` / `ReplaceFile`, and fsnotify reports a different
  Create/Rename/Write sequence than Linux does. `relevant()` accepts all four
  kinds and ark does its own atomic writes knowingly (`server.go:3902`), so it
  probably catches the change, but the trigger has never been watched against
  Windows ordering.
- **Path casing.** The Windows filesystem is case-insensitive, so an event path
  can arrive cased differently from the real path ark indexed under, and ark's
  "a file is its path" model then misses the entry. This is the file-watch face
  of the question `symlinks.md` already owns.
- **Locked files.** A file held open for write by another process on Windows
  cannot be read, so the read-after-event can fail where Unix would not, and
  there is no retry.

The overflow-to-reconcile repair is the one concrete, cheap win here, and it is
worth doing whatever the rest of the Windows verdict turns out to be.

---

When a part lands, its decision goes to `specs/` and `design/` as a requirement
or a comment at the code, and this carve points at where — the discipline the
carves directory exists to enforce. Part 1 already did: R3120's Windows
narrowing lives in the code comments, and this carve names it rather than
holding the fact alone.
