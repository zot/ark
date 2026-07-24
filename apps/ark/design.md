# Ark - Design

## Intent

The ark app has four views: Searching (index manager + full-text
search), Messaging (cross-project message dashboard), Curation
(vocabulary-maintenance workshop sitting on top of the Phase 1
chunk → tag, tag → chunk, and tag → tag bridges), and Luhmann
(terminal on the ark-hosted Luhmann session — the in-app
counterpart of `ark luhmann attach`). A thin root object routes
between them via `ui-view`. The MCP shell has four bottom-bar
buttons, one per view.

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

### Tag Forge (machinery reconcile, 2026-07)
```
+----------------------------------------+----------------------+
| Curation  Sweep: idle  [⚡] [⌫ Clear unchanged] [Accept (…)] |
+----------------------------------------+----------------------+
| Pinned chunks (3) [⤓ Sweep older]      | Tag explorer        |
| ┌────────────────────────────────────┐ | [_____________]     |
| │ [🔥][tag ][value][int|ext][add|rep][rem][X] │ Defined tags |
| │ [🔥][tag ][value][int|ext][add|rep][rem][X] │  • design-dec|
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

Primary surface: pinned cards with per-card proposal widget
stacks above the URL, three collapsibles below (current tags,
tag scores, chunk text). Each widget is a **loaded gun**: the
fire button sits ahead of its editing fields, and clicking it
commits that one tag operation immediately through the
approval shims (candidate+accept semantics — see §"Approval
shims"). Tag authoring is fully detached from the editor: the
`[edit|revert]` button left of the URL drives a two-state
machine (viewing / editing) for genuine *content* edits only,
and Accept (panel-level) commits edited text via
`mcp.replaceRegion`. No fold, no staging, no warning dialog.

Secondary surface (right column): the tag explorer — Type a
tag (or click one in the defined-tags picker), see top chunks,
related tags, drift pairs. Click related → switch focus; click
chunk → pin. Retained from the pre-reframe design.

### Luhmann View (2026-07, PENDING #39)

Follows the worked example `install/html/luhmann-terminal.html`
(`design/ui-luhmann-terminal-page.md`).

```
+------------------------------------------------------+
| ● Luhmann: asleep — no session          [Launch…]    |  <- status strip
+------------------------------------------------------+
|                                                      |
|  <luhmann-terminal>                                  |  <- flex: 1, min-height: 0
|    (xterm renders the hosted session here)           |
|                                                      |
+------------------------------------------------------+
```

The strip's lamp + text and the Launch button's visibility are
Lua-driven: the element's bubbling `luhmann-terminal-status`
event (listened on `document` — the strip is the terminal's
sibling) is pushed through the JS→Lua bridge. `[Launch…]` shows
only in `asleep` and opens an `sl-dialog` confirming a paid
session before calling `sys.luhmannLaunch()` (init.lua stub
until PENDING #62 lands the Go verb). The terminal mounts via
`ui-html="terminalHtml()"`, which carries a mount nonce —
`wake()` bumps it, re-mounting the element and forcing a fresh
probe (the element stops probing once it announces `asleep`).

## Data Model

### Ark (root shell)

| Field | Type | Description |
|-------|------|-------------|
| _viewMode | string | "searching", "messaging", "curation", or "luhmann" |
| _searching | Ark.Searching | The index/search view |
| _messaging | Ark.Messaging | The messaging view |
| _curation | Ark.Curation | The Tag Forge view |
| _luhmann | Ark.Luhmann | The Luhmann terminal view |

### Ark.Luhmann

| Field | Type | Description |
|-------|------|-------------|
| termStatus | string | Element state: "connecting", "connected", "waiting", or "asleep" |
| termSession | string | Hosted session id (from the status event; "" when none) |
| termAttempt | number | Reconnect attempt counter (from the status event) |
| statusBridge | string | JS→Lua bridge: JSON `{state, session, attempt}` from the status event; cleared by `processStatus()` |
| confirmOpen | bool | Launch confirmation dialog visibility |
| _mountNonce | number | Bumped by `wake()`; changes `terminalHtml()` so the element re-mounts |

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

### Ark.ProposalWidget

A loaded gun on a `Ark.PinnedChunk`'s widget stack (renamed from
`Ark.PendingWidget` — nothing is pending anymore). Each widget
authors one tag operation (add / replace / remove, internal or
external disposition) against the chunk and fires it in one
gesture through the approval shims.

| Field | Type | Description |
|-------|------|-------------|
| _chunk | Ark.PinnedChunk | Parent card reference (for fire + refresh callbacks) |
| tagName | string | Tag name input value |
| tagValue | string | Tag value input value (ignored when removeMode true) |
| removeMode | boolean | true = remove this tag from chunk; false = add/replace |
| disposition | string | "internal" (tag written into the file body) or "external" (routed via `@ext` mirror). Locked to external when the chunk can't host an internal tag. |
| replaceMode | boolean | The machinery's `replace` token: true = collapse the tag's values to this one; false = add. Pre-set true when the tag already exists on the chunk. |
| _fireError | string | Last fire error ("" when none) — widget keeps its state for retry |
| extBase | string | "uuid" or "path" — only meaningful when disposition external |
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
| _widgets | Ark.ProposalWidget[] | Proposal widget stack. Empty-start invariant: always at least one empty widget. |
| _chunkInfo | table | Cached `mcp.chunkInfo` result: `{chunkID, fileID, path, range, byteStart, byteEnd, writable, commentSyntax}`. Nil until loaded. |
| _chunkInfoLoaded | boolean | True after the first chunkInfo fetch (success or failure) |
| _chunkInfoError | string | Last chunkInfo error ("" when none) |
| _chunkText | string | Cached `mcp.chunkText` result. Populated lazily on `[edit]` click or current-tags expand. |
| _chunkTextError | string | Last `mcp.chunkText` error ("" when none) |
| _editing | boolean | True when CM6 editor is mounted |
| _chunkOriginalText | string | Snapshot of chunk text at `[edit]` time. Used for dirty check. |
| _isChunkEdited | boolean | JS-bridge-pushed flag: `editor.getDoc() != _chunkOriginalText`. Drives Accept(N) reactively. |
| _savedEditorText | string | Editor draft preserved on `[revert]`. Consumed by next `[edit]` as initial doc (perfect restore). |
| _editorContent | string | JS-synced editor text. Read by Accept. |
| _extTags | Ark.ExtTagRow[] | Scraped from iframe `<ark-ext-tags>` children: `{name, value, externalfile, externaltarget}`. Persists across edit-mode transition. |
| _currentTagsView | Ark.CurrentTagRow[] | Derived: union of inline tags from `_chunkText` and `_extTags` — the chunk's tags as they stand. Rebuilt after each fire. |
| _suggestions | Ark.TagSuggestion[] | Loaded tag candidates from `mcp.suggestTagNames` |
| _suggestionsLoaded | boolean | Whether the suggestion load completed |
| _suggestionsError | string | Last suggestion load error ("" when none) |
| _acceptError | string | Last per-card Accept error ("" when none) |
| _confirmDismiss | boolean | UI flag: confirm-dismiss alert visible (pending > 0 or editing) |

### Ark.CurrentTagRow

A read-only row in the per-card current-tags collapsible: the
union of inline and ext sources as they stand on disk. Its two
affordances (`rem` checkbox, row click) load pre-filled widgets
into the proposal stack — the row itself never fires anything.

| Field | Type | Description |
|-------|------|-------------|
| name | string | Tag name |
| value | string | Tag value |
| kind | string | "inline" or "ext" — drives row styling and the loaded widget's disposition |
| externalfile | string | For ext rows: source mirror file path |
| externaltarget | string | For ext rows: TARGET spec the routing carries |
| _chunk | Ark.PinnedChunk | Back-reference for widget loading |

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

## Approval shims

@prototype: go-ext-hooks

The fire path routes through two module-level Lua functions in
`curation.lua`, named and shaped like the future `mcp` bindings
(code as sketchpad — the shim *interface* is the concrete
requirement PENDING #65 reads off when binding the real Go hooks;
the shim *implementation* is disposable). Both are marked
`-- @prototype: go-ext-hooks`.

```lua
-- future mcp.extAccept(target, tag, value, opts) → result, err
extAccept(target, tag, value, opts)
-- future mcp.extRemove(target, tag, opts) → result, err
extRemove(target, tag, opts)
```

- `target` — external: the composed TARGET spec string
  (`BASE` / `BASE:NARROWER`); internal: the chunk's
  `{path, byteStart, byteEnd, text}` (shim-only shape — the real
  hook resolves a spec server-side). Internal targets are
  re-fetched fresh at fire time; a fire racing the async reindex
  of a just-fired write can still see a stale range (shim
  limitation, solved server-side by the real hook).
- `opts` — `{disposition, replace}` for accept;
  `{disposition}` for remove.
- `result` — `{disposition}`: the disposition **actually
  applied** after degrade, so the caller's feedback stays honest.
- `err` — non-nil message on failure; nothing was written.

Shim implementation over the old primitives:

| cell | rides |
|------|-------|
| external add / replace | `mcp.setExtTag(target, tag, value)` (set semantics approximate both cells) |
| external remove | `mcp.removeExtTag(target, tag)` |
| internal add | `prependTag` transform → `mcp.replaceRegion` |
| internal replace | `replaceTagLine` transform → `mcp.replaceRegion` (degrades to add when no matching line) |
| internal remove | `removeTagLine` transform → `mcp.replaceRegion` (the machinery's internal-remove gap — shim-only path, decision rides with #65) |

The `findTagLine` / `replaceTagLine` / `removeTagLine` /
`prependTag` helpers are the old fold's text transforms,
repurposed: they now feed `mcp.replaceRegion` directly instead of
a CM6 transaction. Degrade approximation: internal on a chunk
with `writable == false` locks to external before dispatch. The
shims use the markdown stencil only — per-chunker stencils
(comment-wrapping, indent matching) and the comment-syntax
degrade are the real hooks' job (#65).

**Honesty rule.** The real machinery writes a ledger trail
(candidate line, `@count`, positive judgment); the shims do not.
Fire feedback says what happened ("tag applied" / "routed
externally — chunk can't host inline tags"), never "candidate
recorded", until #65 lands the real hooks.

## Methods

### Ark (root)

| Method | Description |
|--------|-------------|
| new() | Create instance with Searching, Messaging, Curation, and Luhmann children |
| currentView() | Return _searching, _messaging, _curation, or _luhmann based on _viewMode |
| showSearching() | Set _viewMode = "searching" |
| showMessaging() | Set _viewMode = "messaging", refresh messaging |
| showCuration() | Set _viewMode = "curation", call _curation:onViewOpen() |
| showLuhmann() | Set _viewMode = "luhmann", call _luhmann:wake() (fresh mount + probe on every entry) |
| curate(chunkID, fileID, path) | Pin a chunk to the Tag Forge without flipping the view (always-add never-flip) |

### Ark.Luhmann

| Method | Description |
|--------|-------------|
| new() | Create instance |
| terminalHtml() | `<luhmann-terminal></luhmann-terminal>` + mount-nonce comment; changing the nonce re-mounts the element |
| wake() | Reset status to "connecting", bump _mountNonce — re-mount forces a fresh probe. Public: the PENDING #62 Go push calls this on CLI launch |
| processStatus() | Bridge trigger (`?priority=high`): parse statusBridge JSON into termStatus/termSession/termAttempt, clear the bridge |
| statusText() | Strip text per state (mirrors the worked example's wording) |
| lampColor() | CSS color per state (`--term-*` vars, same mapping as the worked example) |
| notAsleep() | Launch-button hiding (`ui-class-hidden`) |
| openLaunchConfirm() | Set confirmOpen = true |
| cancelLaunch() | Set confirmOpen = false (also wired to the dialog's sl-after-hide) |
| confirmLaunch() | Close dialog, call `sys.luhmannLaunch()` (stub until PENDING #62) |

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
| new(instance) | Construct; init subscription to `tmp://sweep/hot-correlations.md` when a sweep is in flight. |
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
| **pendingCount()** | Sum of filled widgets across all cards (used by Clear-unchanged and dismiss guards, not by Accept). |
| **acceptDisabled()** | `editedCount() == 0` (button gray, no-op label). |
| **acceptLabel()** | `"Accept (no changes)"` or `"Accept (N changed)"`. |
| **acceptChanges()** | Call `_doAccept()` directly — no warning dialog (tag ops fire per-widget; Accept only covers content edits). |
| **_doAccept()** | For each card with `_isChunkEdited`: request editor.getDoc() via JS bridge → `mcp.replaceRegion(path, byteStart, byteEnd_orig, text)`. On success, card returns to viewing state. On error, `_acceptError` set; visual cues activate. |
| **clearUnchanged()** | Dismiss every card where `_isChunkEdited == false` and no filled proposal widgets. |
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
| **widgets()** | Return `_widgets`; lazy ensures empty-start invariant. Visible in all states — tag authoring is independent of edit mode. |
| **addWidget()** | Append a new empty `Ark.ProposalWidget` to `_widgets`. |
| **removeWidget(widget)** | Kill `X` — remove `widget` from `_widgets`; ensure empty-start invariant |
| **filledWidgetCount()** | Count of widgets where `:isFilled()` is true |
| **pendingCount()** | `filledWidgetCount()` (guards dismiss + Clear unchanged; Accept ignores it). |
| **hasChanges()** | `_isChunkEdited || filledWidgetCount() > 0` (guards dismiss confirmation). |
| **noChanges()** | inverse |
| **fireWidget(widget)** | Fire path. Compose the shim call from the widget (target: `widget:targetSpec()` for external, `{path, byteStart, byteEnd, text}` from `_chunkInfo`/`_chunkText` for internal); dispatch `extAccept` / `extRemove`; on success notify honestly (resolved disposition), reset the widget, and `refreshTags()`; on error set `widget._fireError`, keep state for retry. |
| **refreshTags()** | Post-fire refresh: clear `_chunkText` cache and re-fetch, bump the iframe nonce (reload re-scrapes `_extTags`), rebuild `_currentTagsView`. |
| **editButtonIcon()** | "pencil-square" when `!_editing`, "arrow-counterclockwise" when `_editing`. |
| **editButtonAccent()** | true when `_isChunkEdited`; drives the icon's color (accent vs gray). |
| **edit()** | `[edit]` click handler. Snapshot `_chunkOriginalText`; mount CM6 with initial doc = `_savedEditorText` (if present, then clear it) else `_chunkOriginalText`. Set `_editing = true`. |
| **revert()** | `[revert]` click handler. Snapshot `_savedEditorText = editor.getDoc()` via JS bridge; destroy editor; clear `_chunkOriginalText`, `_isChunkEdited`. Set `_editing = false`. |
| **onEditorDocChanged(dirty)** | JS bridge callback. Sets `_isChunkEdited = dirty`. |
| **onExtTagsScraped(json)** | JS bridge callback fired after iframe load. Decodes JSON into `_extTags`. Triggers `_currentTagsView` re-derivation. |
| **currentTagsView()** | Union of inline tags from `mcp.parseTagBlock(_chunkText)` plus `_extTags` — current state, no overlay. Returns `Ark.CurrentTagRow[]`. |
| **loadRemoveWidget(row)** | `rem` checkbox on a current-tags row: load a pre-filled *remove* widget (tag name, disposition per row kind) into the stack for the user to fire. |
| **loadReplaceWidget(row)** | Click on a current-tags row: load a pre-filled *replace* widget (tag, current value, disposition per row kind) into the stack for the user to revise and fire. |
| acceptError() / noAcceptError() / hasAcceptError() | per-card error display helpers |
| clearAcceptError() | Manual dismiss for the error alert |
| loadSuggestions() | Lazy `mcp.suggestTagNames(chunkID, K)`; populates `_suggestions` / `_suggestionsError` |
| suggestions() / suggestionsError() / noSuggestionsError() / hasSuggestions() | tag-scores accessors (renamed view; data path unchanged) |
| iframeURL() | `/content<path>?range=…&toggle=false` for the read-only chunk-text iframe |
| editorMountCode() | Returns inline JS string for `ui-code` binding: imports `/ark-markdown-editor.js`, calls `createInkArkEditor` on mount div, registers docChanged callback wiring to `onEditorDocChanged`. |

