# App Source Tree Support

Go-side support for the Frictionless ark app's source tree UI. The
app currently shells out to `ark config show-why` per node and `ls`
per directory — hundreds of subprocesses for a single source. These
functions replace that with in-process calls on the `mcp` table.

Language: Go. Environment: Linux (primary), macOS.

## Directory listing — `mcp:listSource(sourcePath, prototype)`

Returns entries for one directory level within a configured source,
with classification (included/excluded/unresolved) already applied.
Replaces `listDir()` + per-node `applyWhy()` in the Lua app.

### Arguments

- `sourcePath` — absolute path to list. Must be within a configured
  source directory.
- `prototype` — optional Lua prototype table (e.g. `Node`). When
  non-nil, the Go code checks once whether the prototype itself
  (rawget, not inherited) has a `new` method. If present, calls
  `prototype:new(table)` which handles instance creation with
  custom init. If not, calls `session:create(prototype, table)`
  directly for mutation tracking. Falls back to bare metatable
  wiring if session:create is unavailable. When nil, returns bare
  Lua tables.

### Returned table

A Lua array of tables, each with:

- `name` — filename (basename only)
- `relPath` — path relative to the source root
- `fullPath` — absolute path
- `isDir` — boolean
- `state` — "included", "excluded", or "unresolved"
- `whyPatterns` — comma-separated matching patterns
- `whySources` — comma-separated pattern sources
- `whyConflict` — boolean, true when include and exclude both match
- `isMissing` — boolean, true when path is in the index but not on disk
- `hasIgnoreFile` — boolean (directories only), true when a
  .gitignore or .arkignore exists in this directory

Entries are sorted: directories first, then alphabetically by name.

### Missing files

Files that are in the index but no longer on disk (from the DB's
missing list) are included in the results with `isMissing = true`.
Only missing files at the listed directory level are included —
not descendants.

### Classification

Each entry is classified against the source's config using the same
logic as `Config.ShowWhy`: global and per-source include/exclude
patterns, plus .gitignore/.arkignore patterns. This happens
in-process — no subprocess, no CLI round-trip.

### One level only

The function lists a single directory level. The Lua app calls it
once for root nodes, then again on expand for child directories.
This matches the existing lazy-expand UX.
