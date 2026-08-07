# Search

## Combined search

Both engines query the same text. Results merged and re-ranked.

- microfts2 returns file/chunk matches with trigram scores
- The Librarian + EC chunk-embedding pipeline returns chunk matches
  with cosine similarity scores
- Ark merges by (fileid, chunkid), combining scores
- Results sorted by combined score descending
- Output: filepath:startline-endline with score
- When the embedding pipeline is unavailable (no `[embedding] model`
  configured, model file missing), combined search falls back to
  FTS-only — same shape, no semantic component

## Split search

Targeted queries:

- `--about <text>` runs the embedding pipeline (Librarian.EmbedQuery
  + chunk-level cosine ranking against EC records)
- `--contains <text>` goes to microfts2 (exact)
- `--regex <pattern>` goes to microfts2 (regex)
- `--contains` and `--regex` compose: `--contains` drives FTS, `--regex` post-filters
- Either flag works alone (single-engine search, no intersection)
- Both `--about` + `--contains`/`--regex` — results intersected
  by (fileid, chunkid)
- Output: same format as combined
- `--about` requires `[embedding] model` to be configured; otherwise it
  errors with an actionable message

## File-centroid pre-filter (config-gated)

Each file maintains a centroid embedding (EF record). When enabled,
"about" queries can use the centroid as a coarse pre-filter — files
whose centroid sits more than the configured cosine distance from
the query are skipped before chunk-level scoring.

Two `ark.toml` keys control this:

```toml
about_centroid_filter = false   # default: off (chunk-level scan only)
about_centroid_threshold = 0.3  # cosine similarity gate when filter is on
```

Default is off because file centroids hide variance: files whose
chunks scatter widely have centroids that sit in space neither
chunk inhabits, so a centroid-based filter can suppress files whose
outlier chunks would have matched. Enable for very large corpora
where the chunk-level scan cost is unacceptable.

## About filter top-K override

Each about-mode filter row keeps a top-K chunk set during the EC walk.
The default `about_filter_top_k` (200) can be overridden per row:

- **Config:** `about_filter_top_k` in `ark.toml` sets the default
  chunk count for every about filter row (already wired).
- **CLI:** `--filter-k N` (or `-filter-k N`) after an `-about` filter
  entry sets `ChunkFilterRow.K` for that row. Only meaningful for
  about-mode filters; primarily a tuning/test knob — most users will
  rely on the config default (200). If placed after a non-about entry
  or after `-with`/`-without` with no prior filter entry, warns that
  `--filter-k` has no effect.

## Search output modes

- Default: `filepath:startline-endline` (one per line), with optional `\tscore`
- `--chunks` — emit chunk text as JSONL: `{"path", "range", "score", "text"}` (with `--preview N`, `text` is replaced by a `preview` window)
- `--file-content` — emit full file content as JSONL for matching files
- `--wrap <name>` — wrap output in XML tags for direct context
  injection into AI sessions. The tag name is the wrapper argument:
  `--wrap memory` produces `<memory source="path" lines="start-end">content</memory>`,
  `--wrap knowledge` produces `<knowledge source="path">content</knowledge>`.
  Works with `--chunks` and `--file-content`. Also works on `ark fetch`.
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

The output is agent-readable markdown: the discovered tags rendered as
a bullet tree (tag → value → file → range) with per-tag chunk counts.
With `--scores`, each tag header also carries the best chunk score
where the tag appeared. The tree format, its suppression flags
(`-no-values` / `-no-chunks` / `-no-files`), and the `-json` variant
are specified in specs/tags-baby-food.md.

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

## Trigram selection never discards every trigram of the query

Before scoring, ark uses only the query's *discriminating* trigrams: one
carried by more than half the chunks is skipped, because scanning its postings
costs a great deal and narrows almost nothing. The threshold is a fraction of
the corpus size, and on a corpus of a single chunk it computes to zero. Every
real trigram occurs at least once, so at that size the rule discards all of
them and the search returns nothing at all — not "no matches found", but no
candidates to match against in the first place. One indexed note naming Neil
Armstrong cannot be found by searching for Armstrong, and the empty answer is
indistinguishable from a corpus that simply does not contain him.

A trigram is skipped only when it is **both** non-discriminating and
expensive to scan. The share answers the first question; a posting count
answers the second, and nothing was asking it. Without that second clause the
rule discards a trigram appearing in 6 of 10 chunks — saving a six-entry scan,
at the price of the whole answer — while on a large index it keeps the fattest
posting list there is, because at that size "half the corpus" is a very large
number. Ark skips only above 1,000 postings, which is inert on any index of
more than a couple of thousand chunks and rescues every small one.

And below two chunks the rule abstains entirely: the filter passes every
trigram through untouched rather than trying to discriminate. That is deliberate and
it belongs to the index layer rather than to ark — a rule that cannot
discriminate should decline, not be nudged into discriminating. Ark states the
requirement and tests that the guarantee holds; it does not implement it.

The alternative, briefly shipped and withdrawn, was to floor the threshold at
one. It reads as the same repair and is not: "skip trigrams in more than
`ratio` of chunks, except always keep a trigram appearing in exactly one
chunk" is a stranger rule, and its exception has nothing to do with the case
that motivated it. It also misbehaves away from the one ratio it was checked
at — a floor fires whenever `ratio < 1/totalChunks`, so at a low enough ratio
it keeps trigrams on a large corpus too.

This does not make every term findable on a small corpus, and it is not meant
to. A term carried by more than half of a three-chunk corpus is still skipped,
which is the rule working as intended at that size rather than a second
instance of the same defect. What abstention fixes is only the case where
"more than half" has degenerated into "all".

## Common search options

- `-k <num>` — max results (default 20)
- `--scores` — show scores in output
- `--after <date>` — only results newer than date (time filtering)
- `--score <mode>` — scoring strategy: auto (default), coverage, density

## A request the server cannot honour is refused at the door

`POST /search` validates the request before dispatching it, and a request it
cannot honour comes back **400**, never a 500. The classification is the one
[http-operations.md](http-operations.md) already states — bad input is not an
internal failure — but `/search` is an ordinary handler rather than an
operation, so it does not inherit that mapping and has to draw the line itself.

What the server can decide from the request alone is checked there: the
mutually-exclusive output modes, and whether every regex compiles — in the
primary position, in `--except-regex`, and in the **filter rows**, since only
the first `-regex` is the primary and every later one arrives as a chunk
filter. An uncompilable pattern otherwise fails several layers down, inside the
FTS search, where nothing left in the stack can tell a caller's mistake from
ark's own — so `ark search --regex '['` reported that the *server* had
malfunctioned, when the server had understood the request perfectly and found it
invalid.

A pattern is only compiled where the request says it is one. The same
parenthesis in a `-contains` filter is literal text, and rejecting it would turn
a legitimate search into an error.

The 500 was invisible while the CLI swallowed proxy errors and reported a lock
instead (see [cli-dispatch.md](cli-dispatch.md), "A failed proxy is not an
absent server"). One defect hid the other; fixing the first is what exposed
this.

## Search during incomplete embedding

When chunk embedding is in progress, combined search compensates:
- Chunk-level cosine ranking runs against whatever EC records exist
  (partial but not wrong — results are valid, just incomplete)
- A parallel FTS query catches files that the embedder hasn't
  reached yet
- Results merge by (fileid, chunkid), deduplicating and taking the
  best combined score
- This happens transparently — the user doesn't need to know whether
  embedding is complete
