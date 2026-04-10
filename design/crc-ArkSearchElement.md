# ArkSearchElement
**Requirements:** R1356, R1357, R1358, R1359, R1360, R1361, R1362, R1363, R1364, R1365, R1366, R1367, R1372, R1373, R1377, R1386, R1387, R1388, R1389, R1390, R1391, R1392, R1393, R1394, R1406, R1407, R1408, R1409, R1410, R1411, R1412, R1413, R1414, R1415, R1416, R1417, R1418, R1419, R1420, R1421, R1422, R1433, R1434, R1435, R1436, R1437, R1438, R1439, R1440, R1441, R1442, R1443, R1444, R1445, R1446, R1447, R1448, R1449, R1450, R1451, R1452, R1453, R1454, R1455, R1456, R1457, R1458, R1459, R1464, R1465, R1466

Custom element (`<ark-search>`) that renders a tag search panel
with query bar, results area, and resize handle. Pure DOM — no
CM6 dependency.

## Knows
- the SearchAPI implementation (set by host)
- initial tag name and value (set by host)
- current search mode (tag, contains, fuzzy, regex) and its base-query state
- base tag state: name, value, value match mode, name match mode (contains/exact)
- per-row filter state, including per-row tag name match mode
- search results per phase (phase 1, 2, 3)
- which phases are available (checks for optional SearchAPI methods)
- tag boundary heuristic `(^|\s)@NAME:` and tag name char class `[\w.-]`
- per-path result cache (`resultEls`): group element, chunk signature, highlight signature, current phase

## Does
- renders query bar: mode dropdown, swappable inputs (structured tag fields or free text input), clear, close
- in tag mode, renders `@ [~/=] name : [Aa/.*/~] value` — name match toggle between `@` and the name input
- renders scrollable results area with grouped results
- renders drag-to-resize handle
- debounced search on input (300ms), immediate on Enter
- builds a regex query from tag state: contains-name wraps `[\w.-]*` around the escaped name; value is tokenized on whitespace and OR'd
- emits contains-name filter rows as `regex` chunk filters (server's tag filter matches names literally)
- computes a list of highlight regexes (one for the tag name prefix, one per value token) and appends them to iframe preview URLs as repeated `highlight=` query params
- play-button path (`set tag()`) forces name match to exact — exploring "that one tag"
- fires three-phase progressive search: trigram (instant), embedding (~150ms), curation (async)
- phases 1 and 2 fire in parallel; phase 3 fires after phase 2 completes
- merges results client-side, deduplicating phase 2 paths that overlap phase 1
- diffs results by path on every render (R1464): reuses cached group elements and iframes when chunks match; updates phase class in place when only the phase changes; reorders siblings with `insertBefore` so iframes stay attached and never reload
- pushes highlight updates live via postMessage (R1466): when only the highlight signature changes for a cached entry, calls `updateGroupHighlights` which posts `ark-set-highlights` to each loaded iframe and rewrites `dataset.src` on still-lazy iframes
- builds new iframes at `opacity: 0` and fades them in on the `ark-content-ready` postMessage from `/content/` (R1465) — never exposes a gray loading state
- renders phase 1 results with normal styling
- renders phase 2 results with muted color and candidate border/icon
- promotes phase 3 curated results to full color, strikes through rejected
- dispatches 'close' event when close button clicked

## Collaborators
- SearchAPI: all search and navigation calls
- TagWidget (in markdown-editor): creates this element for inline tag panels
- HighlightExtension (in markdown-editor): consumes the `highlight` URL params this element emits

## Sequences
- seq-tag-click.md (the element IS the panel that appears)
