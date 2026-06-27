# SearchCmd
**Requirements:** R649, R650, R651, R652, R653, R1936, R1940

Search-command responsibilities for a single search operation:
capture the parameters, be constructible by CLI / HTTP handler / Lua,
and run either directly or within a named session.

**Realization (no distinct struct):** the designed `SearchCmd` wrapper
with `Execute`/`ExecuteDirect` methods was **not built**. Its
responsibilities are realized by `ark.SearchOpts` (`search.go`), which
each of the three sources constructs and dispatches **inline** (the
`SearchGrouped`/`SearchCombined`/`SearchSplit`/`SearchMulti` switch lives
in `runSearch`, `handleSearch`, and the Lua `search_grouped`, not in a
command object). "Submit to a session" is the `Cache` threading
(R1139/R1140): a non-nil `SearchOpts.Cache` (owned by the session actor)
makes `FillChunks` reuse the session cache; nil = per-query cache =
direct run. `SearchOpts` converts to microfts2's `searchConfig` via the
`With*` options assembled in `defaultSearchOpts`.

## Knows
- Query: string — the search query (passed alongside SearchOpts)
- Opts: SearchOpts — all search options (the param-capturing struct;
  carries Session name and the optional session Cache)
- Session: string — optional session name (empty = no session); on
  SearchOpts as the `Session` field

## Does
- (realized) Construct `SearchOpts` and dispatch inline at each source —
  CLI `runSearch`, HTTP `handleSearch`, Lua `search_grouped` — selecting
  SearchGrouped/SearchCombined/SearchSplit/SearchMulti from the opts.
- (realized) Direct vs session run is the `SearchOpts.Cache` distinction
  (R1139/R1140): nil cache = per-query (direct); session-owned cache =
  session-scoped. No separate `Execute`/`ExecuteDirect` methods exist.

## Collaborators
- Searcher: performs the actual search via its existing methods
- Session: when a session name is set, the session actor supplies the
  shared ChunkCache threaded through SearchOpts.Cache
- microfts2.ChunkCache: passed through from the session or nil
- microfts2 searchConfig: SearchOpts becomes With* options (defaultSearchOpts)
