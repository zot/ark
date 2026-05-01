# ArkExtTagsElement
**Requirements:** R2065, R2066, R2067, R2068, R2069, R2070, R2071, R2072, R2081, R2082

Custom element (`<ark-ext-tags>`) emitted by the server at the
top of any chunk that has ext routings. Hosts the indicator
icon and the pick-list dropdown. Contains one `<ark-tag>` child
per ext-routed tag at that location. No shadow DOM — inherits
host theme CSS.

## Knows
- `<ark-tag>` children — each with `name`/`value` content and
  `externalFile` / `externalTarget` attributes (R2073 of
  ArkTagElement)
- whether the dropdown is currently open
- the active "armed" child while the user is dragging from the
  indicator (mouseover during a click-and-drag gesture)
- the chunk container's positioning context (`position: relative`)
  — used by the absolute positioning of the indicator (R2081)

## Does
- connectedCallback(): render the indicator SVG (single-tag
  glyph if one child, stacked multi-tag glyph if more than one);
  position absolutely within the chunk container so it does not
  consume vertical space (R2066, R2067, R2081); for PDF chunks,
  position via rect-derived coordinates over the `<pdf-chunk>`
  canvas (R2082); attach mousedown / keydown handlers
- onMouseDown(event): open the dropdown listing each `<ark-tag>`
  child with its `tag: value`; track this gesture as
  click-and-drag candidate (R2068)
- onMouseUp(event): if mouseup occurs over a tag row (drag
  gesture), call openPanelForTag(tag, value) on that row;
  otherwise leave the dropdown open for click-and-release
  selection (R2069, R2070)
- onClickOutside(event) / onEscape(): dismiss the open dropdown
  without selecting (R2070)
- openPanelForTag(tag, value): open an `<ark-search>` panel at
  the indicator's seam, seeded with `tag: value`. For HTML
  chunks, insert the panel after the chunk container; for PDF
  chunks, invoke the parent `<pdf-chunk>` element's existing
  "split at the seam" handler with the indicator's seam as the
  target (R2069, R2082); also invoked directly by
  TagOverviewSidebar to bypass the pick-list (R2049 of
  TagOverviewSidebar)
- onKeyDown(event): Enter or Space opens the dropdown; arrow
  keys navigate within the dropdown; Enter on a focused row
  selects it (R2072)
- always-show-dropdown: even with one `<ark-tag>` child, the
  dropdown still appears — no single-tag shortcut (R2071)
- ARIA: role `button`, `aria-haspopup="menu"` (R2068)

## Collaborators
- ArkTagElement: children carrying name/value/externalFile/
  externalTarget; the dropdown rows are these children (or
  rendered from their data)
- ArkSearchElement: opened at the seam, seeded with
  `tag: value`
- PdfChunkElement: parent in PDF content; its
  "split at the seam" handler hosts the search panel inline
- TagOverviewSidebar: invokes openPanelForTag to dispatch
  search-icon clicks from the sidebar

## Sequences
- seq-tag-overview-click.md
