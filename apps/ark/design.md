# Ark Index Manager - Design

## Intent

Visual manager for ark's source configuration. Two-panel layout: source
directories on the left, merged file tree on the right. Shows what's
indexed, excluded, and unresolved — discrepancies are immediately visible.
The app shells out to `ark` CLI commands for all operations.

## Layout

```
+-------------------+------------------------------------------+
| Sources [✓][⇄][+] | ~/work/myproject          [Collapse] [⟳] |
|-------------------|------------------------------------------|
| > ~/work/myproj 🔍 | ├── [✓] src/                             |
|   markdown  47/3/2 | │   ├── [✓] main.go                     |
|                    | │   ├── [✓] server.go                    |
|   ~/notes          | │   └── [?] utils_test.go                |
|   markdown 120/0/0 | ├── [✗] .git/                            |
|                    | ├── [✗] node_modules/                    |
|-------------------|  ├── [✓] README.md                       |
| Claude Quick-Add  | └── [?] scratch.txt                      |
| [Projects]        |------------------------------------------|
| [Chat History]    | ✓ 47 | ✗ 12 | ? 2 | 👻 1 | Server: ● |
| [Memories]        |                                          |
+-------------------+------------------------------------------+
```

Legend:
- `[+]` = Add source button
- `[Collapse]` = Collapse resolved directories
- `[⟳]` = Scan + refresh
- `>` = Selected source
- `47/3/2` = included / excluded / unresolved counts
- `[✓]` = Included (green)
- `[✗]` = Excluded (dim/red)
- `[?]` = Unresolved (amber, bold filename)
- `👻` = Missing (italic filename, ghost file)
- `●` = Server status indicator

### Tree Node States

Each node shows a state icon. Clicking cycles: include → exclude → clear.
Directories cycle applies to all unresolved children only.

Directory rollup (when children are loaded):
- All children included → `[✓]`
- All children excluded → `[✗]`
- Mixed or has unresolved → `[~]`

Before children are loaded, directory shows its own pattern state.

### Exception Combobox

Directories show a combobox for common exception patterns:
```
├── [✓] src/ [ ▾ add exception  ]
│              ├─ *_test.go
│              ├─ *.generated.*
│              ├─ vendor/**
│              └─ type a glob...
```

If the directory is included, exceptions become excludes. If excluded,
exceptions become includes. Pattern is auto-prefixed with the directory's
relative path (e.g., `src/*_test.go`).

### Why Tooltips

State icons have tooltips showing pattern resolution:
- "Included by: *.md (global include)"
- "Excluded by: node_modules/ (.gitignore)"
- "No matching pattern"
- "Included by: *.go (global include) — conflicts with: *_test.go (source exclude), include wins"

## Data Model

### Ark (main app)

| Field | Type | Description |
|-------|------|-------------|
| _sources | Ark.Source[] | Configured source directories |
| selectedSource | Ark.Source | Currently selected source |
| showAddForm | boolean | Show add-source form |
| newDir | string | Add form: directory path |
| newStrategy | string | Add form: strategy name |
| _dbPath | string | Database path (default ~/.ark) |
| _serverRunning | boolean | Whether ark server is responding |
| _statusCounts | table | {included, excluded, unresolved, missing} |

### Ark.Source (source directory)

| Field | Type | Description |
|-------|------|-------------|
| dir | string | Absolute directory path |
| strategy | string | Chunking strategy name |
| includedCount | number | Files matching include patterns |
| excludedCount | number | Files matching exclude patterns |
| unresolvedCount | number | Files with no pattern match |
| _visibleNodes | Ark.Node[] | Flat list of visible tree nodes |
| _nodeMap | table | path → Ark.Node for lookups |
| _missingPaths | table | Set of missing file paths |
| _loaded | boolean | Root nodes have been loaded |
| _loading | boolean | Currently loading nodes |
| _searchIncluded | boolean | Whether this source is included in search filter (default true) |

### Ark.Node (tree file/directory node)

| Field | Type | Description |
|-------|------|-------------|
| name | string | Filename |
| relPath | string | Path relative to source dir |
| fullPath | string | Absolute path |
| isDir | boolean | Is a directory |
| state | string | "included", "excluded", "unresolved" |
| depth | number | Nesting depth (0 = root children) |
| expanded | boolean | Directory is expanded |
| _childrenLoaded | boolean | Children have been fetched |
| isMissing | boolean | Indexed but not on disk (ghost) |
| whyPatterns | string | Cached patterns from show-why |
| whySources | string | Cached sources from show-why |
| whyConflict | boolean | Include-wins conflict applied |
| _whyLoaded | boolean | Why info has been fetched |
| hasIgnoreFile | boolean | Directory contains .gitignore/.arkignore |
| honorIgnore | boolean | Whether to respect the ignore file |
| exceptionPattern | string | Selected exception combobox value |

## Methods

### Ark

