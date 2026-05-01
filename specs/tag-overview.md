# Tag Overview

A right-side sidebar in `/content/` that surfaces a document's
headings and tags as a unified, navigable outline. Inline tags,
ext-routed tags, and headings (markdown and PDF) all appear as
entries. Three sidebar modes let the reader dial visibility from
"out of the way" through "density map" to "full detail."
Language: TypeScript (custom elements + sidebar component), Go
(server-side `/content/` enrichment).

`<ark-search>` results render via `/content/` iframes, so they
inherit the overview automatically — no separate surface.

## Problem

Ext-routed tags don't appear in the body of their target file —
the routing lands them via V records, not file text. Without a
surfacing affordance, the reader has no way to see them while
reading. Headings already render structurally, and inline tags
already render as `<ark-tag>` widgets, but neither is summarized
in a way that supports outline-style navigation across a long
document.

A unified overview solves all three: surface ext routings, give
inline tags a navigable outline, and provide a per-document
table-of-contents that mixes structure (headings) with content
(tags).

## Mental model

- The sidebar is a *document overview*. Headings, inline tags,
  and ext tags share one outline — richer than a heading-only
  TOC because tags carry topic and status.
- Ext tags are first-class entries with a "virtual" icon (⊕)
  marking them and an external-link icon (↗) for navigating to
  the source document.
- The sidebar is a parallel navigator, not a coupled scroll.
  Document scroll updates an auto-track highlight; clicking an
  entry scrolls the document. The doc leads, the sidebar
  follows.
- The sidebar is a *remote control*. Each tag row's icons
  dispatch the same actions as clicking the corresponding body
  element directly — sidebar doesn't run a parallel renderer.

## Sidebar surface

### Scope

Entries shown are for the current scroll position through the
end of the presented content. `/content/` can serve a slice of
a file; the cutoff is the bottom of the slice.

### Three modes

The sidebar has a badge that doubles as a mode-cycle control.
Three modes:

1. **Collapsed (badge only)** — only the badge is shown, sidebar
   hidden. Badge text: first entry of the current section + a
   total count (e.g., `# Munsters + 5`).
2. **Abbreviated sidebar** — names only (heading text, tag
   names), grouped per section. Density map.
3. **Full sidebar** — names with values for tag entries.
   Deep-read mode.

Cycle order on badge click: 1 → 2 → 3 → 1. A small mode glyph
on the badge (e.g., `▢`/`▤`/`▦`) shows current mode so the user
knows what the next click does.

Each file opens in abbreviated mode. Mode does not persist
across files. Sidebar widths *do* persist (one per non-collapsed
mode — see Resize).

A file with no headings and no tags shows no badge — no chrome
for content that doesn't exist.

### Badge layout

`[▦ N tags][▼][filter input ...........]`

Two visibly separate hit zones on the badge:
- `[▦ N tags]` cycles modes.
- `[▼]` opens the category-filter dropdown.

### Entry contents

- **Heading entries**: heading text, indented per heading level.
  No icons. Row click is the only affordance.
- **Tag entries**: tag name (full mode also shows value), grouped
  per section under their nearest heading or chunk location.
  Each tag row carries a search icon (🔍) on the right.
- **Ext entries**: a virtual-tag icon (⊕) precedes the tag name
  and an external-link icon (↗) sits at the right of the row,
  after the search icon. All icons appear in both abbreviated
  and full modes.

In abbreviated mode, hover (desktop) or tap (touch) reveals a
single entry's value inline without leaving abbreviated mode.
At most one entry's peek is open at a time: any tap closes any
open peek; tapping a closed entry then opens it.

### Section semantics

A *section* in the sidebar is anchored by either a heading
(markdown or PDF) or a tagged chunk (whichever appears first).
Auto-track highlights the section currently at the top of the
viewport; that section's entry remains current until the next
heading or tagged chunk reaches the top.

### Click matrix

| Row type    | Row text         | 🔍 search icon                                     | ↗ external link        |
|-------------|------------------|----------------------------------------------------|------------------------|
| Heading     | scroll to chunk  | —                                                  | —                      |
| Inline tag  | scroll to chunk  | dispatch click on body `<ark-tag>`                 | —                      |
| Ext tag     | scroll to chunk  | dispatch click on body indicator with this row tag | navigate to source doc |

The 🔍 icon on a tag row opens an `<ark-search>` panel at the
anchor (scrolling to it if needed), seeded with `tag: value`.
For ext tags, the dispatch parameterizes the indicator's
panel-open action with the specific tag from the clicked row;
no pick list appears for sidebar-driven opens.

The ↗ icon on an ext row navigates to the source document. Its
hover (or long-press on touch) shows a three-line tooltip:

```
DEFINITION-PATH
---------------
THIS-PATH
anchor: ANCHOR-SPEC
```

DEFINITION-PATH is the source file (`externalFile` attribute).
THIS-PATH is the file currently being viewed. The `anchor:` line
is omitted entirely when there is no anchor.

### Filter

Two filter mechanisms in the sidebar header:

**Substring input** — tokenized, order-independent: `reat ever`
matches "every creature." Tokens match against the *currently
visible text* per mode (abbreviated → name + heading text only;
full → name + value + heading text). Hover-revealed values in
abbreviated mode don't count as visible. Match highlighting in
the entries reuses the existing ark search highlight style.

