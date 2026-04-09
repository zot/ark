# ArkSearchElement
**Requirements:** R1356, R1357, R1358, R1359, R1360, R1361, R1362, R1363, R1364, R1365, R1366, R1367, R1372, R1373, R1377, R1386, R1387, R1388, R1389, R1390, R1391, R1392, R1393, R1394, R1406, R1407, R1408, R1409, R1410, R1411, R1412, R1413, R1414, R1415, R1416, R1417, R1418, R1419, R1420, R1421, R1422, R1433, R1434, R1435, R1436, R1437, R1438, R1439, R1440, R1441, R1442, R1443, R1444, R1445, R1446

Custom element (`<ark-search>`) that renders a tag search panel
with query bar, results area, and resize handle. Pure DOM — no
CM6 dependency.

## Knows
- the SearchAPI implementation (set by host)
- initial tag name and value (set by host)
- current query state (tag, value, regex mode)
- search results per phase (phase 1, 2, 3)
- which phases are available (checks for optional SearchAPI methods)
- valid tag name pattern

## Does
- renders query bar: @-sign, tag input, colon, regex toggle, value input, close button
- renders scrollable results area with grouped results
- renders drag-to-resize handle
- debounced search on input (300ms), immediate on Enter
- validates tag name in literal mode (red border on invalid)
- constructs regex query from tag + value fields
- fires three-phase progressive search: trigram (instant), embedding (~150ms), curation (async)
- phases 1 and 2 fire in parallel; phase 3 fires after phase 2 completes
- merges results client-side, deduplicating phase 2 paths that overlap phase 1
- renders phase 1 results with normal styling
- renders phase 2 results with muted color and candidate border/icon
- promotes phase 3 curated results to full color, strikes through rejected
- dispatches 'close' event when close button clicked

## Collaborators
- SearchAPI: all search and navigation calls
- TagWidget (in markdown-editor): creates this element for inline tag panels

## Sequences
- seq-tag-click.md (the element IS the panel that appears)
