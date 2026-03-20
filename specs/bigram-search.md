# Bigram Search

Character-level fuzzy matching via microfts2's bigram index.

## Why

Trigram matching misses near-miss words where a single character
changes — "ran" vs "run" share zero trigrams. Bigrams (2-byte keys)
catch these: they share more overlap for stem variations and typos.

microfts2 v3 adds a bigram index (B records alongside T records).
Bigrams are enabled by default on new databases. Existing databases
need a rebuild to gain bigram support (v2 → v3 format change).

## Bigram as a search strategy

Bigram scoring is a new strategy for `--multi` search. It slots in
alongside coverage, density, overlap, and bm25. When bigrams are
available in the index, `buildStrategies` adds a "bigram" strategy
that scores chunks by bigram overlap with the query.

The bigram strategy uses a different key type (2-byte vs 3-byte),
so microfts2 provides `StrategyBigramOverlap` which tells
`SearchMulti` to pass bigram counts instead of trigram counts to
the scorer.

## Strategy API change

microfts2 v3 changes `SearchMulti` to accept
`map[string]SearchStrategy` instead of `map[string]ScoreFunc`.
Existing score functions are wrapped with `StrategyFunc`. The
bigram strategy uses `StrategyBigramOverlap`.

Ark's `buildStrategies` must:

1. Return `map[string]SearchStrategy` instead of `map[string]ScoreFunc`
2. Wrap each existing score function with `StrategyFunc`
3. Extract query bigrams via `db.QueryBigramCounts(query)`
4. Add `"bigram": StrategyBigramOverlap(queryBigrams)` when
   `db.Settings().BigramsEnabled`

## DB creation

No changes needed — bigrams are always on in microfts2 v3.
Existing databases gain bigram support after `ark rebuild`.

## Single-query bigram search

`WithBigramOverlap()` is available as a search option for
single-query search. This is not exposed as a CLI flag — bigram
scoring is available through `--multi` only, keeping the single-
strategy interface simple.

## Checking bigram availability

`db.Settings().BigramsEnabled` reports whether the index has
bigrams. `buildStrategies` checks this and omits the bigram
strategy gracefully when bigrams are not available (pre-rebuild
databases).

## Index size

Bigrams add ~44 MB to the ark corpus (~62 MB existing → ~106 MB
total, 1.7x). The space is in fat-head bigrams (th, in, er) that
appear in most chunks, but `FilterByRatio` discards these at query
time — only rare bigrams contribute to scoring.
