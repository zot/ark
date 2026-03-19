# Sessions

## Problem

Ark commands arrive from three sources — CLI, HTTP server, and
Lua (UI) — each dispatching differently to the same DB methods.
There is no shared command structure and no way to carry state
across related commands.

The immediate pain: interactive search in the UI fires a query on
every keystroke. Each query builds a fresh ChunkCache (per-query
cache in microfts2), re-reading and re-chunking files that were
almost certainly in the previous result set. Successive keystrokes
produce overlapping queries, but there is no mechanism to reuse
cached data between them.

## Session

A session is a named, server-side closure actor that carries state
across commands. The initial state is a ChunkCache; the design
accommodates future state without changing the model.

- Sessions are identified by name and autocreated on first use.
- A session receives commands as closures and runs them serially
  in its actor loop, with the cache available.
- After each command, the session resets a TTL timer. When the
  timer fires, a closure is sent into the actor that evicts the
  cache. This keeps the cache alive during bursts of activity
  without requiring explicit lifecycle management.
- The TTL is configured in ark.toml (`session_ttl`, duration string)
  and defaults to 30 seconds. A long TTL is safe because the prefix
  test evicts the cache as soon as the query diverges — the TTL
  only covers the "user stopped typing" case.
- For search commands: if the new query is not a prefix of the
  previous query, the cache is evicted before running the search.
  This is a simple heuristic — the user is refining, not starting
  over, as long as they're appending characters.

## Command Object (Search Only)

A SearchCmd struct captures the parameters for a search operation.
All three sources construct a SearchCmd and either run it directly
(no session) or submit it to a named session.

This is an incremental step — not every operation needs a command
object. Search is the one that benefits from session-scoped caching.
Other commands continue to dispatch as they do today. As more
commands benefit from sessions, they get their own command structs.

## Source Integration

### CLI

`ark search` gains a `--session NAME` flag. When present, the
search is proxied to the server (implies server must be running)
and executed within the named session. Without `--session`, search
works as today — direct DB call or server proxy, no session, no
cross-query cache.

### HTTP Server

Search handler accepts an optional `session` field in the JSON
request body. If present, the server looks up (or creates) the
named session and submits the SearchCmd to it. If absent, the
search runs immediately with no session — same as today.

### Lua (UI)

`mcp.search_grouped` accepts an optional `session` field in its
opts table. The UI app passes a fixed session name (e.g. "ui")
for interactive search, so all keystrokes share one cache. The
Lua function constructs a SearchCmd and submits it to the session.

## What a Session Is Not

- Not an LRU or bounded cache. The cache is all-or-nothing: alive
  during a burst, evicted on TTL or query divergence.
- Not user identity or authentication. Single-user system.
- Not required. Every operation works without a session. Sessions
  are an optimization for interactive use.
