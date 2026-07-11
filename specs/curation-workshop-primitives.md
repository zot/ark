# Curation Workshop Primitives

Language: Go (CLI, server runtime), Lua (Frictionless apps).
Environment: ark binary's embedded ui-engine. Lua callers are
viewdefs and app code running under `flib.Runtime.WithLua`.

The curation workshop (`apps/ark/`, designed in
`.scratch/CURATE-ONE-CHUNK.md`) needs five Lua-callable
primitives, a mirror-file layout for authoring `@ext` routings,
and a one-line extension to the hardcoded `~/.ark` source's
include list. The workshop UI's design lives with `/ui-thorough`;
this spec describes only the Go-side surface it depends on.

## `mcp.chunkInfo(chunkID)`

Returns a Lua table of metadata for one chunk. The workshop uses
it to populate the chunk card: range string for the `absolute`
locator option, comment syntax for inline-tag insertion, and the
writable flag for read-only locking.

```lua
local info, err = mcp.chunkInfo(chunkID)
-- info = {
--     chunkID       = 4711,
--     fileID        = 88,
--     path          = "/home/deck/notes/streaming.md",
--     range         = "42-47",
--     byteStart     = 1283,
--     byteEnd       = 1576,
--     writable      = true,
--     commentSyntax = "",
-- }
```

Field semantics:

- `chunkID`, `fileID` — round-trip identifiers.
- `path` — the canonical absolute path microfts2 stores
  (`info.Names[0]`).
- `range` — the chunker-specific range identifier
  (`Chunk.Range`). Workshop's `absolute` locator emits this
  verbatim into the `@ext` TARGET.
- `byteStart`, `byteEnd` — half-open byte range in the file.
  Passes through to `mcp.replaceRegion` when editing the chunk's
  text inline.
- `writable` — `false` when the chunker reports
  `IsWritable() == false` OR the path falls under a hardcoded
  read-only zone (`~/.claude/projects/**` for chat-log
  transcripts). The workshop locks the chunk's text editor and
  forces the ext toggle on when this is false.
- `commentSyntax` — the line-comment delimiter for inline tag
  insertion (`"//"`, `"--"`, `"#"`, `""`, etc.). Empty for
  markdown / raw text where tags are authored without a comment
  wrapper. Sourced from the chunker's `CommentSyntax()` method
  (see "Chunker metadata interface" below).

Lookup is `Sync` (no DB mutation). Errors follow the gopher-lua
`(nil, errstring)` convention. `chunkID` for an unknown or
overlay chunk that has no file linkage returns `(nil, "chunk
not found")`.

### Chunker metadata interface

The workshop's needs add two pieces of metadata to the chunker
contract:

```go
type ChunkerMetadata interface {
    IsWritable() bool       // false for binary/read-only chunkers
    CommentSyntax() string  // line-comment delimiter, "" if n/a
}
```

Optional interface — chunkers that don't implement it default
to `writable=true, commentSyntax=""`. PDFChunker implements it
returning `false, ""`. Bracket and indent chunkers return
`true, <first LineComment>` (or `""` when the language has none).
Markdown / line chunkers return `true, ""`.

This is an additive change to the Chunker contract — no existing
implementations need to change. The metadata layer is the
canonical place to add per-chunker capability flags without
expanding the core `Chunker` interface.

## `mcp.replaceRegion(path, byteStart, byteEnd, newText)`

