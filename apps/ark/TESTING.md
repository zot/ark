# Ark - Testing

## Gaps

### Tag Forge machinery reconcile (2026-07, PENDING #37)
- **Shim: fire-vs-reindex race** — internal fires re-fetch chunkInfo/chunkText at fire time, but a fire racing the async reindex of a just-fired write can still see a stale byte range. Shim limitation; the real Go hooks (#65) resolve targets server-side on the DB actor.
- **Shim: external add ≈ replace** — both external cells ride `mcp.setExtTag` (set semantics); the machinery's true external-add (append edge) waits for the real hooks.
- **Shim: no ledger trail** — the candidate line, `@count`, and positive judgment do not exist on the shim path (honesty rule: no UI copy claims them). Discharged by #65.
- **Per-line annotation** — author a tag *about* a specific line mid-chunk. Internal add prepends to the leading tag block (markdown stencil); mid-chunk authoring is deferred.
- **Tab-out auto-add** — `ProposalWidget:autoAddOnTab()` is currently unwired (dead method kept per design). True keyboard tab-out detection needs a JS bridge or keypress handler the engine doesn't yet expose.
- **Stale persisted pins (pre-existing, observed 2026-07-23)** — `state.json` pins persist chunkIDs across restarts, but reindexing can reassign IDs: a pin's `path` said `.scratch/BRAINSTORM.md` while its chunkID resolved to a read-only chat-JSONL chunk (so its widget correctly locked to ext for the *wrong* chunk). Pre-dates this pass (Accept had the same wrong-target exposure); wants an ark-core fix (validate pin path vs resolved chunkInfo path, drop or re-resolve mismatches).

### Unimplemented Design Features
- Node:hideIgnoreCheckbox, Node:toggleHonorIgnore (ignore file integration)
- Node:noExceptions, Node:applyException (exception combobox)
- Source:notLoading (loading indicator)

### Messaging Dashboard
- Messaging:isEmpty, Messaging:isLoading (empty state, loading indicator)

### External Methods (not dead — called from MCP app)
- Ark:showSearching, Ark:showMessaging, Ark:showCuration, Ark:showLuhmann — called by MCP displayArk/displayArkMessages/displayArkCuration/displayArkLuhmann

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
- Tag Forge rename (displayed title "Curation" → "Tag Forge"; docs updated)
- Search JS bridge: name_tokens/value_tokens/name_match → primary_tag_query/primary_file_tag (sigil-form tag predicate, R2442/R2453)
