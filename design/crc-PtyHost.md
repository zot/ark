# PtyHost
**Requirements:** R3114, R3115, R3116, R3117, R3118, R3119, R3120, R3121, R3122, R3124, R3125, R3126, R3127, R3128, R3136, R3139, R3323, R3324, R3326, R3327, R3328

The server-side owner of one hosted Claude Code pty session. Holds the pty
master and the `claude` child, multiplexes the child's byte stream out to a set
of attached clients, and owns the launch/stop/status lifecycle. A Closure Actor
(ChanSvc): access to the master, the child, and the client set is serialized
through its channel, so no lock is held across pty I/O.

## Knows
- master: the pty master (creack/pty) for the hosted session; nil when none is hosted (R3116).
- child: the `claude` child process; dies on `ark stop`, never auto-relaunched (R3114, R3116).
- clients: the set of attached clients, each addressed through the transport-agnostic client interface (R3117); zero clients is a valid running state (R3121), and the ordinary one for a detached session.
- clientCount: a lock-free mirror of the client-set size, republished inside the actor after every mutation, so the reader can skip the fan-out when nobody is attached without an actor round-trip (R3328). An optimization of actor-owned state, never a second source of truth — the same role masterPtr plays for the input path.
- sessionID: the hosted session's Claude Code id, learned from the seat claim at launch (R3126).
- bootstrap: the first-input string sent at launch (default `load /luhmann`, R3122).
- (no trust state) — the "trust this folder" accepter is **per-launch**, owned by that launch's readLoop, not a field here (R3327).

## Does
- launch: reject if a session is already hosted; clear any stale seat claim (ForceReleaseSeat, R3126); fork the pty (cwd `~/.ark/luhmann`), start `claude` with a clean child environment (childEnv, R3127), then run the content-free confirmation protocol (R3126) — send the bootstrap, await the seat claim (the sole authoritative confirmation) — and return success or a timeout error. The sole spend-consent gate (R3114, R3122). **Publishes its outcome before returning** — `@luhmann-hosted: <session>` on confirmation, `@luhmann-launch-failed: <reason>` on abandonment, both by replacing `tmp://luhmann/launch.md` so only changed pairs fire (R3326). Publishing *here* rather than at a caller is what makes every way in announce identically: a CLI `ark luhmann launch` over the API and the `sys.luhmannLaunch()` binding (R3325) pass through this one point, so a UI showing the terminal wakes on a launch it did not start. Same chokepoint reasoning as finalize's, in the other direction.
- childEnv: build the child's environment from the server's, stripping the Claude Code session-identity markers (`CLAUDECODE`, `CLAUDE_CODE_*`, `AI_AGENT`) so the hosted `claude` comes up as a fresh top-level session, not a detected sub-session that refuses to complete a turn; credentials (`ANTHROPIC*`) and other config pass through, and `TERM` is ensured (R3127). It also *sets* `ARK_MANAGED_PTY=1` so the hosted session can tell it was started by the managed launch (vs. a bare `/luhmann` elsewhere) and surface the detach hint (R3139).
- trustAccepter (per-launch object, R3327): for a launch into a directory Claude Code has not seen, watch the child's early output and, on the "trust this folder" dialog, select the "Yes, I trust" option by the number read from the escape-stripped stream — before the bootstrap, so the menu does not consume it. Reads the number rather than assuming a default. Armed only for new projects, disarmed once handled or a timeout elapses, so it cannot misfire on a running session (R3128). **Owned by one readLoop, so its scan buffer needs no guard** — held on the host it was shared, because a teardown leaves the old readLoop draining through the grace window while the reservation check already permits a new launch, and the departing reader could then answer the arriving launch's dialog from the dying session's output (R3327).
- broadcast: write the child's output to every attached client; drop a client whose bounded buffer overflows rather than block the fan-out (R3118). ark never reads the child's output for session *state* — that comes from the JSONL (R3115). **Allocates only when someone will retain the bytes** (R3328): the reader owns one reused scratch buffer, and a client queues its slice to drain on its own goroutine, so broadcast copies to an exact-size buffer when the client set is non-empty — and the reader skips this call outright when it is empty, reading the lock-free client-count mirror rather than paying an actor round-trip to iterate an empty map.
- mergeInput: write each client's input to the child, serialized so two clients' bytes cannot interleave mid-sequence (R3119).
- resize: recompute the pty size as the minimum of all attached clients' sizes on every attach, detach, or resize, and `SIGWINCH` the child; a disconnect re-runs the min, so the pty grows when the smallest client leaves (R3120).
- forceRepaint: force the child to repaint the whole screen by toggling the pty size one row (shrink, **hold `ptyRepaintNudge`**, then restore) — a real `SIGWINCH` the Ink TUI redraws on, since a same-size `TIOCSWINSZ` is a kernel no-op. The hold matters: a synchronous shrink-then-restore coalesces and the child sees no net change, so the shrink must persist to be observed. Requested over a `repaint` client→host frame (a third frame kind beside input and resize), so one mechanism serves every transport; used on attach (a new client sees the full screen, not only later output) and on a detach-cancel to wipe the prompt. Needed because ark holds no virtual screen (R3115) — only the child can redraw (R3136).
- attach / detach: register / deregister a client; the session survives zero clients (R3121).
- finalize: the single teardown chokepoint, idempotent on master identity, reached both from `stop` and from readLoop's EOF when the child exits on its own. It discharges **both** teardown obligations, so neither path can skip one (R3323): release the Luhmann seat lease, then record an exit for every pool secretary the session was running, so the monitoring log shows no ghosts. The recording runs **off** the actor (the appends are DB writes) and **after** the release, which additionally makes a requeue-driven `pumpCuration` a no-op instead of standing a fresh secretary up into a session being torn down. The caller's reason is carried into the records (R3324). Then clear the actor state, close the clients, and hand the child + master to an off-actor graceful shutdown (R3114, R3116, R3125).
- stop: the graceful-teardown entry point, not a bare kill — ask the child to exit and let it flush its transcript before the kill, and name the reason as ark's own. The obligations themselves belong to finalize (R3125, R3323, R3324).
- status: report whether a session is hosted (master non-nil) plus the pool-secretary roster count from the supervisor state (R3124).

## Collaborators
- attached clients: the transport-agnostic client interface it broadcasts to and merges input from — PtyAttach (CLI, unix socket) and PtyBrowser (browser, websocket) are its two implementations (R3117, R3144). Connected-to.
- Luhmann seat lease: launch waits for the session's `ark luhmann next --first` claim on it — the authoritative confirmation and the session-id source (R3126); the teardown releases the same lease, on either path down (R3125, R3323). Connected-to, not part-of.
- creack/pty: the pty master primitive.
- Server: holds the PtyHost and exposes launch/attach/status/stop over the unix socket (R3116).

## Sequences
- seq-pty-launch.md
- seq-pty-attach.md
