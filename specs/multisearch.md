# Multisearch

Multi-strategy search for the Luhmann agent and broad recall.

## Why

A single scoring strategy misses things. Coverage scoring finds exact
matches but misses paraphrases. Density scoring finds thematic overlap
but buries precise hits. BM25 balances frequency and rarity but
ignores position. The Luhmann research agent needs to cast a wide net
and triangulate — running one query through multiple strategies to
surface results that any single strategy would miss.

microfts2 now provides `SearchMulti` which collects candidates once
(single LMDB transaction) and scores them with each strategy
independently. Ark needs to expose this through the CLI and integrate
it into the search pipeline.

## Multi-strategy search

`ark search --multi` runs the query through multiple scoring
strategies in a single pass. Each strategy returns its own best-K
results. Ark deduplicates and merges them, keeping the best score
per chunk across all strategies.

The strategies used by `--multi`:

- **coverage** — fraction of query trigrams present. Strict AND.
- **density** — token density relative to chunk size. Fuzzy OR.
- **overlap** — raw count of matching trigrams. Simple fuzzy.
- **bm25** — TF-IDF ranking. Balances frequency and rarity.

All four are pure index lookups — no chunk text needed for scoring.

`--multi` composes with existing flags:
- Filters (`--filter-files`, `--exclude-files`, `--filter-file-tags`,
  `--exclude-file-tags`, `--filter`, `--except`) apply to all
  strategies equally.
- `--chunks`, `--files`, `--wrap`, `--tags`, `--scores` work normally
  on the merged results.
- `-k` applies to the final merged set, not per-strategy.
- `--multi` is mutually exclusive with `--score` (which selects a
  single strategy).
- `--multi` works with combined search (query arg) and `--contains`.
  It does not apply to `--regex`, `--about`, or `--like-file`.

## Proximity reranking

`--proximity` reranks the top results by how close the query terms
appear to each other in the chunk text. This is a post-filter — it
reads chunk text for the top candidates and adjusts their scores
based on minimum term span.

`--proximity` works with any search mode including `--multi`. When
used with `--multi`, proximity reranking happens after the
multi-strategy merge.

The number of candidates to rerank defaults to 2x the `-k` value.
This gives proximity enough candidates to work with while keeping
the text-reading cost bounded.

## Strategy tagging

When `--scores` is active with `--multi`, each result includes which
strategy produced it (or "multi" if multiple strategies found the same
chunk). This lets the Luhmann agent understand why something was found.

## SearchMulti Go API

Ark wraps microfts2's `SearchMulti` for internal callers:

```go
func (s *Searcher) SearchMulti(query string, opts SearchOpts) ([]SearchResultEntry, error)
```

This handles filter resolution, strategy setup (including BM25
initialization from index counters), deduplication, proximity
reranking if requested, and the standard resolve/filter pipeline.

SearchGrouped should also support multi-strategy search for the UI.
