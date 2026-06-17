# Temporary Documents

Ephemeral, in-memory documents that live alongside persistent files.
A `tmp://` path prefix is the only distinction — every existing
command, filter, and search works with them seamlessly.

## What they are

A temporary document is content indexed in memory without writing
to disk. It participates in search, tag extraction, file listing,
and filtering exactly like a persistent file. The `tmp://` prefix
in the path is how users and code distinguish them.

Names follow the scheme `tmp://human-readable-name`
(e.g. `tmp://scoring-notes`, `tmp://agent-blackboard`).

Temporary documents exist for the lifetime of the running server.
Server stops, they're gone. There is no persistence, no recovery.
If content is worth keeping, write a real file.

## How they work

microfts2 provides the in-memory overlay: `AddTmpFile`,
`UpdateTmpFile`, `RemoveTmpFile`. The overlay holds trigrams,
tokens, and content in RAM. Search merges overlay results with
index results transparently. Chunk retrieval reads from stored
in-memory content instead of disk. See microfts2's crc-Overlay.md
for the full API.

Ark's job is to:
- Expose tmp:// operations through the existing command surface
- Extract and track tags from tmp:// content
- Handle the search proxy optimization (local vs server)
- Register Lua functions for UI/agent use

## Seamless integration

Existing commands handle `tmp://` paths naturally:

- `ark add tmp://my-notes` — indexes content in memory via
  `AddTmpFile`. Content comes from one of three sources:
  - `--content "text"` — inline content from the flag value
  - `--from-file path` — read content from a file on disk
  - stdin (default) — read until EOF
- `ark add --append tmp://my-notes` — appends content to an
  existing tmp:// document without replacing it. Creates the
  document if it doesn't exist. New chunks are created from the
  appended content. Used by agent DMs, error reporters, and any
  accumulative tmp:// pattern.
- `ark remove tmp://my-notes` — removes from the overlay
- `ark files` — lists tmp:// files alongside persistent files
- `ark search` — includes tmp:// results by default
- `ark search --no-tmp` — excludes tmp:// results
- `ark tag files @sometag` — includes tmp:// files carrying the tag
- `--filter-files`, `--exclude-files` — glob patterns match tmp://
  paths just like any other path

There is no `--tmp` flag to include them — they're included by
default. `--no-tmp` is the opt-out.

## Search proxy optimization

CLI search normally runs locally against the index (the mmap shares
pages with the server). But local search can't see tmp:// documents
because they only exist in the server's memory.

The optimization: when the CLI searches without `--session`, it
asks the server "do you have any tmp files?" via a search request
with an `onlyIfTmp` flag. The server checks `HasTmp()`:

- If no tmp files exist: return a specific HTTP status (no body).
  The CLI proceeds with a local search — no proxy cost, same
  results.
- If tmp files exist: run the search server-side and return
  results. The CLI uses these instead of searching locally.

This means tmp:// documents are invisible to local search (by
necessity — they're not in the index) but the CLI transparently
proxies when needed, and avoids the proxy cost when there are
no tmp docs.

`--no-tmp` on the CLI skips this check entirely and always
searches locally. `--session` always proxies (as before).

## microfts2 search option

`WithNoTmp()` is a microfts2 search option that tells the search
engine to skip the overlay entirely. More efficient than
`WithExcept(TmpFileIDs())` because it avoids trigram intersection
against overlay data. Used when `--no-tmp` is specified.

`HasTmp()` returns true if any tmp:// documents exist in the
overlay. Used by the `onlyIfTmp` optimization.

## Tag extraction

Tags are extracted from tmp:// content using the same regex as
persistent files. Tag counts (T and F records) for tmp:// files
are tracked in memory alongside the overlay — they don't touch
the index. Structure, tvid integration, and the unified read path are
specified in [tmp-tag-overlay.md](tmp-tag-overlay.md).

## Lua integration

Go functions registered on the mcp table:
- `mcp.tmp_add(path, content, strategy)` — add a tmp:// document
- `mcp.tmp_update(path, content, strategy)` — update existing
- `mcp.tmp_remove(path)` — remove
- `mcp.tmp_list()` — list all tmp:// paths
- `mcp.tmp_get(path)` — return the stored content of a tmp://
  document (see below)

These use the same `tmp://` naming convention as CLI commands.

## Content retrieval

`ark fetch tmp://name` returns the full content from the overlay's
stored bytes — no disk read. The overlay keeps the original content
for chunk retrieval; fetch returns it whole.

`ark chunks tmp://name` already works — microfts2's `GetChunks`
handles `tmp://` paths internally, reading from stored content.

### `mcp.tmp_get(path)` — Lua-side read

The Find-Connections-as-service flow in the curation workshop
needs Lua to read the body of a `tmp://` document on terminal-
status transitions. `mcp.tmp_get` is the read primitive that
complements `mcp.tmp_add` / `mcp.tmp_update` — same overlay,
opposite direction.

```lua
local content, err = mcp.tmp_get("tmp://connections/fc-7Yp2K3.md")
```

- Success: `(content, nil)` where `content` is a Lua string of
  raw bytes (UTF-8 preserved verbatim).
- Failure: `(nil, errstring)`. Failure modes: missing `tmp://`
  prefix, document not present in the overlay, Sync error.

Backed by `DB.TmpContent(path string) ([]byte, error)` —
validates the prefix, reads through `db.fts.TmpContent`,
returns the bytes. Sync read; no overlay mutation.
