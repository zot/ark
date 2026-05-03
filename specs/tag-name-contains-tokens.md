# Tag Name Contains-Tokens Filter

Enhance tag mode so contains-name queries use the T record index
instead of falling back to regex chunk scanning. Applies to both
the base query and chunk filter rows.
Language: Go (search.go, store.go, server.go). TypeScript changes
in ark-search-element.ts.

## Problem

When the user types `@~ to proj` in tag mode, the client builds a
regex like `(^|\s)@[\w.-]*to[\w.-]*proj[\w.-]*:` and sends it as
a regex query to `/search/grouped`. For filter rows, it falls back
to a regex chunk filter. Both paths bypass the tag index entirely
and bypass use/mention filtering.

The name match should be a contains-tokens search: split the name
on whitespace, require every token to appear as a substring of the
tag name. `@~[to proj]` matches `to-project`, `to-projects`,
`towards-project` — but not `project` alone (missing `to`).

## Two code paths

### Base query (drives search and scoring)

`buildTagQuery()` produces the regex that becomes the `query`
field sent to `/search/grouped`. For contains-name, this is
currently a single regex that scans all chunks via FTS.

With contains-tokens, the server resolves matching tag names from
T records first, then builds a regex that OR's the matched names.
The client sends a structured request (name tokens + value +
match modes) and the server builds the search query internally.

### Chunk filters (narrowing)

`collectChunkFilters()` emits filter rows. Contains-name tag rows
currently fall back to `mode: "regex"`. With contains-tokens, they
use a new `"tag-contains"` mode that resolves T records on the
server and filters through the tag index path.

## Go changes

### Store: tag name search by tokens

Add a method to Store that scans T records and returns tag names
where every provided token is a case-insensitive substring of the
name. Linear scan of the tag vocabulary — the T record set is
small (hundreds to low thousands of entries).

### Store: value filtering via V records

For each matched tag name, use V records (`QueryTagValues`) to
check which values exist. When the user provides value tokens,
filter V record values the same way — each token must appear as
a case-insensitive substring. V records also carry file IDs
(`TagValueFiles`), which can serve as a file-level prefilter
before chunk scanning.

### Search endpoint: tag-contains query mode

`handleSearchGrouped` accepts a structured tag query (`name_tokens`,
`value_tokens`, `name_match`, `value_match`). The server:

1. Resolves matching tag names from T records via R1467.
2. Collects chunkIDs from F records (name-only) or V records
   (name+value via R1468). V records hold chunkIDs directly —
   no FTS regex assembly is needed.
3. When no other text primary is set (no Contains/About/Regex/
   LikeFile/Fuzzy), bypasses FTS entirely: `Searcher.GroupTagChunks`
   builds GroupedResult straight from the chunkID set, resolving
   path/range via C+F record reads. Stale chunkIDs (deleted or
   replaced chunks) are silently skipped.
4. When combined with a text primary, the chunkID set overlays
   as a `WithChunkFilter` over the chosen FTS pipeline.

The V record IS the index; FTS does not need to re-derive what
the index already knows.

### Token tokenization (CLI and server)

Tag-value strings (e.g. the value half of `name:value` from a CLI
`-tag` flag) tokenize through `TokenizeTagValue`:

- Whitespace separates tokens.
- Double quotes group runs of characters as a single token,
  allowing whitespace inside: `"french toast"` → one token
  `french toast`.
- Backslash escapes the next rune anywhere — inside or outside
  quotes. `\"` produces a literal `"`; `\\` produces a literal
  `\`; `\ ` outside quotes produces a literal space within a
  token. Unmatched trailing quote or backslash is tolerated.
- Empty tokens are dropped.
- All matching is case-insensitive (V record values and tokens
  are lowercased before substring comparison in R1468).

Browser clients that send `value_tokens` as a JSON array have
already split on the client side; server-side tokenization runs
only when the value arrives as a single string (e.g. from the
CLI `-tag name:value` flag). Both paths feed `Store.MatchTagValues`,
so the matching semantics are identical regardless of where the
split happened.

### Chunk filter: tag-contains mode

Add a `"tag-contains"` mode to `ChunkFilterRow` and
`BuildChunkFilters`. The query carries space-separated name
tokens and an optional value after `:`. The filter resolves
matching tag names from T records, checks V records for value
matches, then filters chunks using the resolved exact names
via `ExtractTagValues`.

BuildChunkFilters needs access to the Store to resolve T and
V records.

## TypeScript changes

### Base query

`buildTagQuery()` stops building a regex for contains-name.
Instead, the search request includes structured fields: name
tokens, value, and match modes. The server resolves and searches.

### Chunk filters

`collectChunkFilters()` sends `mode: "tag-contains"` instead of
`mode: "regex"` for contains-name tag rows. The query format is
`token1 token2:value`.

### Highlight regexes

`buildHighlightRegexes()` and `tagRowRegex()` continue to build
client-side regexes for iframe highlighting and OR group
serialization — these don't go through the server.

## Backward compatibility

Old clients sending `mode: "tag"` with exact names continue to
work unchanged. The new mode is additive.
