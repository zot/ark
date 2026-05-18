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

### Tag Forge (workshop slice B+C, 2026-05)
```
+----------------------------------------+----------------------+
| Curation  Sweep: idle  [⚡] [⌫ Clear unchanged] [Accept (…)] |
+----------------------------------------+----------------------+
| Pinned chunks (3) [⤓ Sweep older]      | Tag explorer        |
| ┌────────────────────────────────────┐ | [_____________]     |
| │ [tag ][value][rem][ext][X]         │ | Defined tags        |
| │ [tag ][value][rem][ext][X]         │ |  • design-decision  |
| │ [+]                                │ |  • feedback         |
| │ [edit] NEW path/file.md         [X]│ |                     |
| │  > current tags                    │ | Focused: design-dec │
| │   [@topic: streaming ↻]            │ |  Top chunks (10)    |
| │   [@status: draft ↻]               │ |   • path/A  0.83 📌 |
| │  > tag scores                      │ |  Related tags       |
| │   • design-decision  0.83          │ |   • feedback  0.78  |
| │   • feedback         0.72          │ |  Drift              |
| │  > chunk text (iframe / CM6)       │ |   • A↔B  0.62       |
| └────────────────────────────────────┘ |                     |
+----------------------------------------+----------------------+
```

Primary surface: pinned cards with per-card pending widget
stacks above the URL, three collapsibles below (current tags,
tag scores, chunk text). The `[edit|revert]` button left of
the URL drives a four-state machine: clean / pending-only /
editing-clean / editing-dirty (see `.scratch/CHUNK-CARD.md`).
On `[edit]`, inline widgets fold into a CM6 editor as one
undoable transaction; ext widgets stay (they don't fold).
Accept (panel-level) commits every card's edited text via
`mcp.replaceRegion` and every card's filled ext widgets via
`mcp.setExtTag` / `mcp.removeExtTag`. Pending inline widgets
are NOT executed by Accept — they require `[edit]` first.

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
| _curation | Ark.Curation | The Tag Forge view |

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
| _sweepProgress | string | Live `@sweep-progress` value from `tmp://sweep/hot-correlations.md` subscription |
| _sweepResult | string | Final sweep summary on terminal `@sweep-status` |
| _definedTags | table[] | Lazy-loaded `{tag, description}` list for the picker |
| _definedTagsLoaded | boolean | Whether `mcp.definedTags()` has been called |
| _connRequestID | string | Current find-connections request ID (`""` when none in flight) |
| _connStatus | string | Live `@connections-status` (`""`, `pending`, `working`, `completed`, `errored`) |
| _connProgress | string | Live `@connections-progress` |
| _connElapsed | number | Live `@connections-elapsed` (seconds) |
| _connError | string | `@connections-error` on terminal errored state |
| _connThemes | Ark.ConnectionTheme[] | Parsed themes section of the connections doc body |
| _connSharedTags | Ark.ConnectionSharedTag[] | Parsed shared-tag candidates of the connections doc body |
| _acceptWarnVisible | boolean | Accept warning dialog visibility |

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
state: pending widget stack, edit-mode state, lazy-loaded
chunk metadata, lazy-loaded tag suggestions, ext-tag cache.

| Field | Type | Description |
|-------|------|-------------|
| viewItem | Variable | ViewList item handle (gives access to `baseItem` for chunkID/path/pinnedAt) |
| _widgets | Ark.PendingWidget[] | Pending widget stack. Empty-start invariant: always at least one empty widget. |
| _chunkInfo | table | Cached `mcp.chunkInfo` result: `{chunkID, fileID, path, range, byteStart, byteEnd, writable, commentSyntax}`. Nil until loaded. |
| _chunkInfoLoaded | boolean | True after the first chunkInfo fetch (success or failure) |
| _chunkInfoError | string | Last chunkInfo error ("" when none) |
| _chunkText | string | Cached `mcp.chunkText` result. Populated lazily on `[edit]` click or current-tags expand. |
| _chunkTextError | string | Last `mcp.chunkText` error ("" when none) |
| _editing | boolean | True when CM6 editor is mounted (state ∈ {editing-clean, editing-dirty}) |
| _chunkOriginalText | string | Snapshot of chunk text at `[edit]` time. Used for dirty check. |
| _isChunkEdited | boolean | JS-bridge-pushed flag: `editor.getDoc() != _chunkOriginalText`. Drives Accept(N) reactively. |
| _savedPendings | table[] | Snapshot of inline-mode filled widgets at `[edit]` time. Restored on explicit `[revert]`. |
| _savedEditorText | string | Editor draft preserved on `[revert]`. Consumed by next `[edit]` as initial doc (perfect restore). |
| _editorContent | string | JS-synced editor text. Read by Accept. |
| _extTags | Ark.ExtTagRow[] | Scraped from iframe `<ark-ext-tags>` children: `{name, value, externalfile, externaltarget}`. Persists across edit-mode transition. |
| _currentTagsView | Ark.CurrentTagRow[] | Derived: union of (inline tags from chunk text or editor draft) and `_extTags`, with pending ops applied for desired-state rendering. |
| _suggestions | Ark.TagSuggestion[] | Loaded tag candidates from `mcp.suggestTagNames` |
| _suggestionsLoaded | boolean | Whether the suggestion load completed |
| _suggestionsError | string | Last suggestion load error ("" when none) |
| _acceptError | string | Last per-card Accept error ("" when none) |
| _confirmDismiss | boolean | UI flag: confirm-dismiss alert visible (pending > 0 or editing) |

### Ark.CurrentTagRow

A row in the per-card current-tags collapsible. Renders desired-
state: union of inline and ext sources with pending ops applied.

| Field | Type | Description |
|-------|------|-------------|
| name | string | Tag name |
| value | string | Tag value (desired state — reflects pending change if any) |
| kind | string | "inline" or "ext" — drives row styling and edit semantics |
| externalfile | string | For ext rows: source mirror file path |
| externaltarget | string | For ext rows: TARGET spec the routing carries |
| status | string | "" \| "changed" \| "removed" — pending-op overlay indicator |
| _chunk | Ark.PinnedChunk | Back-reference for edit-mode mutations |

### Ark.ExtTagRow

A row scraped from the iframe `<ark-ext-tags>` element on
chunk-text-iframe load. Cached on the parent PinnedChunk so it
persists when the iframe is destroyed during edit mode.

| Field | Type | Description |
|-------|------|-------------|
| name | string | `<name>` child text |
| value | string | `<value>` child text |
| externalfile | string | `externalfile` attribute (mirror file path) |
| externaltarget | string | `externaltarget` attribute (TARGET spec) |

### Ark.ConnectionTheme

A theme row in the Find Connections proposals panel.

| Field | Type | Description |
|-------|------|-------------|
| text | string | Theme summary |
| evidence | number[] | Chunk IDs the theme spans |

### Ark.ConnectionSharedTag

A shared-tag candidate row in the proposals panel.

| Field | Type | Description |
|-------|------|-------------|
| tag | string | Tag name |
| value | string | Tag value |
| evidence | number[] | Chunk IDs the proposal applies to |

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
| curate(chunkID, fileID, path) | Pin a chunk to the Tag Forge without flipping the view (always-add never-flip) |

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
| new(instance) | Construct; init subscriptions to `tmp://sweep/hot-correlations.md` and `tmp://connections/<id>.md` when sweeps / connections requests are in flight. |
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
| **editedCount()** | Sum of cards where `_isChunkEdited == true`. |
| **pendingCount()** | Sum of filled widgets (inline + ext) across all cards. |
| **acceptDisabled()** | `editedCount() == 0` (button gray, no-op label). |
| **acceptLabel()** | Returns one of `"Accept (no changes)"`, `"Accept (N changed)"`, `"Accept — M pending"`, `"Accept (N changed, M pending)"` per the variants. |
| **acceptChanges()** | If `pendingCount() > 0`, set `_acceptWarnVisible = true` and return. Otherwise call `_doAccept()`. |
| **confirmAccept()** | Called by the "Save staged changes only" dialog button. Calls `_doAccept()`; clears `_acceptWarnVisible`. |
| **cancelAccept()** | Called by "Go back to editing"; clears `_acceptWarnVisible`. |
| **_doAccept()** | For each card: if `_isChunkEdited`, request editor.getDoc() via JS bridge → `mcp.replaceRegion(path, byteStart, byteEnd_orig, text)`. For each filled ext widget: `mcp.setExtTag` / `mcp.removeExtTag`. On success, card returns to clean state. On error, `_acceptError` set; visual cues activate. |
| **clearUnchanged()** | Dismiss every card where `_isChunkEdited == false`, no filled inline widgets, no filled ext widgets. |
| loadDefinedTags() | Lazy `mcp.definedTags()` populate of `_definedTags` |
| filteredDefinedTags() | Filtered view over `_definedTags` per `_focusInput` |
| filteredDefinedTagCount() | length of the filtered view |
| focusTagFromInput() | Call focusTag(_focusInput) if non-empty |
| focusTag(tag) | Populate `_focusedChunks` / `_focusedRelated` / `_focusedDrift` via bridge calls |
| clearFocus() | Reset focus state |
| isFocused() / notFocused() / focusError() / noFocusError() / focusedChunkCount() / focusedRelatedCount() / focusedDriftCount() | tag-explorer accessors (unchanged) |
| **sweepNow()** | Fire-and-forget: `mcp.sweepHotCorrelationsAsync()`; subscribe to `tmp://sweep/hot-correlations.md` for live `@sweep-status` / `@sweep-progress`. On terminal status, `mcp.tmp_get` reads the final body for the summary. |
| sweepStatusText() / sweepBusy() | sweep-state accessors |
| **onSweepEvent(events)** | Subscription callback for `tmp://sweep/hot-correlations.md`. Updates `_sweepBusy`, `_sweepProgress`, `_sweepResult`. |
| **findConnections()** | Call `mcp.findConnections(pinnedChunkIDs, opts)`. Store request ID in `_connRequestID`. Subscribe to `tmp://connections/<id>.md`. |
| **onConnectionsEvent(events)** | Subscription callback for `tmp://connections/<id>.md`. Updates `_connStatus`, `_connProgress`, `_connElapsed`. On terminal status, `mcp.tmp_get` reads the doc body, splits Themes and Shared Tag Candidates sections, populates `_connThemes` / `_connSharedTags`. |
| **fillProposal(sharedTagIdx)** | For each evidence chunk ID in the selected `Ark.ConnectionSharedTag`, look up the matching pinned card's presenter and inject a pre-filled `Ark.PendingWidget` (inline, add, with the proposal's tag and value). |
| **clearConnections()** | Reset `_connRequestID`, `_connStatus`, `_connThemes`, `_connSharedTags`. Used by retry / dismiss. |

