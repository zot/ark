# PtyAttach
**Requirements:** R3117, R3120, R3123

The CLI-side `ark luhmann attach` client: a raw-mode terminal loop that connects
to the hosted session over the unix socket. It is one implementation of
PtyHost's transport-agnostic client interface (R3117); the unix-socket transport
is phase 1's only client, with the browser xterm.js client a later, separate
implementation of the same contract.

## Knows
- conn: the unix-socket connection to the server's PtyHost.
- size: the local terminal's current width×height, reported to the host for the smallest-wins resize (R3120).
- detachSeq: the tmux-style escape sequence that ends the attach without stopping the session (R3123).

## Does
- attach: put the local terminal in raw mode, connect to the PtyHost, then pipe stdin → host and host → stdout until detach or disconnect (R3123). Detaching leaves the session running.
- reportSize: send the terminal size on connect and on every local `SIGWINCH`, so the host can recompute the min (R3120).
- receiveOutput: write the host's broadcast bytes to stdout — the client-interface output sink (R3117).
- sendInput: forward local stdin to the host — the client-interface input source (R3117) — except the detach escape, which it consumes locally (R3123).

## Collaborators
- PtyHost: the server-side host it attaches to as one client (R3117); receives broadcast output, sends input and size.
- local terminal: raw mode and `SIGWINCH` (R3123, R3120).

## Sequences
- seq-pty-attach.md
