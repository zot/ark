# Tag Search Panel

Inline search panel triggered from tag widgets in the markdown
editor. Supports spectral hypergraph queries — narrowing tag
membership by value.
Language: TypeScript (search panel component), Go (show-in-folder
endpoint). Environment: ark markdown editor (CM6/ink-mde), ark
UI server.

## Context

Tag widgets currently show a play button (▶) after each tag that
fires `api.search(tagText)` but discards the results. This feature
makes the results visible in a scrollable panel that opens inline
below the tag, with a query bar that supports spectral narrowing.

See HYPERGRAPH.md for the theoretical framing — tags are
hyperedges, tag values rotate the polarizer, and the search bar
lets the user control the rotation angle interactively.

## Query Bar

When the user clicks ▶ on a tag, a panel opens below the tag line
containing a query bar and a results area. The query bar has three
parts:

```
@[tag___] [.*] [value___]
```

- **Tag field**: pre-filled with the clicked tag name. Editable,
  with tag name autocompletion from the index.
- **Regex toggle**: button showing `.*` when regex is active,
  plain text otherwise. Toggles between regex and literal
  matching for the value field.
- **Value field**: initially empty. As the user types, results
  update live. The value filters tag content — this is the
  spectral narrowing (rotating the polarizer to select a subset
  of the hyperedge).

Pressing Enter or a short debounce triggers the search. The
search query sent to the API is constructed from the tag and
value fields: `@tag: value` for literal, `@tag:` with a regex
filter for regex mode.

## Results Area

Below the query bar, a scrollable area shows search results
grouped by file, styled like search engine results:

- **File path** as a clickable link (navigates to `/content/PATH`)
- **Show location button** (folder icon) next to the path —
  opens the native file manager with the file selected
- **Chunk previews** rendered as HTML — markdown chunks get
  goldmark-rendered HTML, code chunks get syntax-highlighted
  `<pre>` blocks

The panel is resizable by dragging its bottom edge. Clicking ▶
on a tag that already has an open panel closes it (toggle).

## Show in Folder

A new HostAPI method `showInFolder(path)` and corresponding
server endpoint `POST /file/show` that opens the native file
manager with the file selected:

- Linux: `gdbus call --session --dest org.freedesktop.FileManager1 --object-path /org/freedesktop/FileManager1 --method org.freedesktop.FileManager1.ShowItems "['file:///path/to/file']" ""`
- macOS: `open -R <path>`
- Windows: `explorer.exe /select,"<path>"`

The endpoint validates the path is within an indexed source
(same check as other content endpoints).

## Integration

The search panel component is a standalone TypeScript module
that can be used in both the CM6 editor (Frictionless app) and
the ink-mde editor (`/content/` page). It receives a HostAPI
and renders into a provided container element.

The `TagSearchWidget` in `tag-widget.ts` changes from firing a
search-and-forget to creating/toggling a search panel below the
tag line.

## Search Precision

All tag searches use regex mode for precise matching. The query
is constructed as `@tag:\s*value` — this ensures only actual
tag-value pairs match, not arbitrary text containing the words.

In literal mode, the value is escaped (regex metacharacters
quoted). In regex mode, the raw value is used as a pattern.
Invalid tag names in literal mode show a red border and tooltip.

The server's `handleSearchGrouped` supports `mode: "regex"`
which routes through `SearchRegex`. The multi-strategy guard
excludes regex queries. Regex highlights use the raw pattern
directly instead of the tokenize-and-escape path.

## JS Bundle Cache Busting

The content template uses `{{.BundleHash}}` in the dynamic
import URL — a `?v=mtime` query parameter that changes when
the bundle is rebuilt, forcing browser cache invalidation.

## Template Installation

Canonical templates live in `install/html/` with empty
`<!-- #frictionless -->` markers. The Makefile copies them to
the build cache. At server startup, `flib.InjectAllThemeBlocks`
patches all HTML files with theme CSS links.
