# Ark - Design

## Intent

The ark app has three views: Searching (index manager + full-text
search), Messaging (cross-project message dashboard), and Curation
(vocabulary-maintenance workshop sitting on top of the Phase 1
chunk → tag, tag → chunk, and tag → tag bridges). A thin root
object routes between them via `ui-view`. The MCP shell has three
bottom-bar buttons, one per view.

## Layout

### Root (thin shell)
```
+------------------------------------------------------+
| <div ui-view="currentView()">                        |
|   renders Ark.Searching or Ark.Messaging              |
+------------------------------------------------------+
```

### Searching View (existing)
```
+-------------------+------------------------------------------+
| Sources [⇄][⟳]    | <ark-search>                             |
|  filter bar       |   [tag v] [@ ~ name : Aa value] [×][✕]  |
|-------------------|   [+ add filter]                         |
| > project-name    |   [+ save]                               |
|   [📄][🧠][💬]     |   ┌─────────────────────────────────┐    |
|   data-source     |   │ file.md              [📁]        │    |
|   [📊]            |   │ ┌─ iframe preview ────────────┐  │    |
|-------------------|   │ │  chunk content with highlights│ │    |
| [✏️ Choose Projects]│ └───────────────────────────────┘  │    |
+-------------------+------------------------------------------+
| ✓ 1929 | ✗ 1 | ? 2625 | Server: ●                           |
+--------------------------------------------------------------|
```

### Messaging View
```
+------------------------------------------------------+
| Messages                              [↻ Refresh]    |
|------------------------------------------------------|
| Open        | Accepted    | In-Progress | Future     |
|-------------|-------------|-------------|------------|
| ┌─────────┐ | ┌─────────┐ |             | ┌────────┐ |
| │chunker  │ | │         │ |             | │tag+val │ |
| │microfts2│ | │         │ |             | │ark→ark │ |
| └─────────┘ | └─────────┘ |             | └────────┘ |
| ┌─────────┐ |             |             |            |
| │fuzzy    │ |             |             |            |
| │microfts2│ |             |             |            |
| └─────────┘ |             |             |            |
+------------------------------------------------------+
```

Columns only shown when they have items. Cards use `content-card`
theme class. Click card → `mcp:open(path)`.

### Curation View (workshop reframe, 2026-05)
```
+----------------------------------------+----------------------+
| Curation  Sweep: idle  [⚡][Accept (N)] |                      |
+----------------------------------------+----------------------+
| Pinned chunks (3) [⤓ Sweep older]       | Tag explorer         |
| ┌────────────────────────────────────┐ | [_____________]      |
| │ [tag ][value][rem][ext][X]         │ | Defined tags         |
| │ [tag ][value][rem][ext][X]         │ |  • design-decision   |
| │ [+]                                 │ |  • feedback          |
| │ NEW path/file.md  (2 pending)   X  │ |                      |
| │  [↓ Stage]  [↶ Revert]              │ | Focused: design-dec  |
| │  > tag suggestions  (8)             │ |  Top chunks (10)     |
| │   • design-decision  0.83           │ |   • path/A  0.83 📌  |
| │   • feedback         0.72           │ |  Related tags        |
| └────────────────────────────────────┘ |   • feedback  0.78   |
| ┌────────────────────────────────────┐ |  Drift               |
| │ [tag ][value][rem][ext][X]         │ |   • A↔B  0.62        |
| │ [+]                                 │ |                      |
| │ path/other.md                    X  │ |                      |
| │  > tag suggestions                 │ |                      |
| └────────────────────────────────────┘ |                      |
+----------------------------------------+----------------------+
```

Primary surface: pinned cards with per-card pending widget
stacks. Each widget authors one tag operation; Stage folds
filled widgets into a per-card staged-ops buffer (in-memory).
Accept (panel-level) commits every card's ops via
`mcp.replaceRegion` (inline) and `mcp.setExtTag` /
`mcp.removeExtTag` (ext).

Secondary surface (right column): the tag explorer — Type a
tag (or click one in the defined-tags picker), see top chunks,
related tags, drift pairs. Click related → switch focus; click
chunk → pin. Retained from the pre-reframe design.

## Data Model

### Ark (root shell)

| Field | Type | Description |
|-------|------|-------------|
| _viewMode | string | "searching", "messaging", or "curation" |
| _searching | Ark.Searching | The index/search view |
| _messaging | Ark.Messaging | The messaging view |
| _curation | Ark.Curation | The curation workshop view |