### Ark.ProposalWidget

| Method | Description |
|--------|-------------|
| new(chunk) | Construct an empty widget bound to its parent PinnedChunk. Disposition defaults internal for writable chunks, external (locked) otherwise. |
| isEmpty() | true when `tagName == ""` and `tagValue == ""` and no non-default modes |
| isFilled() | true when `tagName != ""` (value can be empty for remove ops or boolean tags) |
| **fire()** | The loaded gun's trigger: call parent `chunk:fireWidget(self)`. |
| **fireDisabled()** | true when `tagName == ""`. |
| **fireError() / noFireError() / clearFireError()** | Per-widget fire-error display for retry. |
| toggleRemove() | Flip `removeMode` |
| **toggleDisposition()** | Flip internal ↔ external (no-op when locked to external). On first switch to external, call `mcp.suggestExtLocator(chunkID)` to populate `extBase` / `extBaseValue` / `extLocatorKind` / `extLocatorText` / `_extScopeChunks` / `_extScopeFiles` / `_withinFileDupCount`. Reads `locatorText` for the actual locator value (`locator` field has a known Go bug). |
| **toggleReplace()** | Flip `replaceMode` (add ↔ replace). |
| **onTagNameChange()** | When the entered tag already exists on the chunk (per `currentTagsView`), pre-set `replaceMode = true` — the visible default replacing the old infer-by-existence rule. |
| kill() | Call parent `chunk:removeWidget(self)` |
| setBase(base) | Update `extBase`; re-run suggestExtLocator to get new defaults if needed |
| setLocatorKind(kind) | Update `extLocatorKind`; clear `extLocatorText` for `bare`, otherwise leave editable |
| dispositionLocked() | true when parent chunk can't host an internal tag (read-only, or comment-less code) — control locked to external |
| scopeReadout() | `"will route to N chunks across M files"` string for the readout line |
| baseChoices() | `["uuid", "path"]` or `["path"]` (UUID hidden when chunk has no @id) |
| locatorChoices() | `["string", "regex", "absolute", "bare"]` |
| dupFlag() | `"UUID: %s... (×%d in this file)"` when `_withinFileDupCount > 1`, empty otherwise |
| autoAddOnTab() | Called from viewdef tab-out event. If `isFilled()` and the parent's last widget is this one, call `chunk:addWidget()`. |
| targetSpec() | Compose `BASE` or `BASE:NARROWER` from the ext fields (used by fire to build the external target) |

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
| Ark.Luhmann.DEFAULT.html | Ark.Luhmann | Status strip (lamp + text + Launch button) over `<luhmann-terminal>` in a flex column; launch-confirm `sl-dialog`; script loads the element module and forwards `luhmann-terminal-status` events through the JS→Lua bridge |
| Ark.Searching.DEFAULT.html | Ark.Searching | Sidebar + `<ark-search>` element + JS SearchAPI bridge |
| Ark.Messaging.DEFAULT.html | Ark.Messaging | Kanban columns with message cards |
| Ark.Message.list-item.html | Ark.Message | Card in kanban column |
| Ark.MessageDetail.DETAIL.html | Ark.MessageDetail | Dialog with rendered markdown, controls, Complete button |
| Ark.Curation.DEFAULT.html | Ark.Curation | Two-column workshop: pinned chunks + tag explorer. Header carries `[⚡ sweep]`, `[⌫ Clear unchanged]`, `[Accept (…)]`. |
| Ark.PinnedChunk.list-item.html | Ark.PinnedChunk | Pinned-chunk card: proposal widget stack + URL row with `[edit|revert]` button (color-changing icon: gray when clean, accent when the editor is dirty) + three collapsibles (current tags, tag scores, chunk text). Chunk-text body holds the iframe-and-click-shield in read-only mode and the CM6 mount div in edit mode. Inline JS via `ui-code` drives the bridge (iframe scrape, editor mount/destroy/read). |
| Ark.ProposalWidget.list-item.html | Ark.ProposalWidget | Proposal widget row, fire first: 🔥/tag/value/int-ext/add-replace/remove/X (+ base/locator/scope when external) + fire-error alert |
| Ark.CurrentTagRow.list-item.html | Ark.CurrentTagRow | Current-tags row: same widget shape as `Ark.ProposalWidget`, always readonly. `rem` loads a remove widget; row click loads a replace widget. Ext rows carry an externalfile badge. |
| Ark.TagSuggestion.list-item.html | Ark.TagSuggestion | One-line tag candidate with score (rendered under the "tag scores" collapsible) |
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

