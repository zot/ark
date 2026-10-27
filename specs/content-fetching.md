# Content Fetching

Serve indexed files through the UI server for browser consumption.
Language: Go. Environment: ark UI server (HTTP port, browser-facing).

## Context

Ark's API server (unix socket) already has `POST /fetch` for JSON
content retrieval. These new routes live on the **UI server** (HTTP
port) registered via `Runtime.UIHandleFunc`, making indexed content
directly accessible to the browser.

Three routes serve the same content at different levels of
richness. Together they let the browser show any indexed file —
markdown files get the full CodeMirror 6 editor experience,
everything else gets a reasonable presentation.

### PATH in a URL

PATH is the file's absolute path in slash form behind a single `/`, so a Unix
path reads as itself (`/content/home/me/x.md`) and a Windows path keeps its
drive (`/content/C:/Users/me/x.md`), the convention `file:///C:/` URLs use. The
routes drop the `/` in front of a drive. Every place that builds one of these
URLs builds it this way: the server's link and image rewrites, `@link`
rendering and `/raw` URLs, the search element's result links and previews, the
tag overview's source links, the content pages themselves, and the ark app.
Gluing the prefix straight onto a Windows path gives `/contentC:/…`, which no
route matches. A content page reads its own file path back from its URL with
the inverse rule, since saving and tagging need the file's path, not its URL
form.

## `/fetch/PATH` — JSON content retrieval

Returns file content as JSON, same as the existing `POST /fetch`
on the API mux but as a GET with the path in the URL. This is the
programmatic access point — the markdown editor's HostAPI or any
JavaScript code can fetch content without form-encoding a POST body.

Request: `GET /fetch/absolute/path/to/file.md`

Response:

```json
{
  "path": "/absolute/path/to/file.md",
  "content": "raw file content",
  "contentType": "markdown"
}
```

The `contentType` is derived from the file's indexing strategy,
same mapping as the editor endpoints (markdown, text, json, code).
The path must be within an indexed source — 404 if not found, 403
if not indexed.

## `/content/PATH` — rich presentation

Returns an HTML page that presents the file based on its content
type. This is what you'd link to or open in a browser tab.

- **Markdown**: an HTML shell that loads the CodeMirror 6 markdown
  editor bundle (`ark-markdown-editor.js`). The shell fetches
  content from `/fetch/PATH` and creates an `ArkEditor` instance
  with a HostAPI wired to the editor HTTP endpoints (`/search/grouped`,
  `/tags/complete`, `/tags/values`, `/save`, `/set-tags`). The editor
  handles rendering, tag widgets, completion, and save.

- **Other types**: a minimal HTML page with the raw content in a
  `<pre>` block. Future enhancement: syntax highlighting for code,
  rendered HTML for `.html` files, image display for images.

Request: `GET /content/absolute/path/to/file.md`

Response: HTML page (Content-Type: text/html).

## `/raw/PATH` — raw content

Returns the file content verbatim with an appropriate Content-Type
header (text/markdown, text/plain, application/json, etc.). No
wrapping, no JSON, no HTML shell. This is for downloading,
curl, or embedding in an iframe.

Request: `GET /raw/absolute/path/to/file.md`

Response: raw file bytes with mime-type Content-Type header.

## Route Registration

Routes are registered via `srv.uiRuntime.UIHandleFunc()` after the
UI engine starts. They share the UI server's HTTP port (the same
port the browser loads the Frictionless app from). The handlers
need access to the DB actor for `IsIndexed` checks and file reads.

## Path Validation

All three routes validate that the requested path is within an
indexed source directory — not that the file itself is indexed.
This is broader than `handleSave`'s check because content routes
need to serve non-indexed assets (images, CSS, etc.) that live
alongside indexed files. For example, a markdown file may embed
`![diagram](./arch.png)` where the image isn't indexed but lives
in a source directory.

Paths are cleaned via `filepath.Clean` and must be absolute. The
check walks configured sources to see if the path falls under any
of them.
