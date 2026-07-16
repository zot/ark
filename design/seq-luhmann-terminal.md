# Sequence: `<luhmann-terminal>` — the browser's attach

**Requirements:** R3151, R3152, R3153, R3154, R3155, R3156, R3157, R3158, R3159

How a browser page reaches the hosted pty: the probe that gates every socket
open, the connect handshake (resize first, then repaint), the two byte
directions, the debounced resize, and the classify loop that tells a bounce
apart from a session that ended.

Participants: **Page** (any host — ui-luhmann-terminal-page.md, later #39's
panel) · **Element** (`<luhmann-terminal>`, crc-LuhmannTerminalElement.md) ·
**Protocol** (crc-PtyProtocol.md, pure) · **Xterm** (`Terminal` + `FitAddon`) ·
**PtyBrowser** (`GET /luhmann/pty` + the `/luhmann/pty-status` mirror,
crc-PtyBrowser.md) · **PtyHost** (the fan-out, crc-PtyHost.md) · **Child** (the
hosted `claude`).

```
1.    Page: <luhmann-terminal> enters the DOM                 [connectedCallback]
1.1     Element -> Protocol: themeOptions(getComputedStyle(document)) -- --term-bg/text/accent/mono (R3155)
1.2     Element -> Xterm: new Terminal(theme) ; loadAddon(FitAddon) ; open(this)  [ANSI 16 left at defaults, R3155]
1.3     Element -> Xterm: new ResizeObserver(onResize).observe(this)              (R3154)
1.4     Element: classify(immediate=true) -> 2. -- the probe gates every open   (R3157)
1.5     Element -> Protocol: ptyEndpoint(location) -> wss?://host/luhmann/pty     (R3151)
1.6     Element -> PtyBrowser: new WebSocket(url) ; binaryType = 'arraybuffer'
1.7     Element -> Page: announce {state:'connecting'}                            (R3159)
1.8     Element: onopen -> Xterm: fit() -> {cols, rows}
1.9     Element -> PtyBrowser: send resizeFrame(cols, rows)      [FIRST message, or R3143 closes it] (R3152)
1.10    PtyBrowser -> PtyHost: Attach(client, size) -> registration               (R3144)
1.11    Element -> PtyBrowser: send repaintFrame()               [ark holds no screen; else blank] (R3152)
1.12    PtyBrowser -> PtyHost: ForceRepaint() -> Child redraws the whole screen   (R3136, R3137)
1.13    Element: attempt := 0 ; announce {state:'connected', session}             (R3159)
1.14    Element: ws.onclose -> classify(immediate=false) -> 2.                    (R3157)
```

```
2.    Element: classify(immediate)              [before every open (1.4), and on every close (1.14)]
2.1     Element -> PtyBrowser: fetch GET /luhmann/pty-status   [runs at once -- no backoff first] (R3149)
2.2     Element -> Protocol: parseProbe(body) -> hosted{session} | asleep | unreachable  (R3157)
2.3     Element: detached mid-probe ? -> return          [isConnected false; nothing to announce]
2.4     Element -> Protocol: nextAction(probe, attempt) -- the three-way classification:
2.4.1     asleep      -> {state:'asleep'}   session ended; nothing to wait for -> announce, stop (R3157, R3158)
2.4.2     hosted      -> {state:'waiting', delayMs}   session := probe.session
2.4.3     unreachable -> {state:'waiting', delayMs}   ark is down or bouncing -- a wait condition, not an error
2.5     Element: hosted AND immediate ? -> openSocket() -> 1.5     [first attempt: no reason to wait]
2.6     Element -> Page: announce {state:'waiting', session, attempt}             (R3159)
2.7     Element: attempt++ ; setTimeout(classify(immediate=true), delayMs)        (R3157)
2.8     Element: the retry RE-PROBES at 2.1 -- the session may have ended during the wait
2.9     Element: a reconnect re-runs 1.8-1.12 whole -- resize then repaint restore what the bounce erased (R3152)
```

```
3.    Child -> Element: output                                [the steady state]
3.1     Child -> PtyHost -> PtyBrowser: broadcast chunk -> binary message         (R3142, R3118)
3.2     Element -> Xterm: write(new Uint8Array(data))          [Xterm owns the escapes; ark models none] (R3153)
3.3     Element: text message ? -> ignore  [the host sends none; not a case to guess at] (R3153)
```

```
4.    Page: user types / resizes                             [the input directions]
4.1     Xterm -> Element: onData(s)    -> Protocol: encodeData(s)   -> send binary  (R3153)
4.2     Xterm -> Element: onBinary(s)  -> Protocol: encodeBinary(s) -> send binary  (R3153)
4.3     Element -> PtyHost: input merged serialized with every other client's       (R3119)
4.4     Element: Ctrl-] arrives at 4.1 as ordinary input -- no interception (R3156)
4.5     Page: box changes -> onResize -> debounce -> fit() -> send resizeFrame      (R3154)
4.6     PtyHost: recompute smallest-wins across all clients -> SIGWINCH             (R3120)
```

```
5.    Page: <luhmann-terminal> leaves the DOM                 [disconnectedCallback = detach]
5.1     Element: clear retryTimer + resizeTimer ; observer.disconnect()
5.2     Element -> PtyBrowser: ws.close() -> PtyHost: Detach(reg) -- session survives (R3121, R3156)
5.3     Element -> Xterm: dispose()   [no DEC sanitize: R3140 restores a real terminal; this one is gone] (R3156)
```

Note — 1.9 before 1.11 is not stylistic: R3143 closes a connection whose first
message is not a resize, so a repaint-first element would never attach at all.

Step 2 exists because a browser cannot read a rejected upgrade's status: a
no-session 409 and a dead server both arrive as an unexplained 1006, so
"asleep" and "bouncing" are indistinguishable until the element asks. It gates
the open (1.4) rather than only explaining a failure, because `asleep` is the
*common* case — Luhmann is usually not running — and socket-first would answer
the ordinary page load with a doomed connection and a console error before
reaching the same conclusion. 2.4.1 is the arm that keeps R3114's
never-start-a-paid-session promise: the element stops rather than waiting on a
session that has ended, and it has no way to start one either way.

Step 2.5 is the only asymmetry: a first attempt connects at once, while a retry
after a failure waits. That is why 2.7 re-enters with `immediate=true` — the
backoff has already been served by the timer, so the next pass connects as soon
as the probe says it can.
