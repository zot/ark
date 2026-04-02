# TagWidget
**Requirements:** R7, R8, R9, R10, R11

CM6 ViewPlugin that finds ArkTag nodes in the syntax tree and
places interactive decorations on them. Different tag types get
specialized widgets.

## Knows
- tag type dispatch: schedule → date picker, status → dropdown, ack → gap helper, default → search-on-click
- the set of known status values (open, accepted, in-progress, completed, denied, future)
- current read/edit mode (from ModeToggle state field)

## Does
- decorates ArkTag nodes with WidgetType instances based on tag name
- on click (default): opens a search panel below the line with the tag text pre-selected
- on click (schedule): opens a date picker for the value
- on click (status): opens a dropdown with known values
- on click (ack): opens gap-detection helper
- suppresses interactive behavior in edit mode

## Collaborators
- ArkTagParser: provides ArkTag nodes in the syntax tree
- HostAPI: search-on-click calls search(), status dropdown calls setTags()
- SearchResultView: renders search results when tag is clicked
- ModeToggle: checks mode to enable/suppress widgets

## Sequences
- seq-tag-click.md