| Method | Description |
|--------|-------------|
| new() | Create instance, set _dbPath to "~/.ark", call loadConfig() |
| loadConfig() | Shell `ark config -db <path>`, parse JSON, populate _sources |
| refresh() | Shell `ark scan -db <path>` then `ark refresh -db <path>`, reload config |
| selectSource(source) | Set selectedSource, load root if not _loaded |
| openAddForm() | Set showAddForm = true |
| cancelAddForm() | Set showAddForm = false, clear fields |
| addSource() | Shell `ark config add-source`, reload config, select new source |
| removeSource() | Shell `ark config remove-source`, reload config |
| collapseResolved() | For each node in selectedSource._visibleNodes, collapse if fully included or excluded |
| checkServer() | Try `ark status`, set _serverRunning |
| visibleNodes() | Return selectedSource._visibleNodes or empty |
| hasSelectedSource() | selectedSource ~= nil |
| showSourceDetail() | has selected and not showAddForm |
| hideSourceDetail() | not showSourceDetail() |
| hidePlaceholder() | has selected or showAddForm |
| showPlaceholder() | not hidePlaceholder() |
| hideAddForm() | not showAddForm |
| statusText() | Format _statusCounts for status bar |
| serverStatusClass() | "running" or "stopped" CSS class |
| quickAddProjects() | List ~/.claude/projects/, pre-fill add form |
| quickAddChat() | Pre-fill with Claude chat history path |
| quickAddMemories() | Pre-fill with Claude memory path |
| sourceHeaderText() | Return selectedSource.dir or "" |
| showSourceFilter() | True when 2+ sources exist |
| selectAllSources() | Set all sources to searchIncluded, re-search |
| invertSourceSelection() | Flip all source search inclusion, re-search |
| sourceFilterFlags() | Return --source/--not-source flags string for search |

### Ark.Source

| Method | Description |
|--------|-------------|
| selectMe() | Call ark:selectSource(self) |
| isSelected() | self == ark.selectedSource |
| countsText() | Return "47/3/2" formatted string |
| loadRootNodes() | Walk root dir, classify each entry, merge missing files, set _loaded |
| loadChildren(node) | Walk node's directory, classify entries, merge missing, insert into _visibleNodes |
| classifyFile(fullPath) | Shell `ark config show-why -db <path> <fullPath>`, parse JSON, return state info |
| getMissingFiles() | Shell `ark missing -db <path>`, filter to this source dir |
| walkDir(dirPath) | Shell `ls -1A <dir>`, return sorted entries with isDir flags |
| insertNodes(parentIndex, nodes) | Insert nodes into _visibleNodes after parentIndex |
| removeDescendants(parentIndex) | Remove all deeper nodes after parentIndex until depth <= parent |
| refreshCounts() | Recount included/excluded/unresolved from _nodeMap |
| removeMe() | Call ark:removeSource() with self.dir |
| isLoading() | Return _loading |
| notLoading() | Return not _loading |
| toggleSearchInclude() | Flip _searchIncluded, re-search if results showing |
| isSearchIncluded() | Return _searchIncluded |
| isSearchExcluded() | Return not _searchIncluded |
| searchFilterIcon() | Return "funnel-fill" if included, "funnel" if excluded |

### Ark.Node

| Method | Description |
|--------|-------------|
| toggle() | If expanded, collapse; else expand |
| expand() | Load children if needed, insert into parent source's _visibleNodes, set expanded |
| collapse() | Remove descendants from _visibleNodes, set expanded = false |
| cycleState() | include → exclude → clear: call appropriate ark config command, re-classify |
| stateIcon() | Return "✓", "✗", or "?" |
| stateClass() | Return "included", "excluded", or "unresolved" for CSS |
| nameClass() | "missing" if isMissing, "unresolved" if state == "unresolved", else "" |
| indentPx() | Return depth * 20 for padding-left |
| isExpandable() | isDir |
| notExpandable() | not isDir |
| expandIcon() | "▶" if collapsed, "▼" if expanded |
| hideExpandIcon() | not isDir |
| showExpandIcon() | isDir |
| loadWhy() | Shell `ark config show-why`, cache results, set _whyLoaded |
| whyTooltip() | Return formatted why text (load lazily if needed) |
| hasExceptions() | isDir and state ~= "unresolved" |
| noExceptions() | not hasExceptions() |
| applyException() | Add include/exclude with path prefix based on dir state and exceptionPattern |
| showIgnoreCheckbox() | isDir and hasIgnoreFile |
| hideIgnoreCheckbox() | not showIgnoreCheckbox() |
| toggleHonorIgnore() | Toggle honorIgnore, re-classify children |

## ViewDefs

| File | Type | Purpose |
|------|------|---------|
| Ark.DEFAULT.html | Ark | Main two-panel layout with status bar |
| Ark.Source.list-item.html | Ark.Source | Source row in left panel |
| Ark.Node.list-item.html | Ark.Node | Tree node row in right panel |

## Events

### From UI to Claude

```json
{"app": "ark", "event": "chat", "text": "...", "handler": "/ui-fast", "background": false}
```

### Claude Event Handling

| Event | Action |
|-------|--------|
| `chat` | Respond to questions about ark configuration using the ark CLI |
