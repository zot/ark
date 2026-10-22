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
someone else's Linux problem: the unix-socket *listener* (Item 5), the file
watcher (Item 8), embedding provisioning (Item 6), and the server's own shutdown
(Item 2) all have to run on Windows, because on a Windows install the server is
on Windows.

A corollary worth stating plainly: parity flattens priority. Every part must
ship for "ark runs on Windows" to be true, so none is optional or low-priority
as a goal. The only ranking this carve admits is implementation *order*, a
question of what comes first because something else depends on it, not of
whether a part gets done. Dependency sets that order more than taste does: the
socket transport (Item 5) is the gate, since nothing else can even be exercised
until it is verified. Read the per-part state markers (OPEN, NOT VERIFIED,
UNKNOWN) as "where the work stands," never as "whether the work happens."

The cross-OS alternative (a Windows CLI dialing a Linux-hosted server) is set
aside because no user runs that way. It also cannot use the transport ark has:
`~/.ark/ark.sock` is an AF_UNIX path local to one machine, so reaching a Linux
server from Windows would mean TCP and a reworked trust model (Item 5).

The remote-access need that alternative seems to serve is already met, on both
platforms, by SSH: log in to the box ark runs on and use it there. The CLI and
server stay co-located, the socket stays local, and SSH owns the network hop and
its own mature trust model, and forwards the terminal (including window-size
changes, so resize never arises on that path). This is a reason ark should not
grow a cross-machine transport, not a reason to relax the native parity work: an
all-Windows user has no SSH hop and still needs the local socket (Item 5), the
ConPTY host (Item 4), and Windows resize (Item 1). The one soft spot is
direction: SSH out of Windows uses the solid built-in client, while SSH into
Windows (hosting a remotely-reached ark on a Windows box) leans on OpenSSH
server, a less-trodden path there.

## Status

What is still open, in **implementation order** (testability first: what unblocks
fast, trustworthy verification), so the part numbers run out of sequence; detail
is keyed by bare number in the sections below, which stay in numeric order. The
checkbox is authoritative where the three markings disagree. Verification-only
and unknown items carry the negative marker rather than a false green.

- [x] ~~**Item 5 — the AF_UNIX transport under Windows.**~~ **ASSESSED (Bill,
      2026-09-20.)** Verified working on the dockur Windows VM, `ark.exe` from
      09-19, and the verification was the whole part, so it carries its own
      Windows test. `ark.exe serve` bound `ark.sock`, a second `ui.sock`, and
      HTTP `:57801`; clients dialed and round-tripped (`luhmann status` →
      "asleep", `status` → "server: running"); and it re-bound cleanly over the
      stale `ark.sock` left on disk. `sun_path` was 26 bytes, far under the
      ceiling. The gate is open — the sequence below is unblocked.
