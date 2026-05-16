# Ark

@note: add interactive searching -- this is the user's google and yahoo on all their stuff and all of the assistant's stuff. Frictionless chat and other events links to the AI partner directly
@note: need to have a way to show unresolved files deep in the trees. Ark CLI should help with this. `ark unresolved` has "path" right now but it should be <pattern>

The ark app has three top-level views:

- **Searching** — index manager + full-text search (ark-searcher)
- **Messaging** — cross-project message dashboard (ark-messenger)
- **Curation** — vocabulary-maintenance workshop (chunk → tag, tag → chunk, tag → tag)

A thin root object routes between them. The MCP shell's bottom bar
has three ark buttons: searching (archive icon), messaging
(envelope icon), and curation (multi-tag icon). Each sets the view
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

The vocabulary-maintenance workshop. The 2026-05 reframe
(`.scratch/CURATE-ONE-CHUNK.md`) turns the pinned-chunks panel into
a **per-chunk editing surface**: each pinned card carries a stack
of *pending widgets* above its URL, lets the user author tag
additions/removals (inline in the chunk text, or routed via
`@ext` mirror files), and commits the batch through
Stage / Revert / Accept verbs.

### File-truth principle

Files → chunks → tags. **Files are the only source of truth.** The
user (or this UI) edits text in a file region; ark rechunks +
reindexes automatically. There is no special "rewrite a chunk" API
— only `mcp.replaceRegion(path, byteStart, byteEnd, newText)` and,
for routed tags, `mcp.setExtTag` / `mcp.removeExtTag` writing
mirror files under `~/.ark/external/`. The workshop never tries to
maintain its own chunk view independent of the file.

### Layout

The pinned-chunks workshop is the primary surface. The tag
explorer is retained as a **secondary panel** for tag-driven
discovery (top chunks for a tag, related tags, drift pairs) —
useful even though the primary authoring path now runs through
per-chunk widget stacks.

```
+--------------------------------------+----------------------+
| Curation  Sweep: idle  [⚡][↻ Accept (N)]                   |
+--------------------------------------+----------------------+
| Pinned chunks (3) [⤓ Sweep older]   | Tag explorer         |
| ┌──────────────────────────────────┐ | [_____________]      |
| │ [tag][val][rem][ext][X]          │ | Defined tags          |
| │ [tag][val][rem][ext][X]          │ |  • design-decision    |
| │ [+]                              │ |  • feedback           |
| │ NEW path/file.md (2 pending) X  │ | Focused: design-…    |
| │  [↓ Stage] [↶ Revert]            │ |  Top chunks (10)     |
| │  > tag suggestions               │ |  Related tags        |
| └──────────────────────────────────┘ |  Drift               |
+--------------------------------------+----------------------+
```

### Pinned-chunks behavior

- **Always-add, never-flip.** `Ark.Curation:curate(chunkID)` adds
  the chunk to the top of the list and never switches the user
  into the view.
- **New chunks land at the top** by pin time.
- **Per-chunk dismiss.** Each card has its own X button. Dismiss
  prompts confirmation when the card has any pending changes.
- **Sweep older.** Drops everything below the topmost pin.
- **New-since-last-viewed accent.** Chunks pinned while the view
  was closed wear a NEW pill, cleared on next view-open.

### Pending widget stack (per card)

Each card carries a stack of *pending widgets* above its URL row.
A widget authors one tag operation (add, change, or remove)
against the chunk.

**Per-widget controls:**

- `[tag]` — tag name input
- `[value]` — tag value input (ignored when remove toggle is on)
- `[remove]` — checkbox; off = add/change, on = remove this tag
- `[ext]` — checkbox; off = inline (insert into chunk text), on =
  routed via `@ext` mirror file (reveals base + locator dropdowns)
- `[X]` — kill the widget unconditionally

**Stack rules:**

- **Empty-start invariant.** When the stack has no pending
  content, exactly one empty widget remains visible — never zero.
- **[+] button.** Adds a new empty widget below the stack.
- **Tab-out auto-add.** Tabbing past the last field of a *filled*
  widget creates a new empty widget. Tabbing past the last field
  of an *empty* widget moves focus past the stack.

**Read-only protections.** When the chunk's `chunkInfo.writable`
is false (PDF chunks, `~/.claude/projects/**` chat logs, any other
chunker reporting `writable: false`), the ext toggle locks on and
inline operations are disabled with a "read-only" hint.

### Ext-tag widgetry

When a widget's ext toggle is on, two more controls reveal:

- **Base dropdown** — `UUID | path`. Defaults to UUID when the
  chunk has an `@id`; path otherwise.
- **Locator dropdown** — `string | regex | absolute | bare`.
  Defaults follow the table in `.scratch/CURATE-ONE-CHUNK.md`
  ("Ext-tag widgetry — default selection table"): bare when the
  UUID is unique within file, string (auto-picked via the
  three-layer algorithm) otherwise, absolute for read-only
  chunkers.

A **scope readout** below the locator preview shows `will route
to N chunks across M files` — important when UUID-base scope
crosses files, or when an auto-picked locator matches more chunks
than the user expected.

Widget defaults come from `mcp.suggestExtLocator(chunkID)`. The
widget reads `locatorText` for the proposed locator value
(`locator` field has a known bug — tracked as a `/mini-spec`
follow-up).

### Stage / Revert / Accept verbs

Three verbs in the workshop, two per-card and one panel-level:

