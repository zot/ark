# Search Filtering

Two-pass scoped search. A filter query narrows the file set before
the main query runs. Composes with path-based file filtering.

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
as WithOnly or WithExcept.

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