### Ark.PinnedChunk

| Method | Description |
|--------|-------------|
| new(listItem) | Construct presenter wrapping a ViewList item. Initializes `_widgets` with one empty widget (empty-start invariant). |
| chunkID() / path() / pinnedAt() | Accessors over `viewItem.baseItem` |
| dismiss() | If `pendingCount() > 0` or `_editing`, set `_confirmDismiss = true`. Otherwise call `sys.curation.dismiss(chunkID())`. |
| confirmDismiss() | Final dismiss after confirmation |
| cancelDismiss() | Clear `_confirmDismiss` |
| isNew() / notNew() | NEW pill helpers (compare pinnedAt to Curation's `_newCutoff`) |
| contentURL() / shortPath() | Path display helpers |
| loadChunkInfo() | Lazy `mcp.chunkInfo(chunkID())` — populates `_chunkInfo` / `_chunkInfoError` |
| chunkInfo() | Lazy accessor; returns `_chunkInfo` |
| commentSyntax() | `_chunkInfo.commentSyntax` (`""` for markdown) |
| writable() | `_chunkInfo.writable` (true unless read-only chunker) |
| readOnly() | inverse |
| **widgets()** | Return `_widgets`; lazy ensures empty-start invariant. Returns empty in edit mode (pending stack hidden). |
| **addWidget()** | Append a new empty `Ark.PendingWidget` to `_widgets`. No-op during edit mode. |
| **removeWidget(widget)** | Kill `X` — remove `widget` from `_widgets`; ensure empty-start invariant |
| **filledWidgetCount()** | Count of widgets where `:isFilled()` is true (across inline + ext) |
| **filledInlineWidgets()** | Subset of `_widgets` with `isFilled() && !extMode`. Used by the fold algorithm. |
| **filledExtWidgets()** | Subset of `_widgets` with `isFilled() && extMode`. Dispatched at Accept. |
| **pendingCount()** | `filledWidgetCount()` (no longer separate staged-ops buffer). |
| **hasChanges()** | `_isChunkEdited || filledWidgetCount() > 0` (the chunk contributes to Accept). |
| **noChanges()** | inverse |
| **editButtonIcon()** | "pencil-square" when `!_editing`, "arrow-counterclockwise" when `_editing`. |
| **editButtonAccent()** | true when `hasChanges()`; drives the icon's color (accent vs gray). |
| **edit()** | `[edit]` click handler. Snapshot `_chunkOriginalText`, `_savedPendings`; fold filled inline widgets into a `foldedText`; mount CM6 with initial doc = `_savedEditorText` (if present, then clear it) else `_chunkOriginalText`, then dispatch fold as one CM6 transaction. Clear folded widgets. Set `_editing = true`. |
| **revert()** | `[revert]` click handler. Snapshot `_savedEditorText = editor.getDoc()` via JS bridge; destroy editor; restore `_widgets` from `_savedPendings`; clear `_chunkOriginalText`, `_savedPendings`, `_isChunkEdited`. Set `_editing = false`. |
| **applyPendingsToText(text, widgets)** | Pure Lua helper. For each `inline-add` widget: prepend `@tag: value\n` to the leading tag block. For `inline-change`: replace the matching `@tag:` line. For `inline-remove`: delete the matching `@tag:` line. Returns the folded text. |
| **onEditorDocChanged(dirty)** | JS bridge callback. Sets `_isChunkEdited = dirty`. Triggers `_currentTagsView` re-derivation. If `dirty == false` AND `_savedPendings` was non-empty (i.e. fold-undo case), auto-exit edit mode (destroy editor, do NOT restore pendings, state → clean). |
| **onExtTagsScraped(json)** | JS bridge callback fired after iframe load. Decodes JSON into `_extTags`. Triggers `_currentTagsView` re-derivation. |
| **currentTagsView()** | Compute the desired-state union: inline tags from `mcp.parseTagBlock(editor draft ? editor.getDoc() : _chunkText)` plus `_extTags`, with pending ops applied (additions overlayed, removals filtered or struck-through, changes shown with new value). Returns `Ark.CurrentTagRow[]`. |
| **queueRemoveFromCurrent(row)** | Convenience for the `rem` checkbox in read-only current-tags. Adds a corresponding `inline-remove` or `ext-remove` widget to the pending stack. |
| **applyCurrentTagEdit(row, newValue)** | In edit mode: for inline rows, dispatch a CM6 transaction rewriting the matching `@tag:` line. For ext rows, queue an `ext-set` pending op carrying the new value. |
| acceptError() / noAcceptError() / hasAcceptError() | per-card error display helpers |
| clearAcceptError() | Manual dismiss for the error alert |
| loadSuggestions() | Lazy `mcp.suggestTagNames(chunkID, K)`; populates `_suggestions` / `_suggestionsError` |
| suggestions() / suggestionsError() / noSuggestionsError() / hasSuggestions() | tag-scores accessors (renamed view; data path unchanged) |
| iframeURL() | `/content<path>?range=…&toggle=false` for the read-only chunk-text iframe |
| editorMountCode() | Returns inline JS string for `ui-code` binding: imports `/ark-markdown-editor.js`, calls `createInkArkEditor` on mount div, dispatches fold transaction, registers docChanged callback wiring to `onEditorDocChanged`. |

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
| Ark.Curation.DEFAULT.html | Ark.Curation | Two-column workshop: pinned chunks + tag explorer. Header carries `[⚡ sweep]`, `[⌫ Clear unchanged]`, `[Accept (…)]`, the Accept warning dialog, and the Find Connections proposals panel. |
| Ark.PinnedChunk.list-item.html | Ark.PinnedChunk | Pinned-chunk card: pending widget stack + URL row with `[edit|revert]` button (color-changing icon: gray when clean, accent when has-changes) + three collapsibles (current tags, tag scores, chunk text). Chunk-text body holds the iframe-and-click-shield in read-only mode and the CM6 mount div in edit mode. Inline JS via `ui-code` drives the bridge (iframe scrape, editor mount/destroy/read). |
| Ark.PendingWidget.list-item.html | Ark.PendingWidget | Pending-tag widget row: tag/value/remove/ext/X (+ base/locator/scope when ext) |
| Ark.CurrentTagRow.list-item.html | Ark.CurrentTagRow | Current-tags row: same widget shape as `Ark.PendingWidget`, readonly in iframe mode / editable in edit mode. Ext rows carry an externalfile badge. |
| Ark.TagSuggestion.list-item.html | Ark.TagSuggestion | One-line tag candidate with score (rendered under the "tag scores" collapsible) |
| Ark.ConnectionTheme.list-item.html | Ark.ConnectionTheme | Theme row in the Find Connections proposals panel — text + chunk-evidence chips. |
| Ark.ConnectionSharedTag.list-item.html | Ark.ConnectionSharedTag | Shared-tag candidate row in the Find Connections panel — `tag: value`, evidence chunk IDs, `[Fill]` button. |
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

## JS Bridge contract (Lua ↔ browser, per-card)

The chunk-card relies on a small inline-JS surface tied to the
per-card mount div via the `ui-code` binding. Each role pushes
back to Lua via `window.uiApp.updateValue(elementID, value)`.

### iframe scrape (read-only mode)

On iframe `load` event (after the iframe's custom elements have
upgraded), the bridge:

1. Walks `iframe.contentDocument.querySelectorAll('ark-ext-tags
   ark-tag')`, builds `[{name, value, externalfile,
   externaltarget}, ...]`.
2. Calls `window.uiApp.updateValue('cur-ext-tags-<chunkID>',
   JSON.stringify(list))`.
3. Lua reads the JSON into `_extTags`; `onExtTagsScraped()` fires
   to rebuild `_currentTagsView`.

Cross-document mutation requires same-origin — confirmed during
`/ui-thorough`.

### Desired-state overlay on iframe

After the scrape, the bridge rewrites the iframe's
`<ark-ext-tags>` `<ark-tag>` children to reflect pending ops
(removing entries marked for removal, adding new entries from
pending ext-set widgets, replacing values for changes). The
custom element's existing dropdown reads from its current
children, so the overlay propagates automatically.

### Click-shield (read-only mode)

A transparent absolutely-positioned div sits over the iframe
with `pointer-events: auto` and `cursor: pointer`. Click handler
fires `[edit]` via `window.uiApp.updateValue` against a hidden
trigger element. The `<ark-ext-tags-indicator>` inside the
iframe stops click-propagation on its own listener so the
dropdown click reaches it without firing the shield.

### Editor mount (edit mode)

Runs once per edit-mode entry. The bridge:

1. Reads `_chunkOriginalText` (or `_savedEditorText`) and the
   target `path` from hidden spans on the card.
2. Imports `/ark-markdown-editor.js`, calls
   `createInkArkEditor({parent, doc, path, api})` with a custom
   `HostAPI` whose `save(_, content)` syncs to Lua via
   `updateValue('cur-content-<chunkID>', content)` instead of
   writing to disk.
3. Reads the pre-computed `_foldedText` from a hidden span; if
   different from `_chunkOriginalText`, dispatches one CM6
   transaction inserting the diff (single undoable history
   entry).
4. Registers a `EditorView.updateListener.of(...)` callback that
   on every `transaction` pushes
   `_isChunkEdited = (state.doc.toString() != originalText)`
   back via `updateValue('cur-dirty-<chunkID>', dirty)`.
5. Stashes the editor on `window.__curEditor_<chunkID>` so the
   Accept-time read can find it.

### Editor destroy (`[revert]`)

`window.__curEditor_<chunkID>` is `.destroy()`'d, deleted, and
the mount div is cleared. Before destroy, the bridge calls
`updateValue('cur-saved-editor-<chunkID>', editor.getDoc())` so
Lua stores `_savedEditorText` for the next `[edit]` (perfect
restore).

### Editor read (Accept time)

`Curation:_doAccept()` triggers a JS pass for each card with
`_isChunkEdited`: reads `window.__curEditor_<chunkID>.getDoc()`
and `updateValue('cur-content-<chunkID>', text)`. Lua then
dispatches `mcp.replaceRegion(path, byteStart, byteEnd_orig,
text)` synchronously.

### Current-tags edits during edit mode

Inline rows: edit-on-blur dispatches via JS to find the
`@tag:` line in the editor's doc (a regex scan over the doc's
leading tag block) and dispatches a CM6 transaction replacing
that line.

