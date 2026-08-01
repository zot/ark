# Sequence: attach + fan-out
**Requirements:** R3117, R3118, R3119, R3120, R3121, R3123, R3333, R3334, R3335, R3336, R3337, R3338, R3339, R3340, R3341, R3342, R3343

Actors: User (CLI) → PtyAttach → PtyHost → pty/child.

One numbered diagram.

1.1. `ark luhmann attach` — PtyAttach puts the local terminal in raw mode and connects to PtyHost over the unix socket (R3123).
1.2. PtyAttach reports its terminal size; PtyHost registers it as a client and recomputes the pty size = min over all clients, then `SIGWINCH`es the child (R3117, R3120).
1.3. Child output → PtyHost fans out to every client; a client that cannot keep up is dropped, never blocking the fan-out (R3118). The path is a cycle of ownership hand-offs with no lock on it (R3333):
1.3.1. A read goroutine owning the read buffer blocks on the master, and on return hands the buffer to the actor in a posted closure and terminates (R3333, R3334, R3335).
1.3.2. In the actor: append the bytes into each connection's pending buffer, dropping any connection that would exceed the byte cap (R3340). A connection already writing is left alone — the in-flight writer will pick the bytes up (R3336).
1.3.3. In the actor, still: spawn the successor read goroutine with the same buffer, now that nothing else holds it (R3333). A chain whose master is no longer the hosted one still reads, but distributes nothing (R3343).
1.3.4. Per connection not already writing: swap pending with the spare, set `writing`, and spawn a write goroutine owning the filled buffer, which writes it whole to its transport off the actor (R3336, R3338, R3341).
1.3.5. The writer empties the buffer and posts a continuation carrying it back: swap and spawn again if output accumulated, else clear `writing` (R3336, R3337). A continuation for a connection that is already gone spawns nothing (R3342); a failed write deregisters instead (R3118).
1.4. Client stdin → PtyAttach forwards to PtyHost → a serialized write to the child so clients' bytes cannot interleave (R3119).
1.5. Local `SIGWINCH` → PtyAttach reports the new size → PtyHost recomputes the min → `SIGWINCH`es the child (R3120).
1.6. Detach escape (or a dropped connection) → PtyHost deregisters the client and recomputes the min, so the pty **grows** if the smallest client just left (R3120, R3121).
1.7. The session keeps running with the remaining clients, or with zero clients attached (R3121).
