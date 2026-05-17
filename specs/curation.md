# Curation Workshop (Go-owned state)

The curation workshop's pinned-chunks state is owned by Go, not Lua.
A `Curation` struct on the Server holds the canonical `Pinned`
slice; Lua sees it through `sys.curation.pinned`, a table mirror
refreshed inside the same Lua-executor closure that mutates the Go
slice. This lets Frictionless's variable-change detection observe
mutations naturally — Frictionless watches Lua tables, and the
mirror reflects the Go state on every change.

## Why Go-owned

Earlier iterations kept pinned state in Lua (`Curation.pinned[]`)
with its own `_persist()` call. The Go-owned shape is cleaner:

1. Single canonical type (`PinnedChunk` Go struct). HTTP endpoints,
   CLI, and other Go-side callers operate on a typed slice rather
   than reaching into Lua.
2. Mutation serialization comes for free from the Lua executor
   goroutine — `flib.Runtime.WithLua` is the closure actor that
   protects the Go slice.
3. Frictionless still observes changes because we refresh the Lua
   mirror inside the same `WithLua` closure that mutates the Go
   slice. One atomic transition per mutation.

## `sys` global Lua table

The Server registers a global `sys` Lua table alongside the
existing `mcp` table during `registerLuaFunctions`. `sys.curation`
is a subtable; `sys.curation.pinned` is the mirror.

## Mutators

- `sys.curation.pin(chunkID, fileID, path)` — add or move-to-top.
  Always-add never-flip: an already-pinned chunkID is moved to
  the top of the list with `PinnedAt = now`. `fileID == 0` or
  `path == ""` preserves the existing values on a re-pin.
- `sys.curation.dismiss(chunkID)` — remove the entry whose
  ChunkID matches, if any. Silent no-op when the chunkID is not
  pinned. Refreshes the Lua mirror in the same tick so
  Frictionless observes the removal.
- `sys.curation.sweepOlder()` — drop every entry except the
  topmost (newest). Silent no-op when zero or one entry is
  pinned. Refreshes the Lua mirror in the same tick.

## PinnedChunk fields

Go-side: `ChunkID`, `FileID` (uint64), `Path` (string),
`PinnedAt` (Unix seconds, int64).
Lua mirror: `chunkID`, `fileID`, `path`, `pinnedAt`.

## Entry-table identity in the Lua mirror

`refreshLuaTable` rebuilds the `pinned` field on the
`sys.curation` Lua table after every mutation, but the entry
sub-tables for surviving ChunkIDs **keep their Lua identity
across refreshes**. The Curation struct caches the entry
`*lua.LTable` per ChunkID; on refresh it mutates each cached
table's fields in place, allocates new tables only for newly
pinned ChunkIDs, and drops cache entries for ChunkIDs that
left the slice.

Why this matters: Frictionless's `ViewList` reuses a per-item
presenter (e.g. `Ark.PinnedChunk`) only when
`view.baseItem == item` (Lua table identity). If
`refreshLuaTable` allocated a fresh entry table for every
survivor, every `dismiss` / `sweepOlder` / re-pin would
recreate every presenter — wiping per-pin reactive UI state
(`_suggestionsLoaded`, error flags, etc.).

## HTTP Curate-pin endpoint

A POST endpoint accepts a curate request from web-component
contexts that can't reach Lua directly (chunk-row buttons in
`<ark-search>`, content-view iframes, future PDF chunks). It
runs inside `srv.uiRuntime.WithLua` so the Go mutator and the
Lua mirror refresh share a single tick.

`POST /curation/pin` — request body JSON:

```json
{ "chunkID": 4711, "fileID": 88, "path": "knowledge/notes.md" }
```

- `chunkID` is required; `fileID` and `path` are optional and
  follow the same preserve-on-zero rule as `sys.curation.pin`.
- Response: `200 OK` with empty body on success;
  `400 Bad Request` for malformed JSON or missing chunkID;
  `503 Service Unavailable` when the Lua runtime is not wired.

The endpoint is the Go-side substrate for the Curate buttons
in `apps/ark/<ark-search>` chunk rows and the content-view
iframes. It is not the Lua app's path — Lua callers go through
`sys.curation.pin` directly.

`POST /curation/dismiss` — request body JSON:

```json
{ "chunkID": 4711 }
```

- `chunkID` is required.
- Response: `200 OK` with empty body on success (whether or not
  the chunkID was actually pinned — silent no-op matches the
  `sys.curation.dismiss` Lua semantic); `400 Bad Request` for
  malformed JSON or missing chunkID; `503 Service Unavailable`
  when the Lua runtime is not wired.