### Ark.Searching (renamed from Ark)

All existing fields and methods from the previous Ark type, minus
the old search UI state. Search is now handled by the `<ark-search>`
web component in the right panel.

Key fields: _sources, selectedSource, _searchView,
_displayItems, _projects, _dataSources, _projectSearchOpen,
_projectCandidates, _showPatterns, _statusCounts, _serverRunning.

Removed fields (now in `<ark-search>` element):
searchQuery, searchMode, _searchGroups, _hitsPerFile,
_showFilterPanel, filterFiles, excludeFiles, filterContent,
excludeContent.

### Ark.Messaging

| Field | Type | Description |
|-------|------|-------------|
| _messages | Ark.Message[] | Merged conversations from mcp:inbox() |
| _loading | boolean | Refresh in progress |
| _chips | Ark.FilterChip[] | One per project, cycles filter modes |
| _statusChips | Ark.StatusChip[] | One per status, toggles column visibility |
| _sortField | string | Current sort field: "date", "to", "from", "subject" |
| _sortDesc | boolean | Sort direction: true = descending |

### Ark.Message

A conversation: one request merged with its response(s). Column
placement uses the **request's** `@status` — the requester owns the
issue. One card per conversation, never duplicated.

| Field | Type | Description |
|-------|------|-------------|
| requestId | string | Conversation ID (from @ark-request/@ark-response) |
| kind | string | "request", "response", or "self" |
| reqStatus | string | Request's @status |
| reqTo | string | Request's target project |
| reqFrom | string | Request's source project |
| reqSummary | string | Request's @issue text |
| reqPath | string | Request file path |
| respStatus | string | Response's @status (empty if no response) |
| respTo | string | Response's target project |
| respFrom | string | Response's source project |
| respSummary | string | Response's @issue text |
| respPath | string | Response file path |
| _hasResponse | boolean | Whether a response file exists |
| reqResponseHandled | string | Request's @response-handled value |
| respRequestHandled | string | Response's @request-handled value |
| statusDate | string | @status-date for sorting ("1970-01-01" if missing) |

### Ark.FilterChip

| Field | Type | Description |
|-------|------|-------------|
| project | string | Project name |
| mode | string | "all", "to", "from", "none" — cycles on click |
| matchCount | number | Messages matching current mode |
| toCount | number | Messages where this project is target |
| fromCount | number | Messages where this project is sender |

### Ark.StatusChip

| Field | Type | Description |
|-------|------|-------------|
| status | string | Status value (open, accepted, etc.) |
| count | number | Messages in this status |
| visible | boolean | Whether this column is shown |

### Ark.MessageColumn

| Field | Type | Description |
|-------|------|-------------|
| status | string | Column status label |
| _items | Ark.Message[] | Messages in this column |

### Ark.Curation

The workshop. The canonical pinned list lives Go-side as
`sys.curation.pinned`; Lua reads it through a host-mirrored table
and decorates entries with `Ark.PinnedChunk` presenters via
`itemWrapper`. Tag explorer fields (focus, defined-tag picker)
are retained from the pre-reframe design.

| Field | Type | Description |
|-------|------|-------------|
| focusedTag | string | Currently focused tag (explorer panel), "" when unfocused |
| _focusInput | string | Text input value for tag focus / picker filter |
| _focusedChunks | Ark.HotChunk[] | Top-K chunks for focusedTag |
| _focusedRelated | Ark.RelatedTag[] | Related tags for focusedTag |
| _focusedDrift | Ark.DriftPair[] | Drift pairs within focusedTag |
| _focusError | string | Last focus error message ("" when none) |
| _newCutoff | number | Threshold timestamp — pins newer than this are NEW |
| _lastViewedAt | number | Unix epoch of last view activation |
| _sweepBusy | boolean | True while a sweep call is in flight |
| _sweepResult | string | Last completed sweep summary ("" when none) |
| _definedTags | table[] | Lazy-loaded `{tag, description}` list for the picker |
| _definedTagsLoaded | boolean | Whether `mcp.definedTags()` has been called |

### Ark.PendingWidget

A single pending tag operation queued on a `Ark.PinnedChunk`'s
widget stack. Each widget authors one (tag, value) pair against
the chunk, either inline (text edit) or routed (ext mirror).

