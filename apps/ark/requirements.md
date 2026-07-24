# Ark

@note: add interactive searching -- this is the user's google and yahoo on all their stuff and all of the assistant's stuff. Frictionless chat and other events links to the AI partner directly
@note: need to have a way to show unresolved files deep in the trees. Ark CLI should help with this. `ark unresolved` has "path" right now but it should be <pattern>

The ark app has four top-level views:

- **Searching** — index manager + full-text search (ark-searcher)
- **Messaging** — cross-project message dashboard (ark-messenger)
- **Curation** — the Tag Forge, a vocabulary-maintenance workshop (chunk → tag, tag → chunk, tag → tag). Displayed title: "Tag Forge".
- **Luhmann** — terminal on the ark-hosted Luhmann session (browser counterpart of `ark luhmann attach`)

A thin root object routes between them. The MCP shell's bottom bar
has four ark buttons: searching (archive icon), messaging
(envelope icon), curation (multi-tag icon), and luhmann (terminal
icon). Each sets the view mode and displays the ark app.

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

The vocabulary-maintenance workshop. The 2026-07 reconcile
(`.scratch/FORGE-UPDATE.md`) reroutes the workshop through the
`@ext-candidate` machinery: each pinned card carries a stack of
*proposal widgets* above its URL, and each widget fires its tag
operation (add, replace, or remove — inline in the file body or
routed via `@ext` mirror) **in one gesture** through the
machinery's candidate+accept path. The CM6 editor remains for
genuine content edits only; tag authoring never touches it.

### File-truth principle

Files → chunks → tags. **Files are the only source of truth.** The
user (or this UI) edits text in a file region; ark rechunks +
reindexes automatically. There is no special "rewrite a chunk" API.
Content edits go through `mcp.replaceRegion(path, byteStart,
byteEnd, newText)`; tag operations go through the ext-candidate
machinery, which itself only writes files (the target file's body
for internal disposition, mirror files under `~/.ark/external/`
for external). The workshop never tries to maintain its own chunk
view independent of the file.

### Approval machinery

@prototype: go-ext-hooks

Widget approval routes through the `@ext-candidate` machinery
(`specs/internal-disposition.md`, `specs/at-ext-parsing.md`): on
fire, the app authors a candidate carrying the disposition and
replace token, then accepts it — the same back-to-back pair `ark
ext candidate` + `ark ext accept` make at the CLI. The machinery
owns the write semantics: the four accept cells (external/internal
× add/replace), capability degrade (internal disposition on a
chunk type that can't host it — lines, chat-jsonl, pdf,
comment-less languages, read-only files — falls back to external),
and the ledger trail (dated candidate line, `@count`, positive
`@ext-judgment`).

