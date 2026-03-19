# SearchCmd
**Requirements:** R649, R650, R651, R652, R653

Command object for search operations. Constructed by CLI, HTTP
handler, or Lua function. Runs directly or within a session.

## Knows
- Query: string — the search query
- Opts: SearchOpts — all search options (existing struct)
- Session: string — optional session name (empty = no session)

## Does
- Execute(db *DB, cache *microfts2.ChunkCache) (any, error):
  run the search using the provided cache (may be nil for
  no-session path). Dispatches to SearchGrouped, SearchCombined,
  SearchSplit, or SearchMulti based on Opts, same logic as today.
  When cache is non-nil, passes it through so FillChunks uses
  the session cache instead of creating a per-query one.
- ExecuteDirect(db *DB) (any, error): convenience — calls
  Execute with nil cache (per-query cache behavior, same as today).

## Collaborators
- Searcher: delegates actual search to existing methods
- Session: when Session name is set, submitted to session actor
- microfts2.ChunkCache: passed through from session or nil
