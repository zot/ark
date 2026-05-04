# TagOverviewSidebar
**Requirements:** R2032, R2033, R2034, R2035, R2036, R2037, R2038, R2039, R2040, R2041, R2042, R2043, R2044, R2045, R2046, R2047, R2048, R2049, R2050, R2051, R2052, R2053, R2054, R2055, R2056, R2057, R2058, R2059, R2060, R2061, R2062, R2063, R2064, R2083, R2084, R2130, R2131

Right-side sidebar in `/content/` that surfaces a document's
headings and tags as a unified, navigable outline. Mounts in any
`/content/` view (standalone or `<ark-search>` iframe). Acts as a
*remote control* for the body — its icons dispatch the same
actions that the corresponding body element does when clicked
directly. No shadow DOM — inherits host theme CSS.

## Knows
- mode: `collapsed` | `abbreviated` | `full` — current display
  mode. Each file opens in `abbreviated`; mode does not persist
  across files (R2033, R2036)
- entries: ordered list of `{kind, chunkId, headingText?,
  headingLevel?, tagName?, tagValue?, ext?}` — derived from
  document scan of `<h1>`–`<h6>`, `<ark-tag>`, and
  `<ark-ext-tags>` elements (R2040, R2041, R2042, R2043)
- currentSection: id of the entry whose section start most
  recently passed the top of the viewport (R2045)
- openPeek: id of the single entry currently expanded in
  abbreviated mode, if any (R2044)
- filterText: substring filter input (R2052)
- filterCategories: subset of {headings, inline, ext} that
  passes the category filter; empty = all (R2055)
- widthByMode: `{abbreviated, full}` — mode-specific persisted
  widths loaded from DB I records `sidebar-width-tag-name` and
  `sidebar-width-tag-name-value` (R2063)
- defaultWidth: 25% of viewport, used on first open if no I
  record present (R2062)
- viewport: bounding rect of the presented content slice — the
  cutoff for "tags ahead" (R2032)

## Does
- mount(host): scan host DOM for `<h1>`–`<h6>`, `<ark-tag>`,
  `<ark-ext-tags>` to build entries; observe chunk/heading
  intersections for auto-track (R2045); render the badge
  + sidebar; load widthByMode from server I-records via
  HostAPI; if entries empty, render nothing (R2037)
- cycleMode(): on badge main-zone click, advance mode
  collapsed → abbreviated → full → collapsed; resize sidebar to
  widthByMode[next] when applicable (R2034, R2063)
- openCategoryDropdown(): on badge `[▼]` click, present a custom
  popover with three checkboxes (headings, inline, ext); empty
  selection means all (R2039, R2055)
- updateBadge(): set badge text — total count when no filter
  active, filtered count + indicator-on-`[▼]` when filtered;
  in collapsed mode, prepend the current section's first entry
  (R2038, R2056)
- handleScroll(): IntersectionObserver fires when a
  chunk/heading boundary crosses the viewport top; recompute
  currentSection; update auto-track highlight; if currentSection
  is filtered out, fall back to nearest visible entry above
  (R2045, R2058)
- applyFilter(): tokenize filterText on whitespace; for each
  entry, match all tokens (substring, case-insensitive) against
  the entry's currently-visible text (mode-dependent — name +
  heading text in abbreviated, name + value + heading text in
  full; hover-revealed values excluded); intersect with
  filterCategories; highlight matched substrings using the
  existing ark search highlight style (R2052, R2053, R2054)
- onRowClick(entry): scroll the document to entry.chunkId
  (R2046, R2047)
- onSearchIconClick(entry): for inline tags, dispatch a click
  on the corresponding body `<ark-tag>` (which scrolls and
  opens an `<ark-search>` panel) (R2048); for ext tags,
  invoke the body `<ark-ext-tags>` element's
  `openPanelForTag(tag, value)` method, bypassing the pick-list
  (R2049)
- onExternalLinkClick(entry): navigate the page to entry's
  `externalFile` source document, scrolling to the relevant
  chunk (R2050)
- onExternalLinkHover(entry): show tooltip with three lines —
  DEFINITION-PATH (= entry.externalFile), divider, THIS-PATH
  (= current page path), and `anchor: ANCHOR-SPEC` line (omit
  when entry.externalTarget is empty) (R2051)
- togglePeek(entry): in abbreviated mode, open this entry's
  inline value display; close any other open peek; tap on an
  open peek closes it; tap elsewhere closes the open peek
  without opening another (R2042, R2044)
- handleResize(): drag handle on left edge sets sidebar width;
  enforce min (badge-readable) and max (`viewport - 3rem`);
  persist new width to widthByMode[mode] via HostAPI; resize
  handle inactive in collapsed mode (R2060, R2061, R2064);
  touch-draggable (R2060)
- onModeSwitch(): when mode changes among non-collapsed modes,
  resize sidebar to widthByMode[new mode] (R2063)
- inheritsInIframe: same component instance mounts in
  `<ark-search>` result iframes — no separate code path
  (R2083)
- publishWidth(): on every width change (drag, mode switch,
  mount, collapse), set `--ark-tag-overview-width` on
  `document.documentElement` to the sidebar's current
  `offsetWidth`, so unrelated content (e.g. PDF-host
  `<ark-search>` panels) can position itself relative to the
  sidebar without coupling. The injected stylesheet also carries
  a `body[data-pdf-host] ark-search` rule that consumes the var
  with a fixed gutter fallback (R2130, R2131)

## Collaborators
- ArkTagElement: dispatch target for inline-tag search-icon
  clicks; the element's existing click handler scrolls and
  opens the `<ark-search>` panel
- ArkExtTagsElement: dispatch target for ext-tag search-icon
  clicks; exposes `openPanelForTag(tag, value)` to bypass
  the pick-list
- HostAPI / Server: read and write per-mode I-record widths
  (`sidebar-width-tag-name`, `sidebar-width-tag-name-value`);
  receive scroll-to-chunk navigation requests
- Server (Go): emits `<ark-ext-tags>`, extended `<ark-tag>`
  attributes, `<ark-heading>` (PDF), and `id` on inline
  `<ark-tag>` and `<h1>`–`<h6>` so the sidebar can scan and
  anchor entries

## Sequences
- seq-tag-overview-load.md
- seq-tag-overview-click.md
