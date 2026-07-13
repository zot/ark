# Sequence: attach + fan-out
**Requirements:** R3117, R3118, R3119, R3120, R3121, R3123

Actors: User (CLI) → PtyAttach → PtyHost → pty/child.

One numbered diagram.

1.1. `ark luhmann attach` — PtyAttach puts the local terminal in raw mode and connects to PtyHost over the unix socket (R3123).
1.2. PtyAttach reports its terminal size; PtyHost registers it as a client and recomputes the pty size = min over all clients, then `SIGWINCH`es the child (R3117, R3120).
1.3. Child output → PtyHost broadcasts to every client; a client whose bounded buffer overflows is dropped, never blocking the fan-out (R3118).
1.4. Client stdin → PtyAttach forwards to PtyHost → a serialized write to the child so clients' bytes cannot interleave (R3119).
1.5. Local `SIGWINCH` → PtyAttach reports the new size → PtyHost recomputes the min → `SIGWINCH`es the child (R3120).
1.6. Detach escape (or a dropped connection) → PtyHost deregisters the client and recomputes the min, so the pty **grows** if the smallest client just left (R3120, R3121).
1.7. The session keeps running with the remaining clients, or with zero clients attached (R3121).