| Field | Type | Description |
|-------|------|-------------|
| _chunk | Ark.PinnedChunk | Parent card reference (for stage callbacks) |
| tagName | string | Tag name input value |
| tagValue | string | Tag value input value (ignored when removeMode true) |
| removeMode | boolean | true = remove this tag from chunk; false = add/change |
| extMode | boolean | true = route via `@ext` mirror; false = inline text edit |
| extBase | string | "uuid" or "path" — only meaningful when extMode |
| extBaseValue | string | UUID string or absolute path matching extBase |
| extLocatorKind | string | "string", "regex", "absolute", or "bare" |
| extLocatorText | string | Locator value to embed in the target spec |
| _extScopeChunks | number | Cross-file scope readout: chunks |
| _extScopeFiles | number | Cross-file scope readout: files |
| _withinFileDupCount | number | Within-file `@id` duplication count (0 if none) |
| _extLoaded | boolean | True after `mcp.suggestExtLocator` populated defaults |

### Ark.PinnedChunk

Per-pin presenter created via `itemWrapper`. Decorates a
host-mirrored entry from `sys.curation.pinned` (carrying
`chunkID`, `fileID`, `path`, `pinnedAt`) with the per-card UI
state: pending widget stack, staged-ops buffer, lazy-loaded
chunk metadata, lazy-loaded tag suggestions.

| Field | Type | Description |
|-------|------|-------------|
| viewItem | Variable | ViewList item handle (gives access to `baseItem` for chunkID/path/pinnedAt) |
| _widgets | Ark.PendingWidget[] | Pending widget stack. Empty-start invariant: always at least one empty widget. |
| _stagedOps | table[] | Per-card buffer of `{kind, ...params}` records ready for Accept. Cleared on revert or successful Accept. |
| _chunkInfo | table | Cached `mcp.chunkInfo` result: `{chunkID, fileID, path, range, byteStart, byteEnd, writable, commentSyntax}`. Nil until loaded. |
| _chunkInfoLoaded | boolean | True after the first chunkInfo fetch (success or failure) |
| _chunkInfoError | string | Last chunkInfo error ("" when none) |
| _suggestions | Ark.TagSuggestion[] | Loaded tag candidates from `mcp.suggestTagNames` |
| _suggestionsLoaded | boolean | Whether the suggestion load completed |
| _suggestionsError | string | Last suggestion load error ("" when none) |
| _acceptError | string | Last per-card Accept error ("" when none) |
| _confirmDismiss | boolean | UI flag: confirm-dismiss alert visible (pending > 0) |

**Staged op record shape** (`_stagedOps` entries):

| Field | Type | Description |
|-------|------|-------------|
| kind | string | "inline-add", "inline-remove", "ext-set", or "ext-remove" |
| tagName | string | Tag name |
| tagValue | string | Tag value (ignored for `inline-remove` / `ext-remove`) |
| targetSpec | string | For ext-* ops: composed `BASE` or `BASE:NARROWER` string |

### Ark.TagSuggestion

A row in a pinned chunk's tag-suggestions panel. Shape mirrors the
Lua bridge return from `mcp.suggestTagNames`.

| Field | Type | Description |
|-------|------|-------------|
| tag | string | Suggested tag name |
| score | number | Cosine score (higher = better match) |
| motivatingFiles | table[] | Definition files that contributed |

### Ark.HotChunk

A chunk row in the focused-tag panel's "Top chunks" list. Shape
mirrors the Lua bridge return from `mcp.topKChunksForTag` /
`mcp.chunksForTag`.

| Field | Type | Description |
|-------|------|-------------|
| chunkID | number | Chunk identifier |
| fileID | number | Owning file's ID |
| path | string | Absolute path of the chunk's file |
| score | number | Aggregate cosine score |

### Ark.RelatedTag

A row in the focused-tag panel's "Related tags" list. Shape
mirrors `mcp.relatedTags`.

| Field | Type | Description |
|-------|------|-------------|
| tag | string | Related tag name |
| score | number | Cosine score |
| srcPath | string | Path of focused tag's contributing definition file |
| dstPath | string | Path of related tag's contributing definition file |

### Ark.DriftPair

A row in the focused-tag panel's "Drift" list. Shape mirrors
`mcp.tagDrift`.

| Field | Type | Description |
|-------|------|-------------|
| pathA | string | Path of one definition file |
| pathB | string | Path of the other definition file |
| score | number | Cosine between the two ED vectors |

## Methods

### Ark (root)

