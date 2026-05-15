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
