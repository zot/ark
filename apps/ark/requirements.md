# Ark

@note: add interactive searching -- this is the user's google and yahoo on all their stuff and all of the assistant's stuff. Frictionless chat and other events links to the AI partner directly
@note: need to have a way to show unresolved files deep in the trees. Ark CLI should help with this. `ark unresolved` has "path" right now but it should be <pattern>

The ark app has two top-level views, mirroring the two ark agents:

- **Searching** — index manager + full-text search (ark-searcher)
- **Messaging** — cross-project message dashboard (ark-messenger)

A thin root object routes between them. The MCP shell's bottom bar
has two ark buttons: one for searching (archive icon), one for
messaging (envelope icon). Each sets the view mode and displays the
ark app.

## Architecture

**Lua + in-process Go:** The app uses `mcp:search_grouped()` for search
and `mcp:inbox()` for messaging (both in-process Go functions). Config
operations use `ark` CLI commands.
If we discover missing CLI support during development, that's the app
doing its job — we go add it to ark.

## Source List

Left panel. Shows configured source directories grouped into projects
and data sources.

Each source shows:
- Directory path (compressed for display, full path in tooltip)
- Strategy name (markdown, lines, etc.)
- File counts: included / excluded / unresolved

Clicking a source shows its file tree in the right panel.

### Source Display Grouping

Sources are grouped into two categories:

- **Projects** — Claude project directories, showing resolved project
  name, file/memory/chat toggle switches
- **Data Sources** — standalone directories with a data toggle

Filter bar at top of source list controls which categories are
included in search: data, projects, memory, chats. Each has a
tri-state icon (on/mixed/off). Double-click to solo. Reset button
restores defaults.

### Project Editor

"Choose Projects" button opens an overlay panel in the sidebar:
- Search/filter bar with refresh button
- Upper section: currently configured projects (with checkboxes)
- Lower section: available projects (not yet configured)
- Checkboxes toggle selection, badges show "new" / "removed" state
- Save button applies add/remove deltas to ark config
- Cancel discards changes, Reset restores original selection

### Pattern Editing

Each source has a gear icon that reveals include/exclude pattern
textareas. Patterns are one per line (glob syntax). Save button
diffs old vs new patterns and applies add-include/add-exclude/
remove-pattern commands. Errors are reported via notifications.

## File Tree

Right panel. Shows the actual filesystem merged with ark's index
state. Every file and directory shows one of three states:

- **Included** (green check) — matches an include pattern, will be indexed
- **Excluded** (red X) — matches an exclude pattern, skipped
- **Unresolved** (gray ?) — matches nothing, ark won't touch it

### Merged View

The tree shows files from two sources overlaid:
- Files that exist on disk (from walking the source directory)
- Files that are indexed but missing from disk

Visual treatment:
- Normal text — file exists on disk and is accounted for (included or excluded)
- *Italic* — indexed in ark but missing from disk (ghost file)
- **Bold** — exists on disk but not accounted for (unresolved)

### Expanding and Collapsing

All directories are expandable regardless of state. A fully-included
`[✓]` directory can be expanded to carve out exceptions.

**Default collapse:** Fully-included and fully-excluded directories
start collapsed. Mixed directories (`[~]`) start expanded.

**"Collapse resolved" button:** Collapses all fully-included and
fully-excluded directories in one click.

### Lazy Loading

Children are loaded in batches when a directory is expanded.
Directories that have never been expanded have no child nodes.

### Changing State

Clicking a file's state indicator cycles: include → exclude → unresolved.
Clicking a directory's state applies to all unresolved children only.

### Why Tooltips

Every state indicator has a tooltip explaining why the file has
that state (pattern match, .gitignore, etc.).

### Resizable Panels

A draggable handle between left and right panels allows resizing.
Minimum 180px, maximum 50% of viewport width.

## Search

### Search Bar

Always visible at top of right panel. Contains:
- Filter panel toggle (funnel icon, fills when active)
- Mode selector: contains / about / regex
- Search input with live search (fires on 3+ chars, Enter for immediate)
- Clear button (visible when results showing)

### Search Results

Results grouped by file using `SearchFileGroup` type:
- File header: compressed path (accent color), top score, "+N more" count
- Expandable: click to show/hide additional chunks
- Each chunk: line range, score, preview text (HTML with highlights)
- Hits per file: cycle button (1 / 3 / all) re-searches with adjusted k

Search uses `mcp:search_grouped()` (in-process Go function) with
filter opts built from source filter buttons + filter panel fields.

### Session Cache

Search passes `session = "ui"` in the opts table to
`mcp.search_grouped()`. This uses a server-side session that keeps
the ChunkCache alive across keystrokes — successive queries that
are prefixes of each other reuse cached file reads instead of
re-reading from disk on every keystroke.

### Filter Panel

Collapsible 2×2 grid above search bar:
- Filter files (glob patterns, one per line)
- Exclude files (glob patterns)
- Filter content (FTS queries, one per line)
- Exclude content (FTS queries)

All fields compose with source filter buttons via intersection.

**Filter file intersection:** When source buttons produce positive
filter patterns (e.g. `~/work/ark/**`) and the user also provides
file patterns (e.g. `*.go`), the two sets are ANDed — each source
pattern is narrowed by each user pattern (`~/work/ark/**/*.go`).
Without this, the broad source patterns would match everything and
the user's pattern would be redundant (OR semantics). In exclude
mode (no partial sources), user patterns work standalone.

## Status Bar

Bottom of the app. Shows:
- Total files: included / excluded / unresolved / missing
- Server status (running or not)

## Messaging Dashboard

Kanban-style view showing cross-project messages grouped by status
columns. Data comes from `mcp:inbox()` (in-process Go function).

### Layout

Status columns displayed horizontally. Only columns with items are
shown. Target normal monitors (external monitor or XR glasses on
the Deck, not the 7-inch screen).

Status columns (from ARK-MESSAGING.md):
- Future, Open, Accepted, In-Progress, Completed, Denied

### Filter Chips

Two rows of chips above the kanban:

- **Project chips** — one per project involved in any message. Click
  cycles: all → to → from → none. Shows directional counts (to/from).
- **Status chips** — one per status. Click toggles column visibility.

### Message Cards

Each card represents a **conversation** — one request merged with its
response(s). A request can have multiple responses (one per
participating project).

Each card shows:
- Summary (from @issue tag)
- From/To project names
- Status badge
- Click to open file via `mcp:open(path)`

Column placement is driven by the **request's** `@status` — the
requester owns the issue and decides its overall state.

### Bookmark Lag

Cards show stale bookmark chips when a participant's `@*-handled:`
tag is behind the counterpart's `@status`. Format: `PROJECT:status`.
A clean card means everyone is current. Chips mean someone owes work.

### Refresh

Manual refresh button. Real-time updates are V3 territory.

### What's NOT in scope

- Status mutation from the UI
- Cross-project file writes (never, by design)

## MCP Shell Integration

Two ark buttons in the MCP bottom bar:
- **Archive icon** — displays ark in searching mode (current behavior)
- **Envelope icon** — displays ark in messaging mode (new)

Each button sets the view mode on the ark instance before calling
`mcp:display("ark")`.