- Mirror of `POST /curation/pin`. Used by the pin button's
  toggle path: a currently-pinned chunk's button calls
  `dismiss` instead of `pin`.

`GET /curation/pinned` — response body JSON:

```json
{ "chunkIDs": [4711, 4712, 4715] }
```

- The chunk IDs of every currently pinned entry, in the same
  newest-first order the in-memory slice keeps.
- The endpoint is read-only — no Lua-executor entry needed; it
  reads `sys.curation.pinned` through the Go-side snapshot
  helper.
- Used by web-component consumers (chunk-row pin buttons,
  content-view inline JS) to display the pinned vs unpinned
  state on initial render. Live updates within the same page
  are tracked locally (each button knows its own state after
  it's clicked). Cross-page synchrony (workshop pins → iframe
  state) is not provided in v1 — re-loading the iframe picks
  up the change.

## Curate buttons (web-component consumers)

The content view (`/content/PATH`) renders each chunk in a
`<div class="ark-chunk">` wrapper. Two HTML data attributes
carry the identifiers the pin button needs:

```html
<div class="ark-chunk" data-range="42-47" data-chunkid="4711" data-fileid="88">
  ...
</div>
```

- `data-chunkid` — chunk's stable uint64 ID, resolved via
  `srv.db.ChunkIDsForPath(path)` (already called in the
  current rendering path for `@ext` tag-block lookup).
- `data-fileid` — the file's uint64 ID, resolved once per
  file from microfts2's `CheckFile` / `FileInfoByID`.
- `data-range` — unchanged, already present.

Both `content-markdown.html` and `content-plain.html` render
the chunk div and include a small inline `<script>` that adds
a pin-icon button to each chunk on `DOMContentLoaded`,
positioned at the chunk's upper-left corner. The script:

1. Fetches `GET /curation/pinned` once on load to seed
   pinned-state. Failures fall back to "everything unpinned"
   (display-only feature — should not break the page).
2. For each `<div class="ark-chunk">`, prepends a `<button
   class="ark-curate-pin">` with `aria-pressed="true"` when
   the chunk's `data-chunkid` is in the pinned set, otherwise
   `aria-pressed="false"`. CSS distinguishes the two states
   with the same icon glyph (filled vs outlined).
3. On click:
   - If `aria-pressed="false"`: POST `/curation/pin` with
     `{chunkID, fileID, path}` from the data attributes plus
     the page's path. On success, flip to `aria-pressed="true"`.
   - If `aria-pressed="true"`: POST `/curation/dismiss` with
     `{chunkID}`. On success, flip back.
   - Non-2xx responses leave the visual state unchanged and
     log to the console.

Path resolution: the inline JS reads the file path from the
URL — `location.pathname` after stripping the `/content/`
prefix, decoded. The same convention `<ark-search>` already
uses for navigation.

Icon: a single SVG glyph rendered inline (no sprite file).
Filled vs outlined is a CSS class toggle driven by
`aria-pressed`. Width/height ~16 px, color follows the
existing `--term-accent` / `--term-text-dim` theme variables.
The button is absolutely positioned at the chunk div's
upper-left with a small inset so it overlays the chunk's own
content margin without intruding on the text.

### PDF chunk pin overlays

PDF chunks render inside a single page-aggregated `<pdf-chunk>`
element (one per page covering 0,0,pw,ph) rather than as
individual `<div class="ark-chunk">` wrappers. Each chunk on the
page carries a `rect` attribute in page coordinates; the
`<pdf-chunk>` web component already positions `<ark-tag>`
overlays at those rects. The pin button piggybacks on the same
machinery.

For every chunk with a `rect`, the server emits an
`<ark-curate-region>` overlay child inside the `<pdf-chunk>`:

```html
<ark-curate-region chunkid="4711" fileid="88" rect="120,400,360,140">
</ark-curate-region>
```

- `chunkid` / `fileid` — plain HTML attributes (matching the
  `<pdf-chunk>` attribute convention rather than `data-`).
- `rect` — `x,y,w,h` in PDF page coordinates, same encoding as
  `<ark-tag rect="...">`.

`pdf-chunk-element.ts` gains a `positionRegions` pass run from
`render()` alongside `positionHitRegions`. It iterates
`:scope > ark-curate-region[rect]` children and converts the
page-coord rect to CSS pixels using the same formula
`positionHitRegions` already uses for ark-tag overlays:

```
leftPx   = (regionRect.x - chunkRect.x) * cssScale
topPx    = (chunkRect.y + chunkRect.h - regionRect.y - regionRect.h) * cssScale
widthPx  = regionRect.w  * cssScale
heightPx = regionRect.h  * cssScale
```