Ext rows: edit-on-blur calls back through `updateValue` to add
a new `ext-set` pending widget (Lua-side mutation, no editor
involvement).

---

## Sweep Behavior (async retrofit)

`sweepNow()` is fire-and-forget:

1. Call `mcp.sweepHotCorrelationsAsync()` (non-blocking; returns
   immediately).
2. `mcp.subscribe(sessionID, {tag = "sweep-status", filterFiles =
   {"tmp://sweep/hot-correlations.md"}})` and a parallel
   subscription for `"sweep-progress"`. Subscription events
   route to `onSweepEvent(events)`.
3. UI shows live `_sweepBusy = true` with `_sweepProgress` text.
4. On terminal `@sweep-status: completed | errored`, the callback
   reads the doc body via `mcp.tmp_get("tmp://sweep/hot-
   correlations.md")` and stores the formatted summary in
   `_sweepResult`. Sets `_sweepBusy = false`.

The retrofit lets the workspace remain responsive while a sweep
runs and shows mid-flight progress. Subscription lifetime is the
session — no explicit unsubscribe.

## Find Connections orchestration

The proposals panel surfaces themes and shared-tag candidates
from the ark-connections sidecar.

1. **Request**: `findConnections()` calls
   `mcp.findConnections(pinnedChunkIDs, opts)`. On success
   returns request ID → stored in `_connRequestID`. On
   `"agent unavailable"` → surface a friendly message in the
   panel.
