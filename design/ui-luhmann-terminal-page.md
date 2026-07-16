# UI: Luhmann terminal page
**Requirements:** R3159, R3161

`install/html/luhmann-terminal.html`, served at `/luhmann-terminal.html` — a
full-viewport terminal on the hosted session with a one-line status strip. The
browser counterpart of `ark luhmann attach`: open it and you are watching the
session.

It ships rather than staying a local fixture because it is that capability, and
it doubles as the element's browser-testable surface and as the worked example
of the host interface (R3159) that #39's panel will follow.

## Layout

```
┌──────────────────────────────────────────────────────────┐
│ ● Luhmann: awake (session 6e4bed60)                      │  <- status strip
├──────────────────────────────────────────────────────────┤
│                                                          │
│  <luhmann-terminal>                                      │  <- flex: 1, fills the rest
│    (xterm renders the hosted session here)               │
│                                                          │
│                                                          │
└──────────────────────────────────────────────────────────┘
```

- The page is `display: flex; flex-direction: column`, `height: 100dvh`, so the
  element's box *is* the viewport minus the strip — R3154's `ResizeObserver`
  then makes a window resize a pty resize, with no layout code on the page.
- Chrome, colors, and font come from ark's `--term-*` theme variables, the same
  ones the element reads for xterm (R3155).

## Status strip

The page holds no connection state. It listens for the element's bubbling
`luhmann-terminal-status` event (R3159) and renders `detail.state`, which is the
entire host interface:

| state        | lamp                     | text                              |
| ------------ | ------------------------ | --------------------------------- |
| `connecting` | `--term-text-dim`        | Luhmann: connecting…              |
| `connected`  | `--term-success`         | Luhmann: awake (session S)        |
| `waiting`    | `--term-warning`         | Luhmann: reconnecting (attempt N) |
| `asleep`     | `--term-text-muted`      | Luhmann: asleep — no session      |

`asleep` deliberately offers no launch button: ark never proactively starts a
paid session (R3114, R3158), and this page attaches to what exists. Launching
stays an explicit `ark luhmann launch`.

## Refs
- crc-LuhmannTerminalElement.md — the element the page hosts
- seq-luhmann-terminal.md — the status events the strip renders (step 2.4)
