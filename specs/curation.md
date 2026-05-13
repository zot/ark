# Curation Workshop (Go-owned state)

The curation workshop's pinned-chunks state is owned by Go, not Lua.
A `Curation` struct on the Server holds the canonical `Pinned`
slice; Lua sees it through `ark.curation.pinned`, a table mirror
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

## `ark` global Lua table

The Server registers a global `ark` Lua table alongside the
existing `mcp` table during `registerLuaFunctions`. `ark.curation`
is a subtable; `ark.curation.pinned` is the mirror.

## Mutators

- `ark.curation.pin(chunkID, fileID, path)` — add or move-to-top.
  Always-add never-flip: an already-pinned chunkID is moved to
  the top of the list with `PinnedAt = now`. `fileID == 0` or
  `path == ""` preserves the existing values on a re-pin.

## PinnedChunk fields

Go-side: `ChunkID`, `FileID` (uint64), `Path` (string),
`PinnedAt` (Unix seconds, int64).
Lua mirror: `chunkID`, `fileID`, `path`, `pinnedAt`.
