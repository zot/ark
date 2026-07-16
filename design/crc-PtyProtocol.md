# PtyProtocol
**Requirements:** R3151, R3152, R3153, R3157, R3162, R3155

The browser pty client's decision logic, factored out of the element as a pure
module (R3162): endpoint derivation, control-frame and input encoding, the
close→probe→action classification, and the backoff schedule. It imports neither
DOM nor WebSocket, so every rule the terminal follows is unit-testable without a
browser (test-PtyProtocol.md). LuhmannTerminalElement is the shell that wires
this core to xterm and a socket.

## Knows
- Nothing. Stateless pure functions — the element owns the socket, the terminal, and the attempt counter, and passes what each call needs.
- backoff constants: base delay, cap, and jitter fraction for the reconnect schedule (R3157).

## Does
- ptyEndpoint: derive the websocket URL from a location-shaped `{protocol, host}` — `/luhmann/pty` on the page's own origin, `wss:` when the page is `https:`, else `ws:` (R3151). Not configurable: R3141's upgrader is same-origin, so no other endpoint could connect.
- resizeFrame / repaintFrame: build the JSON control frames R3143 defines. The element sends resize first, always (R3152).
- encodeData: UTF-8 encode xterm's `onData` string for a binary input message (R3153).
- encodeBinary: encode xterm's `onBinary` byte-per-character string, masking each char to its low byte (R3153).
- parseProbe: turn a `GET /luhmann/pty-status` response body into a probe outcome — hosted plus session, or not hosted — tolerating a malformed body by reporting it unreachable rather than throwing (R3157).
- nextAction: classify a probe outcome into the element's next move (R3157). Not hosted → stop, state `asleep`: the session ended and no reconnect brings it back (which is also what keeps R3158's never-start-a-session promise). Hosted, or the probe itself failed → retry after `backoffDelay`, state `waiting`: a bounce is a wait condition, not an error (Stubborn Plumbing).
- backoffDelay: the jittered exponential schedule for an attempt number — doubling from the base to the cap, then spread by the jitter fraction using an injected random source so the schedule is testable (R3157).
- themeOptions: map ark's `--term-*` variables onto xterm's options through an injected lookup — background, foreground, cursor, fontFamily, each with a fallback so an unset variable never reaches xterm as `''` (R3155). Sets no ANSI-16 keys: that palette is the child's, not the app's chrome. The injected lookup is what keeps this side of the theme testable without a document.

## Collaborators
- LuhmannTerminalElement: the sole caller. It owns all state and I/O and asks this module what to do. Connected-to, not part-of — the split exists so the rules can be tested without a browser (R3162).

## Sequences
- seq-luhmann-terminal.md
