# LuhmannTerminalElement
**Requirements:** R3150, R3152, R3153, R3154, R3155, R3156, R3158, R3159, R3160, R3161

The `<luhmann-terminal>` custom element: a live terminal on the ark-hosted
Luhmann session, driven over R3141's websocket — the browser's `ark luhmann
attach` (R3150). A composable primitive with no chrome of its own: no launch
button, no lamp, no panel frame, no status text. It is the thin shell that wires
PtyProtocol's rules to an xterm instance and a socket; its placement in the
Frictionless app is a separate UI slice (#39).

## Knows
- term: the xterm `Terminal`, which owns escape-sequence parsing and the screen — ark models neither (R3115, R3153).
- fit: xterm's `FitAddon`, converting the element's CSS box into a (cols, rows) pair (R3154).
- ws: the open websocket, or null when disconnected (R3151). Nulled before close on detach, so a pending `onclose` identifies itself as superseded and stays silent.
- session: the session id the last probe reported, carried on the status event (R3157, R3159).
- attempt: the reconnect attempt counter PtyProtocol's backoff reads; reset on a successful connect (R3157).
- retryTimer / resizeTimer: the pending reconnect and the debounce timer, both cleared on disconnect (R3154, R3157).
- observer: the `ResizeObserver` watching the element's box (R3154).

These are element properties, not closure-captured variables — inspectable on
the live element from devtools, and they cannot go missing under DOM surgery
(same rule as crc-PdfChunkElement.md's caches).

## Does
- connectedCallback: build the terminal with the theme read from ark's `--term-*` variables (R3155), open it into the element, observe its box (R3154), and classify (R3150).
- classify: probe the status route, then act — the probe gates every socket open rather than only explaining a failure, because `asleep` is the common case (Luhmann is usually not running) and socket-first would answer an ordinary page load with a doomed connection and a 409. `asleep` stops; `hosted` on a first attempt opens at once; anything else announces `waiting` and re-enters after the backoff, re-probing rather than blindly reopening (R3157). Returns silently if the element detached mid-probe.
- openSocket: on open, run the handshake in order — fit, send the resize frame **first** (R3143 closes a connection that opens with anything else), then send repaint so the child redraws at once rather than leaving a blank screen (R3152). Announce `connected` and reset `attempt`. A close re-enters classify.
- onMessage: write a binary message's bytes straight to xterm (R3153). Ignore text messages — the host sends none, so one arriving is not a case to guess at.
- onInput: forward xterm's `onData` (UTF-8) and `onBinary` (low-byte) through PtyProtocol's encoders as binary messages (R3153).
- onResize: on a box change, debounce, re-fit, and send a resize frame — undebounced, a drag would `SIGWINCH` the child on every intermediate size (R3154). Reports only this client's size; the minimum across clients is PtyHost's (R3120).
- probe: fetch `GET /luhmann/pty-status` (R3149) and parse it, reporting unreachable for a network failure or a non-OK status. Runs immediately on a close, ahead of any backoff, so `asleep` is reported at once rather than one backoff late (R3157). Never calls launch — the element attaches to what exists or reports that nothing does (R3158, R3114).
- announce: dispatch the bubbling, composed `luhmann-terminal-status` event carrying `{state, session, attempt}` — the element's entire host interface (R3159). `session` is what the probe last reported, which is why it is known before `connected` is ever announced.
- disconnectedCallback: close the socket, dispose the terminal, disconnect the observer, clear both timers. This *is* detach; the session survives (R3121, R3156).
- key handling: none. `Ctrl-]` is ordinary input — no detach prompt, no escape state machine (R3123, R3138 exist for a constraint the browser lacks), and no DEC-mode sanitize on exit (R3140 restores the user's real terminal; here the terminal is the disposed instance) (R3156).
- registration: the bundle registers the element on load and injects xterm's inlined stylesheet once, so a page needs one `<script>` and no stylesheet link (R3160). The injected sheet is prepended with `luhmann-terminal { display: block; }` — a custom element is inline by default, and width/height do not apply to an inline box, so without it a host's sizing CSS is silently ignored and FitAddon measures a collapsed box (R3154). No size is declared: the box is the host's to give.
- delivery: ships with a standalone host page, `install/html/luhmann-terminal.html` (ui-luhmann-terminal-page.md) — the browser counterpart of `ark luhmann attach`, and the worked example of the status-event interface (R3161).

## Collaborators
- PtyProtocol: supplies every rule — endpoint, frames, encodings, probe classification, backoff. This element supplies the state and the I/O. Connected-to.
- xterm.js (`Terminal`, `FitAddon`): renders the stream and reports the box's (cols, rows). Bundled locally, never a CDN (R3160). Connected-to.
- PtyBrowser: the server end of the socket — `GET /luhmann/pty` and the `/luhmann/pty-status` mirror this element probes (R3149). Connected-to.
- host page: listens for the status event to render a lamp; ui-luhmann-terminal-page.md is the worked example (R3159, R3161). Connected-to, not part-of.

## Sequences
- seq-luhmann-terminal.md