| Method | Description |
|--------|-------------|
| new() | Create instance with Searching, Messaging, and Curation children |
| currentView() | Return _searching, _messaging, or _curation based on _viewMode |
| showSearching() | Set _viewMode = "searching" |
| showMessaging() | Set _viewMode = "messaging", refresh messaging |
| showCuration() | Set _viewMode = "curation", call _curation:onViewOpen() |
| curate(chunkID, fileID, path) | Pin a chunk to the curation workspace without flipping the view (always-add never-flip) |

### Ark.Searching

Source management, file tree, project editor unchanged. Search UI
is now the `<ark-search>` web component — Lua no longer does search.

**Right-panel layout:** `<ark-search>` always fills the right panel.
The file tree (`.ark-tree-panel`) and Add Source form (`.ark-add-form`)
sit on top as `.ark-overlay` elements (`position: absolute; inset: 0`)
with a `transform: translateX(100%)` resting state. The
`.ark-overlay-open` class is bound to `showSourceDetail()` (tree)
and `showAddForm` (form), animating them in from the right.

**Source-click toggle:** `selectSource(source)` toggles —
re-clicking the currently selected source clears `selectedSource`,
collapsing the tree overlay. `deselectSource()` is the same close
path, used by the X button in the tree header.

Key method: `searchFiltersJSON()` — builds filter_files and
exclude_files arrays from sidebar buttons, returns JSON string.
Read by JS SearchAPI via hidden span bridge.

Removed methods (now in `<ark-search>` element):
onSearchInput, search, buildFilterOpts (replaced by searchFiltersJSON),
searchResults, searchResultCount, clearSearch, hideSearchResults,
setModeContains, setModeFuzzy, setModeRegex, modeIsContains,
modeIsFuzzy, modeIsRegex, cycleHitsPerFile, hitsPerFileText,
toggleFilterPanel, filterPanelIcon, hasActiveFilters, hideFilterPanel.

Removed types: Ark.SearchFileGroup, Ark.SearchResult.

### Ark.Messaging

| Method | Description |
|--------|-------------|
| new() | Create instance, init chips |
| mutate() | Init chips/statusChips on schema change, trigger refresh |
| refresh() | Call mcp:inbox(true), group by requestId, merge into Messages, rebuild chips |
| columns() | Build MessageColumn[] from _messages, filtered by chips. Priority=high (runs before chip styling) |
| columnOrder() | Return ordered list of statuses that have messages |
| isLoading() | Return _loading |
| messageCount() | Total message count |
| filteredCount() | Count of messages passing current filter |
| countLabel() | "N of M messages" display string |
| cycleSortField() | Cycle sort field: date → to → from → subject |
| toggleSortDir() | Toggle sort direction (asc/desc) |
| sortLabel() | Current sort field name for display |
| sortDirLabel() | "▼" or "▲" for current direction |
| showDetail(msg) | Open message detail dialog |
| detail() | Return current MessageDetail or nil |
| hasDetail() | Whether detail dialog is open |

### Ark.FilterChip

| Method | Description |
|--------|-------------|
| cycle() | Advance mode: all → to → from → none → all (skips modes with 0 count) |
| label() | Project name + directional count display |
| chipClass() | CSS class based on mode and matchCount |

### Ark.StatusChip

| Method | Description |
|--------|-------------|
| toggle() | Toggle column visibility |
| label() | Status name + count |
| chipClass() | CSS class based on visibility and count |

### Ark.MessageColumn

| Method | Description |
|--------|-------------|
| statusLabel() | Human-readable column header |
| itemCount() | Number of messages in column |
| statusClass() | CSS class for column header color |

### Ark.Message

| Method | Description |
|--------|-------------|
| effectiveStatus() | Request's @status drives column placement |
| openFile() | Open request file (primary) via mcp:open() |
| openResponse() | Open response file via mcp:open() |
| shortSummary() | Truncated @issue for card display (60 char max) |
| projectLabel() | "from → to" formatted string |
| hasResponse() | Whether a response file exists |
| noResponse() | Inverse of hasResponse |
| responseStatusLabel() | Human-readable response status |
| statusClass() | CSS class for status badge color |
| bookmarkChips() | Return stale bookmark chips as "PROJECT:status" strings. Empty when all bookmarks current. |
| showDetail() | Open message detail view via messaging:showDetail(self) |

### Ark.MessageDetail

