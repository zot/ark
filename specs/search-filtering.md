# Search Filtering

Two-pass scoped search. A filter query narrows the file set before
the main query runs. Composes with path-based file filtering.

> **This spec owns the mechanism, not a CLI surface.** The user-facing
> `ark search` flag names it once used (`--filter`, `--except`,
> `--filter-files`, `--exclude-files`, `--filter-file-tags`,
> `--exclude-file-tags`) are **gone**, subsumed by the unified filter
> stack — see [search-cli-filters.md](search-cli-filters.md), which owns
> the user-facing syntax (`-contains`, `-tag`, `-files`, …) and the alias
> map. What persists, and what this spec documents, is the underlying
> file-ID filter **mechanism**: the `SearchOpts` structural fields
> (`FilterFiles` / `ExcludeFiles`, set by the Lua UI for sidebar source
> filtering), the `system_exclude` and `search_exclude` configs (this doc
> is their canonical home), and the pubsub subscription filters. Sections below name the
> structural fields, never retired flags.

## Content filtering

`Filter` runs a preliminary FTS search for each of its queries. The
matching file IDs become the initial scope for the main search. Multiple
`Filter` entries intersect (all must match). The main query then searches
only within that file set.

`Except` runs a preliminary FTS search and subtracts those file IDs from
the scope. Multiple `Except` entries union (any match is excluded).

These are file-level: a file is in or out by whether *any* of its chunks
matched. The filter stack's `-with`/`-without -contains` rows are chunk-level
predicates, so they are not a spelling of these fields.

Examples (as `SearchOpts`):
- `Filter: ["@project: ark"]` with the query `@note: fred` — only
  `@note: fred` hits from files that have a `@project: ark` chunk
- `Except: ["draft"]` with the query `final version` — exclude files
  containing "draft"

## Path filtering

`SearchOpts.FilterFiles` restricts search to files whose paths match a
glob. Multiple patterns are OR'd — a file matching any one is included.

`SearchOpts.ExcludeFiles` excludes files whose paths match a glob, also
OR'd.

Path filtering matches against the full file path, not just the source
directory name, which is why it replaced the older `--source` /
`--not-source` pair entirely.

**Anchoring depends on who set the field.** CLI callers anchor to the
client's current directory before the request is sent; the Lua/MCP and
`search_exclude` callers are rootless, so a bare pattern means any depth.
Both run through the one shared matcher. See
[main.md](main.md#glob-patterns).

Examples (as reaching the mechanism, already anchored):
- `/**/*.md` — only search markdown files, at any depth
- `/home/deck/work/*` — only files directly under work/
- excluding `/**/*.jsonl` — skip conversation logs

## Composition

All filters produce file ID sets and compose:
- Positive content filters (`Filter`) intersect
- Negative content filters (`Except`) subtract
- Positive path filters (`FilterFiles`) intersect
- Negative path filters (`ExcludeFiles`) subtract

Evaluation order: path filters first (cheap — no FTS needed), then
content filters. The combined file ID set is passed to microfts2
as WithOnly, with negative filters subtracted from the set before
it is passed. The negatives-only case still uses WithOnly, not
WithExcept.

## Tag filtering

`FilterFileTags` restricts search to files that contain the given tag. It
uses the tag index to resolve file IDs, with no chunk scanning, so it is much
faster than a regex for `@tag:` at file level. `ExcludeFileTags` excludes
files that contain the given tag, by the same mechanism. At the CLI the
filter stack's `-file-tag` row is the file-level tag filter
([search-cli-filters.md](search-cli-filters.md)).

Multiple patterns supported (same composition rules as other filters).
Tag argument matches tag names, not values.

Examples (as `SearchOpts`):
- `FilterFileTags: ["request"]` — only search request files
- `ExcludeFileTags: ["msg"]` — skip files with legacy `@msg` tags
- `FilterFileTags: ["status"]` with a `requests/*` path filter — message
  files with status tags

## Mechanism fields

Each is repeatable and composes per the rules above:

- `Filter` — content-based positive filter
- `Except` — content-based negative filter
- `FilterFiles` — path-based positive filter
- `ExcludeFiles` — path-based negative filter
- `FilterFileTags` — tag-based positive filter
- `ExcludeFileTags` — tag-based negative filter

The CLI surface that populates these is the filter stack
([search-cli-filters.md](search-cli-filters.md)); quoting and backslash
escaping are handled there (paths with spaces).
Works with combined search, split search, and tag search.

## Default excludes: the fence and the noise filter

Some indexed content should not come back as an answer. It falls into
two kinds that want opposite treatment, so there are two settings.

- **`system_exclude`, the fence.** Files ark writes for its own
  bookkeeping, which nothing should ever serve as evidence. Default: the
  scheduler's log directory, `<db>/schedule/**` (`~/.ark/schedule/**` in a
  default install). It holds on every
  surface that serves indexed content: search in every mode (tag search
  included), recall on every leg, the bloodhound's seeds and hunts, tag-value
  lookups, and pubsub subscriptions.
