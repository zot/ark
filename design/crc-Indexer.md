# Indexer
**Requirements:** R36, R37, R38, R39, R40, R41, R42, R43, R44, R117, R118, R121, R126, R360, R361, R362, R363, R364, R365, R366, R367, R368, R369, R385, R386, R502, R503, R505, R511, R517, R518, R519, R520, R521, R522, R751, R752, R754, R755, R756, R757, R795, R796, R797, R866, R868, R869, R870, R872, R933, R934, R935, R953, R954, R956, R873, R1009, R1018, R1019, R1021, R1022, R1037, R1038, R1103, R1104, R1105, R1106, R1113, R1114, R1115, R1116, R1117, R1118, R1119, R1120, R1121, R1122, R1123, R1124, R1125, R1126, R1127, R1128, R1317, R1318, R1319, R1320, R1321, R1322, R1323, R1324, R1325, R1849, R1850, R1851, R1852, R1853, R1854, R1869, R1890, R1891, R1892, R1893, R1894, R1895, R1896, R1897, R1898, R1899, R1900, R1901, R1904, R1905, R1906, R1907, R1908, R1923, R1926, R1996, R2000, R2001, R2002, R2003, R2004, R2005, R2006, R2007, R2008, R1983, R1984, R2012, R2016, R2018, R2022, R2024, R2026, R2110, R2111, R2112

Coordinates adding, removing, and refreshing files. Drives microfts2
indexing, manages orphan-EC cleanup via callback, extracts tags from
file content, and updates the Store. Embeddings are written
asynchronously by Librarian.BatchEmbedChunks post-reconcile —
indexer doesn't write EC records itself. (R1923, R1926)

`ParseExtTarget(value) (target, tags, ok)` lives in `ext.go` next to the
chunkAccumulator. It splits an `@ext:` value into the TARGET substring
plus the chain of routed `TagValue` entries. (R1983, R1984)

`@ext` routing is delegated to ExtMap. During the indexed-chunk
callback, for each `TagValue{Tag: "ext", Value: V}` in the chunk's
extracted tags, the indexer calls `ExtMap.IndexExt(tvid_ext,
sourceChunkID, V, sourceFileid, txn, tt)`. The source chunkID
threads in so ExtMap can compute `bothPersistent` per target.
The reindex callback (microfts2 fires once per file with
`(fileid, orphanedChunkIDs, addedChunkIDs)`) calls
`ExtMap.ReresolveOnReindex(...)` for the canonical re-resolution
flow. The orphan callback path for source chunks calls
`ExtMap.CleanupSource(sourceChunkID, tvid_ext, txn, tt)` for each
tvid_ext held in `F[source][ext]`. (R1996, R2000-R2007, R2008,
R2012, R2016, R2018, R2022, R2024, R2026)

## Knows
- fts: *microfts2.DB — trigram engine
- store: *Store — tag storage and EC/EF records (R1923)
- pubsub: *PubSub — notified after tag extraction (nil if no server)

## Does
- AddFile(path, strategy): add to microfts2 via AddFileWithContent with
  WithChunkCallback (R1113) and WithIndexedChunkCallback (R1904).
  Text-only callback feeds tags/defs/values for file-level pubsub/
  schedule and D-record writes (R1117, R1118, R1926). Indexed
  callback feeds chunkid-keyed F/V/T writes (R1904). Embeddings for
  newly-indexed chunks are written later by Librarian.BatchEmbedChunks.
  Eliminates splitChunks call (R1123).
- RemoveFile(path): resolve path to fileid via microfts2, use
  RemoveFileWithCallback to atomically delete orphaned EC records
  via Store.DeleteChunkEmbeddingInTxn and delete the EF centroid
  via Store.DeleteFileCentroidInTxn. Remove tags and V records.
  (R1105, R1850, R1852, R1853)
- RemoveByID(fileid): same callback pattern as RemoveFile. (R1851)
- RefreshFile(path): check for append-only change first. If append:
  use AppendChunks path. Otherwise full re-add to microfts2 via
  ReindexWithCallback to atomically delete orphaned EC records and
  track new chunkIDs (R1849, R1852, R1854). WithChunkCallback (R1114)
  accumulates chunks and extracts tags from clean text. Eliminates
  splitChunks call (R1124). Tag extraction moves from prepareRefresh
  to executeFullRefresh (R1126).