| Field | Type | Description |
|-------|------|-------------|
| _message | Ark.Message | The message being viewed |
| _html | string | Rendered request markdown |
| _tags | table | Request tag block as {name=value} |
| _reqPath | string | Request file path |
| _respPath | string | Response file path |
| _respHtml | string | Rendered response markdown |
| _respTags | table | Response tags |
| status | string | Editable status (bound to dropdown) |
| reqResponseHandled | string | Editable response-handled value |
| respRequestHandled | string | Editable request-handled value |
| _open | boolean | Dialog visibility |

### Ark.Curation

| Method | Description |
|--------|-------------|
| new(instance) | Construct |
| mutate() | Init missing arrays/fields on schema change |
| pinned() | Return `sys.curation.pinned` for ViewList binding |
| curate(chunkID, fileID, path) | Proxy to `sys.curation.pin` — always-add never-flip |
| sweepOlder() | Proxy to `sys.curation.sweepOlder` |
| pinnedCount() | `#sys.curation.pinned` |
| newCount() | Count of pins where `pinnedAt > _newCutoff` |
| noNew() | newCount() == 0 |
| hasPinned() | `#sys.curation.pinned > 0` |
| noPinned() | inverse |
| onViewOpen() | Rotate `_newCutoff = _lastViewedAt`; set `_lastViewedAt = now` |
| **acceptChanges()** | Iterate pinned cards; for each card with filled-or-staged work: implicit-stage unstaged widgets, then execute staged ops via the per-card `executeStagedOps()`. Per-card errors land on the card's `_acceptError`. Successful cards clear `_stagedOps`. |
| **totalPendingCount()** | Sum of (filled-widget count + staged-op count) across every PinnedChunk presenter. Drives the `Accept changes (N)` badge. |
| **noPendingChanges()** | `totalPendingCount() == 0` (used by `ui-attr-disabled` on Accept) |
| loadDefinedTags() | Lazy `mcp.definedTags()` populate of `_definedTags` |
| filteredDefinedTags() | Filtered view over `_definedTags` per `_focusInput` |
| filteredDefinedTagCount() | length of the filtered view |
| focusTagFromInput() | Call focusTag(_focusInput) if non-empty |
| focusTag(tag) | Populate `_focusedChunks` / `_focusedRelated` / `_focusedDrift` via bridge calls |
| clearFocus() | Reset focus state |
| isFocused() / notFocused() / focusError() / noFocusError() / focusedChunkCount() / focusedRelatedCount() / focusedDriftCount() | tag-explorer accessors (unchanged) |
| sweepNow() | (unchanged for now — blocking call. Retrofit deferred to /mini-spec for async variant.) |
| sweepStatusText() / sweepBusy() | sweep-state accessors |

### Ark.PinnedChunk

