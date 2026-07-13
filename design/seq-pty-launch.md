# Sequence: pty launch + content-free confirmation
**Requirements:** R3114, R3116, R3122, R3126

Actors: User (CLI) → LuhmannCLI → PtyHost → pty/child → Luhmann seat lease.

One numbered diagram.

1.1. `ark luhmann launch [--bootstrap]` — the user runs the consent-gate verb; LuhmannCLI proxies it to the server's PtyHost (R3114, R3122).
1.2. PtyHost rejects if a session is already hosted — one at a time (R3122).
1.3. PtyHost forks the pty with cwd `~/.ark/luhmann` and starts `claude` as its child (R3116).
1.4. PtyHost locates the new session JSONL under `~/.claude/projects/<cwd-encoded>/` and waits for its **second record** — the liveness gate; timeout → launch error (R3126).
1.5. PtyHost writes the bootstrap (`load /luhmann`) to the pty master (R3122, R3126).
1.6. The session loads `/luhmann`, whose startup runs `ark luhmann next --session <id> --first`, claiming the seat lease (R3126).
1.7. PtyHost, waiting on the seat lease, observes the claim — the authoritative confirmation — records `<id>` as the hosted session id, and returns launch success; a timeout on the wait → launch error (R3126).