The region is `position: absolute` with `pointer-events: auto`,
a transparent default border, and a hover state that reveals
the chunk's outline so the user can see chunk boundaries on the
page. The pin button sits at the region's upper-left corner —
the same CSS `.ark-curate-pin` rule used for the
`<div class="ark-chunk">` case applies inside `<ark-curate-region>`
unchanged.

The inline pin-injection script extends its selector to match
both element types:

```js
const elems = document.querySelectorAll(
  'div.ark-chunk[data-chunkid], ark-curate-region[chunkid]'
);
```

When iterating, the script reads identifiers via
`el.dataset.chunkid` for the div case or
`el.getAttribute('chunkid')` for the region case; the rest of
the install/toggle logic is shared.

PDF salvage chunks (no rect — already wrapped in
`<div class="ark-chunk">` by `renderPdfChunksByPage`'s salvage
fallback) get the standard pin via the existing div selector.

### Hover outlines for chunk boundaries

Both `<div class="ark-chunk">` and `<ark-curate-region>` show a
faint dashed outline on `:hover` so the user can see chunk
boundaries when they want to. Default state is invisible
(transparent border) so the reading view stays clean.

## Persistence

The pinned-chunks list survives server restart via
`curation.toml`, co-located with `ark.toml` in the database
directory.

### File location

`filepath.Join(dbPath, "curation.toml")`. The file is excluded
from indexing — `arkSourceIncludePatterns` does not list it, so
the watcher / scanner skip it.

### File format

TOML, one `[[pinned]]` table per entry, in canonical
newest-first order (the same order the in-memory slice keeps).
A top-level `version = 1` field gates future schema changes.

```toml
version = 1

[[pinned]]
chunkID = 4711
fileID = 88
path = "knowledge/notes.md"
pinnedAt = 1715800000

[[pinned]]
chunkID = 8123
fileID = 92
path = "specs/curation.md"
pinnedAt = 1715798200
```

Field names match the JSON tags on `PinnedChunk`
(`chunkID`/`fileID`/`path`/`pinnedAt`). The `pinnedAt` value is
Unix seconds, matching the in-memory representation.

### Load

`Curation.Load(path)` runs during `Server.New` after
`newCuration()` and before `registerLuaFunctions` — the Lua
mirror is populated from disk before any Lua code sees it.

Behavior:
- Missing file → silent no-op (first run, or user wiped the
  state). Pinned list starts empty.
- Malformed TOML, unknown version, or unparseable entries → log
  the error, leave the in-memory slice empty. The server keeps
  running. Subsequent mutations overwrite the broken file on
  next save.
- Entries reference `chunkID`/`fileID` values that may or may
  not still exist in the current DB. The workshop's chunk-
  resolution path is responsible for handling stale references
  (e.g., displaying "chunk no longer indexed" when a pinned
  entry can't be reached). Load does not validate against the
  DB.

### Save

After every `pin`, `dismiss`, and `sweepOlder` call — inside the
same `WithLua` closure that mutates the Go slice and refreshes
the Lua mirror. One atomic write per mutation.

Implementation: write to `curation.toml.tmp` then rename over
`curation.toml`. This is sufficient atomicity for a single-file
state — readers (the next process startup) see either the old
file or the new, never a partial write.

Failure handling:
- Disk full / permission denied → log the error, keep in-memory
  state as-is. The next mutation's save will retry; if the
  server crashes before then, that single mutation is lost.
- The in-memory state is the source of truth during the session;
  the file is a checkpoint. Save failures don't roll back the
  Go-side mutation.

### Not used

- No periodic save — every mutation saves.
- No debounce — pinned-list mutations are user-initiated (pin
  button, dismiss button, sweep), so the rate is human-scale
  (< 1/sec under all realistic usage). The write cost is
  negligible.
- No shutdown save — every mutation already saved.

## Lua bridge: defined-tags listing

`mcp.definedTags()` — returns an array of `{tag, description}`
tables drawn from the same store as the HTTP `POST /tags/defs`
handler. Read-only; runs under `Sync`. Sorted by tag name
ascending; duplicate `tag` entries (from multiple D records)
are deduplicated keeping the first non-empty description seen.

```lua
local defs, err = mcp.definedTags()
-- defs = { { tag = "decision", description = "…" }, … }
```

Errors follow gopher-lua `(nil, errstring)` convention. Empty
result is an empty Lua table, never nil. The bridge powers the
curation view's tag picker (replaces the text-parse of
`io.popen("ark tag defs")`).
