# ArkSearchElement
**Requirements:** R1356, R1357, R1358, R1359, R1360, R1361, R1362, R1363, R1364, R1365, R1366, R1367, R1372, R1373, R1377

Custom element (`<ark-search>`) that renders a tag search panel
with query bar, results area, and resize handle. Pure DOM — no
CM6 dependency.

## Knows
- the SearchAPI implementation (set by host)
- initial tag name and value (set by host)
- current query state (tag, value, regex mode)
- search results
- valid tag name pattern

## Does
- renders query bar: @-sign, tag input, colon, regex toggle, value input, close button
- renders scrollable results area with grouped results
- renders drag-to-resize handle
- debounced search on input (300ms), immediate on Enter
- validates tag name in literal mode (red border on invalid)
- constructs regex query from tag + value fields
- renders results as HTML: path links, show-in-folder buttons, chunk previews
- dispatches 'close' event when close button clicked

## Collaborators
- SearchAPI: all search and navigation calls
- TagWidget (in markdown-editor): creates this element for inline tag panels

## Sequences
- seq-tag-click.md (the element IS the panel that appears)
