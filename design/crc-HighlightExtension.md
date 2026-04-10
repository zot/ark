# HighlightExtension
**Requirements:** R1459, R1460, R1461, R1462, R1463, R1466

CM6 `StateField` + `StateEffect` + `ViewPlugin` trio that highlights
regex matches inside the editor viewport. Fed by the `highlight=<regex>`
URL params that `<ark-search>` appends to iframe chunk-preview URLs,
and updated at runtime via `ark-set-highlights` postMessage from the
parent window so the iframe can swap highlight patterns without
reloading. Lives in `markdown-editor/src/highlight-extension.ts`.

## Knows
- the current compiled regex list (held in a `StateField`)
- the `setHighlightPatternsEffect` that updates the field
- the message listener it attached to `window` inside the iframe
- whether the first-match scroll has already fired

## Does
- compiles each pattern string with the `gm` flags; drops anything that throws
- stores the compiled regex list in a `StateField`; the field's update function re-compiles when a `setHighlightPatternsEffect` is dispatched
- walks `view.visibleRanges` on construction, on `docChanged`/`viewportChanged`, and whenever the pattern field changes
- slices the doc text for each visible range, runs every regex, and emits a `Decoration.mark({class: "ark-search-highlight"})` for each hit
- skips zero-length matches to avoid infinite loops on patterns like `(^|\s)`
- returns a sorted `DecorationSet` via `Decoration.set(ranges, true)` — overlapping marks are allowed
- on first construction with non-empty patterns, finds the earliest match across all regexes over the whole doc and dispatches `EditorView.scrollIntoView(firstMatch, {y: "center"})` in a microtask, so the iframe opens scrolled to the first match
- registers a `message` listener on `window` that watches for `{type: 'ark-set-highlights', patterns: [...]}` from the parent and dispatches `setHighlightPatternsEffect.of(patterns)` on its own `EditorView`; the listener is removed in `destroy()`

## Collaborators
- `<ark-search>` (in ark-search): builds the regex list via `buildHighlightRegexes()`, embeds it in iframe URLs for the initial load, and pushes in-place updates via postMessage through `updateGroupHighlights` (R1466)
- `content-markdown.html`: parses `highlight` query params and passes them to `createInkArkEditor({ highlights })`
- `createInkArkEditor` (in ink-spike.ts): conditionally includes this extension when `highlights` is non-empty
- CodeMirror 6 StateField / StateEffect / ViewPlugin / Decoration APIs

## Sequences
- seq-content-fetching.md (iframe preview load path, where highlights are applied)