**Lua shims until the Go hooks land.** The `mcp` surface has no
candidate/accept/reject bindings yet. This slice implements the
approval path as Lua shims named and shaped like the future
bindings (code as sketchpad); the shim implementations ride the
old primitives (`mcp.setExtTag` / `mcp.removeExtTag` /
`mcp.replaceRegion`) and approximate the machinery's degrade rule
via `chunkInfo.writable` alone, with the markdown stencil only —
per-chunker stencils and the comment-syntax degrade are the real
hooks' job. Every shim is
marked `-- @prototype: go-ext-hooks`; the follow-up `/mini-spec`
item (PENDING #65) binds the real hooks with signatures read off
the molded shims and discharges the marks.

**Honesty rule.** While the shims ride the old primitives, the
ledger trail does not exist. No UI affordance may claim it does —
fire-button feedback says what actually happened ("tag applied"),
never "candidate recorded", until the real hooks land.

**Removals.** External-disposition removes map to the machinery's
`ark ext remove`. Internal (inline) removal has no machinery verb
yet (the internal-remove gap); the shim implements it as a direct
text edit — locate the `@tag:` line in the chunk and rewrite the
region via `mcp.replaceRegion`. The gap decision rides with #65.

### Layout

The pinned-chunks workshop is the primary surface. The tag
explorer is retained as a **secondary panel** for tag-driven
discovery (top chunks for a tag, related tags, drift pairs) —
useful even though the primary authoring path now runs through
per-chunk widget stacks.

```
+----------------------------------------+----------------------+
| Curation  Sweep: idle  [⚡] [⌫ Clear unchanged] [Accept (…)] |
+----------------------------------------+----------------------+
| Pinned chunks (3) [⤓ Sweep older]      | Tag explorer        |
| ┌────────────────────────────────────┐ | [_____________]     |
| │ [🔥][tag][val][int|ext][add|rep][rem][X] │ Defined tags     |
| │ [🔥][tag][val][int|ext][add|rep][rem][X] │  • design-decision|
| │ [+]                                │ |  • feedback         |
| │ [edit] NEW path/file.md         [X]│ | Focused: design-…   |
| │  > current tags                    │ |  Top chunks (10)    |
| │  > tag scores                      │ |  Related tags       |
| │  > chunk text                      │ |  Drift              |
| │     (iframe; CM6 in edit mode)     │ |                     |
| └────────────────────────────────────┘ |                     |
+----------------------------------------+----------------------+
```

### Pinned-chunks behavior

- **Always-add, never-flip.** `Ark.Curation:curate(chunkID)` adds
  the chunk to the top of the list and never switches the user
  into the view.
- **New chunks land at the top** by pin time.
- **Per-chunk dismiss.** Each card has its own X button. Dismiss
  prompts confirmation when the card has filled proposal widgets
  or unsaved editor changes.
- **Sweep older.** Drops everything below the topmost pin.
- **New-since-last-viewed accent.** Chunks pinned while the view
  was closed wear a NEW pill, cleared on next view-open.

### Proposal widget stack (per card)

Each card carries a stack of *proposal widgets* above its URL row.
A widget authors one tag operation (add, replace, or remove)
against the chunk and fires it in one gesture.

**The loaded gun.** The candidate is already in the chamber: a
fire button sits *in front of* the editing fields. The user *can*
adjust any field before firing (tag, value, disposition,
add-vs-replace, remove) but never *has* to — clicking fire commits
the operation immediately through the approval machinery's
candidate+accept path. Nothing durable is written before the
click, so abandoned widgets leave no litter.

**Per-widget controls (fire first):**

- `[🔥 fire]` — commit this operation now (one-shot
  candidate+accept). Disabled while the tag name is empty. On
  success the widget resets to empty (or is removed, if others
  remain) and the card's tag data refreshes. On error the widget
  keeps its state and shows the error for retry.
- `[tag]` — tag name input
- `[value]` — tag value input (ignored when remove is on)
- `[disposition]` — `internal | external`, the machinery's
  disposition taxonomy. Internal writes the `@tag:` line into the
  file body; external routes via `@ext` mirror file (reveals base
  + locator dropdowns). Defaults to internal for writable chunks;
  locked to external when the chunk can't host an internal tag
  (the machinery's degrade, surfaced as a default rather than a
  silent fallback).
- `[add | replace]` — the machinery's `replace` token, explicit.
  Add appends another value of the tag; replace collapses the
  tag's values to this one. Defaults to add; pre-set to replace
  when the tag already exists on the chunk (the old
  infer-by-existence behavior becomes a visible default, not a
  hidden rule).
- `[remove]` — checkbox; on = remove this tag instead (value and
  add/replace ignored)
- `[X]` — kill the widget unconditionally

**Stack rules:**

- **Empty-start invariant.** When the stack has no filled
  content, exactly one empty widget remains visible — never zero.
- **[+] button.** Adds a new empty widget below the stack.
- **Tab-out auto-add.** Tabbing past the last field of a *filled*
  widget creates a new empty widget. Tabbing past the last field
  of an *empty* widget moves focus past the stack.

**Read-only protections.** When the chunk's `chunkInfo.writable`
is false (PDF chunks, `~/.claude/projects/**` chat logs, any other
chunker reporting `writable: false`), the disposition control
locks to external, and the per-card `[edit]` button is hidden (no
CM6 mount for read-only). Tag operations still work — external
disposition never touches the source file.

**Independent of edit mode.** The widget stack stays visible and
usable while the CM6 editor is mounted — tag authoring and content
editing are separate paths that no longer interact.

### External-disposition targeting

When a widget's disposition is external, two more controls reveal:

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

### Edit / Revert (content edits only)

Tag authoring is detached from the editor: proposal widgets fire
through the approval machinery and never touch CM6. The editor
remains for genuine *content* edits.

Each card has an `[edit|revert]` button to the left of the URL.
The icon itself carries the has-changes cue: gray when the editor
matches the original, accent (`--term-accent`) when it holds
unsaved changes. Two states:

| State | Editor mounted | Icon |
|-------|----------------|------|
| **viewing** | no | `pencil-square` gray |
| **editing** | yes | `arrow-counterclockwise` (gray clean, accent dirty) |

**Click `[edit]`** (from viewing):
- Snapshot `_chunkOriginalText` via `mcp.chunkText(chunkID)`.
- Mount CM6 editor seeded with `_chunkOriginalText` (or the saved
  draft from a prior revert — perfect restore).

**Click `[revert]`** (from editing):
- Snapshot `_savedEditorText = editor.getDoc()` so a subsequent
  `[edit]` can restore the user's draft byte-for-byte.
- Destroy CM6 editor; chunk-text collapsible returns to its
  read-only iframe view.

**Click on rendered chunk text** (when not in edit mode):
equivalent to clicking `[edit]`. A transparent click-shield over
the iframe catches clicks (iframe clicks don't bubble out).

### Accept (N changed)

Panel-level header button committing **content edits only** — tag
operations fire per-widget and are already committed by the time
Accept matters. `N` is the count of cards whose editor holds
unsaved changes (`_isChunkEdited`). Disabled at `N=0` with label
`Accept (no changes)`; enabled with label `Accept (N changed)`.

**Accept execution:**

For each card with `_isChunkEdited`:
- Read `editor.getDoc()` via JS bridge →
  `mcp.replaceRegion(path, byteStart, byteEnd_orig, text)`.
- On success: card returns to viewing state (editor destroyed).
- On error: `_acceptError` set, card retains state for retry.

Per-card errors don't abort the panel — each card processes
independently. Failed cards surface their error visually: red
border on the card, `sl-alert` banner under the URL row showing
the error text, and a small red dot on the card's
`[edit|revert]` button. Manual dismiss clears the error.

### Clear unchanged

Header button next to `[Accept …]`. Dismisses every pinned chunk
that satisfies all of: `_isChunkEdited == false`, no filled
proposal widgets. Useful for clearing out the workshop after a
batch.

### Chunk text view

The chunk text collapsible defaults to **initially expanded** —
it's the primary surface the user wants to see.

Two render modes:

- **read-only** (default): iframe at
  `/content<path>?range=byteStart-byteEnd&toggle=false`, the same
  pattern `<ark-search>` uses for chunk previews. The iframe
  embeds `<ark-ext-tags>` (with a tags icon next to the heading)
  carrying `<ark-tag id externalfile externaltarget>` records
  for every ext routing on the chunk. A transparent click-shield
  div absolutely-positioned over the iframe catches clicks
  (iframe clicks don't bubble out) and triggers `[edit]`. The
  `<ark-ext-tags>` indicator stops click-propagation so its
  dropdown still opens normally.
- **editor**: iframe + click-shield destroyed; CM6 mounted in
  their place via inline JS bridge using `createInkArkEditor`
  from `/ark-markdown-editor.js`. Initial doc is
  `_chunkOriginalText` (or `_savedEditorText` for perfect-restore
  after revert). Save flow: at Accept time, the bridge reads
  `editor.getDoc()` and Lua dispatches `mcp.replaceRegion`.
  Ctrl-S inside CM6 syncs the draft to Lua state but does NOT
  itself fire Accept.

Both modes shrink to fit their content, capped at 280 px. The
iframe wrapper uses `max-height: 280px` and the bridge sets the
iframe's explicit height from `contentDocument.body.scrollHeight`
(re-measuring via `ResizeObserver` to catch late-rendering
widgets like the ark-tag-overview sidebar). CM6 honors the cap
through `.cm-editor { max-height: 280px }` with its own
`.cm-scroller` handling internal overflow. Short chunks no
longer show a half-empty 280-pixel pane.

### Current tags collapsible

A widget-shaped read-only list below the URL showing the chunk's
tags as they stand. With one-shot firing there is no pending
overlay to merge — a fired operation is committed, and the card's
tag data refreshes after each fire. Sources merge into one list:

- Inline tags from `mcp.parseTagBlock(_chunkText).tags`.
- Ext tags from the iframe's `<ark-ext-tags>` children, scraped
  on iframe load via JS bridge (no new Go bridge needed — the
  rendering already embeds the data).

Each row uses the same `<sl-input>` widget shape as proposal
widgets, readonly + grayed. Two convenience actions per row:

- **`rem` checkbox** — loads a pre-filled *remove* widget into
  the proposal stack (tag name, disposition matching the row's
  kind). The user fires it there — destructive operations stay
  behind an explicit fire click, never fire from the row itself.
- **Clicking the row** — loads a pre-filled *replace* widget into
  the proposal stack (tag, current value, disposition matching
  the row) ready for the user to revise and fire.

Ext rows carry a small badge with the externalfile path.

### Tag scores collapsible

Renamed from "tag suggestions". One-line rows showing tag-name
candidates from `mcp.suggestTagNames(chunkID, K)` with their
cosine scores. Click affordances (pin / focus) carry over from
the prior slice unchanged.

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

- **Sweep button** — fire-and-forget. Calls
  `mcp.sweepHotCorrelationsAsync()` (non-blocking) and subscribes
  to `tmp://sweep/hot-correlations.md` via `mcp.subscribe` for
  the `@sweep-status` and `@sweep-progress` tags. The button is
  disabled while a sweep is in flight; the header shows a live
  progress indicator driven by the subscription callbacks.
- **Sweep result** — on terminal `@sweep-status: completed` (or
  `errored`), Lua reads the doc body via `mcp.tmp_get` for the
  final summary (duration, tagsRebuilt, tagsTouched). The
  embedding-unavailable degraded reply is surfaced as a friendly
  one-line message instead.

The retrofit from blocking to async unblocks the UI while a sweep
is running and lets the header reflect mid-flight progress.

### Proposal supply chain (Find Connections removed)

The Find Connections orchestration (the ark-connections sidecar:
`mcp.findConnections` → `tmp://connections/<id>.md` → external
`ark connections --wait` consumer) is **removed** — the sidecar is
retired thinking, its role replaced by the luhmann secretary pool.
The workshop no longer offers a Find Connections button or a
sidecar-sourced proposals panel.

The forge's future proposal source is the **candidate ledger
itself**: agents author `@ext-candidate`s cross-session, and the
forge becomes the human review console that accepts / rejects /
edits them. That review-queue surface lands with PENDING #9 (which
also decides how pool-sourced themes reach the forge); it is out
of scope for this slice.

### Persistence

Pinned chunks and the last-viewed timestamp persist across server
restarts in `~/.ark/apps/curation/state.json`. The directory is
created on first write. State is loaded on view init and rewritten
on every mutation.

### What's NOT in scope (this slice)

- **Candidate review queue** — presenting the ledger's
  agent-authored `@ext-candidate`s for review (accept / reject /
  edit) is PENDING #9 territory. The one-shot widget path never
  creates an unaccepted candidate, so this slice has no reject
  affordance either.
- **Rejected-proposals view** — PENDING #12.
- **Per-line annotation** — authoring a tag *about* a specific
  line mid-chunk is deferred; internal placement follows the
  machinery's stencil (under the chunk's structural opener).
- **Entry 3 (chunk → similar chunks)** — needs a Librarian
  backend (`SimilarChunks(chunkID, k)`) that doesn't exist yet.
- **Tag conflict explorer** — `mcp.tagPairConflict(tagA, tagB)`
  is bridged but the UX is deferred.
- **Vocabulary gaps** (bottom-K against all tag defs) — bottom-K
  inversion of the same engine.
- **Tag-name completion** — typed input only, no autocomplete.
- **Status badge on the bottom-bar button** — the badge with
  pinned-count + new-count lives in the MCP shell, not this app.
- **One-click "convert ext to inline"** — the user removes the
  ext and re-adds inline; a one-click convert action is deferred.
- **Mirror-file compaction** — multi-tag lines targeting the
  same chunk stay one-tag-per-line in v1.

## Luhmann View

A terminal on the ark-hosted Luhmann session — the in-app
counterpart of `ark luhmann attach`, following the worked example
at `install/html/luhmann-terminal.html` (design:
`design/ui-luhmann-terminal-page.md`).

- **Layout** — flex column: one-line status strip on top,
  `<luhmann-terminal>` filling the rest (`flex: 1; min-height: 0` —
  the element sizes itself to its box via ResizeObserver, R3154).
  The element's module loads from `/luhmann-terminal-element.js`
  (idempotent: skip if the custom element is already registered).
- **Status strip** — a lamp dot plus text, driven entirely by the
  element's bubbling `luhmann-terminal-status` event (R3159; listen
  on `document` — the strip is the terminal's sibling, so the event
  never bubbles through it). States: `connecting` (dim), `connected`
  (success, shows session id), `waiting` (warning, shows attempt),
  `asleep` (muted, "no session"). The event is pushed to Lua via the
  JS→Lua bridge so Lua state drives the strip and button visibility.