- **`search_exclude`, the noise filter.** Content that is valuable memory
  but floods interactive results. Default: `["~/.claude/projects/**"]`,
  the conversation logs. It applies to interactive search (the CLI, the
  server's search API, the Lua UI), tag search, tag-value lookups, and pubsub
  subscriptions. It does **not** apply to recall or the bloodhound: the
  conversations are part of the memory those features exist to reach.

Both are top-level lists of rootless glob patterns
([main.md](main.md#glob-patterns)). **A list in ark.toml replaces the
built-in default; it never adds to it.** Leaving a key out, or commenting it
out, means the default. To keep the default and add to it, write the
default's patterns into the list too. An empty list `[]` turns that setting
off.

```toml
system_exclude = ["~/.ark/schedule/**"]
search_exclude = ["~/.claude/projects/**", "~/notes/drafts/**"]
```

**The fence is narrow on purpose.** It names directories one by one, never
the whole ark home, because the ark home also holds content people search
for (patterns, skills, app data, the global `@ext` mirror tree
`~/.ark/external/`). `~/.ark/chimes.md` is ark-maintained but stays outside
it: a fired chime carries that file's path, so fencing it would silence every
chime subscriber. Neither setting removes anything from the index, and the
scheduler reads its logs directly, so the fence never affects scheduling.

**External tags pass the noise filter.** A tag value that reaches a chunk
through an `@ext` routing ([at-ext-storage.md](at-ext-storage.md)) is never
removed by `search_exclude`, on any surface it applies to, even when the
target chunk's file matches it. A tag written inline in a conversation is
almost always a mention (someone talking about `@status:`, a quoted
watermark), and the risk of mistaking a mention for a use is too high to let
inline chat tags into tag search or subscriptions. A routing made in a mirror
file is a deliberate use, made to keep an association. So the test is how the
tag value arrived, not where its chunk lives. The fence still applies to
routed tags.

**Embedding.** The batch embedding pass skips files matching `search_exclude`
([chunk-embeddings.md](chunk-embeddings.md)), so with the default the
conversation logs are not embedded. `system_exclude` does not gate embedding.

### How the noise filter yields to explicit file filters

The `search_exclude` patterns apply as the default exclude scope. They are
**not applied** when the search carries an explicit *narrowing* file
filter — a positive `-files GLOB` in the filter stack, or a structural
`filter_files`/`exclude_files` (the `SearchOpts` fields the Lua UI
sets). Such a filter *replaces* the default scope entirely (R940):
when the caller has narrowed to an explicit file set, the global
excludes are irrelevant, so a positive `-files` pointed at a normally
`search_exclude`-hidden path includes it. A `-without -files GLOB`
only *adds* a subtraction and leaves `search_exclude` in effect. The
filter stack's user-facing surface is specified in
[search-cli-filters.md](search-cli-filters.md); this is its config home.

### How the fence yields, and where it does not

No file filter breaches the fence, positive or not. **Only `ark search
--system` lifts it**, for that one search, so a person can still look at the
scheduler's logs (`ark search --system -files '~/.ark/schedule/*'`). The flag
is the only way past it and is never implied: an agent that copies a scope
into `-files` (the bloodhound's secretary does) cannot reach fenced files by
accident. Nothing else lifts it: not the Lua UI's structural `filter_files`,
not recall, not the bloodhound, not a subscription.

### Subscriptions

A subscription always honors the fence. For the noise filter it follows the
search rule: a subscription without explicit file filters inherits
`search_exclude` as its exclude list, and one with explicit `-files` rows uses
those instead. The inherited list is the current one, so a config reload
reaches subscriptions made before it. Either way an ext-routed tag value is
delivered even when its target chunk's file matches `search_exclude`
([pubsub.md](pubsub.md#ext-routed-tags)).

### Naming normalization

Pubsub once used `ExceptFiles` while search used `ExcludeFiles`.
`ExcludeFiles` is the normalized name everywhere — struct field and
`exclude_files` on the JSON wire. Subscriptions take their patterns
from the same `-files` filter stack as every other command.

## Server API

Filter fields pass through the server proxy via searchRequest JSON:
```json
{
  "filter": ["@project: ark"],
  "except": ["draft"],
  "filterFiles": ["*.md"],
  "excludeFiles": ["*.jsonl"],
  "filterFileTags": ["status"],
  "excludeFileTags": ["msg"]
}
```
