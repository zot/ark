# A TUI, and which of two projects it is

**Problem.** Bill's work is terminal-resident, but several of ark's surfaces
exist only in the browser, so using them means alt-tabbing out of the terminal
and back. The ask — "a decent TUI so I can just stay in the terminal" — sounds
like one project and is actually two, with an order-of-magnitude between them.

Opened 2026-07-25 as a working note. Promoted here 2026-07-28, when the fork
below was settled and the document stopped being a brainstorm.

| Part | Status |
|---|---|
| The fork: second renderer vs API client | **DECIDED** 2026-07-28 — client (b) |
| #72 — build the TUI, first screen first | Open (`/mini-spec`) |
| Whether Lua is earned | Deliberately deferred |

---

## The fork

"The same API the browser uses" reads two ways.

**(a) A second renderer.** The browser gets viewdefs rendered by ui-engine.
Teaching a TUI to speak that protocol means implementing a second UI engine whose
layout model is Lipgloss rather than CSS/DOM. Flexbox, wrappers, theme classes,
and the change-detection machinery do not transfer.

**(b) A client over ark's own API.** Search, fetch, tags, chats, status, monitor
— the HTTP/socket surface ark already exposes. One screenful of Bubbletea per
verb.

- **DECIDED (Bill, 2026-07-28): (b).** It is the reading that actually removes
  the alt-tab, and it is weekend-shaped rather than a second engine. Revisit (a)
  only if a real screen turns out to be unbuildable without viewdefs — and treat
  that discovery as evidence about *that screen*, not as a mandate to port the
  engine.

## The asset already in hand

Ark emits **markdown at nearly every surface**, and that was deliberate: an
agent-facing output is a markdown view of structured data, with the CLI doing the
chewing so no consumer has to parse JSON. `glamour` — the renderer inside `glow`
— takes that markdown directly.

So most screens are "run the verb, render the markdown," with no new formatting
layer to write. The existing habit of reading ark output through `glow` is
already most of the TUI; what is missing is navigation. This is the concrete
reason (b) is cheap, and it is worth stating because it is easy to mistake for a
coincidence rather than a payoff of the markdown-everywhere decision.

## The Lua question, deliberately unanswered

The original prompt said "Lua-programmed." That is worth **earning** rather than
assuming. Frictionless is Lua because viewdefs, hot reload, and multiple apps
justify it; a TUI with six screens does not, on day one. Go-only first.

Add a Lua layer when there is a second author, or a screen worth writing without
a rebuild. If it arrives, the interesting question is what its vocabulary
describes — Charm-shaped screens (list, viewport, form) rather than viewdefs,
because adopting the viewdef vocabulary would be (a) arriving through the back
door.

## First screen

The **chat browser**. It is the workflow being run by hand today, its substrate
landed 2026-07-28 for other reasons, and it is markdown-out, which is the best
case for glamour. See [the chat-browser carve](chat-browser.md) — the Go
substrate there (`ark chats list`, `ark chats`) is a client-facing API, so the
TUI consumes it exactly as the Frictionless panel does.

Plausible screens after that: search (query line, results, preview pane), tag
browse, status/monitor.

## Inventory to look at

`bubbletea` (the loop), `lipgloss` (layout/style), `glamour` (markdown, already
proven via `glow`), `bubbles` (list, viewport, textinput, table), `huh` (forms).
Worth reading what `glow`'s own file browser does, since it is close in shape to
the chat-list screen.

## Open questions

- Does the TUI talk to a running server, or work standalone against the DB the
  way the CLI does? `ark status` already distinguishes server-required verbs, so
  the distinction exists to be inherited rather than invented.
- One binary or two? An `ark tui` subcommand keeps the install story at one file,
  which is the self-contained-tool property the rest of the project holds to.
- Unix socket or HTTP port? Ark listens on both, for different callers.
