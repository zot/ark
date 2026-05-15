# Curation
**Requirements:** R2355, R2356, R2357, R2358, R2359, R2360, R2361, R2362, R2363

Server-owned in-memory state for the curation workshop's pinned
chunks. Canonical store is the Go slice; the Lua table at
`sys.curation` is a mirror, refreshed inside the Lua executor
closure that mutates the Go slice. Frictionless observes the
mirror through its standard variable-change detection.

## Knows
- pinned: []PinnedChunk — canonical store; the workshop's pinned set
  in append-on-top order. Newest first.
- luaTable: *lua.LTable — `sys.curation` Lua mirror; the `pinned`
  field of this table is rebuilt on every mutation.
- entryTables: map[uint64]*lua.LTable — per-chunkID cache of entry
  sub-tables under `pinned`. Refresh mutates each cached table's
  fields in place; survivors keep their Lua identity so
  Frictionless's `view.baseItem == item` reuse rule holds across
  mutations. (R2362)
- mu: sync.Mutex — guards `pinned` for concurrent reads outside the
  Lua executor (e.g. HTTP handlers reading a snapshot without
  entering the executor).

## Does
- newCuration(): construct an empty Curation. (R2355)
- pin(L, chunkID, fileID, path): add or move-to-top. Always-add
  never-flip. Preserves existing FileID/Path on a re-pin when the
  caller passes zero/empty. Refreshes the Lua mirror via
  refreshLuaTable in the same call. Must be called from inside the
  Lua executor (typically a Go function registered on a Lua table,
  or a body passed to `flib.Runtime.WithLua`). (R2358)
- dismiss(L, chunkID): remove the entry whose ChunkID matches, if
  any. Silent no-op when the chunkID is not pinned. Drops the
  cached entry table for the removed chunkID. Refreshes the Lua
  mirror in the same tick. Lua-executor-only. (R2360)
- sweepOlder(L): drop every entry except the topmost. Silent no-op
  for ≤1 pin. Drops cached entry tables for the removed chunkIDs.
  Refreshes the Lua mirror in the same tick. Lua-executor-only.
  (R2361)
- pinnedSnapshot(): copy of the pinned slice under the mutex. Use
  this from goroutines outside the Lua executor. (R2357)
- refreshLuaTable(L): rebuild the `pinned` field on the `sys.curation`
  Lua table to match the Go slice. Reuses cached entry sub-tables
  by ChunkID (mutating fields in place) and allocates new ones only
  for newly pinned ChunkIDs. Drops cache entries for ChunkIDs that
  left the slice. Called automatically by `pin`, `dismiss`, and
  `sweepOlder`. (R2357, R2362)

## Collaborators
- Server: holds the `*Curation` field; registers the `sys` global
  Lua table and sets `Curation.luaTable` during
  `registerLuaFunctions`. Also serves `POST /curation/pin`, which
  enters the Lua executor via `srv.uiRuntime.WithLua` and calls
  `Curation.pin`. (R2363)
- flib.Runtime: the closure-actor that runs `pin`, `dismiss`, and
  `sweepOlder` on the Lua executor goroutine via WithLua.

## Sequences
(none yet)
