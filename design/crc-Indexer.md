# Indexer
**Requirements:** R36, R37, R38, R39, R40, R41, R42, R43, R44, R117, R118, R121, R126, R360, R361, R362, R363, R364, R365, R366, R367, R368, R386, R502, R503, R505, R511, R517, R518, R519, R520, R521, R522, R751, R752, R757, R795, R796, R797, R866, R868, R869, R870, R872, R933, R934, R935, R953, R954, R956, R873, R1009, R1018, R1019, R1021, R1022, R1037, R1038, R1103, R1106, R1113, R1114, R1115, R1116, R1119, R1120, R1121, R1122, R1123, R1124, R1125, R1126, R1128, R1317, R1318, R1319, R1320, R1321, R1322, R1323, R1324, R1325, R1849, R1850, R1851, R1852, R1853, R1854, R1869, R1890, R1891, R1892, R1893, R1894, R1895, R1896, R1897, R1898, R1899, R1900, R1901, R1904, R1905, R1906, R1907, R1908, R1923, R1926, R1996, R2000, R2001, R2002, R2003, R2004, R2005, R2006, R2007, R2008, R1983, R1984, R2012, R2016, R2018, R2022, R2024, R2026, R2110, R2111, R2112, R2365, R2374, R2427, R2696, R2729, R2864, R2913, R2915, R2977, R3008, R3050

Coordinates adding, removing, and refreshing files. Drives microfts2
indexing, manages orphan-EC cleanup via callback, extracts tags from
file content, and updates the Store. Embeddings are written
asynchronously by Librarian.BatchEmbedChunks post-reconcile —
indexer doesn't write EC records itself. (R1923, R1926)

`ParseExtTarget(value) (target, tags, ok)` lives in `ext.go` next to the
chunkAccumulator. It splits an `@ext:` value into the TARGET substring
plus the chain of routed `TagValue` entries. The TARGET/tag boundary
scanner is **anchor-aware**: when scanning for the first `@tag:`
boundary, it skips over `"..."` and `/.../` spans so that an embedded
`@tag:` inside a target anchor is not mistaken for the start of the
tag list. Anchor openers are only consumed when they appear immediately
after a base's `:` (the only legal anchor start position); openers
elsewhere in the TARGET span are treated literally. ParseExtTarget
returns TARGET as authored text — base / modifier / narrower
decomposition happens in `DB.ResolveExtTarget` at resolve time so
the V record stores exactly what the user wrote. (R1983, R1984, R2365)
A reserved `insight: "..."` metadata field (no `@` sigil) may lead an
`@ext-candidate` value, before the TARGET; `ParseExtTarget` peels it via
`stripLeadingInsight`, skipping the quoted span so it may contain `@` or
`:`, and it is excluded from the routed-tag list. Leading-and-quoted
avoids ambiguity with an undelimited, possibly-spacey TARGET. (R3050)

The indexer threads the **source file's absolute directory** through
`runExtRouting` → `collectIndexExtPlans` / `collectReresolvePlans` so
relative `PATH` bases can be absolutized at resolve time. Derived
once per file via `db.fileIDPath(sourceFileID)` and cached for the
loop. Re-resolution paths recover the source path from
`extSource[tvid_ext]` → `chunkFileID` → `fileIDPath`. (R2374)

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

Overlay (`tmp://`) sources route through `runOverlayExtRouting`
(invoked from `DB.AddTmpFile`), which opens a **read-only**
`db.View` and threads that txn into `applyIndexExt`. An overlay
source can route to a *persistent* target whose fileid must be read
(`chunkFileID` → `fts.ReadCRecord`, for the self-reference check and
`fileidToTvids`), and that read needs a live txn even though the
overlay source writes no index records — `bothPersistent` is always
false, so the read-only txn suffices and the `TvidTxn` stays nil. The
cleanup counterpart (`CleanupSource` for an overlay source) keeps its
nil txn because it branches on `bothPersistent` before any index
access. (R2915)

## Knows
- fts: *microfts2.DB — trigram engine
- store: *Store — tag storage and EC/EF records (R1923)
- pubsub: *PubSub — notified after tag extraction (nil if no server)
- recallWatcher: *RecallWatcher — optional post-append hook;
  always present when the server is running (the watcher gates
  itself by reading `[recall].enabled` on each call). The
  indexer's `executeRefresh` isAppend branch calls
  `recallWatcher.OnAppend(path, strategy, newBytes, added)`
  after the chunk commits and the tag-value writes complete
  (R2696, R2729).

## Does
- AddFile(path, strategy): add to microfts2 via AddFileWithContent with
  WithIndexedChunkCallback (R1904). indexedCallback re-extracts per-chunk
  tags from each new chunk's original content (R2913) for chunkid-keyed
  F/V/T writes (R1904). File-level tags + defs for pubsub/
  schedule and D-record writes are re-extracted from the returned content
  via fileLevelTags (R1926, R2913). Embeddings for
  newly-indexed chunks are written later by Librarian.BatchEmbedChunks.
  Eliminates splitChunks call (R1123).
