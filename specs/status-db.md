# Status --db: LMDB Record Counts

`ark status` shows operational health (files, stale, missing). But
sometimes you want to know what's actually in the database — how many
records of each type, across both subdatabases. This is a developer
diagnostic: "what did the indexer produce?"

## Flag

`ark status --db` adds a section showing record counts grouped by
subdatabase and record type. Without `--db`, status output is
unchanged.

## Record Types

Two subdatabases share the LMDB environment. Full record key/value
layouts and the complete prefix inventory live in
[record-formats.md](record-formats.md). The list below names what
`ark status --db` tallies.

**microfts2** (the FTS engine): C, F, H, I, N, T, W. All
microfts2 prefixes are single-byte. (See microfts2 documentation
for layouts.)

**ark** (the zettelkasten layer): every prefix listed in
`record-formats.md` gets its own row — single-byte (M, U, I, T, F,
D, V, X) and multi-byte (E:, EV, EC, EF, PC). Multi-byte prefixes are
not collapsed; counting `E` as a combined bucket would make
`model_mismatch` errors and tag-value embeddings indistinguishable.

`Store.RecordCounts()` returns counts keyed by the full prefix
string. Prefix detection for each key: known multi-byte prefixes
(`E:`, `EV`, `EC`, `EF`, `PC`) are matched first; anything else
(including `X` ext-routings) falls back to its single-byte prefix.

## Output

The `--db` section prints after the normal status output. Each
subdatabase is a header line. Each record type shows: prefix letter,
purpose label, record count, key bytes, and value bytes.

```
db: microfts2
  C  chunks            155683  keys 592.0 KB    vals 120.5 MB
  F  files               5013  keys 14.6 KB     vals 42.8 MB
  H  hashes            155683  keys 4.9 MB      vals 440.0 KB
  I  config                17  keys 277 B       vals 104 B
  N  paths               5013  keys 440.1 KB    vals 446.1 KB
  T  trigrams          182969  keys 714.7 KB    vals 56.5 MB
  W  tokens            464687  keys 2.2 MB      vals 10.0 MB

db: ark
  D  tag-defs             105  keys 1.8 KB      vals 9.7 KB
  E: errors                 0  keys 0 B         vals 0 B
  EC chunk-embeds           0  keys 0 B         vals 0 B
  EF file-centroids         0  keys 0 B         vals 0 B
  EV tag-value-embeds       0  keys 0 B         vals 0 B
  F  file-tags           1973  keys 23.3 KB     vals 11.6 KB
  I  settings              17  keys 201 B       vals 4.6 KB
  M  missing                0  keys 0 B         vals 0 B
  PC page-content         767  keys 3.7 KB      vals 866.4 KB
  T  tag-totals           170  keys 1.5 KB      vals 680 B
  U  unresolved          1862  keys 129.2 KB    vals 267.3 KB
  V  tag-values          1313  keys 83.3 KB     vals 5.8 KB
  X  ext-routings           0  keys 0 B         vals 0 B

db total: 975272 records, 9.1 MB keys, 231.8 MB vals (240.9 MB data in 489.3 MB map)
```

Record types are sorted alphabetically within each subdatabase.
Counts are right-aligned for readability. A total line summarizes
all records across both subdatabases with aggregate key/value sizes
and their proportion of the LMDB map.

## Server Endpoint

When proxied through the server, `GET /status?db=true` includes
the record counts in the JSON response. The CLI `--db` flag sets
this query parameter.
