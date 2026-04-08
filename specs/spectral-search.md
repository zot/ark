# Spectral Search

LLM-powered query expansion for the ark search widget. A cheap model
(Haiku) acts as a librarian — given what the user typed, it suggests
alternative terms that a human would think to check. This is the
Librarian Search pattern: vector-free semantic search via query
expansion.

Language: Go (expansion endpoint, Haiku session management),
TypeScript (ark-search web component). Environment: ark server,
browser.

## Context: Ark Search Web Component

Spectral expansion lives inside a planned `<ark-search>` web
component. The component is not designed here — this spec
covers only the expansion backend and the two-phase result behavior
that the component will consume.

For Phase A, expansion integrates into the existing tag search
panel in the markdown editor. The `<ark-search>` component
(Phase B) will inherit this capability.

## The Librarian (Expansion Endpoint)

Each pipeline step is a `claude --print --model haiku
--output-format json` invocation. Conversation context persists
via `--resume SESSION_ID` — the session ID from the first
invocation is stored and reused for subsequent messages.

`--system-prompt` (not `--append-system-prompt`) replaces all
default Claude Code instructions. The Librarian is a specialized
expansion oracle, not a general assistant — it doesn't need coding
guidelines, tools, or personality. `--tools ""` disables all tool
access.

Two spawns per expansion: one for step 1 (expand), one for step 3
(curate). Claude's session caching means the system prompt tokens
are paid once; subsequent messages in the same session pay only
message tokens.

No API key management in ark — `claude` handles its own auth.
The CLI is effectively a local API gateway.

## Expansion Pipeline

Expansion is a three-step server-side pipeline. The client sends
one request and receives curated results — no client-side search
or merging logic.

### Tag Search (Phase A — this work)

For tag search, the pipeline operates on the tag vocabulary
(V records in LMDB) rather than full-text content:

1. **Haiku expands** — given the user's tag name and value,
   suggests alternative tag names and values that a human would
   think to check.

2. **Trigram fuzzy match** — each alternative is fuzzy-matched
   against the V records (tag-value index). This is cheap:
   in-process LMDB reads against the existing vocabulary, no
   full-text search needed. Results are (tag, value, count,
   score) tuples.

3. **Haiku curates** — sees the matched tag/value pairs with
   scores, prunes false positives, selects what's actually
   relevant to the user's intent. Returns a curated subset.

The server then fetches the actual search results for the curated
tags and returns them to the client as expansion-sourced results.

### General Search (Phase B — future)

For non-tag search modes (contains, fuzzy, regex), the pipeline
adapts:

1. **Haiku expands** — suggests related search terms
2. **Fuzzy search** against the full-text index (word presence
   matters, order doesn't, partial matches score)
3. **Haiku curates** — prunes the fuzzy results to what's
   relevant

The mechanism is the same — expand, search, curate — but the
search step uses the full-text index instead of V records.

### Endpoint

`POST /search/expand` accepts `{mode, tag, value}` and returns
`{results: [{path, strategy, chunks, source: "expansion"}]}`.

Results look like regular search results but are marked as
expansion-sourced. The full expand→search→curate pipeline runs
server-side. The client just displays them interspersed with
literal results.

## Two-Phase Results

**Phase 1 (immediate):** The literal search fires as the user types,
exactly as today. Results appear instantly.

**Phase 2 (expansion):** When spectral mode is on, the expansion
request goes to the server after a longer debounce. The server
runs the full pipeline internally. Curated results come back
pre-searched — the client just inserts them interspersed among
existing results, highlighted with a visual marker (accent color
border or tint) so the user can distinguish librarian-found
results from literal matches. Results height-transition in so
the appearance isn't jarring.

If the expansion returns no new results, no visual change occurs.

## Toggle

A button in the search bar toggles spectral expansion on/off.
Default is off. When off, only literal search fires. The toggle
state persists in localStorage. If `claude` is not available, the
toggle is hidden.

## Throttling

Literal search debounces at ~300ms (existing behavior). Expansion
requests debounce at ~1-2 seconds since LLM turnaround is slower.
A new keystroke cancels any in-flight expansion request.

## Availability

Spectral search requires `claude` on PATH. The server checks at
startup and reports availability via a capability flag (e.g.,
`GET /status` includes `spectral: true`). If `claude` is not
found, the toggle is hidden in the UI.

## Searching Directory

`~/.ark/searching/` contains:
- `CLAUDE.md` — expansion instructions (system prompt for Haiku)

Created by `ark init` if it doesn't exist. Ships with sensible
defaults that can be customized.

## Implementation Phasing

**Phase A (this work):** Tag search expansion — Haiku co-process,
three-step pipeline (expand → V-record fuzzy match → curate),
expansion endpoint, spectral toggle in the markdown editor's tag
search panel.

**Phase B (future):** General search expansion (full-text fuzzy),
`<ark-search>` web component, Frictionless Searching view migration.

**Phase C (future):** OR groups, multi-row expansion UI, filter
persistence, source-type bar integration.
