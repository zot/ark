# Fuzzy Search

Typo-tolerant search via microfts2's `SearchFuzzy`.

## Why

Standard search uses trigram intersection (AND semantics) — a single
character typo destroys up to 3 trigrams and causes zero results.
`SearchFuzzy` uses OR-union of trigrams with posting-list tally, then
re-scores top-k candidates from C records. This catches single-char
typos while staying fast.

This is distinct from `--multi` (multiple scoring strategies on the
same AND-intersection candidates) and `WithLoose()` (OR across terms).
`SearchFuzzy` is a separate search method with its own candidate
collection strategy.

## CLI flag

`ark search --fuzzy <query>` calls `SearchFuzzy` instead of the
default `SearchCombined`.

`--fuzzy` is mutually exclusive with `--multi`, `--score`, `--about`,
`--regex`, `--like-file`, and `--contains`. It is a standalone search
mode that takes a positional query, like `--multi`.

`--fuzzy` composes with:
- All filter flags (`--filter-files`, `--exclude-files`,
  `--filter-file-tags`, `--exclude-file-tags`, `--filter`, `--except`)
- `--proximity` (reranking on top-k results)
- `--no-tmp`
- `-k` (max results)
- `--chunks`, `--files`, `--tags`, `--scores`, `--wrap`, `--preview`
- `--after`, `--before`

## Go API

`Searcher.SearchFuzzy(query string, opts SearchOpts)` wraps
`microfts2.SearchFuzzy(query, k, ...searchOpts)`. It resolves
filters, applies proximity reranking if requested, and runs the
standard filterAndResolve pipeline.

## Grouped search

`SearchGrouped` should support `--fuzzy` for the app UI, dispatching
to `SearchFuzzy` when `opts.Fuzzy` is true.

## Server proxy

When a session is active, `--fuzzy` proxies to the server like other
search modes. The `Fuzzy` field is added to the search request JSON.
