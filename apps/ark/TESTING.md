# Ark - Testing

## Gaps

### Tag Forge reframe (slice B/C deferred items)
- **Per-line annotation** — author a tag *about* a specific line mid-chunk. v1's inline-remove deletes the matching `@tag:` line (the fold algorithm does this at `[edit]` time); inline-add prepends to the leading tag block. Mid-chunk authoring is deferred.
- **Tab-out auto-add** — `PendingWidget:autoAddOnTab()` is wired to `ui-event-blur` on the value input as a proxy. True keyboard tab-out detection needs a JS bridge or keypress handler the engine doesn't yet expose.
- **Same-origin iframe DOM mutation** — the desired-state overlay rewrites `<ark-ext-tags>` children in the chunk-text iframe. Requires same-origin; confirm during `/ui-thorough` testing.
- **CurrentTagRow:applyEdit (ext-row in-edit editing)** — defined per design.md but the viewdef-side wiring (input edit-on-blur → applyEdit) is deferred. Read-only `queueRemove()` works in both modes.
- **Desired-state overlay on iframe DOM** — `iframeBridgeCode` currently only scrapes; the overlay pass that rewrites `<ark-ext-tags>` children to reflect pending ops is deferred. Current-tags display still computes desired state Lua-side.

### Unimplemented Design Features
- Node:hideIgnoreCheckbox, Node:toggleHonorIgnore (ignore file integration)
- Node:noExceptions, Node:applyException (exception combobox)
- Source:notLoading (loading indicator)

### Messaging Dashboard
- Messaging:isEmpty, Messaging:isLoading (empty state, loading indicator)

### External Methods (not dead — called from MCP app)
- Ark:showSearching, Ark:showMessaging, Ark:showCuration — called by MCP displayArk/displayArkMessages/displayArkCuration

### Missing Methods (referenced in viewdefs, not defined in Lua)
- `noBookmarkLag()`, `bookmarkLabel()` — bookmark-lag display fields; viewdef references precede Lua implementation

### Fast Code
Features added via rapid prototyping that may need review:
- Search panel renovation: file tree + Add Source form moved to slide-over `.ark-overlay` (search owns full right-panel height); `selectSource()` now toggles, new `deselectSource()` for X close button
- Removed dead methods after overlay refactor: `hideSourceDetail`, `showPlaceholder`, `hidePlaceholder`, `hideAddForm` (placeholder element deleted, hide-toggles replaced by `ark-overlay-open` class binding)
- In-process search migration (io.popen → mcp:search_grouped)
- SearchFileGroup grouped results with expandable chunks
- Filter panel (4-field compose with source filters)
- Project editor overlay (Choose Projects)
- Pattern editing (gear toggle, textarea diff save)
- Resize handle (JS mouse tracking)
- Hits per file cycling (1/3/all)
- buildFilterOpts() — complex filter composition logic
- resolveSlugName() — double-dash scope suffix handling
- Ark root shell + Ark.Searching rename
- Ark.Messaging with mcp:inbox() data pipe
- KANBAN namespace ViewList for horizontal column layout
- Conversation merging (request + response grouped by requestId)
- FilterChip type with cycle behavior and OR composition
- Bold italic addressing on cards
- Response status line with dashed divider
- Three-state chip visuals (inactive/active/empty)
- Filter file intersection (cross-product AND semantics with source patterns)
- Session cache (opts.session = "ui" for ChunkCache reuse)
- FilterChip smart cycling (skip zero-count states, mode renamed both→all)
- StatusChip type with toggle visibility per status column
- Bookmark lag display (bookmarkChips, bookmarkLabel, yellow pills)
- effectiveStatus simplified (request status only, no response rank)
- searching local→global forward reference fix
- about→fuzzy mode rename (search mode, mutate migration)
- mcp.listSource() replacing listDir + N applyWhy subprocesses
- Node collapse resets _childrenLoaded for re-fetch
- Messaging sort controls (cycleSortField, toggleSortDir, mcp.sort)
- statusDate field on Message for sorting
- MessageDetail presenter (load, tabs, status controls, complete)
- Message card click → showDetail() instead of openFile()
- Detail dialog inline in Messaging viewdef