- RemoveFile(path): resolve path to fileid via microfts2, use
  RemoveFileWithCallback to atomically delete orphaned EC records
  via Store.DeleteChunkEmbeddingInTxn and delete the EF centroid
  via Store.DeleteFileCentroidInTxn. Remove tags and V records.
  (R1850, R1852, R1853)
- RemoveByID(fileid): same callback pattern as RemoveFile. (R1851)
- RefreshFile(path): check for append-only change first. If append:
  use AppendChunks path. Otherwise full re-add to microfts2 via
  ReindexWithCallback to atomically delete orphaned EC records and
  track new chunkIDs (R1849, R1852, R1854). Per-chunk tags arrive via
  WithIndexedChunkCallback (re-extracted from each chunk's original content, R2913);
  file-level tags + defs are re-extracted from prep.data in
  executeFullRefresh (R1126, R2913). Eliminates splitChunks call (R1124).
- RefreshStale(patterns): get stale files from microfts2, optionally filter
  by patterns, refresh each one in parallel. Worker pool (NumCPU goroutines)
  reads files and detects appends. Per-chunk tags arrive via the indexed
  callback (re-extracted from each chunk's original content, R2913); file-level tags + defs are
  re-extracted in executeRefresh from prep.data (full) or prep.newBytes
  (append) (R1126, R2913). ChanSvc actor serializes all index writes.
  Errors skip the file and log a warning.
- DetectAppend(path, fileid): get FileInfo from microfts2, check
  FileLength > 0, stat file for growth, hash first FileLength bytes,
  compare to stored ContentHash. Returns true if append-only.
  (Future: back-seek from last chunk for unclean boundary detection.)
- Unchanged-skip (R2864): when a change is not an append, prepareRefresh
  compares the already-read `data` to the stored FileInfo — if
  `len(data) == FileLength` and `sha256(data) == ContentHash` it sets
  `prep.unchanged`, and executeRefresh returns without re-chunking.
  Skips the expensive no-op full refresh on redundant / no-growth events
  for large append-only files (chat JSONLs); an in-place edit fails the
  hash and still full-refreshes.
- AppendFile(path, fileid, strategy): read new bytes from FileLength
  to EOF, parse last ChunkRange for base line, call AppendChunks
  with WithBaseLine/WithContentHash/WithModTime/WithFileLength and
  WithIndexedChunkCallback (R1894). Embedding refresh is handled by
  Librarian on the next BatchEmbedChunks pass. Per-chunk tags arrive via
  the indexed callback (re-extracted from each chunk's original content, R2913); file-level tags +
  defs are re-extracted from the appended bytes (fileLevelTags).
  Store.AppendTagValues writes chunkid-keyed F/V/T records for
  newly-inserted chunks. (R1894, R1923, R2913)
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
- (substrate v3, R2913) **Full-text trigram; tag-strip at embed only.** The
  trigram index is full-text — microfts2 indexes chunk content verbatim (no
  ContentTransform; the hook was rolled back), so an FTS query for a literal
  tag (`@note: bubba`) finds the chunk carrying it. ark strips ark-tag spans
  (`stripArkTags` — a full-line tag also drops its trailing newline, an inline
  tag keeps the rest of its line) only on the **meaning axis**, in
  `Librarian.BatchEmbedChunks`, so the EC embedding is tag-free while the
  trigram index and retrieval keep the original content. A chunk that is all
  `@tag:` lines strips to empty and is skipped at embed (the tag axis carries
  it). Per-chunk tags reach the indexer by re-extracting (`ExtractTagValues`)
  from each new chunk's **original** content in `indexedCallback`, written as
  chunkid-keyed F/V records; a content-dedup'd chunk shares the chunkid and
  its F/V records, so the single fire that lands suffices. File-level tags +
  defs are re-extracted directly from the source bytes (`fileLevelTags`).
  ark's F/V records are the canonical per-chunk tag store — no tags are
  duplicated into microfts2 Attrs. Tag *recognition*
  (ExtractTagValues/ExtractTagDefs, R1317-R1325) is unchanged. Forces a
  one-time re-index + re-embed (operator-run). Within-file duplicate *defs*
  still collapse (deferred — ARK-STATE.md #14).
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

## Collaborators
- microfts2.DB: file identity, trigram indexing, staleness detection
- Store: tag record storage (T/F/V prefix keys), EC/EF record cleanup via callback
- Config: schedule tag declarations for date indexing
- PubSub: notified after tag extraction (Publish call)
- Librarian: writes EC/EF records post-reconcile (BatchEmbedChunks); not invoked synchronously from the Indexer (R1923)
- ExtMap: orchestrates @ext routing during the indexed-chunk
  callback, the reindex callback, and the source-side orphan
  callback
- RecallWatcher: post-append hook for `chat-jsonl` sources;
  the indexer calls `OnAppend(path, strategy, newBytes, added)`
  with the freshly-appended bytes and the chunkIDs the chunker
  emitted, letting the watcher run its turn-boundary state
  machine (R2696, R2729)

## Sequences
- seq-add.md
- seq-server-startup.md
- seq-pubsub.md
- seq-ext-routing.md
- seq-recall-watcher.md
