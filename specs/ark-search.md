# Ark Search Component

Standalone `<ark-search>` web component for tag search and result
display. Extracted from the markdown editor's tag-widget search
panel code.
Language: TypeScript. Environment: browser (custom element).

## What It Is

A custom element (`<ark-search>`) that renders a search interface:
query bar with tag/value fields, result area, and resize handle.
The same element is used by the CM6 markdown editor (inline tag
search panel), the Frictionless search view, and any future host.

## SearchAPI

The component communicates with the server through a `SearchAPI`
interface — the search-relevant subset of HostAPI:

- `search(query, mode?)` — run search, return grouped results
- `tagComplete(prefix)` — tag name completions
- `tagValueComplete(tag, prefix)` — value completions
- `navigate(path)` — open a file
- `showInFolder?(path)` — show in file manager

HostAPI extends SearchAPI with CM6-specific methods (`save`,
`setTags`). The search component depends only on SearchAPI.

### Three-Phase Progressive Search Methods

Optional methods on SearchAPI enable progressive search. If
absent, the element falls back to trigram-only (phase 1).

- `embedMatch?(query, k?)` — embedding similarity search,
  returns `TagMatch[]` (tag, value, count, score)
- `expandSearch?(tags)` — search for file results matching
  tag/value pairs, returns `SearchResultGroup[]`
- `curateRequest?(tag, value, candidates)` — queue Haiku
  curation of TagMatch candidates, returns requestId
- `curateResult?(id)` — poll for curation result, returns
  the curated TagMatch subset plus rejected list

The element checks for method existence and activates phases
accordingly:
- Phase 1 (trigram): always — `search()` fires immediately
- Phase 2 (embedding): if `embedMatch` and `expandSearch`
  exist — fires in parallel with phase 1, results from
  `expandSearch` shown muted/bordered
- Phase 3 (curation): if `curateRequest` and `curateResult`
  exist — fires after phase 2 completes, promotes chosen
  results to full color, strikes through rejected ones

Client-side merge: each phase is a separate response. The
element merges results progressively as they arrive. Phase 2
results that duplicate phase 1 paths are deduplicated — the
phase 1 result takes precedence for display, the phase 2
result is dropped.

## Custom Element

`<ark-search>` is a standard HTMLElement (not shadow DOM — it
inherits the host's theme CSS). It accepts configuration through
properties set by the host after creation:

- `api: SearchAPI` — required, the server interface
- `tag: string` — initial tag name (optional)
- `value: string` — initial value filter (optional)

The host creates the element, sets properties, and appends it.
The element initializes on `connectedCallback` if `api` is set,
or defers until `api` is assigned.

## What Moves

From `markdown-editor/src/tag-widget.ts`:
- `TagSearchPanelWidget` class → becomes the internal rendering
  of the `<ark-search>` element
- `renderTagSearchResults` function → becomes the element's result
  renderer
- `PanelState` interface → internal element state

What stays in `tag-widget.ts`:
- `TagSearchWidget` (the ▶/▼ button on each tag)
- `createOpenSearchPanels` (CM6 StateField managing panel open/close)
- `buildTagDecorations`, `needsRedecoration` (CM6 decoration logic)
- `StatusWidget` (status dropdown)
- `toggleSearchPanel` effect

The toggle mechanism stays CM6-side. When a tag panel opens,
tag-widget creates an `<ark-search>` element and inserts it as
the block widget content.

From `markdown-editor/src/search-result-view.ts`:
- Stays in markdown-editor — it creates CM6 EditorView instances
  for markdown chunk rendering, which is a CM6-specific concern.
  The `<ark-search>` element uses its own HTML-based result
  rendering (the existing `renderTagSearchResults` approach).

`ark-search-block.ts` stays in markdown-editor — it is a CM6
ViewPlugin extension. It may adopt `<ark-search>` later when the
improved search UI lands, but that is future work.

## Package Structure

`ark-search/` is a sibling directory to `markdown-editor/`:
- `ark-search/src/search-api.ts` — SearchAPI interface + shared types
- `ark-search/src/ark-search-element.ts` — the custom element
- `ark-search/src/index.ts` — exports
- `ark-search/package.json` — no runtime deps (pure DOM)
- `ark-search/tsconfig.json` — same settings as markdown-editor

`markdown-editor/` imports SearchAPI types and the element from
`ark-search/` via relative path import (`../ark-search/src/...`).
HostAPI extends SearchAPI. The final bundle is still one esbuild
output from `markdown-editor/` — ark-search has no separate
bundle.

## Result Rendering

The element renders results as plain HTML:
- File path as clickable link (calls `api.navigate`)
- Show-in-folder button when `api.showInFolder` exists
- Chunk previews: pre-rendered HTML from the server (the `preview`
  field in SearchChunk). No CM6 instances — the standalone
  component works without CodeMirror.

This is the same rendering used by `renderTagSearchResults` in
the current tag-widget.ts. The CM6-based rendering in
search-result-view.ts remains a markdown-editor concern for
ark-search code blocks.
