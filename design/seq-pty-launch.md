# Sequence: pty launch + content-free confirmation
**Requirements:** R3114, R3116, R3122, R3126, R3128

Actors: User (CLI) → LuhmannCLI → PtyHost → pty/child → Luhmann seat lease.

One numbered diagram.

1.1. `ark luhmann launch [--bootstrap]` — the user runs the consent-gate verb; LuhmannCLI proxies it to the server's PtyHost (R3114, R3122).
1.2. PtyHost rejects if a session is already hosted — one at a time (R3122).
1.2.1. PtyHost clears any stale seat claim (`ForceReleaseSeat`) so the new session's `--first` is the one it observes; the managed launch is the authoritative start (R3126).
1.3. PtyHost forks the pty with cwd `~/.ark/luhmann` and starts `claude` as its child (R3116).
1.3.1. If the cwd is new to Claude Code (no `~/.claude/projects/<cwd-encoded>/` yet), PtyHost watches the child's early output for the "trust this folder" dialog and selects the "Yes, I trust" option by the number read from the escape-stripped stream — before the bootstrap, so it reaches the message box, not the menu. Best effort: proceeds after a timeout if no dialog appears (R3128).
1.4. PtyHost lets the TUI settle into raw mode, then writes the bootstrap (`load /luhmann`) to the pty master (R3122, R3126).
1.5. The session loads `/luhmann`, whose startup runs `ark luhmann next --session <id> --first`, claiming the seat lease (R3126).
1.6. PtyHost, waiting on the seat lease, observes the claim — the authoritative, content-free confirmation — records `<id>` as the hosted session id, and returns launch success; a timeout on the wait → launch error (R3126).
