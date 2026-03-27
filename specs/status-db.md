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
- M — missing file records
- U — unresolved file records
- I — settings record
- T — tag total counts
- F — per-file tag counts
- D — tag definition records
- E — event day bucket records
- V — day bucket reverse index records

## Output

The `--db` section prints after the normal status output. Each
subdatabase is a header, each record type is a line with count and
purpose label:

```
db: microfts2
  C chunks        12847
  F files           423
  H hashes        12847
  I config             5
  N paths            423
  T trigrams      189201
  W tokens         34512

db: ark
  D tag-defs          47
  E day-buckets         0
  F file-tags        3891
  I settings            1
  M missing            12
  T tag-totals        284
  U unresolved          3
  V day-reverse         0
```

Record types are sorted alphabetically within each subdatabase.
Counts are right-aligned for readability.

## Server Endpoint

When proxied through the server, `GET /status?db=true` includes
the record counts in the JSON response. The CLI `--db` flag sets
this query parameter.