- **Item 2 — `ark stop` speaks only Unix signals.** **SPLIT (Bill, 2026-09-24.)**
      Was the do-second item: the testing-rate multiplier. A live server, `ps`
      shows it, stop reports "not running." The fallback is a manual kill, and
      kill-then-restart is how the old binary ends up still serving while you
      believe you restarted, so a broken stop makes every Windows test result
      untrustworthy, not merely slow.
  - [x] ~~**2.1 — graceful stop over the socket.**~~ **LANDED (2026-09-22 —
        8bfea51, 1e4643c.)** Confirmed on Windows 2026-09-20: `ark stop` printed
        `process 3792 not running: not supported by windows` while `tasklist`
        showed PID 3792 alive; `taskkill /F` was needed. DECIDED 2026-09-22 (A):
        graceful `POST /shutdown` on every platform, socket-dial liveness,
        timeout then Linux-kills / Windows-errors. Implemented R3440–R3445;
        compiles Linux + Windows and stop tests pass; validate failures are
        pre-existing debt, not this part.
  - [x] ~~**2.2 — test on Windows.**~~ **ASSESSED (Bill, 2026-09-22.)** Live on
        the dockur VM: `ark stop` cleanly stopped a running server (reported "ark
        server stopped (pid 5220)", process gone, server logged `received POST
        /shutdown, shutting down`), where the 09-19 binary said "not running."
        Surfaced a pre-existing shutdown-race (process exits 1 on the
        closed-listener error, racing gracefulShutdown's exit 0; safe under
        bbolt), fixed by R3446 (atomic shutdown latch, 1e4643c) and re-verified
        live: graceful stop now exits 0 with no `error: accept` line.
- **Item 4 — the managed-pty path assumes a Unix terminal.** **SPLIT (Bill,
      2026-09-24.)** Was the gate before Item 1's test. Required under parity:
      Luhmann must host on Windows (ConPTY child, `ResizePseudoConsole`,
      `TerminateProcess`, VT-mode attach). Proving Item 1 end to end needs a
      Windows-hosted session to attach to, so this precedes it.
  - [x] ~~**4.1 — the pty backend seam.**~~ **LANDED (2026-09-23 — 5c3681d.)**
        DECIDED 2026-09-23 (Bill, after a live Windows spike): (b) — creack/pty
        on Unix + UserExistsError/conpty on Windows, behind a
        `ptyMaster`/`ptyChild` seam (a type substitution for the `*os.File`
        master, not a three-syscall swap). The spike ruled out both unified
        libraries: go-pty drags in x/crypto+u-root, and phuslu/pty mis-binds its
        ConPTY child on Windows 26200. Implemented R3447–R3450:
        `pty_backend.go` (the seam), `pty_backend_unix.go` (creack),
        `pty_backend_windows.go` (conpty), and the `ptyhost.go` rework; compiles
        Linux + Windows, and the 31 Unix PtyHost tests pass. See Item 4.
  - [x] ~~**4.2 — test on Windows.**~~ **ASSESSED (Bill, 2026-09-23.)** Live on
        the dockur VM (build 26200). A `[luhmann] command` stand-in launched a
        ConPTY session (`status` reported awake, the master installed);
        `luhmann stop` terminated it (TerminateProcess) and status returned to
        asleep; and a self-exiting stand-in (`ping -n 3`) self-cleared
        awake→asleep on its own, the watcher closing the master so the read
        chain EOF'd and finalize fired, with the launch reporting `claude exited
        during startup`.
- **Item 1 — SIGWINCH.** **SPLIT (Bill, 2026-09-24.)**
  - [x] ~~**1.1 — Windows resize detection.**~~ **LANDED (2026-09-23 —
        c9311dd.)** The compile break was fixed 2026-09-20. Implemented R3451,
        R3452: the Windows client polls the console window rect (no `SIGWINCH`)
        and re-reports its size. A second Windows bug surfaced and was fixed:
        `term.GetSize` on the stdin console handle returns "the handle is
        invalid" (measured on the VM), so the client's size reads moved to
        stdout (R3451). Compiles Linux + Windows.
  - [x] ~~**1.2 — test on Windows.**~~ **ASSESSED (Bill, 2026-09-23.)**
        Interactive attach over ssh: a live window resize showed the
        size-printing stand-in updating its reported size as the window changed,
        so the whole chain fired (poller → resize frame → host →
        `ResizePseudoConsole`).
- **Item 3 — `os.Symlink` needs privilege on Windows.** **SPLIT (Bill,
      2026-09-24.)** DECIDED 2026-09-24 (Bill): directory links only (symlink
      on Unix, junction on Windows, one helper shared with app-activation and
      [setup-and-update.md](setup-and-update.md), no copying).
      Junction-following by Claude Code and Go's junction semantics measured
      on the VM. Where the links go moved to setup-and-update; the bundle
      extraction symlinks are gated on app-activation, and the llama ones on
      Item 6. See Item 3.
  - [x] ~~**3.1 — the portable directory-link helper.**~~ **LANDED (2026-10-04, #136.)**
  - [x] ~~**3.2 — test on Windows.**~~ **ASSESSED (2026-10-04.)** The helper
        works for a standard user outside ssh; unprivileged `setup` still stops
        at bundle extraction, as predicted. Measured in Item 3. **Re-tested
        2026-10-13, after app-activation:** a fresh standard user (`arkfresh`)
        at its own desktop ran `setup` end to end (six apps activated as
        junctions, libs, runtime, model), then `serve` logged "Loaded 48
        viewdefs from 6 apps", the UI came up, and `ui conflicts` answered
        through the server.
  - [ ] **3.3 — detect junctions the session cannot read.** **OPEN (not
        queued.)** Optional. DECIDED (Bill, 2026-10-13, superseding the
        same day's "required"): normal users always run from the desktop, which
        does not enforce RedirectionTrust; ssh is only how we test from Linux,
        and the harness already copes (`winrun.sh`). So this is not product work.
        Item 3.
- **Item 9 — the install surface on Windows.** **SPLIT (Bill, 2026-09-24.)**
      Rides on [setup-and-update.md](setup-and-update.md): setup's self-copy
      into `~/.ark` and `update`'s replacement of a running `ark.exe`; the
      Windows exe semantics are measured. See Item 9.
  - [x] ~~**9.1 — self-copy and exe replacement.**~~ **ASSESSED (Bill,
        2026-10-21.)** Closed with no change of its own: setup's self-copy
        (#136, run as `arkfresh` 2026-10-13) and the running exe's rename-aside
        (#142's staged path, #147's plain update) are proven on `windows-gpu`.
        See Item 9.
  - [x] ~~**9.2 — test on Windows.**~~ **LANDED (2026-10-21 — `#147`.)** Plain `ark update`
        from an installed `~/.ark/ark.exe`, download path included. Needs
        9.3 and 9.4 first. See Item 9.
  - [x] ~~**9.3 — the release carries what `update` fetches.**~~ **LANDED (2026-10-21 — `#145`.)** Filed by Bill, 2026-10-21. `/mini-spec`.
  - [x] ~~**9.4 — a release-source override for testing.**~~ **LANDED (2026-10-21 — `#146`.)** Filed by Bill, 2026-10-21. `/mini-spec`.
- **Item 6 — llama.cpp embedding provisioning on Windows.** **SPLIT
      (2026-10-04.)** Measured on the VM: provisioning dropped every file of
      ggml-org's flat Windows zips (fixed, 6.1); with the files in place,
      `ggml.dll` will not load until Windows can find its dependencies in
      `lib\` (6.6) *and* the MSVC runtime is present (6.2). Both landed, and
      embedding now runs end to end on a clean Windows (6.4), and on an NVIDIA
      GPU through Vulkan (6.5) and, when asked for, CUDA (6.7).
      See Item 6.
  - [x] ~~**6.1 — extract flat Windows zips.**~~ **LANDED (2026-10-04.)**
        `extractZip` strips a release directory only when every entry shares
        one. R2969, `test-LlamaLibs.md`; live on the VM, 30 DLLs in `lib\`.
  - [x] ~~**6.2 — the MSVC runtime, app-local.**~~ **LANDED (2026-10-04,
        R3486.)** Every provisioning run checks System32 and `lib\` for
        `msvcp140.dll`, `vcruntime140.dll`, `vcruntime140_1.dll`; when any is in
        neither, it fetches ark's pinned runtime zip (GitHub release
        `msvc-runtime-14.51.36247`, Microsoft-signed DLLs) into `lib\`. Runs
        even when the libs were already present, so existing installs heal.
  - [x] ~~**6.3 — auto-detection requires a real Vulkan device.**~~
        **ASSESSED (2026-10-04): harmless.** `auto` does pick the Vulkan build
        on a GPU-less VM, but that zip also ships the CPU backends and ggml
        uses whichever finds a device; it embedded on the CPU with no error.
  - [x] ~~**6.4 — test on Windows, CPU path.**~~ **ASSESSED (2026-10-04.)** On
        the clean VM (no GPU driver, no MSVC runtime), a fresh `ark setup`
        provisioned the libs, fetched the runtime and the model, and `ark serve`
        embedded 156 chunks on the CPU backend with nothing done by hand. On an
        install whose libs predate 6.2, `ark embed install` fetched only the
        runtime, and a second run fetched nothing.
  - [x] ~~**6.5 — test on Windows, GPU path.**~~ **ASSESSED (2026-10-04),
        Vulkan only.** On a second VM with the host's RTX 2060 Max-Q passed
        through, ark with `backend = "vulkan"` offloaded all 13 layers to the
        GPU and embedded 162 chunks. `auto` picks CUDA there, and that fails
        (6.7).
  - [x] ~~**6.7 — the CUDA asset map.**~~ **LANDED (2026-10-05, R3487-R3493;
        R2967 retired.)** On Windows with an NVIDIA GPU, `auto` resolved to
        `cuda` and provisioning 404'd: ark asked for `win-cuda-13.1`, while
        llama.cpp b9592 publishes `cuda-12.4` and `cuda-13.3`, each with a
        separate `cudart` zip ark never fetched, and a CUDA build loads only
        if the driver supports its version. **DECIDED (Bill, 2026-10-04):** on
        Windows `auto` picks Vulkan (or CPU) and never CUDA, since Vulkan ran
        the whole model on the GPU for about 38 MB against roughly 620 MB for
        CUDA. An explicit `backend = "cuda"` reads the driver's CUDA version
        from `nvidia-smi`, takes the newest build it supports, and fetches its
        `cudart` too; Linux picks the builder's `cuda-13` or `cuda` (12) the
        same way. Live on the GPU VM: `auto` provisioned Vulkan; explicit
        `cuda` with driver CUDA 12.5 took the 12.4 build and runtime, and
        embedding ran on `CUDA0` (13/13 layers). That first CUDA run took
        about 35 s against Vulkan's 4 s, likely one-time CUDA start-up, not
        measured further. Leftover Vulkan DLLs are harmless: llama.cpp skips
        the second view of the same card by PCI id.
  - [x] ~~**6.6 — find dependencies in `lib\`.**~~ **LANDED (2026-10-04,
        R3485.)** `addDllSearchDir` sets the lib dir as the process DLL
        directory (`SetDllDirectoryW`) before yzma loads; a DLL loaded by full
        path does not search its own folder. Live on the VM: the model loads
        with no `PATH` change, and the Windows test's fire alarm rang.
- [ ] **Item 10 — a winget package.** **OPEN (not queued.)** The ordinary
      Windows install path, beside `ark setup`, not replacing it: a portable
      package whose manifest declares the VC++ redistributable as a dependency.
- [x] ~~**Item 11 — Claude in WSL2, ark on Windows.**~~ **LANDED
      (2026-10-05, R3494-R3514.)** Projects on NTFS, the ark server native on
      Windows, Claude Code inside WSL2 using the CLI. `ark.exe` called from
      WSL2 detects its `wsl.exe` parent and translates paths both ways; `eval
      "$(…/ark.exe wsl-init)"` builds the Linux side (a `~/.ark/ark` shim that
      reads the drvfs mounts, links into the Windows install). Verified live on
      `windows-gpu` (WSL2 needed a named CPU model there). Open: gaps O221
      (Ctrl-C and `luhmann attach` through interop) and O222 (a working
      directory on the Linux filesystem). See Item 11.
- [ ] **Item 12 — `ark claude`: run Claude Code inside the ark server.**
      **OPEN (not queued.)** PARKED (Bill, 2026-10-13): not now; the
      level-set in Item 3 removed its Windows motivation. Originally, DECIDED
      (Bill, 2026-10-13): the general way past
      the ssh junction problem. Gated: measured 2026-10-13, a default standard user
      cannot run a task from ssh, so it needs another way to start the server
      outside the session (Item 3 lists the candidates).
      See Item 12.
- [ ] **Item 8 — filesystem change events under Windows.** **OPEN (not
      queued.)** Not verified, and an independent track: the mechanism is
      portable (fsnotify); conduct under Windows' quirks, and recovery from
      dropped events, are unchecked.
- **Item 7 — the audit past the grep seed.** **SPLIT (Bill, 2026-10-19.)**
      Runs alongside, not last. Items 1–6 came from one afternoon's greps, not a
      sweep. The apps' shell-outs were split out as 7.1–7.3; 7.4 is the rest of
      the sweep. See Item 7.
  - [x] ~~**7.1 — portable file and process primitives in ui-engine.**~~ **LANDED (2026-10-19 — `#141`.)**
  - [x] ~~**7.2 — ark's update bridge for the UI.**~~ **LANDED (2026-10-19 — `#142`.)**
  - [x] ~~**7.5 — `sys.updateStatus` names the staged version.**~~ **LANDED (2026-10-19 — `#143`.)**
  - [x] ~~**7.3 — the apps drop their shell-outs.**~~ **LANDED (2026-10-19 — `#144`.)**
  - [ ] **7.4 — the rest of the sweep.** **OPEN (not queued.)** The Go-side
        audit: Unix-only `os/exec`, file modes, path separators as data,
        case-insensitivity, CRLF. See Item 7.
  - [x] ~~**7.6 — verify the ark app's checkpoint repo after 7.3.**~~
        **ASSESSED (Bill, 2026-10-21.)** Real: the `local` branch is gone, and
        the loss is accepted, since the app's source is in git. The cause
        predates 7.3 (phantom artifacts left by the July #63 incident), and
        the baseline flow destroys the old repo before it knows the restore
        will work. That fault is filed separately; it is not Windows-specific.
        See Item 7.

Not a defect, recorded so nobody repairs it: the `/tmp/add`, `/tmp/append`, and
`/tmp/remove` strings in `cmd/ark/main.go` and `message_cli.go` are HTTP route
paths for `tmp://` documents. They are URLs, not filesystem paths, and mean
nothing to the Windows filesystem.

<a id="1"></a>

## Item 1

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
resizes the pty (Item 4), so R3120 is met on both ends of the wire, not only the
client.

<a id="2"></a>

## Item 2

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
guarantee Item 2 exists to give the test loop, so the "small" fix undercuts the
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
  response means alive, a refused dial means down. That is portable (Item 5
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

This fixes Item 2's far end for free: the server stops depending on a `SIGTERM`
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

## Item 3

**The `linkapp`/Frictionless-app half of this part is redesigned in
[app-activation.md](done/app-activation.md).** That carve replaces the per-file
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
  (`llamalibs.go:283`), the latter also entangled with Item 6.

A second symlink mechanism, missed by the `os.Symlink` grep and found live on
2026-09-20: bundled Frictionless Lua shells out to the Unix `ln` binary —
`os.execute('ln -sfn "target" "link"')` at `apps/mcp/app.lua:46` (grafted into
the binary) and `frictionless/apps/job-tracker/app.lua:101`. On Windows this
prints `'ln' is not recognized` twice during `serve` startup and is non-fatal, so
whatever those links wire silently does not. It is worse than the `os.Symlink`
sites in one respect: `ln` is a Unix executable absent from Windows entirely,
where `os.Symlink` is at least a portable API gated only on privilege. This is
Item 7's "os/exec of Unix-only helpers" class caught in the act, and the fix
belongs to a Frictionless (Lua) pass, not the mini-spec Go side.

The user's own Windows box did not hit the skills-symlink error, only the
agent-not-found one, which means Developer Mode is probably on there — so this
part is invisible to the reporter and real for everyone else. State the negative
loudly: a repaired agent glob looks like a fixed install, and is not, on an
unprivileged Windows account.

**The fork, as it stood.** (a) Require Developer Mode and document it: zero
code, a real adoption wall. (b) Copy instead of symlink on Windows, or junction
the directory trees. (c) Teach the runtime to read from the bundle or a manifest
so nothing needs linking. The per-file agent links were what kept (b) awkward,
since a junction links only directories, and a hard link to an agent file breaks
silently when `setup` rewrites it.

**DECIDED (Bill, 2026-09-24): directory links only.**

- **Every install link is a directory link.** Claude Code scans the agents
  directory recursively and takes an agent's identity from its `name:`
  frontmatter alone, not from its path, so ark's agents need no per-file links:
  one link `~/.claude/agents/ark` → `~/.ark/agents` carries the whole bundled
  set, and a newly bundled agent appears without re-running install (which also
  retires the drift R432's glob was guarding against). The skills (`ark`, `ui`)
  were already directories.
- **One portable primitive.** A symlink on Unix, a junction on Windows. A
  junction needs no privilege and no Developer Mode. The same helper serves
  [app-activation.md](done/app-activation.md)'s one-link-per-app, so it is built once.
- **Where the links go, and who makes them, moved to
  [setup-and-update.md](setup-and-update.md).** Once every link was a directory
  link into `~/.claude/`, the project-level install had nothing left to do, and
  the whole install surface got redrawn there: `ark setup` as the only install
  verb, `--local` for a project, no copying, retiring the old copies, and
  `ark update`. What stays here is the link mechanism and its measurements.

**Measured, not assumed (2026-09-24).** On Linux, a fresh headless session
listed a probe agent whose only path was a symlinked subdirectory of
`~/.claude/agents/`, while the running session could not spawn it or a
real-subdirectory control. So discovery follows symlinks, and it happens at
session start. On the dockur VM (build 26200, Claude Code 2.1.281), a probe
junctioned into `~/.claude/agents/` appeared in a fresh session's roster. A Go
1.26 probe on the same VM showed four more facts:

- `Lstat` reports a junction as `ModeIrregular`, neither a directory nor a
  symlink.
- `os.Readlink` returns the junction's absolute target, so the helper can tell
  "already our link" from someone else's.
- `os.Remove` and `os.RemoveAll` both remove only the link and leave the target
  intact.
- Reading through the junction works.

The helper still uses `os.Remove` on a link and refuses a real directory at the
destination, since a non-link directory there is someone's data.

**What remains of Item 3 after this.** The **bundle extraction** sites (`setup`'s
extraction through Frictionless `installFile`, and `ark bundle cp`) create the
shipped image's own symlinks: `lua/<app>`, `lua/<app>.lua`, the per-viewdef
links, and `html/mcp`. Those are the aggregation layer app-activation retires,
so on an unprivileged Windows account `setup` is gated on that carve, not on
this part. *Resolved 2026-10-07:* with app-activation Items 1-4 the bundle
ships no symlinks and setup activates apps with junctions; a standard user's
`ark setup` completes (measured on the test VM, see app-activation Item 3). The **llama tar symlinks** (`llamalibs.go:283`) are Linux `.so`
version links and belong to Item 6.

**Copying is almost as good as linking (Bill, 2026-10-05).** If a link into
`~/.claude` is ever unavailable, copying the skills and agents from `~/.ark`
into `~/.claude` keeps nearly all the value, because `~/.claude` is per-user
and global: a copy still serves every project, as the link does. What copying
gives up is freshness: a copy goes stale when the install changes, so it must
be refreshed by `ark update` (whose manifest can tell an untouched copy from
a user's edit) rather than following the install automatically. Directory
links stay the decision; this is the fallback, and the reason it is a mild
one.

**Measured on Windows (2026-10-04, build 26200, part 3.2).** Run as a standard
user with no symlink privilege, from a scheduled task:

- The directory-link helper works: a junction is created without privilege and
  read through, and `TestMakeAndReadDirLink` passes. As an administrator,
  `ark setup` from a fresh home links all of `~/.claude` as junctions, and a
  second run is a no-op.
- Unprivileged `ark setup` still fails, at the first shipped symlink:
  `failed to extract html/mcp: symlink ..\apps\mcp ...: A required privilege is
  not held by the client`. That is the bundle-extraction case above, so the
  gate on app-activation is now measured rather than predicted.

**RedirectionGuard, and why the test ran from a task.** Over ssh the same
junction test fails, for a reason that has nothing to do with ark. Windows'
OpenSSH server runs with the RedirectionTrust process mitigation enforced, and
every process under an ssh session inherits it. A process that enforces it
refuses to traverse any junction a non-administrator created (error 448, "The
path cannot be traversed because it contains an untrusted mount point"), whoever
is reading: an administrator could not follow a standard user's junction either.
A process cannot clear the policy for itself (`SetProcessMitigationPolicy`
answers Access is denied). A desktop process and a scheduled task do not enforce
it, so for those the junctions work. *Measured 2026-10-13 on `windows-gpu`:* the
RedirectionTrust policy (`GetProcessMitigationPolicy`, policy 16) of the console
desktop's `explorer.exe` reads `flags=0x0`, and of an ssh PowerShell `flags=0x1`
(enforce). A program started from the desktop is explorer's child and inherits
its policy. Then end to end, the same day: the standard user `arkfresh`, logged on
at its own desktop, ran `type` through a junction that the standard user `arktest`
had made and read the file, while the same read over ssh (as `zot`, an
administrator) was refused with "untrusted mount point". `arkfresh`'s desktop
explorer also reads `flags=0x0`. So a normal user creates and reads junctions on
the desktop with no privilege, and only ssh sessions are affected.

The consequence for users is narrow but real: a **standard user running ark or
Claude Code over ssh** cannot read the junctions `setup` made, and nothing in
ark can fix that, since Claude Code is a separate process. An option, not a
decision: `setup` could read its own policy (`GetProcessMitigationPolicy`) and
warn when enforcement is on and the user is not an administrator.

SUPERSEDED the same day by the level-set below. DECIDED (Bill, 2026-10-13),
superseding "an option, not a decision" above: this
is required, and `ark serve` needs it as much as `setup`. App-activation made the
server read every app through its `~/.ark/lua/<app>` junction, so the same ssh
session that cannot follow `~/.claude`'s junctions also starts a server with no
apps, and nothing says why, since ui-engine's discovery skips an entry `os.Stat`
fails on. Open fork for the design: ask the policy up front
(`GetProcessMitigationPolicy` plus an admin check), or recognise error 448 where
a junction read actually fails, or both.

PROPOSED (Bill, 2026-10-13), not decided: a flag such as `ark serve --task` that
starts the server as a scheduled task, outside the ssh session, so it does not
enforce RedirectionTrust and reads the junctions. This is the route `winrun.sh -b`
already uses, and on 2026-10-04 ssh-side `status`, `luhmann status` and `stop`
worked against a task-served server. It covers Claude Code as well, when ark hosts
it: a ConPTY session is a child of the server, so it inherits the task's context
and reads `~/.claude`, and the ssh user attaches with `luhmann attach` as the same
user (also proven 2026-10-04). Only `claude` run directly in the ssh shell stays
broken, and the detection's message can point at the hosted route. Points to
settle:
- **Measure first:** whether a fresh standard user can register its own task from
  ssh. `arktest` works only because it was granted "log on as a batch job", and
  S4U self-registration was measured only for `zot`. If a default standard user
  cannot, the flag fails for exactly the users it exists for.
- The paths that start a server themselves (`ark ui install` and its kin) would
  start one inside the restricted session; they likely want the same route when
  enforcement is on.
- The detection message names the flag, e.g. "this ssh session cannot read
  junctions; run `ark serve --task`".

**MEASURED (2026-10-13, `windows-gpu`, build 26200): a default standard user
cannot run a task from ssh.** A fresh account `arkfresh` (member of Users only,
nothing granted) logged in over ssh by password. The `SeBatchLogonRight` list
there is `arktest` (granted by hand), Administrators, Backup Operators and
Performance Log Users; Users is not on it.
- PowerShell's `Register-ScheduledTask` family fails before reaching Task
  Scheduler: "Cannot connect to CIM server. Access denied" (it goes through CIM,
  which a standard ssh user cannot reach). Not the route ark would take anyway.
- `schtasks /create /ru arkfresh /NP` (S4U, no stored password): prompts for a
  password regardless, then "ERROR: Access is denied."
- `schtasks /create` with no `/ru` (Interactive only): registers and "runs", but
  never executes, `Last Result 267011` (`0x41303`, task has not run), since the
  user has no desktop session.
- `schtasks /create /ru arkfresh /rp …` (password logon): registers with "Batch
  logon privilege needs to be enabled for the task principal", then never
  executes, `Last Result 267011`.

So the `--task` route fails for exactly the users it was for, and Item 12 cannot
rest on it as proposed. What is left, none measured yet:
- **A one-time grant.** An administrator gives the user (or Users) the batch-logon
  right; after that the password-logon task works, as it does for `arktest`. It
  costs an admin step and a stored password (S4U was still denied above).
- **Start the server from the desktop.** A per-user autostart (the `HKCU` Run key
  or the Startup folder needs no privilege) runs `ark serve` in the desktop
  session, which does not enforce the policy; ssh clients then reach it over the
  socket. Only for a user who logs on at the desktop at least once per boot.
- **Admin-made junctions.** The policy refuses junctions a *non-administrator*
  created. Whether a standard user can follow, over ssh, a junction an
  administrator made is not measured; if so, an elevated setup step would make the
  links readable everywhere.

**DECIDED (Bill, 2026-10-13), the level-set:** normal users always run ark from
the desktop, where none of this applies. ssh exists so we can test from Linux, and
getting ssh to work for users outside our environment is not a goal. So 3.3 is
optional again, the `--task` proposal is dropped, and the measurement above stays
as **test-harness knowledge**: it is why `arktest` needed the batch-logon right and
why junction-shaped tests run as tasks. One piece survives on its own merits: a
`lua/<app>` entry that `os.Stat` cannot read vanishes from ui-engine's discovery
with no log line, for any reason (a broken link, permissions), and deserves one.

The consequence for testing: an ssh shell gives the wrong answer for anything
junction-shaped, so Windows tests run as scheduled tasks (S4U for the
registering account, a password logon for another), with only console work such
as `luhmann attach` done over ssh as the same user. The LinkTable tests also
cannot run unprivileged on Windows: their fixtures call `os.Symlink` directly.

<a id="4"></a>

## Item 4

The managed-pty feature (`ark luhmann`) assumes a Unix terminal in more than the
SIGWINCH spot Item 1 touched, and under the parity commitment every one of those
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
  host end of Item 1; R3120 is met on both ends of the wire or on neither.
- **Stopping the child.** `gracefulShutdown` (`ptyhost.go:890`) sends
  `syscall.SIGTERM`. The escalation `child.Process.Kill()` at 896 already maps to
  `TerminateProcess` and is portable, but the graceful first step is not. The
  Windows analog is a console `CTRL_BREAK` to the process group, the platform
  split Item 2 already settled for the server.
- **Raw-mode attach.** The client hijacks the socket into raw terminal mode and
  reads a byte stream. On Windows that means enabling the console's VT
  input/output modes and settling the single-reader `pipeInput` question Item 1
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
assumption at the center of Item 4 is a *type*, "the master is one file," rather
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
integration code. This is the shape Item 2 chose deliberately, a unified path
both platforms exercise, so a bug surfaces on Linux where it is cheap and no
Windows-only branch is left to rot the way O212 and the `withFTS` dead code did.
The catch: it reworks the verified Steam Deck path to gain Windows, and it adds a
dependency in the concurrency-critical spot.

(b) **Keep creack/pty on Unix, add a Windows-only ConPTY**
(`UserExistsError/conpty` behind the seam): the verified Linux path stays
untouched byte-for-byte and Windows gets its own implementation. This is
don't-poke-the-bear applied to the path that is load-bearing on the Steam Deck.
The catch is the mirror of (a)'s virtue: the Windows pty code becomes a config
only Windows users ever run, the rot-shape Item 2 argued against, kept honest
only by the Windows test loop.

(c) **A creack/pty fork with ConPTY** (`photostorm/pty` or similar): smallest diff
if the fork truly presents ConPTY under creack's existing API, since the import
path changes and little else. Two doubts hold it back until a spike checks them:
a fork's maintenance and trust, and whether a drop-in `Setsize(*os.File)` can
honestly carry a resize that is fundamentally an `HPCON` operation.

(d) **Hand-roll ConPTY on `x/sys/windows`**: no dependency beyond what is already
transitive, maximum control, most code. The carve's own "a Go ConPTY wrapper
exists" is the argument against reinventing it.

The tension that decides this is (a) versus (b). Item 2's precedent points at the
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
maintains, the rot risk Item 2 named, held in check by the Windows test loop this
spike established.

Implementation notes the seam already absorbs: conpty takes a command-line string
instead of an `*exec.Cmd`, so the Windows `ptyChild` wraps a `*conpty.ConPty`
(Pid, Wait, Close) and composes argv into a command line; there is no
`cmd.Process` on Windows; the child's output EOF arrives from Wait+Close, not from
the read itself; and the Windows stop is a terminate rather than a graceful
`CTRL_BREAK`, the same platform asymmetry Item 2 already accepts.

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

## Item 5

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

## Item 6

Embedding provisions llama.cpp shared libraries at runtime via
`ark embed install` (they are deliberately not bundled). On Windows the artefacts
are `.dll`, the download URLs and archive shapes differ, and `llamalibs.go:283`
extracts tar symlinks (Item 3). Whether any of this has a Windows path is
unexamined. Flagged as UNKNOWN rather than worked, so the carve does not imply an
analysis that has not happened. A Windows binary that builds and serves but
cannot embed is a real and partial "works," and the release should say which.

**Measured on the VM (2026-10-04, build 26200).** Three failures, each hidden
behind the one before it:

1. **Provisioning extracted nothing (6.1, fixed).** ggml-org's Windows zips are
   flat, with `llama.dll` at the root, unlike the Linux tarballs' `llama-bXXXX/`
   wrapper. The extractor stripped a leading directory unconditionally, so every
   entry flattened to nothing, the download "completed", and only the post-check
   noticed an empty `lib\`. A zip can be pre-scanned, so flattening is now
   decided per archive; the tar path is unchanged.
2. **`ggml.dll` will not load (6.6 and 6.2).** With all 30 DLLs present,
   loading fails with "The specified module could not be found", which on
   Windows names a missing *dependency*. Two causes, each sufficient alone:
   - **The search path (6.6).** yzma loads `lib\ggml.dll` by full path through
     plain `LoadLibrary`, and Windows then looks for that DLL's own imports
     (`ggml-base.dll`, the runtime) in the *application's* directory, System32
     and `PATH`, never in the DLL's folder. Even an installed runtime would not
     have rescued `ggml-base.dll`.
   - **The MSVC runtime (6.2).** Read from the DLLs' import tables, every
     llama.cpp DLL imports `msvcp140.dll`, `vcruntime140.dll` and
     `vcruntime140_1.dll`, and a clean Windows has none of them. (The
     `api-ms-win-crt-*` imports are the Universal CRT, part of Windows 10+;
     `libomp140` ships in the zip.)

   Confirmed both ways: with `lib\` on `PATH` and the three runtime DLLs placed
   in `lib\`, the model loads and 159 chunks embed in about four seconds;
   removing only the runtime DLLs brings the failure back.
3. **Vulkan chosen with no Vulkan device (6.3, harmless).** The VM's only GPU is
   a VirtIO display adapter with no Vulkan driver, yet `auto` resolved to
   `vulkan`, likely because the Vulkan loader (`vulkan-1.dll`) is present. It
   does no harm: the Vulkan zip carries the CPU backends too, and ggml falls back
   to them.

**Where the runtime DLLs could come from (6.2, undecided).** conda-forge's
`vc14_runtime` package is a pinned, hash-checkable archive of Microsoft's
redistributable DLLs (version 14.51.36247 carries all three, 870 KB together),
fetched over plain HTTPS. Three ways to use it, or any other source:

- **Bundled into ark's Windows binary at release time.** The Makefile fetches
  the package once against a pinned SHA-256 and the three DLLs ride in the
  Windows bundle; provisioning copies them into `lib\` when System32 lacks them.
  No download at install time and no new Go dependency; about 870 KB on the
  Windows binary only.
- **Downloaded at provision time from conda-forge.** Pinned URL and SHA-256,
  like the model; but a `.conda` file is a zip of zstd-compressed tars, which
  needs a zstd decoder ark does not currently import.
- **Re-hosted as an asset on ark's own GitHub release**, downloaded the same
  pinned way. No new dependency, but ark's release process must carry it.

**Why app-local for the runtime.** Windows has no apt-style dependency resolution
for a plain installer: MSI, Inno Setup and WiX bundle their prerequisites and run
them, and Microsoft's `vc_redist.x64.exe` needs administrator rights, which an
unprivileged `ark setup` does not have. MSIX does declare dependencies, but it
virtualizes the filesystem, a poor fit for a CLI that owns `~/.ark` and runs a
server. Microsoft's license permits shipping the three runtime DLLs beside the
application ("app-local"), and Windows finds a DLL's dependencies in the same
folder, so `~/.ark\lib` can hold them and nothing system-wide changes. winget is
the closest thing to apt and can declare the redistributable as a dependency;
that is Item 10, a distribution channel rather than the fix.

**The GPU path (6.5).** The VM cannot test it as built: a Windows guest gets
real GPU acceleration only by PCI passthrough (VFIO), since QEMU's paravirtual
3D (virgl, Venus) has guest drivers for Linux only. The container host has a
candidate, an NVIDIA RTX 2060 Max-Q beside the Intel UHD the host can keep, in a
clean IOMMU group (its four functions plus the CPU's PCIe bridge) and bound to
the open `NovaCore` driver rather than anything the host depends on. The likely
obstacles are laptop-specific: the card's VBIOS is often not readable from the
card and has to be supplied to QEMU, and dockur's support for passthrough to a
Windows guest is unverified. The GPU-less VM is the more valuable test bed
first, because 6.3's fallback is what a VM or office machine gets.

**What passthrough took (2026-10-04).** It works, with four laptop-specific
fixes found in order. Waking the card from D3cold under `vfio-pci` hung the
host, so the card is held in D0 (`vfio-pci disable_idle_d3=1`). The VBIOS read
cleanly once the card was awake (61,952 bytes). The mobile NVIDIA driver
refused to start without a battery, cured by a fake-battery ACPI table. And it
still reported error 43 until the card sat behind a PCIe root port rather than
directly on the guest's root bus; that was the decisive change. The guest then
ran driver 556.12 with `nvidia-smi` reporting the card on a PCIe gen 3 x8 link.

<a id="7"></a>

## Item 7

Items 1–6 are what a single afternoon of targeted greps surfaced (signals,
`os.Symlink`, unix sockets, leading-slash paths). A real sweep should also look
at file-mode assumptions, `os/exec` of Unix-only helpers, path separators
crossing the socket as data, case-sensitivity, and CRLF. Named as its own part
so the depth of the audit is honest: shallow, deliberately, for now.

First hits already in hand (2026-09-22, found while grounding
[app-activation.md](done/app-activation.md)): the mcp app shells out to Unix binaries
at run time — `ln -sfn` (`apps/mcp/app.lua:46`, activation, owned by
app-activation.md), `mkdir -p` (`app.lua:86`, `762`), and `base64 -d`
(`772`). The `os/exec`-of-Unix-helper class is not hypothetical; those three
break on Windows today and want Go-provided portable primitives the apps call
instead.

**The full inventory, measured 2026-10-19** (`grep -n 'os.execute\|io.popen'`
over both bundled apps; the line numbers above are from 09-22 and have drifted).
The `ln` hit is gone (app-activation, #138). What remains:

| App | Line | Shells out to | For |
|---|---|---|---|
| mcp | 68, 744 | `mkdir -p` | settings dir, uploads dir |
| mcp | 754 | `base64 -d` | decode a pasted image to a file |
| mcp | 77 | `ls -1d */` | `listDirs` |
| mcp | 23 | `curl` | Frictionless release check |
| mcp | 881–883 | `uname -s/-m` | platform for the update instructions |
| ark | 124 | `ls -1AF` | directory listing |
| ark | 689 | `test -d` | is-directory check |
| ark | 91 | `PATH=… ark … 2>/dev/null` | the app's whole CLI bridge (`run`, so every `arkCmd`) |

The last row is the worst: `PATH=x cmd` is POSIX shell syntax, so on Windows
every ark-app call that goes through the CLI fails, not just its stderr
redirect.

**DECIDED (Bill, 2026-10-19): the portable primitives live in ui-engine as
`ui.*`,** not in ark's `sys`. A portable file API is not ark-specific, and
ui-engine is the layer every Frictionless app (and the upstream copy of mcp that
ark's fixes flow back to) already has. **7.1** is that set: make a directory,
list one with an is-dir flag per entry, test for a directory, base64-decode,
run a program from an argv with no shell and return its output, and report
OS/arch. It is a mini-spec pass in the ui-engine repo, mirrored in ark's
`specs/lua-api.md`.

**DECIDED (Bill, 2026-10-19): the mcp app's self-update is ported to update ark,
not Frictionless.** It cannot simply be re-pointed: the Frictionless flow pushes
a `perform-update` event for an MCP-connected Claude to act on, and ark has no
MCP server, so nothing would receive it. `ark update` already does the whole job
(checksum, the Windows rename-aside, the asset refresh), but it lives in
`cmd/ark`. So **7.2** gives the UI a version check and an update action, the
latter running ark's own binary as `<exe> update` with no shell. That drops the
`curl` and `uname` rows outright.

**DECIDED (Bill, 2026-10-19): after a UI-driven update, the user restarts ark.**
The UI says the update is installed and takes effect on the next start; ark does
not restart itself. A self-restart would have to weigh pending requests, a
running Luhmann session and connected UIs, and updating next time rather than
this minute costs little.

**7.5** adds `stagedVersion` to `sys.updateStatus()`, read from `update.json`, so after a restart the UI can still say which version is staged (found 2026-10-19 while checking 7.3's call sites; without it a staged update from an earlier session reads as staged with no version). It keeps 7.3 Lua-only.

**7.3** is the `/ui-*` pass that moves both apps onto 7.1 and 7.2. It is gated on
both, and separate from them because Frictionless and mini-spec work do not share
a pass.

**What 7.3 found (2026-10-19), tested in an isolated `setup` + `serve`:**

- **ark registers `sys` after the mcp app loads.** The shell's `app.lua` runs
  inside the UI engine's start; `registerLuaFunctions` runs only once that start
  returns. A shell that touches `sys` at load time takes the whole UI down
  (`ui: start failed … attempt to index a non-table object(nil) with key
  'updateStatus'`). The shell's startup update work therefore waits for `sys`
  (`whenSys` in `apps/mcp/app.lua`). `ui.*` is already there at load. Registering
  `sys` before the apps load would be the cleaner fix, on the Go side.
- **A shell that fails to load leaves one trace.** The server keeps running
  with no UI, the Lua logs (`~/.ark/log/lua*.log`) stay empty, and the only sign
  is a `ui: start failed: … failed to load mcp/app.lua: …` line in the server
  log (`~/.ark/logs/ark.log`). Look there first when the UI is missing.
- **`ark status` has no `--dir` flag**, so the ark app's server light had always
  read "not running": the old bridge threw stderr away and got empty stdout. The
  argv bridge surfaced the error. `checkServer` now runs `status` without
  `--dir`, and matches `server: running (pid …)` by prefix.
- **ark reports release tags with their `v`** (`latest = "v0.5.0"`); the shell
  strips it before adding its own.
- **`HOME` is unset on Windows**, so both apps fall back to `USERPROFILE`; without
  it the ark app's `DB_PATH` came out as `/.ark`.

**7.4** is the sweep this item's opening paragraph asked for, minus what 7.1–7.3
covered. Those parts handled one class, `os/exec` of Unix helpers, in one place:
the two bundled apps' Lua. Nothing has looked at the rest yet, so its depth is
still the afternoon of greps behind Items 1–6. In scope, across the Go code
(`*.go`, `cmd/ark/`, and the ark-owned parts of ui-engine and microfts2 that ark
drives):

- **`os/exec` of Unix-only programs from Go.** `sh -c`, `fossil`, `git`, `xdg-open`
  and the like: which exist on Windows, which have a portable path, and which
  need a Windows branch.
- **File-mode assumptions.** `0o755`/`0o600` permission bits, the executable bit
  as a signal, `Chmod` results that mean nothing on NTFS.
- **Path separators crossing as data.** Paths stored in bbolt keys, sent over the
  socket, embedded in URLs or `tmp://` names, or compared as strings. A path
  written with `\` on Windows and read back expecting `/` breaks lookup silently.
  Chunk addresses like `C:\x\y:chunk` contain two colons.
- **Case-insensitivity.** NTFS treats `Foo.md` and `foo.md` as one file. The index
  keys paths case-sensitively, so one file can end up indexed under two names.
- **CRLF.** Chunkers, the tag parser (a tag runs to end of line), and any
  byte-offset arithmetic, when a file has `\r\n` line endings.

How it runs: one measured grep or probe per class, recorded here with its date
and command like the 2026-10-19 inventory above. Each real hit becomes its own
part (7.7, 7.8, …), queued as usual. Each false hit is written down as a
non-defect, like the `/tmp/add` note in Status, so the next sweep doesn't repair
it. 7.4 is done when every class has a recorded result, not when every hit is
fixed.

**7.6** checks whether 7.3 left the ark app's checkpoint repo damaged. On
2026-10-19, `ark ui checkpoint baseline ark` failed while restoring the `local`
branch (`fossil bundle import … artifact hash error`). Afterwards
`apps/ark/checkpoint.fossil` appeared to hold trunk only. That observation came
mid-pass and has not been rechecked. The outer `ark-code.fossil` holds a copy of
the file from 2026-09-24.

Verify first:

- Is the `local` branch really gone, or only hidden (`fossil branch list --all`,
  `fossil timeline` against the repo)? Does `fossil rebuild` / `fossil test-integrity`
  pass?
- What did the failed import leave behind? A stale bundle file, a half-written
  repo, or nothing at all?
- Does `checkpoint local` / `checkpoint baseline` work on the repo as it stands
  now?

If it is damaged, the fix has two halves. Restore the history (the 09-24 copy
plus whatever `local` commits came after it, if any survive). Then find out why
the bundle import failed. That second half is #63's machinery (`baseline`'s
export/re-import of `local`), so it reopens #63 if it is a code fault rather
than a one-off. The skill depends on where the fault turns out to be.

**Result (measured 2026-10-21).** The damage is real, and it started in July.

- `apps/ark/checkpoint.fossil` is a new project created 2026-10-19 14:39
  (project code `73e08ab7…`, not the old `7722ee95…`). It holds trunk only,
  with two check-ins (the initial empty one and `=== BASELINE ===`). It is
  sound: `fossil test-integrity` reports 0 errors and it has no phantoms. No
  bundle file was left behind.
- The 09-24 copy (`fossil cat -r 3b69a35621 apps/ark/checkpoint.fossil` from
  `ark-code.fossil`) still has `local`, with four check-ins from 07-23 to
  07-25. It also holds **14 phantom artifacts**: rows in `phantom`, with blob
  size -1. One of them is curation.lua `95dc2022…`, from #37's 07-23
  check-in. They date from the July #63 incident, whose bundle import left
  `local` as "a content-less skeleton" (ark-code.fossil, 2026-07-23).
- Re-running baseline's own sequence on a scratch copy of the 09-24 repo
  (export `local` with `--standalone`, `init`, commit, `bundle import --force
  --publish`) fails with the same `artifact hash error on 95dc2022…`. A branch
  with phantoms exports to a bundle that cannot be imported, so the 10-19
  baseline was bound to fail whatever 7.3 did.

What was lost is a checkpoint journal: the four 07-23..07-25 `local`
check-ins, whose content is in git (#39 `1d20cb7`, #37 `d239a17`, #65
`8cd17fa`), plus any `checkpoint local` run between 09-24 and 10-19, of
which no copy exists. DECIDED (Bill, 2026-10-21): keep the new repo and
accept the loss, because both Frictionless and ark keep their apps in git.
Restoring the 09-24 branch would bring the phantoms back with it.

**The fault that matters is in the flow, not the repo.** Ark records users'
work in fossil, so users must be able to rely on it (Bill, 2026-10-21). Both
implementations destroy before they verify:

- `cmd/ark/checkpoint.go` `checkpointBaseline` (R3240) exports the bundles,
  removes the old repo and its checkout, re-inits, and only then imports. An
  import failure is reported, but by then the old repo is gone, and the
  deferred `os.RemoveAll(bundleDir)` deletes the bundles as well. The error
  arrives after the damage is done and can't be undone.
- Frictionless's `install/mcp` `checkpoint_baseline` (also deployed as
  `~/.ark/mcp`) does the same, except that it sends the import's stderr to
  `/dev/null`, ignores its exit code, deletes the bundle, and prints
  "preserved branches restored" anyway.

The fix is to change the order. Build the new repo beside the old one, import
every bundle into it, and swap it into place only after all of them succeed.
On any failure, leave the old repo and checkout untouched and say so. A trial
import is what detects phantoms, so this order also covers damage like
July's. Filed for ark's Go flow as design gap O225 (back-linked from R3240),
and reported to Frictionless for its script.

<a id="12"></a>

## Item 12

DECIDED (Bill, 2026-10-13): an `ark claude` subcommand that starts Claude Code
as a pty session hosted by the ark server and attaches the current terminal to
it. On Windows this is the general way past the ssh junction problem in
[Item 3](#3): a hosted session is a child of the server, so when the server runs
outside the ssh session (as a scheduled task) Claude Code inherits that context
and reads the `~/.claude` junctions, while the user drives it from ssh through
the attach path Item 4 and Item 1 already proved there. It is an explicit command,
so it stays inside the rule that ark never starts Claude on its own.

Grounded 2026-10-13: the server holds exactly one `PtyHost` (`server.go:100`,
made at `:349`) and it is the Luhmann seat, so this needs hosts per session, not
a new mode on the existing one.

Open questions:
- **The gate.** Whether a fresh standard user can start the server as a task from
  ssh (Item 3's `--task` proposal). If not, this does not help the users it is for.
- One session per invocation, or named sessions a user can detach from and
  re-attach to later.
- How it carries the caller's working directory, environment and arguments into
  the hosted `claude`.
- Whether `ark claude` starts the server itself, as a task, when it finds none
  and the session enforces RedirectionTrust.
- What it means off Windows, where it is a persistent, re-attachable session
  (tmux-like) rather than a workaround.

<a id="8"></a>

## Item 8

Unlike Items 1–4, file-change notification is the primitive ark already routed
through a portable seam: `github.com/fsnotify/fsnotify v1.9.0`, which carries a
real Windows backend (`ReadDirectoryChangesW`). It compiles and runs there, and
`watcher.go` uses it the way that survives the platform. It watches each source
directory rather than leaning on a recursive watch that no OS's fsnotify offers,
re-watches a newly created subdirectory on its `Create` event
(`watchDirRecursive`, ~140), debounces the three-to-seven-event burst one editor
save fires, and drains the `Errors` channel so it cannot deadlock. So "should it
work?" is yes at the level of the mechanism, which lands this in the same
untested-but-mechanically-sound tier Item 5 occupied until it was verified on
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

<a id="9"></a>

## Item 9

**This part rides on [setup-and-update.md](setup-and-update.md).** That carve
redraws the install surface for every platform. What is Windows-specific about it
is the binary itself: setup copies the running executable into `~/.ark`, and
`update` may have to replace a running `~/.ark/ark.exe`. Measured on build 26200
(2026-09-24, table in that carve's *Measured* section): a running exe can copy
itself and rename itself aside, but cannot be overwritten in place, and the
renamed-aside copy cannot be deleted until its process exits. So the self-copy
needs nothing special, and `update` needs rename-aside plus a deferred delete.
The Windows test for this part is those two paths run against the real binary.

**Where it stands (reviewed 2026-10-21).** Most of 9.1 is already proven.
`SwapExe` (`cmd/ark/update_windows.go`) does the rename-aside. #142 verified the
staged-update path on `windows-gpu`: the swap, the chain, the asset refresh, and
`ark.exe.old` swept at every start. Setup's self-copy ran end to end as the
standard user `arkfresh` on 2026-10-13.

What no test has run, on any platform, is `update`'s **download path**. The unit
tests inject a `fakeFetcher` (`cmd/ark/staged_update_test.go`), and #142's live
run used a hand-staged binary. A live test cannot simply use GitHub either,
for two reasons:

- **The release and the updater disagree on names.** The updater fetches
  `ark-<version>-<os>-<arch>` (`.exe` on Windows) and a `SHA256SUMS` file
  (`releaseAssetName` and `checksumAssetName` in `cmd/ark/update.go`). The
  Makefile's `release` target builds unversioned names (`ark-windows-amd64.exe`),
  `release-archives` zips or tars them, and nothing writes `SHA256SUMS`. v0.5.0
  has one asset, `ark-0.5.0-linux-amd64`, and no checksums, so `update` is
  expected to fail at the checksum step on every platform today. That
  expectation comes from reading the code; it has not been run.
  [setup-and-update.md](setup-and-update.md) noted that the release process has
  to change with `update`; no part tracked it until 9.3.
- **`releases/latest` skips drafts and pre-releases**, so a GitHub test needs a
  published release, and the release URL is a constant with no override.

**9.3** makes `make release` produce exactly what the updater fetches: one
asset per platform under `releaseAssetName`'s pattern, and a sha256sum-format
`SHA256SUMS` covering them. Worth a check that ties the two together so they
cannot drift again, e.g. a test that runs the updater's own name and checksum
parsing over a `make release` output directory.

**9.4** adds a release-source override: an environment variable (name to
settle, e.g. `ARK_RELEASE_URL`) naming a GitHub-shaped release document that
`githubReleaseFetcher` reads instead of the API constant. A test then serves real
`make release` output over plain HTTP and runs a full `ark update` on
`windows-gpu` with nothing published. It stays useful for every later release as
a pre-publish check.

**9.2** itself is then that run: an older stamped `ark.exe` installed in a
throwaway `USERPROFILE`, a newer `make release` served through 9.4, and plain
`ark update` from the installed exe, with and without a server running.

**The 9.2 run (2026-10-21, `windows-gpu`, scheduled task as `zot`).** Setup
installed 0.5.0 into `USERPROFILE=C:\t147` (assets, Vulkan llama libraries with
the MSVC runtime, the model). A 0.5.1 `make release` was served from the dev box
(`10.10.10.2:8765`) beside a rig-written release document. Results:

- **No server running: passed.** The installed `ark.exe update` read the served
  document, fetched the 0.5.1 exe and `SHA256SUMS` (the HTTP log shows each
  request), renamed itself to `ark.exe.old`, and the new exe reported 0.5.1. The
  next `serve` start swept `ark.exe.old`.
- **Server running: passed, after a ui-engine fix.** The first attempt's
  graceful stop never finished. `LuaSession.Shutdown` panicked with "close of
  closed channel": `cancelTimer` ignored its own already-cancelled flag, so an
  interval Lua had cleared (the mcp shell's `whenSys` wait, since 7.3) was
  closed a second time. Every graceful stop of ark since 7.3 hung until the
  timeout, on Linux too. Fixed in ui-engine (R102 honoured,
  `test-SessionTimer.md`). With both binaries rebuilt, update stopped the 0.5.0
  server, installed 0.5.1, and left no ark process.
- **A pitfall for the next run:** the "old" binary must already contain
  `ARK_RELEASE_URL`. The first attempt installed a 0.5.0 built before #146,
  which correctly ignored the variable and asked GitHub.
- **Found, not fixed:** update reported three bundled html files as
  user-modified. Nobody had edited them: every `serve` start injects the theme
  block between their `<!-- #frictionless -->` markers
  (`flib.InjectAllThemeBlocks`), so they never match the manifest again and no
  update will refresh them. It is not Windows-specific; it belongs to
  [setup-and-update.md](setup-and-update.md).

<a id="10"></a>

## Item 10

winget is Windows' package manager and the nearest thing it has to apt. A
manifest can declare `PackageDependencies` (here `Microsoft.VCRedist.2015+.x64`),
which winget installs first, and a "portable" package type that fits a single
binary. That gives Windows users the install they expect, `winget install ark`,
with the runtime handled by the system. It is a distribution channel beside
`ark setup`, not a replacement: `setup` still owns `~/.ark`, and 6.2's app-local
runtime still covers a machine where ark arrived some other way. Not examined:
the manifest details, the review process for the public winget repository, and
whether the redistributable's admin prompt is acceptable on that path. The
dependency support was described from memory and wants checking against
winget's documentation before this part is queued.

<a id="11"></a>

## Item 11

**The scenario (Bill, 2026-10-05).** Windows users who run Claude Code in WSL2
will mostly keep their projects on NTFS, since the Ubuntu image is close to
disposable. So ark has to launch on Windows, beside the projects, while Claude,
inside WSL2, still uses the ark CLI. That CLI path is what needs testing from
inside WSL2. Three questions, none answered yet:

1. **How the CLI reaches the server.** WSL2 can run Windows executables
   directly (interop), so Claude could call `ark.exe`, which then dials the
   AF_UNIX socket under `C:\Users\…\.ark` as any Windows process does. A Linux
   `ark` inside WSL2 would have to cross the VM boundary instead; AF_UNIX
   interop between Windows and WSL2 is believed unsupported (WSL1 had it), which
   would leave TCP, a transport ark's CLI does not have. To verify.
2. **Paths, both directions.** Claude works in `/mnt/c/Users/…`; ark records
   `C:\Users\…`. Arguments reach `ark.exe` untranslated, and the Windows paths
   in ark's output are not paths Claude's tools can open. Something translates
   (`wslpath` does the mapping): a wrapper, or ark itself.
3. **Skills and agents.** Claude Code inside WSL2 reads the `~/.claude` in its
   Linux home, not the Windows one `ark setup` links into, so the WSL side needs
   its own install of the ark skills and agents.

**DECIDED (Bill, 2026-10-05):**

1. **`ark.exe` through interop**, because it keeps ark a single binary. No Linux
   build and no TCP transport for the CLI.
2. **Paths are translated by ark, told it was called from WSL2.** Ideally
   `ark.exe` detects the WSL2 caller by itself (how is to be measured: what a
   process launched through interop can see in its environment or its parent).
   Failing that, a Linux shim script that runs `ark.exe` adds an argument such
   as `--wsl2` or sets an environment variable.
3. **No WSL-specific skills or agents.** WSL's `~/.claude` entries are symlinks
   into the Windows install under `/mnt/c`, and the shim from 2 covers what
   differs.

**Measured 2026-10-05 on `windows-gpu` (WSL 3.0.1, kernel
6.18.40.1-microsoft-standard-WSL2, Ubuntu 26.04.1):**

- **Question 1 is answered: the interop route works.** With `ark serve`
  running natively under `zot`'s Windows home, `/mnt/c/…/ark.exe status` run
  from a Linux shell inside WSL2 reached it (`server: running`, index and
  chunks reported).
- **Detection (decision 2) has a signal: the parent process.** A Windows
  process launched from a Linux shell through interop has `wsl.exe` as its
  parent. The Linux side's `WSL_DISTRO_NAME`, `WSL_INTEROP` and
  `WSL2_GUI_APPS_ENABLED` do not reach it, because `WSLENV` is empty, so the
  environment is no signal without a shim. It holds both for a session started
  as `wsl.exe -e …` (non-interactive, as Claude Code's Bash tool runs) and for
  an interactive WSL terminal in Windows Terminal (checked by Bill). The latter
  adds `WSLENV=WT_SESSION:WT_PROFILE_ID:`, which Windows Terminal sets in any
  tab, WSL or not, so those variables are no WSL signal either.
- **The working directory is translated for free:** `/mnt/c/Users/zot` arrives
  as `C:\Users\zot`. Arguments and output paths are what ark must translate.
- **DECIDED (Bill, 2026-10-05), the design:**
  1. **Translation lives in the client only.** The `ark.exe` that WSL
     launches detects its `wsl.exe` parent and translates; the server is
     untouched. Arguments matching `/mnt/<letter>/…` become `<LETTER>:\…`.
     Output is translated generically: stdout and stderr pass through a filter
     that turns `X:\…`, including JSON's escaped `X:\\…`, into `/mnt/x/…`,
     rather than each command converting its own paths.
  2. **WSL-native paths are an error for now.** `/home/…` would need the
     distro name, which does not reach the Windows process; projects belong
     under `/mnt/c`, and the error says so.
  3. **The WSL side is created from inside WSL**, by
     `eval "$(/mnt/c/Users/<you>/.ark/ark.exe wsl-init)"`. `wsl-init` prints a
     script that makes `~/.ark/ark` a shim running `ark.exe`, links the rest
     of `~/.ark` into the Windows install, and links `~/.claude`'s ark skills
     and agents the same way. A Windows process cannot make Linux symlinks,
     and `wsl.exe` from Windows reaches only the default distro, which may not
     be the user's.
- **The WSL2 VM has no network** here (`eth0` has an address, pings fail),
  likely WSL's NAT under nesting. Interop needs none; `apt` inside will.

**Verified live (2026-10-05, `windows-gpu`, WSL 3.0.1, Ubuntu).** As the WSL
user, `eval "$(/mnt/c/Users/zot/.ark/ark.exe wsl-init)"` returned 0 and left
`~/.ark` a real directory of links into `/mnt/c/Users/zot/.ark` plus the marked
`ark` shim, and `~/.claude/agents/ark` and each skill linked to the Windows
install's real directories. Then, against `ark serve` running natively:
`~/.ark/ark status` reported the server; `files` printed `/mnt/c/Users/zot/…`
paths (translated from `C:\…`); `fetch /mnt/c/Users/zot/.ark/tags.md`
returned the file; `fetch /home/zot/notes.md` was refused with the message
pointing at `/mnt/c`; and a bare `ark.exe files`, no shim, still translated,
so the `wsl.exe` parent check works on its own. Not exercised: Ctrl-C,
`luhmann attach`, a custom automount root (gaps O221, O222).

**Getting WSL2 onto a test VM.** The test VMs are Windows guests of QEMU, which dockur runs inside a podman
container with KVM acceleration. WSL2 runs its Linux kernel in a lightweight
Hyper-V VM, so WSL2 inside these guests is nested virtualization: Hyper-V
inside a KVM guest. That needs the host's KVM to allow nesting and QEMU to
pass the CPU's VT-x (`vmx`) through to the guest.

**Measured 2026-10-04, on `windows-gpu`.**

- The host allows it: `kvm_intel` reports `nested = Y`, and the CPU has `vmx`.
- dockur withholds it: QEMU runs with `-cpu host,kvm=on,…,-vmx,hv_passthrough`,
  so the guest sees no VT-x (`VMMonitorModeExtensions=False`, `SLAT=False`).
  `VirtualMachinePlatform`, `Microsoft-Windows-Subsystem-Linux` and
  `Microsoft-Hyper-V-All` are all disabled.
- The switch is dockur's `VMX` environment variable, read by its base image
  (qemus/qemu `src/proc.sh`). Unset, it defaults to `Y`, except for a Windows
  guest, where it becomes `N` with the comment "Turn off nested virtualization
  by default to prevent a crash caused by a recent Windows update". The
  comment names neither the update nor the crash.

**The plan.** Add `-e VMX=Y` to the chosen VM's creation script and re-create
the container; the Windows disk lives in the storage directory and survives,
so the experiment is cheap to try and to undo. Confirm the guest now reports
`VMMonitorModeExtensions=True`, enable `VirtualMachinePlatform` and
`Microsoft-Windows-Subsystem-Linux`, reboot, and run `wsl --install`. The crash
dockur warns about is the risk to watch for on first boot.

**DECIDED (Bill, 2026-10-05): `windows-gpu` is the only test VM.** A first
attempt on the CPU-only `windows` VM got VT-x into the guest
(`VMMonitorModeExtensions=True`, a clean boot), but after enabling the two
features and rebooting, its sshd never answered within about 20 minutes; it was
stopped without a look at its screen, so whether that was dockur's crash or a
slow feature install is unknown. That VM is back on its original settings, with
the two features still enabled on its disk. (Exit code 143 from `podman stop`
proves nothing: a healthy dockur VM stops with it too.) Before repeating the
step on `windows-gpu`, its storage directory was copied, sparse, while QEMU was
stopped (22 GB on disk; `~/windows-gpu/storage.bak-20261005` on the host), so a
bad outcome restores the working passthrough VM.

**What nested Hyper-V took (2026-10-05).** Three CPU configurations, with the
WSL features enabled each time:

| QEMU `-cpu` | result |
|---|---|
| `host,…,+hypervisor,…,hv_passthrough` (dockur's default plus `VMX=Y`) | hangs at the firmware logo, one vCPU at 100% |
| `host,…` with `HV=N`, then also `VM=N` | boots, Windows' own Hyper-V runs, but starting a WSL2 VM resets the guest (Kernel-Power 41, no dump) |
| `Skylake-Client-noTSX-IBRS,kvm=on,l3-cache=on,vmx=on,mpx=off` (`CPU_MODEL`, `CPU_FLAGS`, with `VMX=Y HV=N VM=N`) | **works**: WSL2 starts, no reset |

The decisive change was a named CPU model instead of `host`: `host` hands the
guest every VMX capability of the real CPU, and KVM's nested support evidently
cannot back all of them for a nested Hyper-V. The recipe came from
dockur/windows#1191 (`Broadwell-noTSX-IBRS,hypervisor=off,vmx=on,…`); the model
here is the nearest to the host's i9-10885H (Comet Lake: Skylake core, no TSX).
Installing WSL itself needed the MSI from microsoft/WSL's GitHub releases:
Windows' inbox `wsl.exe` is a stub, and `wsl --install` fetched nothing from an
ssh session.

---

When a part lands, its decision goes to `specs/` and `design/` as a requirement
or a comment at the code, and this carve points at where — the discipline the
carves directory exists to enforce. Item 1 already did: R3120's Windows
narrowing lives in the code comments, and this carve names it rather than
holding the fact alone.
