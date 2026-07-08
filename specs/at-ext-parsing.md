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
line. The embedded `@x: y` segments inside `VALUE` are NOT extracted
as inline tags by `ExtractTagValues`; they belong to the `@ext` flow,
which calls `ParseExtTarget` to recover them as routed-tag pairs.
`ParseExtTarget(value string) (target string, tags []TagValue, ok bool)`
splits the captured `VALUE`:

- The TARGET is the substring up to the first `@tag:` pattern,
  trimmed. The scanner that locates this boundary is **anchor-aware**:
  it skips over `"..."` spans and `/.../` spans so that a `@tag:`
  pattern inside a target-anchor is not mistaken for the start of
  the tag list. The opener token must be at the position right after
  a base's `:` (the only place an anchor can begin); openers elsewhere
  in the TARGET span are treated literally.
- Each `@tag: v` segment becomes a `TagValue`, with `v` clipped at
  the next `@tag:` boundary or end of string. Inside an `@tag:`
  value, the anchor-skip rule does not apply — values are plain text.
- `ok` is false when no embedded tag is found (TARGET-only @ext is
  meaningless — nothing to apply).
- `ok` is also false when the TARGET is empty.

Tags are not deduped; an `@food` repeated in the same `@ext:` value
produces two entries (the storage layer will dedupe per V record).
Tag names are lowercased.

`ParseExtTarget` returns the TARGET as authored text. The base /
modifier / narrower decomposition happens inside `ResolveExtTarget`
at resolve time so that the V record stores exactly what the user
wrote (matching the verbatim-preservation rule for relative paths
and `~/` expansion — see "Relative paths" below).

### Reserved metadata field: `insight`

An `@ext-candidate` value may begin with a reserved `insight: "…"`
field — a quoted, free-text rationale carried with the proposal (see
`curation-workshop-primitives.md` "Staging ledger"). It is **not** a
routed tag and carries **no `@` sigil**, precisely because it is
metadata. It sits **first, before the TARGET**, and its value is
**always quoted**. `ParseExtTarget` peels a leading `insight: "…"`
before it parses the TARGET, skipping the quoted span (via
`indexUnescapedByte`) so the rationale may hold `@` or `:` without being
misread.

Leading-and-quoted is what keeps it unambiguous. A bare TARGET (a path,
a `%uuid`, or a RANGE_STRING) is undelimited and can contain spaces, so
a metadata field placed *between* the TARGET and the routed tag could
not be told apart from the TARGET. Putting it first, quoted, with no
sigil, avoids that entirely. The field is recognized only when
`insight:` is followed by whitespace and then a quote, so a
relative-path base literally named `insight` with a `:"anchor"` narrower
(no space before the quote) is not mistaken for it. Because the insight
is part of the line's text, two proposals of the same tag with different
insights remain distinct lines (preserved, not merged).

## Target syntax

```
TARGET        := PATH MODIFIER? PATH_NARROWER?
               | UUID MODIFIER? UUID_NARROWER?
PATH          := absolute path, home-relative, or source-relative
               | "\" "%" REST                (literal "%" at the start of a relative path)
UUID          := "%" UUID_VALUE              (% sigil disambiguates from PATH)
MODIFIER      := "[" N "]" | "^"             (only meaningful with NARROWER)
UUID_NARROWER := ":" UUID_ANCHOR
PATH_NARROWER := ":" PATH_ANCHOR
PATH_ANCHOR   := UUID_ANCHOR
               | RANGE_STRING                (anything else; PATH base only)
UUID_ANCHOR   := QUOTED_STRING               (starts with ")
               | REGEX                       (starts with /)
```

- **PATH** — absolute (`/...`), home-relative (`~/...`), or
  source-relative (anything else). See "Relative paths."
- **UUID** — `@id` value, prefixed with `%`. The sigil is required
  because `@id` values are free-form strings (see `specs/at-id.md`)
  and can collide with relative path names. Without the prefix
  there is no syntactic way to tell whether `notes` is a path or
  a UUID. See "Why a sigil" below.
- **QUOTED_STRING** — `"..."`, literal substring match against chunk
  text. Spaces allowed inside the quotes. Quote escape: `\"`.
