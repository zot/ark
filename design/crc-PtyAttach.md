# PtyAttach
**Requirements:** R3117, R3120, R3123, R3137, R3138, R3140

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
- requestRepaint: send a `repaint` frame to the host immediately after connecting, so this freshly attached (or re-attached) client sees the current full screen at once instead of only subsequent output (R3137). The host does the redraw via a forced `SIGWINCH` (R3136); the client cannot, since neither it nor ark models the screen (R3115).
- detachPrompt: on `Ctrl-]`, paint a transient one-line prompt (bottom row) acknowledging the escape and naming the keys, so pressing it is never silent (R3138). `d`/`D` detaches (R3123); any other key cancels — discard it and the prefix (a mode key, not input) and send a `repaint` frame (R3136) to wipe the prompt. On detach the client clears the prompt itself (bottom-row erase) before exit — instant, no child round-trip, which would race the disconnect.
- restoreTerminal: on exit (detach or disconnect), restore the terminal fully — `term.Restore` resets the termios line discipline, plus a sanitizing escape sequence undoing the child's DEC private modes (cursor visibility, bracketed paste, mouse reporting, scroll region, SGR) that `term.Restore` does not touch, so the shell returns clean without a manual `reset` (R3140).

## Collaborators
- PtyHost: the server-side host it attaches to as one client (R3117); receives broadcast output, sends input and size.
- local terminal: raw mode and `SIGWINCH` (R3123, R3120).

## Sequences
- seq-pty-attach.md
