# Test Design: PtyProtocol
**Source:** crc-PtyProtocol.md

Unit tests for the browser pty client's pure core (R3162), run with `node
--test` — Node strips the types natively, so these need no runner and no
dependency. The element's DOM and socket surface needs a real browser and is
covered by the #40 smoke-test family; every *rule* it follows is here.

## Test: endpoint is ws on http
**Purpose:** R3151 — the derived endpoint follows the page's own origin.
**Input:** `ptyEndpoint({protocol: 'http:', host: 'localhost:8080'})`.
**Expected:** `ws://localhost:8080/luhmann/pty`.
**Refs:** crc-PtyProtocol.md, seq-luhmann-terminal.md#1.5

## Test: endpoint is wss on https
**Purpose:** R3151 — a secure page must not open an insecure socket (the browser would block it).
**Input:** `ptyEndpoint({protocol: 'https:', host: 'ark.example:443'})`.
**Expected:** `wss://ark.example:443/luhmann/pty`.
**Refs:** crc-PtyProtocol.md, seq-luhmann-terminal.md#1.5

## Test: resize frame matches the wire
**Purpose:** R3152, R3143 — the frame the Go side parses, byte for byte.
**Input:** `resizeFrame(80, 24)`.
**Expected:** parses to `{t: 'resize', cols: 80, rows: 24}`; `t` is exactly `resize` (Go's `parsePtyControl` errors on an unknown type).
**Refs:** crc-PtyProtocol.md, crc-PtyBrowser.md, seq-luhmann-terminal.md#1.9

## Test: repaint frame matches the wire
**Purpose:** R3152 — the repaint request the child redraws on.
**Input:** `repaintFrame()`.
**Expected:** parses to `{t: 'repaint'}` with no size fields.
**Refs:** crc-PtyProtocol.md, seq-luhmann-terminal.md#1.11

## Test: encodeData is UTF-8
**Purpose:** R3153 — xterm's `onData` yields a JS string; the pty takes bytes.
**Input:** `encodeData('é')` and `encodeData('hello')`.
**Expected:** `[0xC3, 0xA9]` and the 5 ASCII bytes — a multi-byte char must not be truncated to one byte.
**Refs:** crc-PtyProtocol.md, seq-luhmann-terminal.md#4.1

## Test: encodeBinary masks to the low byte
**Purpose:** R3153 — `onBinary` yields byte-per-character, so UTF-8 encoding it would corrupt it.
**Input:** `encodeBinary('\x1b[Mé')`.
**Expected:** `[0x1b, 0x5b, 0x4d, 0xe9]` — one byte per char, not the two bytes UTF-8 would give the last one.
**Refs:** crc-PtyProtocol.md, seq-luhmann-terminal.md#4.2

## Test: Ctrl-] encodes as ordinary input
**Purpose:** R3156 — the CLI's detach key is not special in the browser; it must reach the child.
**Input:** `encodeData('\x1d')`.
**Expected:** `[0x1d]` — forwarded, not swallowed.
**Refs:** crc-PtyProtocol.md, seq-luhmann-terminal.md#4.4

## Test: parseProbe reads a hosted session
**Purpose:** R3157 — the shape `handleLuhmannStatus` actually writes.
**Input:** `parseProbe('{"hosted":true,"session":"6e4bed60","secretaries":2}')`.
**Expected:** `{kind: 'hosted', session: '6e4bed60'}`.
**Refs:** crc-PtyProtocol.md, seq-luhmann-terminal.md#2.2

## Test: parseProbe reads an unhosted server
**Purpose:** R3157 — the arm that stops the retry loop.
**Input:** `parseProbe('{"hosted":false,"session":"","secretaries":0}')`.
**Expected:** `{kind: 'asleep'}`.
**Refs:** crc-PtyProtocol.md, seq-luhmann-terminal.md#2.2

## Test: parseProbe treats a malformed body as unreachable
**Purpose:** R3157 — a half-written response from a bouncing server must not throw out of the close handler and strand the element.
**Input:** `parseProbe('<html>502 Bad Gateway')`, an empty body, and a body whose `hosted` is the wrong type.
**Expected:** `{kind: 'unreachable'}` for each — no throw. Unreachable retries, which is the safe reading: a garbled answer is not evidence the session ended.
**Refs:** crc-PtyProtocol.md, seq-luhmann-terminal.md#2.2

## Test: not hosted stops the loop
**Purpose:** R3157, R3158 — the session ended; reconnecting cannot bring it back, and the element must never wait on a session that would have to be started.
**Input:** `nextAction({kind: 'asleep'}, 0, rand)`.
**Expected:** `{state: 'asleep'}` with no delay — no retry scheduled.
**Refs:** crc-PtyProtocol.md, seq-luhmann-terminal.md#2.4.1

## Test: hosted retries
**Purpose:** R3157 — the pty is still there; only our socket dropped.
**Input:** `nextAction({kind: 'hosted', session: 'S'}, 0, rand)`.
**Expected:** `{state: 'waiting', delayMs > 0}`.
**Refs:** crc-PtyProtocol.md, seq-luhmann-terminal.md#2.4.2

## Test: unreachable retries
**Purpose:** R3157 — Stubborn Plumbing: a bounce is a wait condition, not an error.
**Input:** `nextAction({kind: 'unreachable'}, 0, rand)`.
**Expected:** `{state: 'waiting', delayMs > 0}` — the same arm as hosted, deliberately.
**Refs:** crc-PtyProtocol.md, seq-luhmann-terminal.md#2.4.3

## Test: backoff doubles and caps
**Purpose:** R3157 — the schedule must not hammer a bouncing server, and must not grow without bound.
**Input:** `backoffDelay(n, () => 0.5)` (jitter pinned to its midpoint) for n = 0, 1, 2, and a large n.
**Expected:** base, 2×base, 4×base, and exactly the cap for large n — monotonic non-decreasing, never above the cap.
**Refs:** crc-PtyProtocol.md, seq-luhmann-terminal.md#2.7

## Test: backoff jitter stays in band
**Purpose:** R3157 — jitter spreads a thundering herd without ever yielding a zero or negative delay.
**Input:** `backoffDelay(0, () => 0)` and `backoffDelay(0, () => 1)` — the extremes of the injected random source.
**Expected:** both are > 0 and within the jitter fraction of the base; the injected source is what makes this deterministic rather than flaky.
**Refs:** crc-PtyProtocol.md, seq-luhmann-terminal.md#2.7

## Test: theme options map ark's variables
**Purpose:** R3155 — the terminal follows the active ark theme.
**Input:** `themeOptions` given a fake style lookup returning values for `--term-bg`, `--term-text`, `--term-accent`, `--term-mono`.
**Expected:** those land on `theme.background`, `theme.foreground`, `theme.cursor`, and `fontFamily`; no ANSI-16 keys are set (the child's palette is not the app's chrome).
**Refs:** crc-PtyProtocol.md, seq-luhmann-terminal.md#1.1

## Test: theme options fall back
**Purpose:** R3155 — a page without ark's theme loaded still renders a usable terminal.
**Input:** `themeOptions` given a lookup returning `''` for every variable (what `getPropertyValue` gives for an unset custom property).
**Expected:** every field is a non-empty fallback — no `''` reaches xterm, which would render an invisible or unstyled terminal.
**Refs:** crc-PtyProtocol.md, seq-luhmann-terminal.md#1.1
