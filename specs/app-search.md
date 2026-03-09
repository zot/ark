# App Search Support

Go-side support for the Frictionless ark app's search UI. These
features shape the search response and expose server state for the
app to render — the app itself is pure Lua.

## Grouped search response

The search API gains a grouped response mode for the app. Instead of
a flat list of chunks, results are returned as a tuple array grouped
by file:

```
[[filename, [chunk, ...]], ...]
```

Each element is a two-element array: the file path (string) and an
array of chunk objects. Files are sorted by their best chunk score
(descending). Chunks within each file are sorted by score (descending).

This is a new endpoint (`POST /search/grouped`) — the existing
`POST /search` response is unchanged.

### Chunk object

Each chunk in the grouped response includes:

- `range` — chunk line range (e.g. "5-12")
- `score` — combined score
- `preview` — pre-rendered HTML of the chunk text

Preview rendering uses goldmark for markdown files, JSON pretty-print
for JSON files (when output is under a length threshold), and plain
text with HTML escaping for everything else. Query tokens are
highlighted with `<mark>` tags in all preview formats.

The strategy that indexed the file determines which renderer to use.
The search response includes the strategy alongside the file path.

## Click to open

`POST /open` accepts a file path and opens it with the system viewer
(`xdg-open` on Linux, `open` on macOS). The app calls this when a
user clicks a search result. The endpoint returns immediately — the
viewer opens asynchronously.

## Indexing state

`GET /indexing` returns a JSON array of source directory paths that
are currently being indexed (scan or refresh in progress). Empty array
when idle.

The app polls this at 250ms intervals to show/hide spinners on
sources in the UI.

The server exposes this to Lua via `mcp:indexing()` — a Go function
registered on the mcp table after Frictionless setup. Returns a Lua
table (array of strings). This lets the app call `mcp:indexing()`
directly instead of making an HTTP request.
