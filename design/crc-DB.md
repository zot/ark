# DB
**Requirements:** R1, R2, R3, R5, R6, R7, R28, R29, R30, R33, R40, R31, R32, R34, R127, R128, R129, R136, R138, R130, R135, R137, R161, R162, R163, R166, R167, R168, R196, R197, R198, R199, R200, R236, R246, R248, R237, R238, R239, R240, R241, R242, R243, R244, R245, R247, R249, R250, R251, R252, R253, R254, R255, R257, R258, R382, R383, R392, R506, R510, R563, R564, R565, R566, R567, R568, R605, R606, R617, R618, R619, R621, R622, R624, R625, R626, R627, R628, R629, R630, R636, R637, R638, R663, R666, R667, R682, R664, R665, R668, R692, R714, R716, R719, R720, R721, R723, R765, R766, R909, R899, R904, R905, R906, R907, R908, R986, R987, R988, R989, R990, R993, R995, R1020, R1021, R1022, R1051, R1052, R1053, R1054, R1055, R1056, R1057, R1058, R1059, R1060, R1061, R1062, R1063, R1064, R1065, R1066, R1067, R1068, R1130, R1145, R1146, R1147, R1148, R1149, R1150, R1507, R1508

Main ark facade. Owns the LMDB lifecycle and coordinates microfts2,
microvec, and the ark subdatabase. Entry point for all operations.

All operations are serialized through a closure actor (ChanSvc).
The actor is an implementation detail — the public API stays unchanged
(db.Search, db.AddFile, etc.). Each method wraps itself in Svc
(fire-and-forget for watcher mutations) or SvcSync (synchronous for
operations that return results). Callers never see the channel.
Go-side caches are safe by construction — only accessed inside the
actor. Methods with synchronization delay (blocking until queued
operations complete) document this on the API. (R986, R993, R995)

## Knows
- fts: *microfts2.DB — trigram search engine
- vec: *microvec.DB — vector search engine
- store: *Store — ark's own subdatabase
- config: *Config — parsed source configuration
- dbPath: string — database directory path
- svc: ChanSvc — closure actor channel, serializes all DB access
- writeQueue: []func() — queued write closures, drained one at a time (R1053)
- writing: bool — true while a write goroutine is in flight (R1067)

## Does
- Init(path, opts): create new database — open microfts2, pass env to
  microvec, create ark subdatabase, write default config, register
  func strategies (lines, chat-jsonl, markdown), register chunker
  strategies from ark.toml [[chunker]] entries, create starter tags.md.
  Seed ark.toml from install/ark.toml via BundleReadFile if not present.
- Open(path): open existing database — same sequence, read config.
  Registers func strategies (lines, chat-jsonl, markdown). Registers
  chunker strategies from ark.toml [[chunker]] entries. Passes store
  to Indexer for tag tracking.
- registerChunkers(cfg): iterate cfg.Chunkers, construct BracketLang
  from TOML fields, call AddChunker with BracketChunker or IndentChunker
  based on type field. Warn and skip on invalid configs.
- JSONLChunkFunc: content-aware JSONL chunker — parses JSON, extracts
  text and thinking blocks, skips tool_use/tool_result/signatures/metadata.
  Extracts role attr from `type`+`isMeta` fields: human, assistant, or
  skill. For skill chunks, parses `Base directory for this skill: PATH`
  to extract skill name attr. (R1507, R1508)
- Close(): close in reverse order (store, vec, fts)
- TagList(): delegate to Store.ListTags
- TagCounts(tags): delegate to Store.TagCounts
- TagFiles(tags): delegate to Store.TagFiles, resolve fileids to paths/sizes
- TagContext(tags): delegate to Store.TagContext
- TagDefs(tags): delegate to Store.ListTagDefs, resolve fileids to paths
- Inbox(showAll, includeArchived): query TagFiles("status") for candidate
  fileids, filter to /requests/ paths. When !showAll, build exclusion set
  from TagValueFiles("status","completed") and TagValueFiles("status","denied").
  For each remaining candidate, call Store.FileTagValues to get tag values
  from V records (no file reads). Build []InboxEntry from indexed values.
  RequestID from ark-request or ark-response tag. Kind is "request",
  "response", or "self". Comma-separated to-project normalized to first entry.
  ResponseHandled from response-handled tag (empty if absent).
  RequestHandled from request-handled tag (empty if absent).
  StatusDate from status-date tag (empty if absent).
- Fetch(path): verify file is indexed in microfts2, read and return full content
- Status(): return StatusInfo with file counts, total size, chunk count,
  strategy breakdown, source count, LMDB map usage (used/total/percent).
  Computes map usage from env.Info() and env.Stat(). Computes chunk
  count by summing ChunkRanges from FileInfoByID per file. Computes
  total size by summing FileLength from FileInfoByID per file. Counts
  files per strategy from StaleFiles.
- StatusDB(): return DBRecordCounts with per-prefix counts for both
  subdatabases. Delegates to microfts2.RecordCounts() and
  Store.RecordCounts(). (R899, R904, R905, R906, R907, R908)
- QueryTrigramCounts(query): delegate to microfts2, returns trigram counts for CLI grams command
- AddTmpFile(path, strategy, content): delegate to microfts2.AddTmpFile, extract tags from content, track in store
- UpdateTmpFile(path, strategy, content): delegate to microfts2.UpdateTmpFile, re-extract tags
- RemoveTmpFile(path): delegate to microfts2.RemoveTmpFile, remove tag tracking
- HasTmp(): delegate to microfts2 overlay — true if any tmp:// docs exist
- TmpFiles(): list all tmp:// paths from the overlay
- Init seeding: if ark.toml exists, read case_insensitive/aliases from it
- SourcesCheck(): delegate to Config.ResolveGlobs, add new sources, flag MIA, report orphans
- IsIndexable(path): find which source the path belongs to, get effective
  patterns, call Matcher.Classify. Returns true if any source would index it.
- StartActor(): create ChanSvc channel, start RunSvc goroutine. Called
  by Server on startup, or by CLI for cold-start operations. (R986)
- StopActor(): close the ChanSvc channel. Actor goroutine exits on
  channel drain. Called before Close(). (R986)
- enqueueWrite(fn): append write closure to writeQueue. If queue was
  empty and not currently writing, call startWrite(). Called from
  inside the actor only. (R1053)
- startWrite(): dequeue head of writeQueue, set writing=true, spawn
  goroutine. Goroutine calls fts.Copy() to get a cache-less DB copy,
  executes the write closure (file I/O off the actor), then sends a
  reconcile closure back to the actor channel. (R1054, R1055, R1056)
- reconcileWrite(err): called inside the actor from the reconcile
  closure. On success: fts.InvalidateCaches(), commit transaction,
  set writing=false. If writeQueue not empty, call startWrite()
  (continuation). On error: log, skip batch, set writing=false,
  continue with next. (R1057, R1058, R1060, R1061)
- classifyForWrite(paths): partition file list into config files
  (ark.toml) vs content files. Config files processed synchronously
  in the actor; content files queued via enqueueWrite. (R1052, R1062)

## Collaborators
- Config: loads and validates ark.toml
- Store: ark's own LMDB subdatabase (missing, unresolved, tags)
- Scanner: walks directories (uses Config + Matcher)
- Indexer: adds/removes files in both engines, extracts tags
- Searcher: queries both engines and merges results
- Matcher: pattern matching for IsIndexable

## Sequences
- seq-add.md
- seq-search.md
- seq-write-actor.md
