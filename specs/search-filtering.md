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
> filtering), the `search_exclude` config (this doc is its canonical
> home), and the pubsub subscription filters. Sections below name the
> structural fields, never retired flags.

## Content filtering

`--filter <query>` runs a preliminary FTS search. The matching file
IDs become the initial scope for the main search. Multiple `--filter`
flags intersect (all must match). The main query then searches only
within that file set.

`--except <query>` runs a preliminary FTS search and subtracts those
file IDs from the scope. Multiple `--except` flags union (any match
is excluded).

Examples:
- `ark search --filter "@project: ark" "@note: fred"` — only return
  @note: fred hits from files that have a @project: ark chunk
- `ark search --except "draft" "final version"` — search for
  "final version" but exclude files containing "draft"

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
- Positive content filters (`--filter`) intersect
- Negative content filters (`--except`) subtract
- Positive path filters (`FilterFiles`) intersect
- Negative path filters (`ExcludeFiles`) subtract

Evaluation order: path filters first (cheap — no FTS needed), then
content filters. The combined file ID set is passed to microfts2
as WithOnly, with negative filters subtracted from the set before
it is passed. The negatives-only case still uses WithOnly, not
WithExcept.

## Tag filtering

`--filter-file-tags <tag>` restricts search to files that contain
the given tag. Uses the tag index to resolve file IDs — no chunk
scanning needed. Much faster than `--regex "@tag:"` for file-level
filtering.

`--exclude-file-tags <tag>` excludes files that contain the given
tag. Same mechanism.

Multiple patterns supported (same composition rules as other filters).
Tag argument matches tag names, not values.

Examples:
- `--filter-file-tags "request"` — only search request files
- `--exclude-file-tags "msg"` — skip files with legacy @msg tags
- `FilterFileTags "status"` combined with a `requests/*` path filter — message files with status tags

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

## Default search excludes

Some content is indexed for tags and pubsub but shouldn't appear
in search results by default. Conversation logs (`~/.claude/projects/**`),
JSONL files, schedule logs (`~/.ark/schedule/**`) — useful for
subscriptions and tag extraction, noise in search.

`search_exclude` in ark.toml is a top-level list of glob patterns:

```toml
search_exclude = [
  "~/.claude/projects/**",
  "~/.ark/schedule/**",
]
```

These patterns apply as the default exclude scope. They are
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

Subscriptions respect `search_exclude` too. A subscription without
explicit file filters inherits `search_exclude` as its exclude list; a
subscription with explicit `-files` rows uses those instead.

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