- **Launch button** — visible only in `asleep` state. Opens a
  confirmation dialog that says plainly this starts a paid Claude
  session; on confirm, calls `sys.luhmannLaunch()`. Until the Go
  side lands (PENDING #62), init.lua defines a stub that only
  notifies the user to run `ark luhmann launch`. A user click is an
  explicit action, so R3114 (ark never *proactively* starts a paid
  session) is respected.
- **Wake on CLI launch** — the element stops probing once it
  reports `asleep`, so a session launched from the CLI while the
  view is showing is invisible to it. `wake()` re-mounts the element
  (bumps a mount nonce), forcing a fresh probe. The Go-side push
  that calls `wake()` on launch is PENDING #62; until then the user
  can re-enter the view (showing the view always re-mounts).
- **Detach semantics** — leaving the view unmounts the element,
  which closes the socket; the hosted session survives (R3121,
  R3156). No terminal state is kept in Lua.

## MCP Shell Integration

Four ark buttons in the MCP bottom bar:
- **Archive icon** — displays ark in searching mode
- **Envelope icon** — displays ark in messaging mode
- **Multi-tag icon** — displays ark in curation mode
- **Terminal icon** — displays ark in luhmann mode

Each button sets the view mode on the ark instance before calling
`mcp:display("ark")`.
