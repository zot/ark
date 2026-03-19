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
| Sources [⇄][⟳]    | [⫰] [contains][about][regex] [🔍____] [✕] |
|  filter bar       |------------------------------------------|
|-------------------|  Filter panel (collapsible 2×2)           |
| > project-name    |------------------------------------------|
|   [📄][🧠][💬]     | File groups with chunk previews           |
|   data-source     |                                          |
|   [📊]            |                                          |
|-------------------|                                          |
| [✏️ Choose Projects]|                                         |
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

All existing fields and methods from the previous Ark type. No changes
to behavior — only the type name changes. See previous design for the
full field list.

Key fields: _sources, selectedSource, searchQuery, searchMode,
_searchGroups, _searchView, _hitsPerFile, _showFilterPanel,
filterFiles, excludeFiles, filterContent, excludeContent,
_displayItems, _projects, _dataSources, _projectSearchOpen,
_projectCandidates, _showPatterns, _statusCounts, _serverRunning.

### Ark.Messaging

| Field | Type | Description |
|-------|------|-------------|
| _messages | Ark.Message[] | Merged conversations from mcp:inbox() |
| _loading | boolean | Refresh in progress |
| _chips | Ark.FilterChip[] | One per project, cycles filter modes |
| _statusChips | Ark.StatusChip[] | One per status, toggles column visibility |

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

All existing methods unchanged. The type name changes from `Ark` to
`Ark.Searching`; the local shortcut `Searching` replaces bare `Ark`
in method definitions. Internal references to the global `ark` instance
now navigate through `ark._searching` where needed, but most methods
use `self` and are unaffected.

Key method: `buildFilterOpts()` — builds the filter_files,
exclude_files, filter, and except arrays for `mcp:search_grouped()`.

**Session cache:** `onSearchInput()` passes `session = "ui"` in the
opts table. This uses a server-side session that keeps the ChunkCache
alive across keystrokes — successive prefix queries reuse cached file
reads instead of re-reading from disk each time.

**Filter file intersection:** Source buttons produce positive
`filter_files` patterns in partial mode (`hasPartial`). User file
patterns from the filter panel are collected separately, then
cross-producted with the source patterns: `dir/** + *.go →
dir/**/*.go`. This gives AND semantics (only files matching both
the source scope AND the user pattern survive). In exclude mode
(no partial sources), user patterns are appended directly as
standalone positive filters.

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

## ViewDefs

| File | Type | Purpose |
|------|------|---------|
| Ark.DEFAULT.html | Ark | Thin shell: `ui-view="currentView()"` |
| Ark.Searching.DEFAULT.html | Ark.Searching | Full search/index UI (renamed from Ark.DEFAULT.html) |
| Ark.Messaging.DEFAULT.html | Ark.Messaging | Kanban columns with message cards |
| Ark.Message.list-item.html | Ark.Message | Card in kanban column |
| Ark.Source.list-item.html | Ark.Source | (unchanged) |
| Ark.Project.list-item.html | Ark.Project | (unchanged) |
| Ark.DataSource.list-item.html | Ark.DataSource | (unchanged) |
| Ark.Node.list-item.html | Ark.Node | (unchanged) |
| Ark.SearchFileGroup.list-item.html | Ark.SearchFileGroup | (unchanged) |
| Ark.SearchResult.list-item.html | Ark.SearchResult | (unchanged) |
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