- **REGEX** — `/.../`, regex match against chunk text. Spaces allowed.
  Slash escape: `\/`.
- **RANGE_STRING** — chunker-specific identifier for a chunk's
  position (markdown line range `42-47`, PDF position `3/para/4`,
  bracket-chunker line range `113-120`, etc.). Anything after `:`
  that is not a quoted string or regex is treated as a range string.
  **Allowed only with a PATH base.** UUIDs accept only string and
  regex anchors.
- **MODIFIER** narrows the all-matches default of an anchor query:
  - `[N]` — Nth match (1-based)
  - `^` — first match, shorthand for `[1]`
  Modifiers without a narrower are not part of the grammar — a bare
  base without an anchor resolves on its own (preamble for path, all
  `@id` chunks for UUID).

### Why a sigil

`@id` tags accept any string value. A user can write `@id: notes`
just as legitimately as `@id: b62a7423-4e24-...`, and a relative
path `notes/recipe.md` can syntactically look like a UUID. Without
a sigil the resolver would have to guess; the wrong guess silently
mis-routes the annotation. The `%` sigil makes the choice
structural — `%` always means UUID, never means path.

`%` is chosen because:

- It does not collide with any path syntax (`/`, `~/`, `./`, `../`).
- It does not clash with anchor disambiguation (`"` and `/`).
- It is one character — every `@ext` annotation in user content
  carries it, so density matters.
- It echoes the URL-encoding tradition: `%` as a marker for an
  encoded identifier.

### Anchor disambiguation

The character immediately after `:` (whitespace skipped) selects the
anchor type:

| First char    | Anchor type              | Closing            |
|---------------|--------------------------|--------------------|
| `"`           | quoted string            | next unescaped `"` |
| `/`           | regex                    | next unescaped `/` |
| anything else | range string (PATH only) | end of TARGET span |

### Literal `%` in relative paths

A relative filename starting with `%` is escaped with a leading
backslash: `\%weird.md` resolves to the path `%weird.md`. The
escape is required only at the start of a relative path (where `%`
would otherwise be read as the UUID sigil). `%` mid-string is
always literal: `dir/%file.md` needs no escape.

