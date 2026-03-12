# App Search Support

Go-side support for the Frictionless ark app's search UI. These
features are exposed as Lua functions on the `mcp` table — the app
is in-process, so no HTTP round-trip is needed. The app itself is
pure Lua.

## Grouped search — `mcp:search_grouped(query, opts)`

Returns search results grouped by file as a Lua table of tables.
Each element is `{path, strategy, chunks}` where chunks is an array
of `{range, score, preview}` tables. Files sorted by best chunk
score (descending), chunks sorted by score within each file.

### Query and opts

`query` is the search string. `opts` is an optional Lua table:

- `mode` — "contains", "about", or "combined" (default "combined")
- `k` — max results (default 20)
- `filter_files` — glob pattern to restrict paths
- `exclude_files` — glob pattern to exclude paths
- `filter_file_tags` — tag name to restrict by
- `exclude_file_tags` — tag name to exclude by

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

## Click to open — `mcp:open(path)`

Opens an indexed file with the system viewer (`xdg-open` on Linux,
`open` on macOS). Returns immediately — the viewer opens
asynchronously. Errors if the path is not an indexed file.

## Indexing state — `mcp:indexing()`

Returns a Lua array of source directory paths currently being indexed
(scan or refresh in progress). Empty table when idle.

The app polls this at 250ms intervals to show/hide spinners on
sources in the UI.

## HTTP endpoint removal

The HTTP endpoints `POST /search/grouped`, `POST /open`, and
`GET /indexing` are removed. All three operations are available
only as Lua functions on the mcp table. The app is in-process
and does not need HTTP for these operations.
