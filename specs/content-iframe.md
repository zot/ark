# Content Iframe Previews

Query parameters on the `/content/` endpoint that enable search
result chunks to be rendered as iframes with the full CM6 editor.
Language: Go (server), HTML/JS (template). Environment: ark server,
browser.

## Query Parameters

`GET /content/PATH?range=RANGE&edit=true&toggle=false`

- **range=RANGE** — serve only the chunk's content identified by
  the range label (same opaque string from `SearchChunk.range`).
  The server resolves this using microfts2's chunk cache. If the
  range is invalid, falls back to full file.

- **edit=true** — load the CM6 editor immediately in read mode
  (interactive tag widgets, ark-search blocks). Skips the static
  goldmark HTML view. The editor content is the chunk text (or
  full file if no range).

- **toggle=false** — hide the pencil/eye toggle button. The page
  is an embedded preview, not a standalone editor.

## Template Changes

`contentShellData` gains boolean fields: `HideToggle`, `AutoEdit`,
`IsChunk`. The template:
- Hides `#toggle-btn` when `HideToggle` is true
- When `AutoEdit` is true: hides `#content`, shows `#editor`,
  auto-loads the CM6 editor on page load (no click needed)
- When `IsChunk` is true: the `api.save` and `api.navigate`
  behaviors adjust for chunk context (save disabled, navigate
  posts to parent)

## Auto-Height for Iframes

When loaded inside an iframe (`window !== window.parent`), the
content page posts its body height to the parent via
`postMessage({type: 'ark-content-height', height: N})` on load
and resize. The parent `<ark-search>` element listens and resizes
the iframe.
