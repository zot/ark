# Ark

@note: add interactive searching -- this is the user's google and yahoo on all their stuff and all of the assistant's stuff. Frictionless chat and other events links to the AI partner directly
@note: need to have a way to show unresolved files deep in the trees. Ark CLI should help with this. `ark unresolved` has "path" right now but it should be <pattern>

The ark app has three top-level views:

- **Searching** — index manager + full-text search (ark-searcher)
- **Messaging** — cross-project message dashboard (ark-messenger)
- **Curation** — vocabulary-maintenance workshop (chunk → tag, tag → chunk, tag → tag)

A thin root object routes between them. The MCP shell's bottom bar
has three ark buttons: searching (archive icon), messaging
(envelope icon), and curation (compass icon). Each sets the view
mode and displays the ark app.

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

Clicking a source slides its file tree in over the right panel
(see "File Tree" below). Clicking the same source again, or the X
button in the tree's header, dismisses the overlay.

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

Slide-over overlay above the right panel. Search owns the full
right panel; selecting a source slides the file tree in from the
right edge. The tree shows the actual filesystem merged with ark's
index state. Every file and directory shows one of three states:

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

### Slide-Over Behavior

Both the file tree and the Add Source form sit above the search
component as absolute-positioned overlays:

- Default state: search visible, no overlay
- Selecting a source: tree overlay slides in from the right
- "Add Source" button: form overlay slides in from the right
- Re-clicking the selected source toggles the tree off
- X button in the overlay header closes it
- Search remains mounted underneath so its state survives

## Search

### `<ark-search>` Component

The right panel hosts an `<ark-search>` custom element (from
`ark-search/`) that provides the full search UI:
- Search bar with mode selector (tag / contains / fuzzy / regex)
- Stackable filter rows with OR groups
- Source-type toggle bar (hidden — sidebar handles this)
- Saved filter presets (chips)
- Progressive results with iframe chunk previews
- Three-phase search (trigram → embedding → curation) when available

The element talks to the ark server via HTTP endpoints registered
on the UI server (`/search/grouped`, `/tags/complete`, `/tags/values`).
A JS `SearchAPI` adapter wires the element to these endpoints.

### Sidebar Filter Integration

Sidebar filter buttons (data/projects/memory/chats) and per-source
toggles produce filter_files/exclude_files arrays. These are passed
to the `<ark-search>` element as default search scope:

- **Default:** searches use the sidebar's filter settings
- **Override:** if the user adds `[files]` filter rows in the
  element, sidebar settings are ignored entirely
- The element's built-in source toggle bar is hidden (CSS) since
  the sidebar already provides source filtering

This is a temporary JS bridge (SearchAPI reads a hidden span).
A proper `filters` attribute on the element is planned (see PLAN.md).

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

### Sort Controls

Sort field cycles: date → to → from → subject. Direction toggles
ascending/descending. Sorting applies within each column.

### Message Detail

Clicking a card opens a detail dialog showing:
- Project label (from → to) and status date
- Status dropdown (editable, saves via `mcp.setTags`)
- Response-handled / Request-handled dropdowns
- Request/Response tab switcher (when response exists)
- Rendered markdown body
- Footer: Edit (opens in editor) and Complete (sets all statuses
  to completed, closes dialog, refreshes)

### Refresh

Manual refresh button. Real-time updates are V3 territory.

### What's NOT in scope

- Cross-project file writes (never, by design)

## Curation View

The vocabulary-maintenance workshop. Three of the four entry points
from `.scratch/CURATION-VIEW.md` (chunk → similar-chunks deferred,
no backend yet):

- **chunk → tag candidates** (entry 1, `mcp:suggestTagNames`)
- **tag → chunk candidates** (entry 2, `mcp:topKChunksForTag` cached
  / `mcp:chunksForTag` live)
- **tag → tag** (entry 4, `mcp:relatedTags` / `mcp:tagDrift` /
  `mcp:tagPairConflict`)

### Layout

Two-column workshop:

- **Left/main:** pinned-chunks workspace. Each pinned chunk shows
  its path, a content preview, and a tag-suggestions panel
  (entry 1). Per-chunk dismiss; sweep-older bulk action.
