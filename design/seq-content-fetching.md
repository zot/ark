# Sequence: Content Fetching
**Requirements:** R1151-R1167

HTTP GET routes on the UI server for serving indexed file content
to the browser.

## Route Registration (R1151-R1153)

```
Server.startUIEngine        flib.Runtime
  |                            |
  |--rt.Start()--------------->|
  |<--url----------------------|
  |                            |
  |--registerLuaFunctions()    |
  |--registerContentRoutes()   |
  |  |--rt.UIHandleFunc------->|
  |  |  "GET /fetch/"          |
  |  |--rt.UIHandleFunc------->|
  |  |  "GET /content/"        |
  |  |--rt.UIHandleFunc------->|
  |     "GET /raw/"            |
```

## GET /fetch/PATH — JSON retrieval (R1157-R1159)

```
Browser         UI Server           DB Actor
  |                |                    |
  |--GET /fetch/abs/path.md----------->|
  |                |                    |
  |                |--filepath.Clean--->|
  |                |  validate absolute |
  |                |                    |
  |                |--Sync: IsInSource->|
  |                |<--bool-------------|
  |  [not in source: 403]               |
  |                |                    |
  |                |--os.ReadFile------>|
  |                |<--content----------|
  |  [not found: 404]                   |
  |                |                    |
  |                |--strategyToType--->|
  |                |  (same mapping as  |
  |                |   editor endpoints)|
  |                |                    |
  |<--200 JSON {path,content,contentType}
```

## GET /content/PATH — rich HTML (R1160-R1164)

```
Browser         UI Server           DB Actor
  |                |                    |
  |--GET /content/abs/path.md--------->|
  |                |                    |
  |                |  (same path validation + fetch as /fetch/)
  |                |                    |
  |  [markdown?]   |                    |
  |  yes:          |                    |
  |<--200 HTML shell with:              |
  |    <script src="ark-markdown-editor.js">
  |    fetch("/fetch/PATH") → createArkEditor()
  |    HostAPI wired to /search/grouped,
  |    /tags/complete, /tags/values,
  |    /save, /set-tags                 |
  |                |                    |
  |  no:           |                    |
  |<--200 HTML <pre>content</pre>-------|
```

## GET /raw/PATH — raw content (R1165-R1167)

```
Browser         UI Server           DB Actor
  |                |                    |
  |--GET /raw/abs/path.md------------->|
  |                |                    |
  |                |  (same path validation + fetch)
  |                |                    |
  |                |--mimeByExt-------->|
  |                |  .md → text/markdown
  |                |  .json → application/json
  |                |  .go → text/plain  |
  |                |  etc.              |
  |                |                    |
  |<--200 raw bytes, Content-Type set---|
```

## Path Validation (R1154-R1156, shared)

All three handlers share the same validation:
1. Extract path from URL (strip route prefix)
2. `filepath.Clean` + absolute check
3. `Config.IsInSource(path)` via Sync — checks if path falls under
   any configured source directory. 403 if not. This is broader than
   `IsIndexed` — non-indexed assets (images, CSS) are allowed.
4. `os.ReadFile(path)` — 404 if file missing