**Category dropdown** — opens from the badge's `[▼]` hit zone.
Custom popover (themed) with three checkboxes: headings, inline
tags, ext tags. Empty selection means *all* — re-checking one
category from empty shifts to "only that one" without an
intermediate deselect step.

When a filter is active, the badge shows the *filtered* count
with an indicator on the `[▼]` glyph. Filter resets per file;
state persists across mode toggles within the same file. When
the auto-track current entry is filtered out, the highlight
falls to the nearest visible entry above (no highlight if
none).

On narrow sidebars where the filter input doesn't fit next to
the badge, it wraps to the next line.

### Resize

The left edge of the sidebar is a draggable resize handle.
Touch-draggable on touch devices. Minimum width keeps the badge
readable; maximum width is `viewport - 3rem`. Default width
on first open is 25% of viewport.

Two distinct widths are remembered, one per non-collapsed mode
(abbreviated and full). Switching modes resizes the sidebar to
that mode's stored width. Widths are stored as I records in the
DB:
- `sidebar-width-tag-name` (abbreviated mode)
- `sidebar-width-tag-name-value` (full mode)

The resize handle and persisted widths apply only when the
sidebar is open (modes 2 and 3).

## Body surface

### Indicator: `<ark-ext-tags>`

A custom element emitted at the top of any chunk that has ext
routings. Hosts the indicator icon and the pick-list dropdown.
Contains one `<ark-tag>` child per ext-routed tag at that
location.

The indicator icon is a Bootstrap-style tag glyph: a
single-tag glyph when the location has one ext tag, a stacked
multi-tag glyph when there are several. The icon communicates
multiplicity before any interaction. SVG inside the element so
it scales cleanly and themes via CSS.

The indicator does not consume vertical space — it overlays the
chunk's first line area. Text flows as if the indicator weren't
there.

#### Pick-list interaction

Mousedown on the indicator opens a dropdown listing each ext
tag at that location with its value. Two gestures supported,
matching native menu behavior:

- **Click-and-drag**: mousedown on indicator → drag down to a
  tag → mouseup on that tag → opens an `<ark-search>` panel
  at the seam, seeded with `tag: value`. One fluid gesture.
- **Click-and-release**: mousedown on indicator → mouseup
  without moving onto a tag → dropdown stays open. User reads
  the list, then either clicks a tag (opens search) or
  dismisses (click outside, Escape).

Even when only one ext tag exists at a location, the dropdown
still appears — the user may want to read the tag/value without
opening the search panel.

ARIA role: `button` with `aria-haspopup="menu"`. Keyboard
support: Enter/Space opens the dropdown, arrow keys navigate,
Enter selects.

### Tags: `<ark-tag>` (existing element, extended)

The same element type carries both inline and ext-routed tags.
Inline tag rendering is unchanged. When ext-routed (i.e., when
appearing as a child of `<ark-ext-tags>`), the element gains
two attributes:

- `externalFile` — source file path (where the `@ext:`
  declaration lives).
- `externalTarget` — anchor part of the target spec (whatever
  followed the `:`). Empty/omitted when the target was a bare
  path or bare UUID. The target file path is implicit — it's
  the file currently being rendered — so it is not duplicated
  on the element.

CSS distinguishes inline from ext-routed tags via the parent
selector `ark-ext-tags > ark-tag` or the attribute presence
`[externalFile]`.

All `<ark-tag>` elements gain an `id` attribute so the sidebar
can target them via DOM anchoring.

### Headings: `<ark-heading>` and HTML headings

- **Markdown content**: standard HTML heading elements
  (`<h1>`–`<h6>`) — already what the chunker emits. Each gains
  an `id` attribute for sidebar anchoring.
- **PDF content**: server emits `<ark-heading rect="...">`
  elements positioned absolutely over the `<pdf-chunk>` canvas
  at the rect-derived coordinates. v1 is flat — no level info.
  pdftext gains a heading rect output (today's chunker has
  none).

## Server payload

The server enriches `/content/` responses inline (push, not
pull). No separate endpoint. Per chunk, the server emits any
`<ark-ext-tags>` block at the top followed by the chunk's
existing content, with `<ark-tag>` children carrying the
appropriate attributes.

The server consults the inline tag store, `chunkToTargets` for
ext routings, and the chunker / pdftext output for headings.
Implementing this requires a new `tagsForChunk` Go method,
which does not exist today.

## Element positioning

- **HTML chunks**: `<ark-ext-tags>` and `<ark-heading>` (when
  applicable) are positioned absolutely within their chunk
  container — `position: absolute` on a `position: relative`
  chunk wrapper — so they overlay without disrupting flow.
- **PDF chunks**: positioned absolutely over the `<pdf-chunk>`
  canvas at coordinates derived from the rect attribute. The
  same approach `<ark-tag>` already uses for inline PDF tags
  (the canvas is drawn; markup rides above it).

## Search-result inheritance

`<ark-search>` results render in `/content/` iframes. The
sidebar, badge, indicators, and filter all appear in those
iframes the same as in a standalone `/content/` view. No
separate rendering path for search results.

## Scope boundary

This spec covers the *rendered* content view (HTML/markdown/PDF
read views). The CodeMirror-based markdown editing view is out
of scope for v1 — its DOM does not expose HTML heading elements
the way the rendered view does, so the sidebar needs a
different discovery path there. Editing-view support is a v2
follow-up.

PDF heading levels are flat for v1. Future work in pdftext can
compute the percentage by which a heading's dominant font size
exceeds the page median, providing a ranking signal the
sidebar could use for indented levels.