- **Right:** tag explorer. Type a tag to focus it; when focused,
  shows the tag's top-K chunks (entry 2), related tags (entry 4),
  and drift pairs (entry 4). Click a related tag to switch focus;
  click a chunk to pin it.

A header strip carries the title, sweep status, sweep button, and
refresh.

### Pinned-chunks behavior

Four rules from `.scratch/CURATION-VIEW.md`:

- **Always-add, never-flip.** `Ark.Curation:curate(chunkID)` adds
  the chunk to the top of the list and never switches the user
  into the view. Pinning is a stash gesture from anywhere in the
  app (eventually `<ark-search>` results, manually for now).
- **New chunks land at the top** by pin time.
- **Per-chunk dismiss.** Each pinned chunk has its own X button.
- **Sweep older.** A button drops everything below the topmost
  pin in one click. Topmost pin survives as the working anchor.
- **New-since-last-viewed accent.** Chunks pinned while the view
  was closed get a visual accent (NEW pill). Accent clears when
  the view is opened. The "last viewed" timestamp is persisted
  per-user.

### Tag focus

A text input accepts a tag name. Submitting the input "focuses"
that tag, populating:

- **Chunks for this tag** — `mcp:topKChunksForTag(tag, k)` via the
  HC cache. Each row has a pin button. Falls back to
  `mcp:chunksForTag` (live) when the cache is missing or empty
  and the embedding model is available.
- **Related tags** — `mcp:relatedTags(tag, k)`. Click a tag to
  switch focus.
- **Drift pairs** — `mcp:tagDrift(tag)`. Read-only display showing
  which definition files of the tag have diverged.

A "clear focus" button restores the unfocused state of the side
panel (an empty-state hint).

### Sweep controls

- **Sweep button** — calls `mcp:sweepHotCorrelations()`. Runs
  through the write goroutine, identical to
  `POST /sweep/correlations`. Returns the `SweepResult` summary.
  The button is disabled while a sweep is in flight; the header
  shows a "sweeping..." indicator.
- **Sweep result** — once the call returns, the header shows a
  one-line summary (duration, tagsRebuilt, tagsTouched). The
  embedding-unavailable degraded reply is surfaced as a friendly
  one-line message instead.

Live progress (subscribing to the `tmp://sweep/hot-correlations.md`
doc's `@sweep-status` / `@sweep-progress` tags) is a follow-up
slice — Frictionless `mcp:subscribe` is for publisher topics, and
the Lua bridge for tmp:// document subscriptions is not yet in
place.

### Persistence

Pinned chunks and the last-viewed timestamp persist across server
restarts in `~/.ark/apps/curation/state.json`. The directory is
created on first write. State is loaded on view init and rewritten
on every mutation.

### What's NOT in scope (this slice)

- **Entry 3 (chunk → similar chunks)** — needs a Librarian
  backend (`SimilarChunks(chunkID, k)`) that doesn't exist yet.
- **`<ark-search>` Curate button** — separate TypeScript slice;
  for now the curation view is reachable through the bottom-bar
  button and chunks are pinned from the focused-tag list or via
  `ark:curate(chunkID)` from Lua.
- **Tag conflict explorer** — `mcp:tagPairConflict(tagA, tagB)`
  is bridged but the UX for picking two tags is deferred. The
  drift surface covers the within-tag case, which is the more
  common need.
- **Vocabulary gaps** (bottom-K against all tag defs) — bottom-K
  inversion of the same engine; not in this slice.
- **Tag-name completion** — typed input only, no autocomplete.
- **Tag-write actions** — applying a suggestion to a chunk's
  source file is Phase 2 (Workmanlike / Magic modes).
- **Status badge on the bottom-bar button** — the badge with
  pinned-count + new-count is described in CURATION-VIEW.md but
  lives in the MCP shell, not this app. Surfaced as a separate
  slice once the workshop is settled.

## MCP Shell Integration

Three ark buttons in the MCP bottom bar:
- **Archive icon** — displays ark in searching mode
- **Envelope icon** — displays ark in messaging mode
- **Compass icon** — displays ark in curation mode (new)

Each button sets the view mode on the ark instance before calling
`mcp:display("ark")`.
