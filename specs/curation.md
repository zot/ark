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
