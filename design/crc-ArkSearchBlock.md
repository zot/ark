# ArkSearchBlock
**Requirements:** R1339, R1340, R1341, R1342, R1343, R1347

CM6 extension that manages ark-search fenced code blocks. Handles
the three-mode cycle, mode attribute parsing, and coordinates
between source editing and result display.

## Knows
- the three view modes: both, results, src
- the default mode order: both,results,src
- the current mode for each ark-search block in the document
- the mode= attribute parsed from the code fence info string
- current read/edit mode (from ModeToggle state field)

## Does
- detects ark-search fenced code blocks in the syntax tree
- parses mode= attribute from the info string
- cycles through available modes on click
- in "both" mode: shows editable source above, live results below
- in "results" mode: replaces code block with results panel only
- in "src" mode: shows source code block only
- in edit mode: enables all three modes regardless of mode= attribute
- on query change (both/src mode): triggers search and updates results

## Collaborators
- HostAPI: calls search(query) to get results
- SearchResultView: renders search results in both/results modes
- ModeToggle: checks file-level read/edit mode for mode restriction override

## Sequences
- seq-ark-search-render.md
- seq-mode-toggle.md