Atomically replaces the byte range `[byteStart, byteEnd)` in
`path` with `newText`. The implementation does direct file I/O
(matching `mcp.setTags`'s precedent for Lua-driven file mutation);
the watcher picks up the change and triggers reindex.

```lua
local ok, err = mcp.replaceRegion(path, 1283, 1576, newChunkText)
```

Constraints:

- `path` must be an indexed file under one of ark's source roots
  (no tmp:// — those have their own update path via `tmp_update`).
- `byteEnd >= byteStart` and both within file bounds. Out-of-range
  → error.
- `newText` is bytes (Lua string). UTF-8 is preserved verbatim;
  ark applies no encoding transformation.
- Atomicity: write-to-temp-then-rename — readers always see the
  pre- or post-state, never a partial write. Concurrent writers to
  the same path race at the OS level (last writer wins for the
  rename), matching `mcp.setTags`.
- Failure modes: file not found, file not indexed, range
  out-of-bounds, write permission denied, mid-write disk error.
  Each returns `(false, errstring)`; partial writes do not occur.

The primitive does not validate the *meaning* of the new text —
if the caller mangles tag syntax or splits a chunk boundary
incorrectly, ark's reindex picks up whatever bytes are there.
Reindex re-derives chunk boundaries from the chunker, so a
"replace one chunk's worth of bytes" call typically lands one
chunk back; if the new text adds or removes structure the
chunker recognizes, chunk count can shift.

## `mcp.chunkText(chunkID)`

Returns the chunk's text bytes. The workshop wraps each pinned
card in an `<ark-markdown-editor>` and uses `mcp.parseTagBlock`
on the result to compute the `> current tags` reflection.

```lua
local text, err = mcp.chunkText(chunkID)
```

- Success: `(text, nil)` where `text` is a Lua string of raw
  bytes (UTF-8 preserved verbatim — no encoding transformation).
- Failure: `(nil, errstring)`. Failure modes: unknown chunkID,
  chunk whose range no longer resolves (file removed or
  re-chunked between lookup calls), Sync error.

Backed by `DB.ChunkTextByID(chunkID uint64) ([]byte, error)` —
resolves chunkID to `(path, range)` via `ChunkInfo`, reads via
the existing `ChunkText(path, range)` primitive. A `nil` text
return from `ChunkText` (range unresolvable) surfaces as
`"chunk text unavailable"`.

One Sync round-trip from the bridge. Sync read; no DB mutation.

## `mcp.parseTagBlock(text)`

Parses the leading `@name: value` block of `text` into an
ordered tag list plus the body bytes. Wraps the existing
`ParseTagBlock` Go helper. The workshop calls this on the
output of `mcp.chunkText` to derive `> current tags` without a
re-chunk round-trip through the watcher.

```lua
local block, err = mcp.parseTagBlock(text)
-- block = {
--     tags = {
--         { name = "topic", value = "streaming" },
--         { name = "status", value = "draft" },
--     },
--     body = "rest of the chunk...",
-- }
```

Semantics:

- `block.tags` is an ordered Lua array of `{name, value}`
  tables, in the order the `@` lines appear in `text`.
- `block.body` is a Lua string of the bytes after the tag block
  (and after the blank separator line, if one is present).
- A chunk with no leading tag block returns
  `{tags = {}, body = text}` — body is the entire input.

Pure function: no DB lookup, no Sync. Errors only on a non-
string argument (standard Lua type check).

## `mcp.extractTagValues(text, strategy)`

Returns every `@name: value` pair found anywhere in `text`, using
the same scanner the indexer uses (`ExtractTagValues`). Catches
mid-chunk inline tags and `@id` lines that `mcp.parseTagBlock`
(leading-block-only) misses. Used by the workshop's chunk card
to populate the `> current tags` reflection from the full chunk
text, not just the leading tag block.

```lua
local values = mcp.extractTagValues(text, strategy)
-- values = {
--     { name = "topic", value = "streaming" },
--     { name = "id", value = "k-9f3" },
--     { name = "status", value = "draft" },
-- }
```

Semantics:

- `values` is an ordered Lua array of `{name, value}` tables, in
  the order the `@` lines appear in `text`.
- `name` is lowercased; `value` is whitespace-trimmed.
- `strategy` selects the chunker (e.g. `"markdown"`) so
  markdown-specific mention heuristics (fenced/indented code) can
  skip false positives. Defaults to `"markdown"`.
- Compound lines like `@ext: TARGET @t1: v1` yield only the
  outer tag (`@ext` with the full remainder as its value);
  embedded-tag splitting is each outer tag's own job.

Pure function: no DB lookup, no Sync. Errors only on a non-
string argument (standard Lua type check).

## Ext-tag mirror files

The workshop authors `@ext` routings into mirror files under
`~/.ark/external/`. The mirror tree lives outside the corpus so
the user's notes stay clean of workshop-authored annotations;
ark indexes the tree as a normal source so the routings reach
the in-memory ext map.

### Source-slug derivation

The mirror path encodes which source root the target chunk
lives under. Path-as-slug: `/` replaced with `-`,
`/home/deck/work/ark` → `home-deck-work-ark`. The slug is the
top directory under `~/.ark/external/`.

Mirror path layout:

```
~/.ark/external/<source-slug>/<target-path-within-source>.md
```

Example: target at `/home/deck/work/ark/notes/streaming.md`,
source root `/home/deck/work/ark`. Slug: `home-deck-work-ark`.
Mirror: `~/.ark/external/home-deck-work-ark/notes/streaming.md.md`
(the trailing `.md` is always appended so the mirror is a
markdown file regardless of the target's original extension —
binary targets like `notes/diagram.png` mirror to
`notes/diagram.png.md`).

Slug collision (`/home/deck-foo/bar` vs `/home/deck/foo-bar` both
→ `home-deck-foo-bar`) is rare and not addressed in v1. Document
the algorithm; revisit if it bites.

### Source registration

The hardcoded `~/.ark` source gains `external/**` in its include
list. The current list in `config.go`:

```
ark.toml
schedule/**
apps/**
storage/**
```

Becomes:

```
ark.toml
schedule/**
apps/**
storage/**
external/**
```

This is a one-line addition to `arkSourceIncludePatterns`. The
mirror tree is created on first authoring call; subdirectories
are created as needed by `mcp.setExtTag`.

### File content format

Each mirror file holds a flat list of `@ext:` lines, one per
authored `(TARGET, tag, value)` tuple. No structural headings,
no grouping by target — append order is the order they were
authored. Mirror files **use absolute paths** in their `@ext:`
TARGETs (relative paths inside mirror files would resolve
against the mirror file's own directory, which is irrelevant
to the actual target).

```
@ext: /home/deck/work/ark/notes/streaming.md:"Lua coroutine" @topic: streaming-coroutines
@ext: /home/deck/work/ark/notes/streaming.md:"Lua coroutine" @layer: substrate
@ext: %4f8c2d11-7e3a-4b40-9c2e-1a4e6f8c5b21 @rotation: active
@ext: /home/deck/work/ark/notes/errors.md:113-120 @gotcha: yes
```

Multi-tag lines (`@ext: TARGET @t1: v1 @t2: v2 ...`) are
syntactically valid and resolve correctly, but the workshop's
authoring path emits one tag per line for v1. The simpler
shape keeps `setExtTag` / `removeExtTag` line-matching trivial.
Compaction (merging multiple lines targeting the same chunk
into one) is a later optimization, not v1.

### Authoring: `mcp.setExtTag` and `mcp.removeExtTag`

```lua
local ok, err = mcp.setExtTag(targetSpec, tag, value)
local ok, err = mcp.removeExtTag(targetSpec, tag)
```

`targetSpec` is a TARGET string as defined in
`specs/at-ext-parsing.md` — `%UUID` or path (absolute, `~/`, or
relative), with optional modifier and narrower. For mirror-file
authoring the workshop emits absolute paths (relative paths
would re-base against the mirror file's location, not the
target's).

`setExtTag` semantics:

1. Resolve `targetSpec` to a target file (via the path branch or
   UUID branch, see at-ext-parsing.md). For UUID, the source
   slug derives from the source root containing the chunk's
   file. For path, from the source root containing the target
   file.
2. Compute the mirror file path under
   `~/.ark/external/<slug>/<target-path>.md`.
3. Read the mirror file (create empty if absent).
4. **Collapse** every line whose TARGET matches byte-for-byte
   and whose tag list contains `tag` to a single value: rewrite
   the first such `(TARGET, tag)` span's value in place, then
   drop every later `(TARGET, tag)` span (dropping a line that
   is left with no tags). **If** no `(TARGET, tag)` span exists,
   **append** a new line: `@ext: TARGET @tag: value`. In the
   common single-value case (the workshop authors one value per
   `(TARGET, tag)`) this is identical to a plain in-place
   replace; the collapse only bites when `ark ext add` (below)
   has stacked multiple values.
5. Write the file via temp+rename for atomicity (matching
   `mcp.setTags`'s precedent for Lua-driven file mutation).
6. The watcher / indexer picks up the change and reindexes the
   mirror file, which updates the ext map.

`removeExtTag` semantics:

1. Locate the mirror file (resolve as above). Missing file →
   silent no-op.
2. Find **every** line matching `@ext: TARGET @tag:` (all
   values, not just the first). Missing line → silent no-op.
3. For a single-tag line, delete the whole line including its
   trailing newline.
4. For a multi-tag line (rare in v1 since the workshop authors
   one tag per line, but supported for hand-edited mirror
   files), remove the matching `@tag: value` span only,
   preserving the rest of the line.
5. Write atomically; reindex follows.

The underlying `DB.RemoveExtTag(targetSpec, tag, value)` primitive
takes an optional `value` filter: empty removes every
`(TARGET, tag)` routing; non-empty removes only spans whose value
matches. `mcp.removeExtTag(targetSpec, tag)` passes an empty value,
so the Lua path removes all values for `(TARGET, tag)` — identical
to the prior single-value behavior in the workshop's one-value-per-
tag usage.

Both functions return `(true, nil)` on success or `(false,
errstring)`. Errors: source root not found for target, write
permission denied, malformed `targetSpec`.

### CLI authoring: `ark ext {set,add,remove}`

The Lua bindings above are reachable only from a Frictionless
viewdef. A plain-session assistant that reads an `@ext` proposal
(e.g. a recall recommend) needs to apply it without the workshop.
The `ark ext` command group exposes the same mirror authoring from
the CLI. All three verbs act **only on the target's mirror file**
(`~/.ark/external/<slug>/<target-path>.md`) — never on
hand-authored `@ext` lines elsewhere in the corpus (see
"Mirror-file-only scope" below).

```
ark ext set    <target> <tag> <value>   # collapse all values → the one new value
ark ext add    <target> <tag> <value>   # append a value; multiple values per tag OK
ark ext remove <target> <tag> [value]   # remove all values, or just <value>
```

- `set` — wraps `DB.SetExtTag`: replace every `(TARGET, tag)`
  value in the mirror file with the single `<value>` (collapse),
  appending when none exist.
- `add` — wraps the new `DB.AddExtTag`: **append** a
  `(TARGET, tag, value)` routing, leaving any existing values in
  place, so a tag can carry several values (`@topic: recall` **and**
  `@topic: bloodhound`). An exact `(TARGET, tag, value)` duplicate
  is a silent no-op.
- `remove` — wraps `DB.RemoveExtTag`: drop the `(TARGET, tag)`
  routing(s). With `<value>` omitted, removes every value; with
  `<value>` given, removes only spans whose value matches.

`<target>` is a TARGET string as defined in
`specs/at-ext-parsing.md`. For a read-only conversation chunk the
apply command names an absolute-path `path:absolute` TARGET (chat-log
content is unstable, so text/regex locators never apply).

Like every mutating CLI command, `ark ext` **proxies through the
running server** when one is up (the server holds the index; the
mutation runs inside the DB actor and the mirror file write happens
there), and opens the index exclusively when no server is running.

#### Mirror-file-only scope

An `@ext: <target>` routing can be hand-authored in **any** corpus
file, so a literal corpus-wide "replace all values" would have to
scan and edit every file that mentions the target — invasive and
expensive. The `ark ext` verbs deliberately do **not** do this.
`set`/`remove`'s all-value semantics operate *within the mirror
file only*. The effective tag set a chunk carries is the union of
its mirror-file routings and any hand-authored `@ext` lines; the
CLI owns only the mirror file's contribution. Because `SetExtTag`
already resolves to the mirror path, this needs no corpus scan — it
is a single-file edit.

### Staging ledger: proposed and judged routings

The mirror file is not only for committed `@ext` routings. It is the
durable home for a routing's whole curation lifecycle — proposed,
committed, judged — as three tag classes that share the `@ext-*`
family and the same `TARGET @tag: value` grammar:

```
@ext:            TARGET @tag: value
@ext-candidate:  insight: "why" TARGET @tag: value
@ext-judgment:   TARGET @tag:
```

- `@ext` — a committed routing (a live edge).
- `@ext-candidate` — a **proposed** routing: durable, not yet an edge.
- `@ext-judgment` — a durable **judgment** on the `(tag-name, chunk)`
  edge (rejection now, reinforcement later). Tag-name only, no value.

All three index as **ordinary tags** — a normal F and V record for the
outer tag name — because that is the truth of the file: an enumerator
listing a chunk's tags must see `@ext-candidate` sitting there with its
literal value. They differ only in what the indexer *additionally*
derives. `@ext` routes a live edge (an X record plus the routed tag's V
record); `@ext-candidate` and `@ext-judgment` derive a proposal or a
judgment record *instead of* the live edge. **That derivation is a
separate pass — the tag-derived record subsystem — and is out of scope
here; this section covers only the file forms and the authoring verbs.**
See `.scratch/TAG-ENRICHMENT.md` "SETTLED MODEL" for the derivation model.

#### `insight` — proposal rationale

A candidate may carry an `insight` field: a quoted, free-text "why"
that travels with the proposal for whoever makes the accept/reject
call. It is **reserved metadata, not a routed tag**, so it carries **no
`@` sigil** (see `specs/at-ext-parsing.md` "Reserved metadata field")
and sits **first, before the TARGET**. Leading-and-quoted is what keeps
it unambiguous against an undelimited TARGET (a bare path can contain
spaces). The why is always quoted, so it may hold `@` or `:` without
confusing the parser. Because the insight is part of the line's text,
two proposals of the same tag with different insights are distinct
lines, so both survive for the judge to weigh; insights are preserved,
not collapsed into one.

#### Verbs: `ark ext {candidate,accept,reject}`

Three verbs stage a routing through its lifecycle, all riding the same
class-aware mirror-file machinery (`applyExtMirrorEdit`) as
`set`/`add`/`remove`:

```
ark ext candidate <target> <tag> [value] [--insight "why"]   # write an @ext-candidate
ark ext accept    <target> <tag> [value]                     # @ext-candidate → @ext
ark ext reject    <target> <tag> [value]                     # @ext-candidate → @ext-judgment
```

- `candidate` — author an `@ext-candidate` line for `(target, tag[,
  value])`, with the optional quoted `--insight`, carrying a `@count`
  repetition tally. A new `(target, tag, value, insight)` identity writes
  `@count: 1`; an exact-identity duplicate **increments** the existing
  line's `@count` (a differing insight is a distinct proposal with its own
  tally). The read-modify-write runs as one closure-actor op.
- `accept` — rewrite the matching `@ext-candidate` line(s) to `@ext`,
  committing the edge and consuming the candidate in one file edit. The
  candidate's `insight` is **dropped**: the committed routing stands on
  its own (the `@ext` form carries no rationale). This closes the accept
  loop by construction — the candidate is gone and the edge is authored;
  the indexer reflects both on reindex.
- `reject` — rewrite the matching `@ext-candidate` line(s) to a single
  tag-name-only `@ext-judgment: TARGET @tag:` carrying a signed `@count`,
  recording a durable rejection and consuming the candidate(s) for that
  `(target, tag)`. The first reject creates `@count: -1`; a repeat
  decrements it; a `@count` that reaches 0 removes the line.

Like every mutating `ark ext` verb, all three proxy through the running
server or open the index exclusively when stopped, and act **only** on
the target's mirror file.

**Now landed** (the tag-derived RC/RJ subsystem, #22 Pass B+C): the
derivation of `@ext-candidate` → RC and `@ext-judgment` → RJ, and the
signed judgment score (carried in the line's `@count`). **Still deferred:**
a reject rationale and the reinforcement (positive-judgment) producer
(seam 3b).

## `mcp.suggestExtLocator(chunkID)`

Returns the workshop's recommended base + locator for an `@ext`
routing targeting `chunkID`. Runs the three-layer algorithm and
the cross-file scope readout in one call so the widget can bind
its preview without further round-trips.

```lua
local sug, err = mcp.suggestExtLocator(chunkID)
-- sug = {
--     base               = "uuid",       -- "uuid" or "path"
--     baseValue          = "%4f8c2d11-...",  -- TARGET base text
--     locator            = "bare",        -- string / regex / absolute / bare
--     locatorKind        = "bare",
--     locatorText        = "",            -- empty when locator==bare
--     withinFileDupCount = 0,             -- @id dups in target's file
--     crossFileScope     = { chunks = 1, files = 1 },
-- }
```

The Lua binding returns the structured suggestion and the workshop widget
assembles the TARGET string itself. On the Go side, `LocatorSuggestion.Target()`
does that assembly — `baseValue` plus the narrower for its `locatorKind`
(bare → `%uuid`/path, `absolute` → `:range`, `string` → `:"…"`, `regex` →
`:/…/`), the inverse of `ParseExtTargetParts`. `DB.SuggestAnchor(path, range)`
resolves a chunk location to a chunkID and returns that assembled target,
exposed as **`ark chunks -anchor <chunkID | path:range>`** — the CLI
counterpart to `mcp.suggestExtLocator`, so a plain-session assistant can get
the opinionated `@ext` address for a chunk (to feed `ark ext candidate …`)
without the UI. It is generic chunk addressing, not `@ext`-specific.

Field semantics:

- `base` — `"uuid"` when the chunk has an `@id` tag value
  (preferred — `@id` is stable across content edits and ranges),
  else `"path"`.
- `baseValue` — for `"uuid"`, the `%`-prefixed `@id` value. For
  `"path"`, the chunk's file path (absolute).
- `locatorKind` — one of `"string"`, `"regex"`, `"absolute"`,
  `"bare"`. The category of locator the algorithm picked.
- `locatorText` — the locator's text payload (the quoted string,
  the regex body, or the range string). Empty for `"bare"`.
- `withinFileDupCount` — count of other chunks in the *target's*
  file that share the same `@id`. Surfaces accidental copy-paste
  of `@id` values; doesn't gate authoring but flags the choice
  in the UI.
- `crossFileScope.chunks` / `.files` — count of chunks and files
  the resolved `(base, locator)` would route to. The workshop's
  "will route to N chunks in M files" readout.

### Selection rules

Default `base`:
- `@id` exists on the chunk → `"uuid"`.
- Otherwise → `"path"`.

Default `locatorKind` follows
`.scratch/CURATE-ONE-CHUNK.md` "Default selection table":

| Chunk type | `@id`? | Within-file `@id` dups | Default base | Default locatorKind |
|---|---|---|---|---|
| PDF or read-only chunker | n/a | n/a | path | `absolute` (text-search unreliable) |
| `~/.claude/projects/**` | n/a | n/a | path | `absolute` (chat-log content unstable) |
| markdown / text | yes | none | uuid | `bare` (UUID alone is chunk-precise) |
| markdown / text | yes | N > 1 | uuid | `string` (auto-picked, narrows from N dups) |
| markdown / text | no | n/a | path | `string` (auto-picked); fall back to `absolute` if no unique span exists |

### Three-layer locator algorithm

Run in order; fall through if higher-priority layers can't
produce a unique result:

**Layer 1 — Line-prefix token minimum.** For each line in the
target chunk, find the shortest token-aligned prefix that's
unique among length-N line-prefixes across all other chunks in
the same file. Pick the candidate with smallest token count;
tiebreak by earliest line. Tokens split on whitespace and
punctuation; punctuation is kept as its own token where
meaningful for prefix uniqueness; case-insensitive for the
uniqueness comparison, but the emitted locator preserves case.
Empty / blank lines are skipped. If the chosen prefix would
contain a literal `"`, emit it as `regex` (`/literal\.escaped/`)
instead.

**Layer 2 — Rare-trigram-anchored substring.** When no line has
a unique short prefix, search for a mid-line substring
containing a trigram unique to the target chunk. Anchored at the
trigram, expanded to word boundaries, clamped 12–60 characters.

**Layer 3 — `absolute`.** Falls back to the chunk's range string
when no string locator is unique. Unavailable when the chunk's
range string starts with `"` or `/` (the chunker contract is
soft; see `specs/at-ext-parsing.md` "Chunker contract"). In that
edge case `suggestExtLocator` returns the best non-`absolute`
result it found (string or regex layer) even when not unique, or
`locatorKind = "bare"` if no layer produced anything — the
workshop surfaces a warning that the chunk can't be uniquely
addressed via `@ext` locators and offers the user the bare base
or UUID path.

### Cross-file scope readout

After selecting `(base, locatorKind, locatorText)`, the
resolution count is computed by running the same lookup the
resolver would. For UUID bases, this scans every chunk with the
matching `@id` across all files. For path bases, it's scoped to
that file. The result becomes `crossFileScope`.

## Chunker contract dependencies

This spec extends two existing Go contracts:

- `Chunker` (microfts2) — optional `ChunkerMetadata` interface
  per "Chunker metadata interface" above.
- `Config` (ark) — `arkSourceIncludePatterns` adds
  `external/**`.

Neither change is breaking; existing chunkers and config
consumers behave identically when not exercised by the workshop.

## Out of scope for this spec

- Workshop UI itself (pending widget stack, `[edit|revert]`
  state machine, Accept variants, Find-Connections orchestration,
  current-tags desired-state rendering). Belongs to the
  `/ui-thorough` pass that consumes these primitives — see
  `apps/ark/requirements.md` "Curation View" and
  `apps/ark/design.md` for the UI design.
- Per-chunker editor variants (CM6 via `createInkArkEditor` is
  universal in v1; Lua / Python / Go modes deferred).
- Mirror-file compaction (merging multi-tag lines targeting the
  same chunk). v1 authors one tag per line.
- Pinned-list persistence. Extension to `specs/curation.md`.
