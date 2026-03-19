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
LMDB results transparently. Chunk retrieval reads from stored
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

CLI search normally runs locally against LMDB (the mmap shares
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
necessity — they're not in LMDB) but the CLI transparently
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
LMDB.

## Lua integration

Go functions registered on the mcp table:
- `mcp.tmp_add(path, content, strategy)` — add a tmp:// document
- `mcp.tmp_update(path, content, strategy)` — update existing
- `mcp.tmp_remove(path)` — remove
- `mcp.tmp_list()` — list all tmp:// paths

These use the same `tmp://` naming convention as CLI commands.

## Content retrieval

`ark fetch tmp://name` returns the full content from the overlay's
stored bytes — no disk read. The overlay keeps the original content
for chunk retrieval; fetch returns it whole.

`ark chunks tmp://name` already works — microfts2's `GetChunks`
handles `tmp://` paths internally, reading from stored content.
