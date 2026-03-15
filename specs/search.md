# Search

## Combined search

Both engines query the same text. Results merged and re-ranked.

- microfts2 returns file/chunk matches with trigram scores
- microvec returns file/chunk matches with cosine similarity scores
- Ark merges by (fileid, chunknum), combining scores
- Results sorted by combined score descending
- Output: filepath:startline-endline with score

## Split search

Targeted queries for one or both engines:

- `--about <text>` goes to microvec (semantic)
- `--contains <text>` goes to microfts2 (exact)
- `--regex <pattern>` goes to microfts2 (regex)
- `--contains` and `--regex` compose: `--contains` drives FTS, `--regex` post-filters
- Either flag works alone (single-engine search, no intersection)
- Both `--about` + `--contains`/`--regex` — results intersected
  by (fileid, chunknum)
- Output: same format as combined

## Search output modes

- Default: `filepath:startline-endline` (one per line), with optional `\tscore`
- `--chunks` — emit chunk text as JSONL: `{"path", "startLine", "endLine", "score", "text"}`
- `--files` — emit full file content as JSONL for matching files
- `--wrap <name>` — wrap output in XML tags for direct context
  injection into AI sessions. The tag name is the wrapper argument:
  `--wrap memory` produces `<memory source="path" lines="start-end">content</memory>`,
  `--wrap knowledge` produces `<knowledge source="path">content</knowledge>`.
  Works with `--chunks` and `--files`. Also works on `ark fetch`.
  No post-processing needed — output drops straight into context.
  Convention: `memory` for conversation/experience, `knowledge` for
  distilled facts/notes/code.

## File similarity search

`--like-file <path>` finds files with similar content using FTS
density scoring. Reads the file content and uses it as the query.
Density scoring is designed for long queries where most tokens won't
match any given chunk — it measures how much a chunk is *about* the
query terms, not whether it contains all of them.

`--about-file <path>` finds files semantically similar using vector
search. Deferred to V4 (requires chunking the query file to fit the
embedding model's context window).

Both flags together combine FTS and vector scores using the same
merge as combined search. Until V4, only `--like-file` is available.

`--like-file` participates in split search: it can combine with
`--about` (intersect FTS file-similarity with vector text-query)
or stand alone. It is mutually exclusive with `--contains` and
`--regex` (all three are FTS queries — only one at a time).

## Tag-only search

`--tags` changes search output: instead of returning matching chunks,
it returns only the @tags extracted from those chunks. The search
itself runs normally (FTS, vector, or combined), but the output is
the tag vocabulary discovered in the results rather than the content.

Output: one tag per line with count (how many result chunks contained
it). With `--scores`, includes the best chunk score where the tag
appeared.

This lets the agent ask "what tags are relevant to X?" without
reading content — useful for navigation, topic discovery, and the
V3 inspiration engine.

## Source filtering (replaced)

Replaced by search filtering in specs/search-filtering.md.
`--source`/`--not-source` removed in favor of `--filter-files`/
`--exclude-files` (path-based) and `--filter`/`--except` (content-based).

## Scoring strategy

`--score <mode>` controls how microfts2 ranks chunks. Three modes:

- `auto` (default, when `--score` is omitted) — coverage scoring first.
  If zero results, automatically retry with density scoring (fuzzy
  escalation). This is the normal search experience: precise when
  possible, exploratory when needed.
- `coverage` — fraction of query trigrams present in chunk. Strict:
  all query terms should appear. Good for short, targeted queries.
  No escalation.
- `density` — token-density scoring. OR semantics: a chunk matches
  if it contains *any* query token, ranked by how dense the overlap
  is relative to chunk size. Good for exploratory search and long
  queries. No escalation.

Fuzzy escalation only fires in auto mode. When the user explicitly
chooses a scoring strategy, ark respects that choice without fallback.

`--like-file` always uses density scoring regardless of `--score`,
since file-content queries are inherently long and benefit from
density normalization.

## Common search options

- `-k <num>` — max results (default 20)
- `--scores` — show scores in output
- `--after <date>` — only results newer than date (time filtering)
- `--score <mode>` — scoring strategy: auto (default), coverage, density

## Search during incomplete embedding

When vector indexing is in progress, combined search compensates:
- Vector search runs against whatever embeddings exist (partial but
  not wrong — results are valid, just incomplete)
- A parallel FTS query catches files that vector hasn't reached yet
- Results merge by (fileid, chunknum), deduplicating and taking the
  best combined score
- This happens transparently — the user doesn't need to know whether
  vector indexing is complete