For consistency, `\%` may appear anywhere in a TARGET and is always
read as a literal `%`. The V record stores the authored form
verbatim (including the `\`); resolution strips `\%` → `%` at
lookup time.

`\%` is the only escape sequence — `\` before any other character
is literal.

## Target resolution

`DB.ResolveExtTarget(target, sourceDir string) []uint64` returns the
chunkids identified by the target spec. `sourceDir` is the absolute
directory of the source file in which the `@ext:` declaration
appears, used to resolve relative `PATH` bases. Empty slice means
"no resolution" (broken or unknown target).

Resolution proceeds in two phases:

1. **Decompose** the target into `(BASE, modifier, anchor)`. Try
   UUID first (`base` matches the `@id` value format and the V
   record exists); fall back to path. If UUID lookup matches, that
   branch wins even when the base also looks like a path — UUIDs
   are the more specific identifier.
2. **Resolve** the base, then apply the anchor and modifier:

| Base | Bare (no narrower) | `:"string"` | `:/regex/` | `:range` |
|---|---|---|---|---|
| PATH | first chunk (preamble) | all chunks in file with literal substring | all chunks in file matching regex | chunk at that range |
| UUID | every chunk carrying that `@id` across all files | UUID-matching chunks whose text contains the literal substring | UUID-matching chunks whose text matches the regex | (invalid — empty result) |

The `MODIFIER` post-filters the anchor result set:
- No modifier → all matches (hyperedge default).
- `[N]` → keep only the Nth match (1-based; out-of-range → empty).
- `^` → same as `[1]`.

A `PATH:RANGE_STRING` lookup defers to the chunker registered for
the file: the chunker matches the range against its stored chunks.
This is the form the curation workshop emits when authoring an
`@ext` against a specific PDF chunk or a non-`@id`-bearing markdown
section.

### Why bare path = preamble

A `@food: hamburger` `@ext`'d to a whole file should appear once at
the top, not on every chunk of the file. The first-chunk convention
matches how readers scan a document and aligns with markdown's
preamble semantics: the "topic" of the file lives before any heading.

## Relative paths

A `PATH` base is **absolute** if it starts with `/` or `~/` (after
tilde expansion). Otherwise it is **relative to the source file's
directory** — the directory containing the file in which the `@ext:`
declaration appears.

UUID bases are unaffected. The relative/absolute distinction applies
only to `PATH` bases.

Resolution is **eager absolutization**: the V record stores the
TARGET as the user wrote it (relative stays relative, `~/` stays
`~/`); `ResolveExtTarget` substitutes `$HOME` for `~/` and joins a
relative target against `sourceDir` only when looking up the file.
Display and tooling see the user's intent; moving the source
document elsewhere keeps the relative link working because
resolution always re-bases against the current source document path.

The in-memory anchor map (`extByAnchor`, see `crc-ExtMap.md`) is
keyed by the **absolutized** form so that target-side lookups —
which only know absolute paths from microfts2 — find sources that
wrote relative paths. The map keys diverge from the V record value
text, which is fine: the absolutize step happens every time anyone
hits the anchor map.

Normalization is **minimal**: leading `~/` → `$HOME/`, and
`filepath.Join(sourceDir, target)` for relative bases. No `Clean`,
no `EvalSymlinks`, no case folding — same rule microfts2 follows
for path storage.

### Directory portability

The source-relative rule makes a tree of mutually-`@ext`-ing files
self-contained. Copy `notes/` to `notes-archive/` and every
`@ext: relative-path` inside resolves against the new location.
The original and the copy live independently:

- Copying preserves the routing. The copy's `@ext` lines re-base
  against the copy's directory.
- Deleting or renaming the original does not affect the copy.
- Edits to the copy do not affect the original.

This is the "portable bundle" property — directories of notes
behave as movable units, the same way relative markdown links
do. Absolute and `~/`-based paths break under copy because they
re-anchor to the original location; the relative form is the
right default for cross-referenced note trees.

A practical instance: **shareable annotated PDFs.** A PDF is
read-only (binary), so `@ext` lines can't live inside it.
Instead, author a sibling markdown file in the same directory:

```
papers/coroutines.pdf
papers/coroutines.notes.md     # contains @ext: coroutines.pdf ...
```

`coroutines.notes.md` uses a relative path to reach the PDF, so
the pair travels as a unit. Send the directory to someone else,
they index it on their machine, the same annotations land on
the same PDF chunks. The workshop's own authoring path (mirror
files under `~/.ark/external/`) is per-user and uses absolute
paths; this hand-authored pattern is the portable counterpart.

## Chunker contract

A chunker's range string identifier (the value returned in
`Chunk.Range`) **should not start with `"` or `/`**. This is what
keeps the anchor disambiguation rule unambiguous: the first
character after `:` always distinguishes quoted-string / regex /
range-string. The contract holds naturally for current chunkers
(markdown line ranges start with digits, PDF positions start with
page number, bracket-chunker line ranges start with digits).

The contract is **soft**, not mandatory — graceful degradation
over refusal. If a chunker (a user-configured one in
`ark.toml`, or a future variant) emits a range that starts with
`"` or `/`:

- The chunk **still indexes normally**. The range string is
  metadata; it does not affect content indexing or search.
- The chunk **is still searchable** by all normal means (FTS,
  filters, tag queries).
- The chunk **can still be the target of `@ext`** via every
  other locator form: bare path, `:string` literal match,
  `:/regex/` match, UUID, or any of the modifier forms.
- The **only** thing unavailable is the `:RANGE_STRING` form of
  `@ext`, because the parser would (correctly) read the leading
  `"` or `/` as a different anchor type.

The system surfaces the non-conformance loudly to the user
(workshop UI flags it on the affected chunk, suggesting an
alternate locator). User data access stays whole; only an
advanced feature degrades.

This reflects ark's broader principle: **the user's access to
their own data is primary.** Non-conforming or unexpected data
never causes ark to refuse to index, search, or render it. When
a feature can't be applied because data doesn't fit, the feature
degrades visibly; the data stays accessible.

## Out of scope for this spec

- Rendering the corner-chip / search-result indicator's
  re-render-on-relevant-change. Tracked in PLAN.md under the
  manual-curation item: verify tag-overview re-renders on file
  changes that affect it.