New methods:
```lua
function mcp:displayArkCuration()
    if ark then ark:showCuration() end
    mcp:display("ark")
end

function mcp:displayArkLuhmann()
    if ark then ark:showLuhmann() end
    mcp:display("ark")
end
```

Existing `displayArk()` and `displayArkMessages()` unchanged.

### MCP.DEFAULT.html

Third and fourth buttons in bottom bar (after the envelope icon):
```html
<span class="mcp-build-mode-toggle" ui-event-click="displayArkCuration()" title="Tag Forge">
  <!-- inline tag-forge SVG -->
</span>
<span class="mcp-build-mode-toggle" ui-event-click="displayArkLuhmann()" title="Luhmann">
  <sl-icon name="terminal-fill"></sl-icon>
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
3. Registers a `EditorView.updateListener.of(...)` callback that
   on every `transaction` pushes
   `_isChunkEdited = (state.doc.toString() != originalText)`
   back via `updateValue('cur-dirty-<chunkID>', dirty)`.
4. Stashes the editor on `window.__curEditor_<chunkID>` so the
   Accept-time read can find it.

No fold pass: the editor opens on the chunk's original text (or
the preserved draft) — tag operations never enter the editor.

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

## Proposal supply chain (Find Connections removed)

The ark-connections sidecar orchestration (`mcp.findConnections`
→ `tmp://connections/<id>.md` → external `ark connections --wait`
consumer) is removed from the app — the sidecar is retired, its
role replaced by the luhmann secretary pool. The forge's future
proposal source is the candidate ledger (agents author
`@ext-candidate`s cross-session; the forge reviews them) — the
review-queue surface is PENDING #9.