**Stage** (per-card): folds filled widgets into a per-card
*staged ops* buffer. Widgets clear from the stack; pending badge
updates. No disk write yet.

**Revert** (per-card): clears the staged-ops buffer and
recreates widgets from the staged operations. Symmetric to
Stage.

**Accept changes (N)** (panel-level, header button): implicit-
stages any still-unstaged widgets across all cards, then
executes every card's staged operations:

- Inline op → `mcp.replaceRegion(path, byteStart, byteStart,
  newText)` where `newText` is `@tag: value\n` (markdown) or
  `commentSyntax @tag: value\n` (code chunks) per
  `chunkInfo.commentSyntax`.
- Inline remove → `mcp.replaceRegion` collapses the tag line
  (range derivation deferred — v1 prepends a removal marker;
  full per-line removal is a follow-up).
- Ext op → `mcp.setExtTag(targetSpec, tag, value)` or
  `mcp.removeExtTag(targetSpec, tag)` per the widget's base +
  locator selection.

Per-chunk errors surface on the failing card; successful cards
clear their staged ops. The badge N counts filled widgets +
staged ops across every pinned card; the button disables when
N = 0.

### Spectral-value rule

Tag values must be **chunk-specific, not generic** — all chunks
getting the same value is functionally equivalent to no value at
all (spectral search collapses). The widget allows identical
values across cards, but the per-card widget form encourages
per-chunk values. See `.scratch/CURATE-ONE-CHUNK.md` "The
spectral-value rule" for the rationale.

### Tag explorer (secondary panel, kept)

The right-side tag explorer survives the reframe as a secondary
panel for tag-driven discovery. Type a tag name (or click one in
the defined-tags picker); when focused, the panel shows the
tag's top-K chunks (entry 2 from `.scratch/CURATION-VIEW.md`),
related tags, and drift pairs. Click a related tag to switch
focus; click a chunk to pin it.

Entry coverage:

- **chunk → tag candidates** (entry 1, `mcp.suggestTagNames`) —
  surfaces inside each pinned card as the "tag suggestions"
  collapsible.
- **tag → chunk candidates** (entry 2, `mcp.topKChunksForTag`
  cached / `mcp.chunksForTag` live).
- **tag → tag** (entry 4, `mcp.relatedTags` / `mcp.tagDrift` /
  `mcp.tagPairConflict`).

### Tag focus

A text input accepts a tag name. Submitting the input "focuses"
that tag, populating:

- **Chunks for this tag** — `mcp.topKChunksForTag(tag, k)` via the
  HC cache. Each row has a pin button. Falls back to
  `mcp.chunksForTag` (live) when the cache is missing or empty
  and the embedding model is available.
- **Related tags** — `mcp.relatedTags(tag, k)`. Click a tag to
  switch focus.
- **Drift pairs** — `mcp.tagDrift(tag)`. Read-only display showing
  which definition files of the tag have diverged.

A "clear focus" button restores the unfocused state of the side
panel (an empty-state hint).

### Sweep controls

- **Sweep button** — calls `mcp.sweepHotCorrelations()`. Runs
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

- **Chunk-text editor** — `<ark-markdown-editor>` embedded inside
  each card's `> chunk text` collapsible. Needs Go bridges
  (`mcp.chunkText`, `mcp.parseTagBlock`) — deferred to a
  `/mini-spec` follow-up.
- **`> current tags` reflection** — live tags-on-chunk display
  derived from chunk text. Same Go-bridge gap as above.
- **Find Connections integration** — the proposals panel with
  `[Fill]` buttons that inject pre-filled widgets into evidence
  chunks. Needs `mcp.tmp_get` (Lua-side read of tmp:// doc
  content) — deferred to a `/mini-spec` follow-up.
- **Sweep button retrofit** — convert `Curation:sweepNow` from
  blocking to fire-and-forget + `mcp.subscribe` on
  `tmp://sweep/hot-correlations.md`. Needs Go addition
  (`mcp.sweepHotCorrelationsAsync` or non-blocking variant) —
  deferred to a `/mini-spec` follow-up.
- **Inline tag removal precision** — the design calls for
  deleting tag-on-own-line vs. tag-tacked-on-line. v1 widgets
  with `remove` toggle on stage a removal marker; full per-line
  removal is a follow-up.
- **Per-line annotation** — authoring a tag *about* a specific
  line mid-chunk is deferred; v1 inserts at top.
- **Entry 3 (chunk → similar chunks)** — needs a Librarian
  backend (`SimilarChunks(chunkID, k)`) that doesn't exist yet.
- **`<ark-search>` Curate button** — separate TypeScript slice
  (Subtask B in `.scratch/PLAN-CURATE-CHUNK.md`).
- **Tag conflict explorer** — `mcp.tagPairConflict(tagA, tagB)`
  is bridged but the UX is deferred.
- **Vocabulary gaps** (bottom-K against all tag defs) — bottom-K
  inversion of the same engine.
- **Tag-name completion** — typed input only, no autocomplete.
- **Status badge on the bottom-bar button** — the badge with
  pinned-count + new-count lives in the MCP shell, not this app.

## MCP Shell Integration

Three ark buttons in the MCP bottom bar:
- **Archive icon** — displays ark in searching mode
- **Envelope icon** — displays ark in messaging mode
- **Multi-tag icon** — displays ark in curation mode

Each button sets the view mode on the ark instance before calling
`mcp:display("ark")`.
