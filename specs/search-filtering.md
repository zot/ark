# Search Filtering

Two-pass scoped search. A filter query narrows the file set before
the main query runs. Composes with path-based file filtering.

> **Flag names vs. mechanism.** The user-facing `ark search` flag
> *names* in this doc (`--filter`, `--except`, `--filter-files`,
> `--exclude-files`, `--filter-file-tags`, `--exclude-file-tags`) were
> **removed** from `ark search` and subsumed by the unified filter
> stack — see [search-cli-filters.md](search-cli-filters.md), which
> owns the user-facing `ark search` filter syntax (`-contains`, `-tag`,
> `-files`, …) and the alias map. What persists, and what this spec
> documents, is the underlying file-ID filter **mechanism**: the
> `SearchOpts` structural fields (`FilterFiles`/`ExcludeFiles`, used by
> the Lua UI for sidebar source filtering), the `search_exclude` config
> (this doc is its canonical home), and the subscription/pubsub filter
> flags. Read the flag-named sections below as describing that
> mechanism, not current `ark search` CLI surface.

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

`--filter-files <pattern>` restricts search to files whose paths
match the glob pattern. Multiple patterns supported (OR logic —
file matches any pattern is included).

`--exclude-files <pattern>` excludes files whose paths match the
glob pattern. Multiple patterns supported (OR logic — file matches
any pattern is excluded).

These replace `--source` and `--not-source` entirely. Path filtering
is more general — it matches against the full file path, not just
the source directory name. No backward compatibility needed; nobody
uses `--source`/`--not-source` yet outside of testing.

Examples:
- `--filter-files "*.md"` — only search markdown files
- `--filter-files "/home/deck/work/*"` — only files under work/
- `--exclude-files "*.jsonl"` — skip conversation logs

## Composition

All filters produce file ID sets and compose:
- Positive content filters (`--filter`) intersect
- Negative content filters (`--except`) subtract
- Positive path filters (`--filter-files`) intersect
- Negative path filters (`--exclude-files`) subtract

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
- `--filter-file-tags "status"` combined with `--filter-files "requests/*"` — message files with status tags

## CLI

- `--filter <query>` — repeatable, content-based positive filter
- `--except <query>` — repeatable, content-based negative filter
- `--filter-files <pattern>` — repeatable, path-based positive filter
- `--exclude-files <pattern>` — repeatable, path-based negative filter
- `--filter-file-tags <tag>` — repeatable, tag-based positive filter
- `--exclude-file-tags <tag>` — repeatable, tag-based negative filter

Quoting and backslash escaping supported (handles paths with spaces).
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

Subscriptions should respect `search_exclude` too, via
`--except-files` defaults. A subscription without explicit file
filters inherits `search_exclude` as its except-files list.
A subscription with explicit `--filter-files` or `--except-files`
uses those instead.

### Naming normalization

Pubsub uses `--except-files` and `ExceptFiles` while search uses
`--exclude-files` and `ExcludeFiles`. Normalize to `exclude-files`
and `ExcludeFiles` everywhere. The pubsub CLI flag becomes
`--exclude-files` (rename from `--except-files`). The internal
struct field becomes `ExcludeFiles`. JSON wire format becomes
`exclude_files`.

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