2. **Subscribe**: `mcp.subscribe(sessionID, {tag =
   "connections-status", filterFiles =
   {"tmp://connections/<id>.md"}})` plus parallel subs for
   `connections-progress`, `connections-elapsed`,
   `connections-error`. Events route to
   `onConnectionsEvent(events)`.
3. **Live updates**: callback updates `_connStatus`,
   `_connProgress`, `_connElapsed` reactively. UI renders a
   progress strip in the panel.
4. **Terminal**: on `@connections-status: completed`, callback
   calls `mcp.tmp_get("tmp://connections/<id>.md")`, splits the
   body at `## Themes` and `## Shared Tag Candidates`, parses
   each section's items via line-by-line scan (the body uses
   `@theme-evidence:`, `@shared-tag:`, `@shared-tag-value:`,
   `@shared-tag-evidence:` per `connections.go`). Populates
   `_connThemes` and `_connSharedTags`.
5. **Fill**: each `Ark.ConnectionSharedTag` row in the panel has
   a `[Fill]` button bound to `fillProposal(idx)`. The handler
   walks `evidence` chunk IDs, finds each matching pinned
   presenter, and adds a pre-filled `Ark.PendingWidget` to its
   stack: `tagName = proposal.tag`, `tagValue = proposal.value`,
   `extMode = false`. Cards with no matching pin are skipped
   silently.
6. **Errored / retry**: `@connections-status: errored` sets
   `_connError`; UI offers a "Retry" button that calls
   `findConnections()` again.
