# Luhmann Terminal Element

Interactive `<luhmann-terminal>` web component: a live terminal on the
ark-hosted Luhmann session, driven over ark's `GET /luhmann/pty`
websocket. Language: TypeScript (component), Go (one route mirror).

## Problem

ark hosts the Luhmann session in a pty and fans its output out to any
number of clients (`specs/managed-pty.md`). Today the only client is
`ark luhmann attach` — a shell command. The browser transport already
shipped (§Browser transport, R3141–R3144): ark's own websocket, raw
bytes both ways, JSON control frames. Nothing speaks it. So the
Frictionless UI — the project's default face — cannot show the session
ark itself is hosting, and watching or talking to Luhmann means leaving
the UI for a terminal.

## The Primitive

`<luhmann-terminal>` is a custom element of the same category as
`<pdf-chunk>` and `<ark-search>`: a zettelkasten-level primitive that
composes into larger views. It **is** the browser's `ark luhmann
attach` — the live session, rendered. It carries no chrome: no launch
button, no awake/asleep lamp, no panel frame, no toolbar. Its placement
inside the ark Frictionless app is a separate UI slice (#39); this spec
is the terminal itself.

```html
<script type="module" src="/luhmann-terminal-element.js"></script>
<luhmann-terminal></luhmann-terminal>
```

**No attributes.** The endpoint is deliberately not configurable: the
server's upgrader enforces same-origin (R3141's deliberate default), so
an endpoint pointing anywhere else would be refused at the handshake.
The element derives `/luhmann/pty` on the page's own origin — `wss:`
when the page is `https:`, else `ws:` — and there is nothing else to
point it at. Size and theme come from CSS (§Sizing, §Theming).

## The Handshake

R3143 requires the first message to be a resize, so the host knows the
client's size before it attaches. The element therefore measures before
it speaks:

1. Open the websocket; read binary frames as `ArrayBuffer`.
2. On open, fit the terminal to its box and send
   `{"t":"resize","cols":C,"rows":R}` — always first.
3. Send `{"t":"repaint"}`.

Step 3 is the same on-attach repaint the CLI client performs (R3137,
§Screen repaint): ark holds no virtual screen, so a client attaching
mid-session receives only *subsequent* output. The repaint asks the
child to redraw at once. Without it, a freshly opened terminal sits
blank until Luhmann happens to say something.

## The Two Directions

- **Host → element.** Binary messages are raw pty bytes, one message
  per chunk (R3142). They go straight to xterm, which owns the
  escape-sequence parsing and the screen — ark models neither. The host
  sends no text messages; the element ignores any it receives rather
  than guessing at intent.
- **Element → host.** xterm's `onData` yields a string: UTF-8 encoded
  and sent as one binary message. xterm's `onBinary` yields a
  byte-per-character string (mouse reports and some paste paths): sent
  with each character masked to its low byte. Both are input, merged
  onto the child serialized with every other client's keystrokes
  (R3119).

## Sizing

The element is sized by CSS. xterm's `FitAddon` converts the box into a
(cols, rows) pair, and a `ResizeObserver` re-fits on every box change
and sends a resize control frame.

The element declares its own `display: block`. A custom element is
`display: inline` by default, and width/height **do not apply to an
inline box** — so a host's `luhmann-terminal { height: 100% }` would be
silently ignored, `FitAddon` would measure a collapsed box, and the
terminal would come out a few cells wide with nothing to explain why.
The rule ships with the bundle's injected stylesheet so no host has to
know this. It is *prepended*, so a host rule of equal specificity still
wins.

It deliberately declares no *size*. A terminal has no intrinsic one, so
the box stays the host's to give — and a `height: auto` block is worse
than a wrong size: the element would take its height from xterm's
content while `FitAddon` sizes that content from the element, a
self-referential loop that settles at whatever it happened to start at.
The host must put a real height in the chain. Either works:

```css
/* the box is the viewport */
luhmann-terminal { height: 100dvh; }

