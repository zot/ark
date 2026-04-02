# Editor HTTP Endpoints

Go HTTP endpoints that serve the standalone markdown editor's HostAPI.
Language: Go. Environment: ark server (HTTP mux on unix socket + HTTP port).

## Context

The markdown editor component communicates through a HostAPI interface.
When embedded in Frictionless, the host wraps in-process Lua calls.
When standalone, the host wraps HTTP calls to these endpoints.

The Lua equivalents already exist: `mcp:search_grouped`, `mcp.setTags`.
These endpoints expose the same operations over HTTP for the standalone
editor, plus new ones (tag completion, value completion, file save)
that the Lua side doesn't need yet.

## Grouped Search — `POST /search/grouped`

Restore the grouped search HTTP endpoint that was removed when the app
went in-process (see specs/app-search.md "HTTP endpoint removal").

Request body:

```json
{
  "query": "string",
  "mode": "combined|contains|about|fuzzy",
  "k": 20,
  "session": "optional-session-name",
  "filter_files": ["glob"],
  "exclude_files": ["glob"],
  "filter_file_tags": ["tag"],
  "exclude_file_tags": ["tag"],
  "filter": ["fts-query"],
  "except": ["fts-query"]
}
```

Response: array of grouped results. Each chunk includes the fields the
editor needs:

```json
[{
  "path": "string",
  "strategy": "string",
  "chunks": [{
    "range": "5-12",
    "score": 0.85,
    "content": "raw chunk text",
    "contentType": "markdown|text|json|code",
    "preview": "<rendered HTML>"
  }]
}]
```

The `content` field is the raw chunk text (already available from
FillChunks). The `contentType` is derived from the strategy that
indexed the file: "markdown" for markdown strategy, "json" for
chat-jsonl, "code" for bracket/indent chunkers, "text" for
everything else. The `preview` field is the existing pre-rendered HTML.

Uses `SearchGrouped` which already handles mode dispatch, session
scoping, and multi-strategy search. The enhancement is adding
`content` and `contentType` to `GroupedChunk`.

## Tag Completion — `POST /tags/complete`

Return tag names matching a prefix, sourced from D (definition)
records in the index.

Request:

```json
{
  "prefix": "sta"
}
```

Response:

```json
[{
  "name": "status",
  "description": "Lifecycle state of a work item"
}]
```

D records are tag definitions extracted from `@tag: name description`
lines in indexed files. The existing `Store.ListTagDefs` scans all
D records. This endpoint filters by prefix and deduplicates by tag
name (multiple files can define the same tag — use the first
description found).

If prefix is empty, return all known tag names (from T records via
`TagList`) with descriptions filled from D records where available.

## Tag Value Completion — `POST /tags/values`

Return known values for a specific tag, optionally filtered by prefix.

Request:

```json
{
  "tag": "status",
  "prefix": "in"
}
```

Response:

```json
[{
  "value": "in-progress",
  "count": 12
}]
```

Values come from scanning F (per-file tag count) records and reading
the actual tag values from indexed files. Since LMDB stores tag counts
but not tag values directly, this requires reading F records for the
tag to get file IDs, then extracting values from those files.

An alternative: during indexing, also store tag values in LMDB. This
would make value completion fast. But for now, scan files — tag
completion is interactive and the result set is small enough.

## File Save — `POST /save`

Write file content and trigger re-indexing.

Request:

```json
{
  "path": "/absolute/path/to/file.md",
  "content": "new file content"
}
```

Response: 200 OK on success.

The path must be within an indexed source directory. Write the content,
then trigger a refresh of that single file. The watcher will also
notice the change, but an explicit refresh avoids the debounce delay
for immediate feedback.

## Set Tags — `POST /set-tags`

Atomically update tags in a file's tag block.

Request:

```json
{
  "path": "/absolute/path/to/file.md",
  "tags": {
    "status": "completed",
    "priority": "high"
  }
}
```

Response: 200 OK on success.

Reads the file, parses the tag block, sets each tag (including
auto-setting `status-date` when `status` changes), writes the file
back. Same logic as the Lua `mcp.setTags` function. The watcher
picks up the change for re-indexing.

## CORS

All editor endpoints must set CORS headers when the HTTP port serves
the browser UI. The browser loads from `localhost:PORT` and calls the
same origin, so this may not need explicit CORS — but if the editor
is loaded from a different origin (e.g. file://), the endpoints
should allow it. Use the same CORS policy as the existing HTTP port.
