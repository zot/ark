# PtyBrowser
**Requirements:** R3141, R3142, R3143, R3144, R3149

The server-side browser transport for the hosted pty: ark's own
`gorilla/websocket` endpoint at `GET /luhmann/pty`, plus the websocket client
adapter that plugs into PtyHost's fan-out. One implementation of the
transport-agnostic PtyClient interface (R3117, R3144) — the browser counterpart
to the CLI's unix-socket transport. This card covers only the Go endpoint and
its wire; the xterm.js terminal that drives it is
crc-LuhmannTerminalElement.md.

## Knows
- conn: the upgraded `gorilla/websocket` connection to one browser client.
- out: the bounded output channel a write goroutine drains, so a stalled browser tab overflows and is dropped rather than stalling the fan-out (R3142, R3144).

## Does
- handleLuhmannPty: on `GET /luhmann/pty`, reject when no session is hosted, then run ark's own gorilla upgrade — not ui-engine's view-diff socket (R3141). Read the first control frame; it must be a resize (R3143), which supplies the size to Attach to PtyHost as one client (R3144). Then loop: a binary message is input, merged onto the child (R3142, R3119); a text message is a control frame — resize recomputes the smallest-wins minimum (R3120), repaint forces a child redraw (R3136). Connection close deregisters the client (Detach, R3121).
- parsePtyControl: parse a text control frame's JSON into a resize (cols, rows) or a repaint, returning an error for malformed or unknown-type input. Pure, unit-tested (test-PtyBrowser.md).
- Write: enqueue an output chunk onto out non-blockingly; false when the bounded buffer overflows, signalling PtyHost to drop this client (R3118, R3144). The write goroutine sends each chunk as one binary websocket message (R3142).
- Close: tear the websocket down once (drop, detach, or host stop).
- route registration: `GET /luhmann/pty` is wired in `registerContentRoutes` (crc-Server.md) on the UI HTTP server, so it exists whenever the UI runtime is running (R3141). `GET /luhmann/pty-status` is mirrored there too — the same handler the unix-socket mux serves for `ark luhmann status` — so a browser client can ask why a handshake failed (R3149); the rejected upgrade at handleLuhmannPty reaches the page as an unexplained 1006 close, indistinguishable from a bouncing server.

## Collaborators
- PtyHost: attaches as one client of the transport-agnostic interface (R3117, R3144) — receives broadcast output, sends input, resize, and repaint. Connected-to, not part-of.
- gorilla/websocket: the connection upgrade and binary/text message framing (R3141, R3142, R3143).
- Server: holds the PtyHost and the UI runtime; the handler is a Server method registered through `uiRuntime.UIHandleFunc` (R3141). Connected-to.
- LuhmannTerminalElement: the browser consumer that opens the socket, sends resize / repaint / input, renders output, and probes the status mirror when the socket closes (R3149). Connected-to, not part-of.

## Sequences
- seq-pty-attach.md