/* or the classic chain — html must be in it, or `100%` has nothing to resolve against */
html, body { height: 100%; margin: 0; }
luhmann-terminal { height: 100%; }
```

A page with chrome around the terminal wants neither: make the parent a
flex column and give the element `flex: 1; min-height: 0`, so its box is
whatever the chrome leaves over (ui-luhmann-terminal-page.md).

Resize is **debounced**. A drag emits a continuous stream of box
changes, and each one would otherwise become a `TIOCSWINSZ` plus a real
`SIGWINCH` to the child — an Ink TUI re-rendering the whole screen on
every animation frame of the drag, over a socket, for sizes the user is
passing through rather than choosing.

The element reports only its own size. The minimum across all attached
clients is the host's business (smallest-wins, R3120), so a small
browser tab constrains a large attached CLI terminal — the same rule
for the same reason as two CLI clients, and not this element's concern.

## Theming

xterm does not read CSS. The element reads ark's theme variables off
the document at connect and maps them onto xterm's options:

| xterm option       | ark variable   | fallback    |
| ------------------ | -------------- | ----------- |
| `theme.background` | `--term-bg`    | `#0a0a0f`   |
| `theme.foreground` | `--term-text`  | `#dddddd`   |
| `theme.cursor`     | `--term-accent`| `#E07A47`   |
| `fontFamily`       | `--term-mono`  | `monospace` |

The 16 ANSI colors stay at xterm's defaults. They are the *child's*
palette, not the app's chrome: Luhmann's own colored output should look
like itself, not be re-hued by whichever ark theme is active.

Variables are read once, at connect (§What This Does NOT Cover).

## Detach — deliberately unlike the CLI

The CLI client intercepts `Ctrl-]` as a detach escape and paints a
prompt to acknowledge it (R3123, R3138). That exists because a shell
terminal hands the child *every* keystroke: leaving needs a key nobody
else claims, and a silent escape key is what makes detaching feel
untrustworthy.

The browser has neither problem. The page around the terminal is
already outside its keyboard — a click elsewhere leaves, and closing
the tab or removing the element disconnects. So `<luhmann-terminal>`
intercepts **nothing**: `Ctrl-]` is ordinary input, forwarded to the
child like any other key. No detach prompt, no escape state machine, no
cancel-discards-key rule. Their absence is the faithful translation,
not a missing feature — the CLI's escape solves a constraint the
browser doesn't have.

Detaching is `disconnectedCallback`: close the socket, dispose the
terminal. The session survives, as it does for any client (R3121).

The element likewise does not sanitize DEC private modes on exit
(R3140). That exists so the CLI client leaves the *user's real
terminal* clean; here the terminal is the xterm instance being
disposed, and nothing outlives it.

## Status and Reconnect

A browser cannot see why a websocket handshake failed. When no session
is hosted the endpoint answers 409 (R3141), but the WebSocket API
surfaces only an `error` and a 1006 close with no reason. "Luhmann is
asleep," "ark is restarting," and "ark is gone" all arrive at the
element as the same event.

So the element asks — **before** every socket open, and again on every
close. It probes `GET /luhmann/pty-status`, the same route `ark luhmann
status` reads, and classifies:

| probe response   | meaning                             | element             |
| ---------------- | ----------------------------------- | ------------------- |
| `{"hosted":false}` | no session — nothing to attach to | stop; `asleep`      |
| `{"hosted":true}`  | session up; our socket dropped    | retry; `waiting`    |
| fetch fails        | ark is down or bouncing           | retry; `waiting`    |

Probing *first* rather than opening a socket and reading the wreckage is
what makes the common case clean: Luhmann is asleep most of the time, so
the ordinary outcome for a page carrying this element is "no session
hosted." Socket-first would answer that with a doomed connection, a 409,
and a red line in the console before arriving at the same conclusion. The
probe is a cheap same-origin GET; it gates the open, and it is also
where the session id comes from.

The probe always runs *immediately* on a close, ahead of any backoff, so
`asleep` is reported at once rather than one backoff late. Only the
reconnect itself waits.

Retrying is **Stubborn Plumbing**: a server bounce is a wait condition,
not an error. The element reconnects with jittered backoff, and every
reconnect re-runs the whole handshake — resize, then repaint — which
restores precisely the state the bounce erased. Each retry re-probes
rather than blindly reopening, since the session may have ended during
the wait.

