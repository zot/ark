# Sequence: Session Search

Covers how search commands flow through sessions from all three sources.

## Participants
- CLI / HTTP Handler / Lua Function
- Server
- Session (actor)
- SearchCmd
- Searcher
- microfts2.ChunkCache

## Flow: Search with Session (any source)

```
Source ──> construct SearchCmd{Query, Opts, Session: "ui"}
            │
            ├──> Server.GetOrCreateSession("ui")
            │     ├── lock sessions map
            │     ├── if not found: create Session, start loop()
            │     └── return Session
            │
            └──> Session.RunSearch(cmd.Query, func(cache) {
                   return cmd.Execute(db, cache)
                 })
                  │
                  ├──> actor receives closure
                  │
                  ├──> prefix test:
                  │     ├── lastQuery="" → first search, keep cache
                  │     ├── "hel" → "hell" → prefix, keep cache
                  │     ├── "hello" → "world" → not prefix, evict
                  │     └── "hello w" → "hello" → lastQuery is prefix
                  │         of new query (backspace), keep cache
                  │
                  ├──> ensureCache() — create if nil
                  │
                  ├──> cmd.Execute(db, cache)
                  │     ├── Searcher.SearchGrouped(query, opts)
                  │     │    └── (existing flow from seq-search.md)
                  │     │
                  │     └── FillChunks uses session cache
                  │          instead of creating per-query cache
                  │
                  ├──> update lastQuery = cmd.Query
                  ├──> reset TTL timer
                  │
                  └──> return results to caller (via channel)
```

## Flow: Search without Session

```
Source ──> construct SearchCmd{Query, Opts, Session: ""}
            │
            └──> cmd.ExecuteDirect(db)
                  │
                  └──> cmd.Execute(db, nil)
                        │
                        └──> Searcher methods create per-query
                             ChunkCache as today (no change)
```

## Flow: TTL Expiry

```
Session.loop() ──> timer fires
                    │
                    └──> evictCache()
                          ├── cache = nil
                          └── lastQuery = ""
                          (session stays alive — next command
                           will lazily create fresh cache)
```

## Flow: CLI with --session

```
CLI ──> cmdSearch parses --session NAME
         │
         ├──> --session present → force proxy to server
         │     (server must be running, error if not)
         │
         └──> proxy searchRequest{..., Session: NAME}
               │
               └──> Server.handleSearch receives session field
                     └──> (same as "Search with Session" above)
```

## Flow: Lua search_grouped with session

```
Lua ──> mcp.search_grouped(query, {session = "ui", ...})
         │
         ├──> construct SearchCmd from args
         │
         └──> Session.RunSearch(query, func(cache) {...})
               └──> (same as "Search with Session" above)
```

## Notes

- The prefix test is bidirectional: "hello" → "hel" (backspace)
  keeps the cache because the old query is a prefix of... wait, no.
  "hello" → "hel" means the new query "hel" is a prefix of old
  query "hello". The cache from "hello" contains a superset of
  what "hel" would read, so keeping it is safe.
- Cache eviction is cheap — just nil the pointer. Creation is lazy.
- Sessions are never destroyed, only their caches are evicted.
  The session map grows by the number of distinct session names,
  which is bounded (one per UI instance, rare CLI usage).
