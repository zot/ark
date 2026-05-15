# Curation
**Requirements:** R2355, R2356, R2357, R2358, R2359

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
- pinnedSnapshot(): copy of the pinned slice under the mutex. Use
  this from goroutines outside the Lua executor. (R2357)
- refreshLuaTable(L): rebuild the `pinned` field on the `sys.curation`
  Lua table to match the Go slice. Called automatically by `pin`;
  exposed for any future mutation paths. (R2357)

## Collaborators
- Server: holds the `*Curation` field; registers the `sys` global
  Lua table and sets `Curation.luaTable` during
  `registerLuaFunctions`.
- flib.Runtime: the closure-actor that runs `pin` (and any future
  mutator) on the Lua executor goroutine via WithLua.

## Sequences
(none yet)
