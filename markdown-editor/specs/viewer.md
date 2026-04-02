# Markdown Viewer/Editor

CodeMirror 6 markdown viewer with ark tag integration.
Language: JavaScript/TypeScript. Environment: browser, served from ~/.ark/html.

## What It Is

A standalone CodeMirror 6 component that renders markdown files
with interactive ark tag support. It is embedded by host views
(source tree, search results, schedule, browser) but has no
dependency on Frictionless or the host's view framework.

## Host Communication

The viewer communicates with ark through an API object passed in
at construction. The host implements this interface — in the
Frictionless app, the host wraps its in-process Lua `mcp` calls;
in standalone use, the host wraps HTTP calls to ark's server.

- `search(query)` — run an ark search, return grouped results.
  Each result chunk includes the complete raw chunk content (not
  just hit context), content type, and pre-rendered HTML. Chunk
  boundaries come from the indexer (e.g. markdown chunks are
  complete sections — heading + paragraph). The viewer renders
  markdown chunks in a read-only CM6 instance (with tag widgets
  active); non-markdown chunks use the pre-rendered HTML.
- `tagComplete(prefix)` — return tag name completions from D records
- `tagValueComplete(tag, prefix)` — return value completions
- `save(path, content)` — write file back, triggers re-index
- `navigate(path)` — ask the host to open a different file
- `setTags(path, tags)` — update tags via tag block protocol

The viewer never calls ark directly. The host adapts its own
transport (HTTP or in-process) to this interface.

Note: the HTTP endpoints the standalone host needs are specified
on the Go side (specs/app-search.md or a new spec). This spec
covers the viewer component only.

## Tag Widgets

Tags (`@word: value`) detected in the document get interactive
treatment:

- **Any tag** (default): click opens a search panel below the
  line. Search field shows the full tag text, pre-selected, so
  the user can read results or start typing to refine.
- **Schedule tags**: date picker widget for the value.
- **Status tags**: dropdown with known values (open, accepted,
  in-progress, completed, denied, future).
- **Ack tags**: gap-detection helpers.

Widgets render inline or as line decorations. CM6 `WidgetType`
for each tag type.

## Tag Completion

- `@` at the start of a word triggers tag name completion from
  the index (D records via `tagComplete`).
- After the colon in `@tagname:`, triggers value completion from
  the tag index (via `tagValueComplete`).

## ark-search Code Blocks

Fenced code blocks with `ark-search` language tag render as live
search result panels:

````
```ark-search
@status: open
```
````

The block content is the query. Three view modes cycle on click:

- **both** — source (editable) on top, live results below
- **results** — results only, clean dashboard view
- **src** — source only, raw query

The code fence accepts an optional `mode=` attribute that lists
which modes are available and in what order:

````
```ark-search mode=results
@status: open
```
````

The first mode in the list is the initial display mode. Default
is `mode=both,results,src` — show everything first, one click to
reduce noise. `mode=results` means the search is read-only in
the file's read mode; the query can only be changed by switching
to edit mode. Edit mode always enables all three modes regardless
of the attribute.

Markdown results render in read-only CM6 instances with tag
widgets active — results are as interactive as the document
itself. Non-markdown results use pre-rendered HTML. Click a
result to navigate.

ark-search blocks inside results default to `src,both,results`
(source first — no search fires until the user clicks through).
This prevents cascading searches while keeping the blocks
interactive.

## Read/Edit Mode

- Default: read-only. Markdown renders with widgets active.
- Toggle to edit mode for full text editing.
- On save: call `save(path, content)`. Host re-indexes.
- Tag edits can use `setTags` for atomic tag block updates.

## Packaging

Built assets (JS bundle, CSS) are placed in `~/.ark/html/` by
the ark build/install process. The host view loads them via
script/link tags. No npm runtime dependency — build step only.
