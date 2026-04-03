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

Two subdatabases share the LMDB environment:

**microfts2** (the FTS engine):
- C — chunk records (one per indexed chunk)
- F — file records (one per indexed file)
- H — content hash → chunk ID mapping
- I — config/counter records (settings, totalChunks, totalTokens)
- N — filename key chain (path → file ID)
- T — trigram inverted index entries
- W — token inverted index entries

**ark** (the zettelkasten layer):
- D — tag definition records
- F — per-file tag counts
- I — settings record
- M — missing file records
- T — tag total counts
- U — unresolved file records
- V — tag value records (tag/value → fileid mapping)

## Output

The `--db` section prints after the normal status output. Each
subdatabase is a header line. Each record type shows: prefix letter,
purpose label, record count, key bytes, and value bytes.

```
db: microfts2
  C chunks          149277  keys 567.0 KB    vals 95.1 MB
  F files             4163  keys 12.1 KB     vals 29.6 MB
  H hashes          149277  keys 4.7 MB      vals 421.2 KB
  I config              16  keys 264 B       vals 104 B
  N paths             4163  keys 368.6 KB    vals 373.6 KB
  T trigrams        134021  keys 523.5 KB    vals 45.3 MB
  W tokens          307754  keys 1.5 MB      vals 7.9 MB

db: ark
  D tag-defs           101  keys 1.8 KB      vals 9.6 KB
  F file-tags         7032  keys 117.3 KB    vals 27.5 KB
  I settings             1  keys 1 B         vals 17 B
  M missing              0  keys 0 B         vals 0 B
  T tag-totals         295  keys 2.8 KB      vals 1.2 KB
  U unresolved        1738  keys 126.2 KB    vals 256.6 KB
  V tag-values        1247  keys 14.2 KB     vals 8.3 KB

db total: 757838 records, 7.8 MB keys, 178.9 MB vals (186.7 MB data in 405.4 MB map)
```

Record types are sorted alphabetically within each subdatabase.
Counts are right-aligned for readability. A total line summarizes
all records across both subdatabases with aggregate key/value sizes
and their proportion of the LMDB map.

## Server Endpoint

When proxied through the server, `GET /status?db=true` includes
the record counts in the JSON response. The CLI `--db` flag sets
this query parameter.
