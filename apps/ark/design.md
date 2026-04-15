# Ark - Design

## Intent

The ark app has two views mirroring the two ark agents: Searching
(index manager + full-text search) and Messaging (cross-project
message dashboard). A thin root object routes between them via
`ui-view`. The MCP shell has two bottom-bar buttons for each view.

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

### Messaging View (new)
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

## Data Model

### Ark (root shell)

| Field | Type | Description |
|-------|------|-------------|
| _viewMode | string | "searching" or "messaging" |
| _searching | Ark.Searching | The index/search view |
| _messaging | Ark.Messaging | The messaging view |

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

## Methods

### Ark (root)

| Method | Description |
|--------|-------------|
| new() | Create instance with Searching and Messaging children |
| currentView() | Return _searching or _messaging based on _viewMode |
| showSearching() | Set _viewMode = "searching" |
| showMessaging() | Set _viewMode = "messaging" |

### Ark.Searching

Source management, file tree, project editor unchanged. Search UI
is now the `<ark-search>` web component — Lua no longer does search.

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
| Ark.Source.list-item.html | Ark.Source | (unchanged) |
| Ark.Project.list-item.html | Ark.Project | (unchanged) |
| Ark.DataSource.list-item.html | Ark.DataSource | (unchanged) |
| Ark.Node.list-item.html | Ark.Node | (unchanged) |
| Ark.ProjectCandidate.list-item.html | Ark.ProjectCandidate | (unchanged) |

## MCP Shell Changes

### MCP app.lua

New method:
```lua
function mcp:displayArkMessages()
    -- Set messaging mode before display
    ark:showMessaging()
    mcp:display("ark")
end
```

Existing `displayArk()` updated:
```lua
function mcp:displayArk()
    ark:showSearching()
    mcp:display("ark")
end
```

### MCP.DEFAULT.html

Second button in bottom bar:
```html
<span class="mcp-build-mode-toggle" ui-event-click="displayArkMessages()" title="Ark Messages">
  <sl-icon name="envelope-fill"></sl-icon>
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