Retrying stops at `hosted:false` because there is nothing to wait for:
the session ended, and no amount of reconnecting brings it back. That
stop is also what keeps the element inside ark's standing rule that
**ark never proactively starts a Claude session**. Connecting cannot
start one — the endpoint 409s when nothing is hosted — and the element
never calls `launch`. It attaches to what exists, or reports that
nothing does.

State changes are announced as a bubbling, composed
`luhmann-terminal-status` CustomEvent carrying `{state, session,
attempt}`. That event is the entire host interface: a host (#39's
panel, the standalone page) renders a lamp from it and needs to know
nothing else. The element renders no status text of its own — a
primitive with no chrome.

### The route mirror

`GET /luhmann/pty-status` is today registered on the unix-socket API
mux only, so the browser cannot reach it. It gains a UI-mux mirror
alongside `GET /luhmann/pty` — the same handler on both muxes, exactly
as the curation endpoints are mirrored for the content view. The route
is part of the browser wire, so `specs/managed-pty.md` §Browser
transport owns that requirement; this spec is its consumer.

## Package Structure

New `luhmann-terminal/` directory, sibling to `pdf-chunk/` and
`ark-search/`:

- `luhmann-terminal/src/luhmann-terminal-element.ts` — the custom element
- `luhmann-terminal/src/pty-protocol.ts` — the pure wire/policy core
- `luhmann-terminal/package.json` — `@xterm/xterm` + `@xterm/addon-fit`
- `luhmann-terminal/tsconfig.json` — same settings as `pdf-chunk/`
- `luhmann-terminal/Makefile` — esbuild bundle + `node --test`

Build output is `dist/luhmann-terminal-element.js`, layered into
`cache/html/` by the root Makefile's cache rule and installed to
`~/.ark/html/` — the same path `pdf-chunk-element.js` takes. Following
ark's offline-first stance, xterm.js is bundled locally, never loaded
from a CDN. xterm ships its own stylesheet; the bundle inlines it and
injects it once on load, so a page needs one `<script>` and no
stylesheet link.

## Standalone Page

`install/html/luhmann-terminal.html` — a full-viewport terminal on the
hosted session, served at `/luhmann-terminal.html`, with a one-line
status strip driven by the element's status events.

It ships rather than staying a local fixture because it is the browser
counterpart of `ark luhmann attach`: open it and you are watching the
session. It doubles as the element's browser-testable surface and as
the worked example of the host interface (§Status and Reconnect) that
#39's panel will follow.

## Tests

The element's DOM and socket surface needs a real browser; its decision
logic does not. The pure core — endpoint derivation, control-frame
encoding, input encoding, the probe→action classification, and the
backoff schedule — lives in `pty-protocol.ts`, importing neither DOM
nor WebSocket, and is unit-tested with `node --test`. Node strips the
types natively, so this adds no runner and no dependency. The element
is the thin shell wiring that core to xterm and a socket.

Live browser verification against a real hosted session belongs to the
#40 smoke-test family, which already banks the raw pty/websocket wiring
as non-hermetic (it needs a paid `claude` launch and a live server).

## What This Does NOT Cover

- **Placement in the Frictionless app** — the panel, desk-lamp, and
  listener toggle are #39.
- **Launching or stopping a session.** The element attaches to what
  exists; lifecycle stays with `ark luhmann launch|stop` and, later,
  #39's UI. ark never proactively starts a paid session.
- **Live theme-switch reflow.** Theme variables are read at connect, so
  a theme change repaints ark's chrome but leaves an already-open
  terminal on its original palette until it reconnects.
- **xterm addons beyond `fit`** — scrollback search, web links,
  ligatures, WebGL rendering. Deferred until a user wants one.
- **A no-JS path.** A terminal is inherently interactive; without
  JavaScript the element renders nothing. There is no server-side
  render and no text fallback.
- **A second concurrent session.** One hosted session at a time, per
  `specs/managed-pty.md`.
