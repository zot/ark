# Search CLI Filter Stack

The ark-search UI element has a composable filter stack: a primary
search plus stacked filter rows, each with polarity (with/without)
and mode (contains, fuzzy, regex, tag, files). The CLI cannot
express this. This spec adds a unified filter syntax to `ark search`
that matches the UI's capabilities.

## Filter syntax

The search command accepts mode flags that define the primary search
and any number of additional filters:

```
ark search TERM...
    [-contains TERM]...
    [-fuzzy TERM]...
    [-regex PATTERN]...
    [-tag TAG]...
    [-file-tag TAG]...
    [-about QUERY]...
    [-files GLOB]...
    [-with]
    [-without]
```

### Modes

- `-contains TERM`: substring match on chunk text.
- `-fuzzy TERM`: typo-tolerant match on chunk text.
- `-regex PATTERN`: regular expression match on chunk text.
- `-tag TAG`: match chunks whose own text carries a tag accepted by
  the predicate. TAG uses the shared sigil syntax
  `[~|:]NAME [(=|:|~) VALUE]` — see
  [file-tag-filter.md](file-tag-filter.md) for the full table.
  Bare NAME or `:NAME` / `~NAME` with no separator matches any value.
- `-file-tag TAG`: match every chunk on a file that has a matching
  tag somewhere. Same sigil syntax. A file is considered to "have"
  a tag when at least one of its chunks carries that (name, value)
  in its extracted tag set.
- `-about QUERY`: vector similarity match on chunk content.
- `-files GLOB`: match files whose path matches the glob pattern.
  An explicit `-files` filter also changes the default scope: a
  **positive** `-files GLOB` *replaces* the default `search_exclude`
  scope rather than adding to it — so a positive `-files` pointed at a
  path `search_exclude` would normally hide includes it. A
  `-without -files GLOB` subtracts the glob *without* disabling the
  default scope. With no positive `-files` (and no structural
  `filter_files`/`exclude_files`), `search_exclude` applies as the
  default exclude. The `search_exclude` config itself is owned by
  [search-filtering.md](search-filtering.md) §"Default search excludes".

### Polarity

`-with` and `-without` are state toggles. Default polarity is
`with`. Everything after `-without` gets `without` polarity until
the next `-with` resets it. `with` means the filter intersects
(chunk must match). `without` means the filter subtracts (chunk
must not match).

### Bare terms

Bare terms (no leading `-`) are shorthand for `-contains`.
Consecutive bare terms coalesce into a single `-contains` argument.
A mode flag or polarity toggle closes the current group.

### Primary search vs filters

The first filter entry becomes the primary search — it drives the
initial trigram index lookup that produces the candidate result set.
All subsequent entries become chunk-level post-filters that narrow
the results.

**Post-filtering is primary-mode-independent.** The choice of mode for
the primary entry determines *only* the initial candidate set. Every
subsequent filter row — and the default `search_exclude` scope (see
[search-filtering.md](search-filtering.md)) — applies identically to
that candidate set regardless of whether the primary is `-contains`,
`-fuzzy`, `-regex`, `-tag`, `-file-tag`, or `-about`. There is no
primary mode for which post-filters are skipped. In particular the
three index-lookup primaries (`-tag` / `-file-tag` resolve via the tag
index; `-about` ranks via embeddings) do not return their hits
directly: their candidate set passes through the same post-filter
stack and default exclude that the content-scan primaries
(`-contains` / `-fuzzy` / `-regex`) go through.

### The `-parse` flag

`-parse` prints the fully disambiguated command and exits without
searching. Each entry is shown with its explicit mode flag and
quoted value. Polarity toggles are shown at each boundary.

## Examples

Simple search (unchanged from today):
```
ark search fred
```

Bare terms coalesce:
```
ark search fred ethel
# Parsed as: -contains "fred ethel"
```

Mixed modes:
```
ark search fred -without -tag "status:done" -with -files '*.md'
# Primary: contains "fred"
# Filters: without tag "status:done", with files "*.md"
```

Coalescing resumes after mode flags:
```
ark search fred ethel -without -tag "status:done" -with -files '*.md' lucy ricky
# Primary: contains "fred ethel"
# Filters: without tag "status:done",
#          with files "*.md",
#          with contains "lucy ricky"
```

Escaping flag-like terms:
```
ark search -contains "-bubba" fred
# Parsed as: -contains "-bubba fred"
```

Everything together:
```
ark search fred ethel -without -tag "status:done" -with -fuzzy desi -files '*.md' lucy ricky
# Primary: contains "fred ethel"
# Filters: without tag "status:done",
#          with fuzzy "desi",
#          with files "*.md",
#          with contains "lucy ricky"
```

Parse output:
```
$ ark search -parse fred ethel -without -tag "status:done" -with -files '*.md' lucy ricky
ark search -contains "fred ethel" -without -tag "status:done" -with -files '*.md' -contains "lucy ricky"
```

## Removed flags

The old file-level filter flags (`--filter`, `--except`,
`--filter-files`, `--exclude-files`, `--filter-file-tags`,
`--exclude-file-tags`, `--except-regex`) are removed from
`ark search`. The filter stack subsumes them:

- `--filter TERM` → `-contains TERM`
- `--except TERM` → `-without -contains TERM`
- `--filter-files GLOB` → `-files GLOB`
- `--exclude-files GLOB` → `-without -files GLOB`
- `--filter-file-tags TAG` → `-tag TAG`
- `--exclude-file-tags TAG` → `-without -tag TAG`
- `--except-regex PAT` → `-without -regex PAT`

These fields remain in `SearchOpts` and the server JSON API
because the Lua UI uses `filter_files`/`exclude_files` for
sidebar source filtering (a structural concern separate from the
user-facing filter stack).

Non-filter flags (`-k`, `--scores`, `--session`, `--chunks`,
`--file-content`, `--tags`, `--wrap`, `--no-tmp`, `--after`,
`--before`, profiling flags) are orthogonal and compose with the
filter stack.

## Help text

The `ark search --help` output groups options by purpose:

1. Filter stack syntax and examples (top)
2. Output format flags
3. Scoring and analysis flags
4. Profiling flags

## Server endpoint

The existing `/search` endpoint (`handleSearch`) accepts a new
`chunk_filters` field in its request body. The server wires it
through `BuildChunkFilters` the same way `handleSearchGrouped`
already does. The flat `[]SearchResultEntry` response format is
unchanged.

## Language and environment

Go. The arg walker is in `cmd/ark/main.go`. The `ChunkFilterRow`
type and `BuildChunkFilters` function are in `search.go`. The
server endpoint is in `server.go`.
