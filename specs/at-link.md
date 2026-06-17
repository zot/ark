# `@link` Rendering

`@link: VALUE` is a tag whose value is rendered as a clickable link
in the `/content/` view. Resolution is best-effort: the index resolves
UUIDs through `@id` records and falls back to treating the value as a
path. A resolved link becomes a plain `<a>` (no tag widget); an
unresolved link stays an `<ark-tag>` with a "broken" class so the
frontend can style it as such.

## Two value forms

`@link: UUID` — the value is opaque text. The index treats it as a
candidate identifier and resolves it via the `@id` machinery (point 4):
`TvidMap.Lookup("id", VALUE)` → tvid → V record → chunkid → fileid →
(path, chunk Location).

`@link: path` — the value is a file path, absolute or `~/`-style. The
index calls `microfts2.CheckFile(path)` to verify the path is indexed;
if so the URL points at the whole file (no chunk range).

The two forms share one resolver entry point, `DB.ResolveLink(value)`.
ID resolution is tried first because UUIDs and paths are visually
distinct enough that a path is unlikely to be a registered UUID.

## URL shape

Resolved UUID:
```
/content/{path}?range={chunk Location}
```

Resolved path:
```
/content/{path}
```

Both reuse the existing `/content/` route. The `range` query param is
already consumed by `handleContentView` to scope the rendered chunk
(R1423–R1425).

## HTML rendering

Inside `wrapTagElements`, when the matched tag name is `link`:

- **Resolved**: emit `<a class="ark-link" href="/content/PATH?range=LOC">@link: VALUE</a>`.
  The link replaces the would-be `<ark-tag>` wrapper entirely.
- **Broken**: emit `<ark-tag class="ark-link-broken"><name>link</name> <value>VALUE</value></ark-tag>`.
  The frontend can style it with strikethrough or a muted color; the
  tag widget still picks it up so users can edit or search.

Other tag names render unchanged.

## Resolver in DB

`DB.ResolveLink(value string) (path, location string, ok bool)`:

1. UUID branch: `Lookup("id", value)` → tvid. If found, read
   `V[id][value][tvid]` directly (single index Get) and decode the
   first chunkid. Resolve chunkid → fileid via the existing
   `chunkID→fileIDs` resolver, then `FileInfoByID(fileid)` for path
   and chunk Location.
2. Path branch: `microfts2.CheckFile(value)` — if status reports a
   known fileid, return `(value, "")`. The path is used verbatim;
   tilde expansion is left to the caller.
3. Neither: return `("", "", false)`.

`ResolveLink` is called inside the rendering hot path. The TvidMap
lookup is in-memory; the index Get is a single B-tree probe;
FileInfoByID is microfts2's existing per-file index lookup. No
prefix scans, no full-table walks.

## wrapTagElements gains a *DB

`wrapTagElements(html string, db *DB) string`. The 7 callsites in
`server.go` and `search.go` thread `srv.db` through. `db` may be nil
in tests that bypass the server; `nil` short-circuits the link branch
to the broken renderer (no resolver available).

## tmp:// targets

A UUID declared in `tmp://` content resolves to a `tmp://` chunkid
and a `tmp://` path. The resulting URL is `/content/tmp://...`,
which the browser will not encode usefully. v1 produces the URL but
does not promise it works in a browser. tmp:// resolution is included
because it falls out of the unified read path; UI handling of tmp://
URLs is a follow-up.

## Anchors and hash fallback (deferred)

EXT.md sketches `path:line`, `path:string`, `path:/regex/`, and
`path[N]:` anchor forms. v1 does not parse these. A future revision
extends the path branch to recognize the `path:anchor` shape and
resolves the anchor against the file content. CRecord's
`Hash [32]byte` enables a rename-resilient lookup if the path branch
fails — also deferred.

## Out of scope

- Anchors (`path:line`, `path:string`, `path:regex`).
- Content-hash fallback for renamed files.
- `<ark-link>` custom element with hover preview / inline transclusion
  affordances.
- Frontend styling of `.ark-link` / `.ark-link-broken` classes.
