# Session
**Requirements:** R640, R641, R642, R643, R644, R645, R646, R647, R648

Named closure actor that carries a ChunkCache across commands.
Server-side only. Autocreated on first use, evicted on TTL expiry
or query divergence.

## Knows
- name: string — session identifier
- ch: chan func(*sessionState) — actor message channel
- state: *sessionState — cache, last query, timer (owned by actor goroutine)

### sessionState (actor-private)
- cache: *microfts2.ChunkCache — reused across commands
- lastQuery: string — previous search query (for prefix test)
- timer: *time.Timer — TTL countdown, reset on each command
- ttl: time.Duration — from ark.toml session_ttl (default 30s)
- db: *DB — needed to create fresh ChunkCache

## Does
- Run(fn func(*microfts2.ChunkCache) error) error: submit a closure
  to the actor and wait for the result. Resets TTL timer after
  execution.
- RunSearch(query string, fn func(*microfts2.ChunkCache) error) error:
  like Run but applies the prefix test first — if query is not a
  prefix of lastQuery or lastQuery is not a prefix of query, evict
  the cache before running. Updates lastQuery after execution.
- loop(): actor goroutine. Reads from ch, executes closures against
  state. Handles TTL expiry by evicting the cache (not destroying
  the session — session stays alive, just with a nil cache).
- evictCache(): set cache to nil, clear lastQuery. Next command
  will lazily create a fresh cache.
- ensureCache(): if cache is nil, create via db.fts.NewChunkCache().

## Collaborators
- microfts2.ChunkCache: the cached state
- DB: needed to create fresh ChunkCache instances
- Server: owns the session map, creates sessions on demand