| Method | Description |
|--------|-------------|
| new(listItem) | Construct presenter wrapping a ViewList item. Initializes `_widgets` with one empty widget (empty-start invariant). |
| chunkID() / path() / pinnedAt() | Accessors over `viewItem.baseItem` |
| dismiss() | If `pendingCount() > 0`, set `_confirmDismiss = true`. Otherwise call `sys.curation.dismiss(chunkID())`. |
| confirmDismiss() | Final dismiss after confirmation |
| cancelDismiss() | Clear `_confirmDismiss` |
| isNew() / notNew() | NEW pill helpers (compare pinnedAt to Curation's `_newCutoff`) |
| contentURL() / shortPath() | Path display helpers |
| loadChunkInfo() | Lazy `mcp.chunkInfo(chunkID())` — populates `_chunkInfo` / `_chunkInfoError` |
| chunkInfo() | Lazy accessor; returns `_chunkInfo` |
| commentSyntax() | `_chunkInfo.commentSyntax` (`""` for markdown) |
| writable() | `_chunkInfo.writable` (true unless read-only chunker) |
| readOnly() | inverse |
| **widgets()** | Return `_widgets`; lazy ensures empty-start invariant |
| **addWidget()** | Append a new empty `Ark.PendingWidget` to `_widgets` |
| **removeWidget(widget)** | Kill `X` — remove `widget` from `_widgets`; ensure empty-start invariant |
| **ensureEmptyWidget()** | If no widget is empty, append one (called after auto-add / kill) |
| **filledWidgetCount()** | Count of widgets where `:isFilled()` is true |
| **pendingCount()** | `filledWidgetCount() + #_stagedOps` |
| **hasPending()** | `pendingCount() > 0` |
| **noPending()** | inverse |
| **canStage()** | `filledWidgetCount() > 0` |
| **canRevert()** | `#_stagedOps > 0` |
| **stage()** | For each filled widget: build a staged-op record from its fields, append to `_stagedOps`. Remove staged widgets from `_widgets`. Restore empty-start invariant. |
| **revert()** | Recreate widgets from `_stagedOps` (reverse the stage transformation). Clear `_stagedOps`. |
| **executeStagedOps()** | Iterate `_stagedOps`; dispatch each through `mcp.replaceRegion` / `mcp.setExtTag` / `mcp.removeExtTag`. On error, set `_acceptError` and return false; on success, clear `_stagedOps` and return true. Called by `Curation:acceptChanges`. |
| acceptError() / noAcceptError() / hasAcceptError() | per-card error display helpers |
| loadSuggestions() | Lazy `mcp.suggestTagNames(chunkID, K)`; populates `_suggestions` / `_suggestionsError` |
| suggestions() / suggestionsError() / noSuggestionsError() / hasSuggestions() | tag-suggestion accessors |

### Ark.PendingWidget

| Method | Description |
|--------|-------------|
| new(chunk) | Construct an empty widget bound to its parent PinnedChunk |
| isEmpty() | true when `tagName == ""` and `tagValue == ""` and not removeMode and not extMode |
| isFilled() | true when `tagName != ""` (value can be empty for remove ops or boolean tags) |
| toggleRemove() | Flip `removeMode` |
| toggleExt() | Flip `extMode`. On first turn-on, call `mcp.suggestExtLocator(chunkID)` to populate `extBase` / `extBaseValue` / `extLocatorKind` / `extLocatorText` / `_extScopeChunks` / `_extScopeFiles` / `_withinFileDupCount`. Reads `locatorText` for the actual locator value (`locator` field has a known Go bug). |
| kill() | Call parent `chunk:removeWidget(self)` |
| setBase(base) | Update `extBase`; re-run suggestExtLocator to get new defaults if needed |
| setLocatorKind(kind) | Update `extLocatorKind`; clear `extLocatorText` for `bare`, otherwise leave editable |
| canStage() | true when isFilled() and parent chunk allows the operation (ext-only if read-only) |
| extToggleLocked() | true when parent chunk is read-only (ext always-on, can't toggle off) |
| scopeReadout() | `"will route to N chunks across M files"` string for the readout line |
| baseChoices() | `["uuid", "path"]` or `["path"]` (UUID hidden when chunk has no @id) |
| locatorChoices() | `["string", "regex", "absolute", "bare"]` |
| dupFlag() | `"UUID: %s... (×%d in this file)"` when `_withinFileDupCount > 1`, empty otherwise |
| autoAddOnTab() | Called from viewdef tab-out event. If `isFilled()` and the parent's last widget is this one, call `chunk:addWidget()`. |
| targetSpec() | Compose `BASE` or `BASE:NARROWER` from the ext fields (used by stage to build setExtTag arg) |
| stagedOpRecord() | Return a `{kind, tagName, tagValue, targetSpec}` record for the parent's `_stagedOps` |

### Ark.HotChunk

| Method | Description |
|--------|-------------|
| pin() | Call curation:curate(chunkID, fileID, path) |
| openFile() | Call mcp:open(path) |
| shortPath() | Display version of path |
| scoreLabel() | Formatted score string |

### Ark.RelatedTag

| Method | Description |
|--------|-------------|
| focus() | Call curation:focusTag(tag) |
| scoreLabel() | Formatted score |

### Ark.DriftPair

Read-only display object. No actions.

| Method | Description |
|--------|-------------|
| shortPathA() | Display version of pathA |
| shortPathB() | Display version of pathB |
| scoreLabel() | Formatted score |

### Ark.MessageDetail Methods

| Method | Description |
|--------|-------------|
| load(msg) | Load message content via mcp.readMessage(), populate fields |
| close() | Close the dialog |
| isOpen() | Whether dialog is visible |
| title() | Message summary for dialog title |
| projectLabel() | "from → to" formatted string |
| dateLabel() | Status date or empty |
| hasDate() | Whether date is present |
| hasResponse() | Whether response exists |
| requestHtml() | Rendered request body |
| responseHtml() | Rendered response body |
| onStatusChange() | Save status via mcp.setTags(), refresh |
| onReqResponseHandled() | Save response-handled via mcp.setTags(), refresh |
| onRespRequestHandled() | Save request-handled via mcp.setTags(), refresh |
| complete() | Set status=completed + both handled=completed, close, refresh |
| openInEditor() | Open file in editor via mcp:open() |

## ViewDefs

| File | Type | Purpose |
|------|------|---------|
| Ark.DEFAULT.html | Ark | Thin shell: `ui-view="currentView()"` |
| Ark.Searching.DEFAULT.html | Ark.Searching | Sidebar + `<ark-search>` element + JS SearchAPI bridge |
| Ark.Messaging.DEFAULT.html | Ark.Messaging | Kanban columns with message cards |
| Ark.Message.list-item.html | Ark.Message | Card in kanban column |
| Ark.MessageDetail.DETAIL.html | Ark.MessageDetail | Dialog with rendered markdown, controls, Complete button |
| Ark.Curation.DEFAULT.html | Ark.Curation | Two-column workshop: pinned chunks + tag explorer |
| Ark.PinnedChunk.list-item.html | Ark.PinnedChunk | Pinned-chunk card: widget stack + URL row + Stage/Revert + tag-suggestions collapsible |
| Ark.PendingWidget.list-item.html | Ark.PendingWidget | Pending-tag widget row: tag/value/remove/ext/X (+ base/locator/scope when ext) |
| Ark.TagSuggestion.list-item.html | Ark.TagSuggestion | One-line tag candidate with score |
| Ark.HotChunk.list-item.html | Ark.HotChunk | Focused-tag chunk row with pin button |
| Ark.RelatedTag.list-item.html | Ark.RelatedTag | Focused-tag related-tag row, click-to-focus |
| Ark.DriftPair.list-item.html | Ark.DriftPair | Focused-tag drift-pair row (read-only) |
| Ark.Source.list-item.html | Ark.Source | (unchanged) |
| Ark.Project.list-item.html | Ark.Project | (unchanged) |
| Ark.DataSource.list-item.html | Ark.DataSource | (unchanged) |
| Ark.Node.list-item.html | Ark.Node | (unchanged) |
| Ark.ProjectCandidate.list-item.html | Ark.ProjectCandidate | (unchanged) |

## MCP Shell Changes

### MCP app.lua

New method:
```lua
function mcp:displayArkCuration()
    if ark then ark:showCuration() end
    mcp:display("ark")
end
```

Existing `displayArk()` and `displayArkMessages()` unchanged.

### MCP.DEFAULT.html

Third button in bottom bar (after the envelope icon):
```html
<span class="mcp-build-mode-toggle" ui-event-click="displayArkCuration()" title="Ark Curation">
  <sl-icon name="tags-fill"></sl-icon>
</span>
```

## Events

### From UI to Claude

```json
{"app": "ark", "event": "chat", "text": "...", "handler": "/ui-fast", "background": false}
```

### Claude Event Handling

| Event | Action |
|-------|--------|
| `chat` | Respond to questions about ark configuration |

## Persistence

### state.json

`~/.ark/apps/curation/state.json` carries the curation workspace
across server restarts:

```json
{
  "lastViewedAt": 1746833520,
  "pinned": [
    {"chunkID": 4711, "fileID": 123, "path": "/abs/path/to/chunk.md", "pinnedAt": 1746833200},
    {"chunkID": 4523, "fileID": 88,  "path": "/abs/path/to/other.md", "pinnedAt": 1746833100}
  ]
}
```

The directory is created on first write. The file is rewritten in
full on every mutation (small N — pinned set is curated by hand,
not bulk-loaded). On load failure (file missing, JSON invalid),
fall back to an empty workspace and continue without raising.

## Sweep Behavior

`sweepNow()` sets `_sweepBusy = true`, calls
`mcp.sweepHotCorrelations()`, and stores the returned summary in
`_sweepResult`. The button is bound to `canSweep()` so it disables
while a call is in flight. The header reflects the busy state via
`sweepStatusText()`.

Frictionless `mcp:subscribe` is a publisher-topic primitive, not a
tmp:// document watcher; the live progress feed described in
`.scratch/CURATION-VIEW.md` (subscribing to the sweep doc's
`@sweep-status`/`@sweep-progress` tags) is deferred to a follow-up
slice. For the current cut the call is synchronous from the Lua
side — the workspace blocks until the sweep returns, with a
"sweeping..." indicator. Sweep duration depends on corpus shape
and the 1E bookmark state; budget more than a casual click before
it completes.
