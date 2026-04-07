# Sequence: Content Fetching
**Requirements:** R1151-R1189

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

## GET /content/PATH — rich HTML (R1160-R1164, R1168-R1189)

### Read View (default, server-rendered)

```
Browser         UI Server           DB Actor        goldmark
  |                |                    |               |
  |--GET /content/abs/path.md--------->|               |
  |                |                    |               |
  |                |  (same path validation + fetch as /fetch/)
  |                |                    |               |
  |  [markdown?]   |                    |               |
  |  yes:          |                    |               |
  |                |--goldmark.Convert->|-------------->|
  |                |  (rewrite relative |               |
  |                |   img src→/raw/DIR/src             |
  |                |   .md href→/content/DIR/href       |
  |                |   abs/external unchanged)          |
  |                |<--HTML-------------|---------------|
  |<--200 HTML page with:              |               |
  |    rendered markdown in content div |               |
  |    pencil button (upper right)     |               |
  |    <script src="ark-markdown-editor.js">            |
  |                |                    |               |
  |  no:           |                    |               |
  |<--200 HTML <pre>content</pre>------|               |
```

### Edit View (client-side toggle)

```
Browser                          UI Server
  |                                  |
  |  [user clicks pencil]           |
  |--GET /fetch/PATH--------------->|
  |<--{path,content,contentType}----|
  |                                  |
  |  createInkArkEditor({           |
  |    parent, doc, path, api       |
  |  })                              |
  |  hide rendered div               |
  |  show editor div                 |
  |  pencil → eye icon               |
  |                                  |
  |  [user clicks eye]              |
  |  [dirty?]                        |
  |  yes: prompt Save/Discard       |
  |    Save: api.save() → reload    |
  |    Discard: reload              |
  |  no: reload                      |
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
