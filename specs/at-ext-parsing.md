# `@ext` Parsing and Target Resolution

`@ext: TARGET @tag: value @tag2: value2 ...` declares external
annotations: tags that live in the source content but apply to a
target chunk somewhere else. This spec covers two concerns:

1. **Parsing** — split the `@ext:` value text into `(target, [tags])`.
2. **Target resolution** — turn the target spec into the chunkids it
   identifies.

Storage of the routed tags and the in-memory ext map ship in the next
roadmap point; this spec stops at "we know the target chunkids and
the tags to apply."

## Parsing

`@ext: VALUE` is matched the same way as any other tag — the existing
`tagValueRegex` captures `VALUE` as the text after `@ext:` to end of
line. `ParseExtTarget(value string) (target string, tags []TagValue, ok bool)`
splits that text:

- The TARGET is the substring up to the first `@tag:` pattern,
  trimmed.
- Each `@tag: v` segment becomes a `TagValue`, with `v` clipped at
  the next `@tag:` boundary or end of string.
- `ok` is false when no embedded tag is found (TARGET-only @ext is
  meaningless — nothing to apply).
- `ok` is also false when the TARGET is empty.

Tags are not deduped; an `@food` repeated in the same `@ext:` value
produces two entries (the storage layer will dedupe per V record).
Tag names are lowercased.

## Target resolution

`DB.ResolveExtTarget(target string) []uint64` returns the chunkids
identified by the target spec. Empty slice means "no resolution"
(broken or unknown target). v1 supports two forms:

- **UUID** — `Lookup("id", target)` → tvid → V record → chunkid
  list. Returns every chunk that carries that id (multiple chunks
  with the same `@id` are allowed; see `specs/at-id.md`).
- **Path** — `microfts2.CheckFile(target)` → fileid →
  `FileInfoByID(fileid).Chunks` → first chunk only. The path form
  resolves to the file's preamble; full-file targeting is the
  authoring convention for "this annotation applies to the whole
  file."

UUID is tried first. If the target string happens to also be a valid
path, the UUID match wins — UUIDs are the more specific identifier.

### Why not "all chunks" for path

A `@food: hamburger` `@ext`'d to a whole file should appear once at
the top, not on every chunk of the file. The first-chunk convention
matches how readers scan a document and aligns with markdown's
preamble semantics: the "topic" of the file lives before any heading.

## Deferred (post-v1)

Anchored target forms — `path:line`, `path:string`, `path:/regex/`,
`path[N]:anchor`, `path^:anchor` — are documented in `.scratch/EXT.md`
and will land as separate parser branches inside `ResolveExtTarget`.
Each is a self-contained extension; the v1 (target, tags) split and
the UUID/path branches are unaffected.

Quoted-string TARGETs (`@ext: ~/notes/x.md:"secret sauce" @food: ...`)
are also deferred. The current parser will treat the quoted string as
part of the TARGET text up to the first `@tag:`, which works
syntactically but won't resolve until the anchor branches land.

## Out of scope for this spec

- Storing the routed tags in V/F records. Belongs to the next
  roadmap point ("`@ext` V/F records + in-memory ext map").
- Re-resolution when the target file is reindexed. Belongs to the
  ext map work.
- Rendering the corner chip / search-result indicator. Belongs to
  point 8.