- RefreshStale(patterns): get stale files from microfts2, optionally filter
  by patterns, refresh each one in parallel. Worker pool (NumCPU goroutines)
  reads files and detects appends. For full refresh, tag extraction happens
  in executeRefresh via callback (R1126). For append, tags extracted in
  prepareRefresh from tagWindowForAppend (R1127). ChanSvc actor serializes
  all LMDB writes. Errors skip the file and log a warning.
- DetectAppend(path, fileid): get FileInfo from microfts2, check
  FileLength > 0, stat file for growth, hash first FileLength bytes,
  compare to stored ContentHash. Returns true if append-only.
  (Future: back-seek from last chunk for unclean boundary detection.)
- AppendFile(path, fileid, strategy): read new bytes from FileLength
  to EOF, parse last ChunkRange for base line, call AppendChunks
  with WithBaseLine/WithContentHash/WithModTime/WithFileLength,
  WithAppendChunkCallback (R1115), and WithIndexedChunkCallback
  (R1894). Embedding refresh is handled by Librarian on the next
  BatchEmbedChunks pass. Tag extraction proceeds via the callback
  pair. Store.AppendTagValues writes chunkid-keyed F/V/T records
  for newly-inserted chunks. (R1104, R1894, R1923)
- ExtractTags(content []byte): scan content with regex `@[a-zA-Z][\w.-]*:`,
  return map[string]uint32 of tagname → count. Tag name is the part
  between @ and : (lowercase). Matches anywhere in content (inline tags
  are valid).
- ExtractTagValues(content []byte, strategy string): scan content for
  `@tag:` patterns and return one `(Tag, Value)` per match — the *outer*
  tag of each line, with `Value` capturing from after `@tag:` to end of
  line. Compound semantics (the embedded `@x: y` segments inside that
  value) are NOT peeled here; each outer-tag owner — `ParseExtTarget`
  for `@ext`, future handlers for other tags — interprets its own
  embedded structure. Skips mentions per R1317-R1325. (R1317-R1325,
  R2110, R2111, R2112)
- isMention(content []byte, atPos int, markdown bool): check whether a
  tag match at the given byte offset is a mention (not a real tag).
  Four heuristics in order: (1) no preceding whitespace and not at line
  start → mention. (2) Odd quote count (backtick + double-quote) before
  `@` on the same line → mention. (3) markdown only: inside fenced code
  block (track ``` / ~~~ fence delimiters above the line) → mention.
  (4) markdown only: line starts with 4+ spaces or tab → mention.
  (R1317-R1325)
- ExtractTagDefs(content []byte): scan content for `@tag: <name> <description>`
  lines. First word after `@tag:` is the tag name, rest is description.
  Returns map[string]string (tagname → description).
- WriteDateIndex(path string, tagValues []TagValue): for each tag value
  matching a schedule tag in config, check Config.MatchesScheduleFilter(path)
  first — skip if file is outside schedule scope. Then call EnsureUpcoming
  on the scheduler. Called from AddFile, RefreshFile, AppendFile after tag
  extraction. (R866, R868, R869, R870, R872, R953, R954, R956)
- WriteDayBucketsForFile(fileid uint64, path string, content []byte):
  for each schedule tag in content, parse the date value, discretize into
  day buckets, parse @ack: entries in the same chunk, write via
  Store.WriteDayBucketsWithAcks. (R933, R934, R935, R866)

## Collaborators
- microfts2.DB: file identity, trigram indexing, staleness detection
- Store: tag record storage (T/F/V prefix keys), day-bucket storage (TD/TF keys), EC/EF record cleanup via callback
- Config: schedule tag declarations for date indexing
- PubSub: notified after tag extraction (Publish call)
- Librarian: writes EC/EF records post-reconcile (BatchEmbedChunks); not invoked synchronously from the Indexer (R1923)
- ExtMap: orchestrates @ext routing during the indexed-chunk
  callback, the reindex callback, and the source-side orphan
  callback

## Sequences
- seq-add.md
- seq-server-startup.md
- seq-pubsub.md
- seq-ext-routing.md
