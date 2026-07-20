# Test Design: ext_mirror override
**Source:** crc-DB.md, crc-Config.md

Pure path logic — `extMirrorPath` takes strings and `SourceForPath` walks a
config slice. No DB, no server. The default-tree case reads `arkHomeDir()`
(HOME), made hermetic with `t.Setenv`. Covers R3171.

## Test: default tree when ext_mirror unset
**Purpose:** the override changes nothing for a source with no `ext_mirror`.
**Input:** `extMirrorPath("/src/root", "/src/root/notes/a.md", "")` with HOME
set to a temp dir.
**Expected:** `<tmp>/.ark/external/src-root/notes/a.md.md` (slug = path-as-slug
with the leading `/` stripped, trailing `.md` appended).
**Refs:** crc-DB.md, R2392, R3171

## Test: ext_mirror redirects the base in-tree
**Purpose:** R3171 — base moves under the source, layout otherwise identical.
**Input:** `extMirrorPath("/src/root", "/src/root/books/mark.md", "mirrors")`.
**Expected:** `/src/root/mirrors/books/mark.md.md`. No HOME dependency.
**Refs:** crc-DB.md, R3171

## Test: self-mirror rejected
**Purpose:** a target already inside the ext_mirror dir has no mirror
(no mirrors/mirrors nesting).
**Input:** `extMirrorPath("/src/root", "/src/root/mirrors/books/mark.md.md", "mirrors")`
and the exact-dir edge `.../mirrors`.
**Expected:** error both times; empty path.
**Refs:** crc-DB.md, R3171

## Test: target outside source root rejected
**Purpose:** guards the existing `..` rel check still fires with an override.
**Input:** `extMirrorPath("/src/root", "/other/x.md", "mirrors")`.
**Expected:** error.
**Refs:** crc-DB.md, R2392

## Test: glob expansion carries ext_mirror
**Purpose:** R3171 — a glob source's `ext_mirror` must reach every concrete
source `ResolveGlobs` materializes, or the override silently vanishes for
glob-managed trees.
**Input:** a temp root holding one real directory `proj-a`; a Config whose only
source is `{Dir: <root>/*, ExtMirror: "mirrors"}`; run `ResolveGlobs`, then
`SourceForPath(<root>/proj-a/notes/x.md)`.
**Expected:** the expanded source carries `ExtMirror == "mirrors"`, and
`extMirrorPath` on it yields `<root>/proj-a/mirrors/notes/x.md.md`.
**Refs:** crc-Config.md, R3171, R197

## Test: SourceForPath returns the owning source with its fields
**Purpose:** R3171 wiring — resolution must reach `ExtMirror`, not just the root.
**Input:** a Config with sources `{Dir:"/a", ExtMirror:"m"}` and `{Dir:"/b"}`;
query `/a/deep/x.md`, `/b/y.md`, and `/none/z.md`.
**Expected:** first → the `/a` source with `ExtMirror=="m"`; second → `/b`
source, empty ExtMirror; third → ok=false. Glob sources are skipped.
**Refs:** crc-Config.md, R3171
