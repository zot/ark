# PtyHost
**Requirements:** R3114, R3115, R3116, R3117, R3118, R3119, R3120, R3121, R3122, R3124, R3125, R3126

The server-side owner of one hosted Claude Code pty session. Holds the pty
master and the `claude` child, multiplexes the child's byte stream out to a set
of attached clients, and owns the launch/stop/status lifecycle. A Closure Actor
(ChanSvc): access to the master, the child, and the client set is serialized
through its channel, so no lock is held across pty I/O.

## Knows
- master: the pty master (creack/pty) for the hosted session; nil when none is hosted (R3116).
- child: the `claude` child process; dies on `ark stop`, never auto-relaunched (R3114, R3116).
- clients: the set of attached clients, each addressed through the transport-agnostic client interface (R3117); zero clients is a valid running state (R3121).
- sessionID: the hosted session's Claude Code id, learned from the seat claim at launch (R3126).
- bootstrap: the first-input string sent at launch (default `load /luhmann`, R3122).

## Does
- launch: reject if a session is already hosted; fork the pty (cwd `~/.ark/luhmann`), start `claude`, then run the content-free confirmation protocol (R3126) — await the second JSONL record, send the bootstrap, await the seat claim — and return success or a timeout error. The sole spend-consent gate (R3114, R3122).
- broadcast: write the child's output to every attached client; drop a client whose bounded buffer overflows rather than block the fan-out (R3118). ark never reads the child's output for session *state* — that comes from the JSONL (R3115).
- mergeInput: write each client's input to the child, serialized so two clients' bytes cannot interleave mid-sequence (R3119).
- resize: recompute the pty size as the minimum of all attached clients' sizes on every attach, detach, or resize, and `SIGWINCH` the child; a disconnect re-runs the min, so the pty grows when the smallest client leaves (R3120).
- attach / detach: register / deregister a client; the session survives zero clients (R3121).
- stop: graceful teardown — release the Luhmann seat lease and record the hosted session's pool secretaries' exits, then kill the child, so the monitoring log shows no ghosts (R3125).
- status: report whether a session is hosted (master non-nil) plus the pool-secretary roster count from the supervisor state (R3124).

## Collaborators
- attached clients: the transport-agnostic client interface it broadcasts to and merges input from — PtyAttach is phase-1's implementation, a browser client a later one (R3117). Connected-to.
- Luhmann seat lease: launch waits for the session's `ark luhmann next --first` claim on it — the authoritative confirmation and the session-id source (R3126); stop releases the same lease (R3125). Connected-to, not part-of.
- creack/pty: the pty master primitive.
- Server: holds the PtyHost and exposes launch/attach/status/stop over the unix socket (R3116).

## Sequences
- seq-pty-launch.md
- seq-pty-attach.md
