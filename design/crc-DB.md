# DB
**Requirements:** R1, R2, R3, R5, R6, R7, R28, R29, R30, R33, R40, R31, R32, R34, R127, R128, R129, R136, R138, R130, R135, R137, R161, R162, R163, R166, R167, R168, R196, R197, R198, R199, R200, R236, R246, R248, R237, R238, R239, R240, R241, R242, R243, R244, R245, R247, R249, R250, R251, R252, R253, R254, R255, R257, R258, R382, R383, R392, R506, R510, R563, R564, R565, R566, R567, R568, R605, R606, R617, R618, R619

Main ark facade. Owns the LMDB lifecycle and coordinates microfts2,
microvec, and the ark subdatabase. Entry point for all operations.

## Knows
- fts: *microfts2.DB — trigram search engine
- vec: *microvec.DB — vector search engine
- store: *Store — ark's own subdatabase
- config: *Config — parsed source configuration
- dbPath: string — database directory path

## Does
- Init(path, opts): create new database — open microfts2, pass env to
  microvec, create ark subdatabase, write default config, register
  func strategies (lines, chat-jsonl, markdown), create starter tags.md
- Open(path): open existing database — same sequence, read config.
  Registers func strategies (lines, chat-jsonl, markdown). Passes store to Indexer
  for tag tracking.
- JSONLChunkFunc: content-aware JSONL chunker — parses JSON, extracts
  text and thinking blocks, skips tool_use/tool_result/signatures/metadata
- Close(): close in reverse order (store, vec, fts)
- TagList(): delegate to Store.ListTags
- TagCounts(tags): delegate to Store.TagCounts
- TagFiles(tags): delegate to Store.TagFiles, resolve fileids to paths/sizes
- TagContext(tags): delegate to Store.TagContext
- TagDefs(tags): delegate to Store.ListTagDefs, resolve fileids to paths
- Inbox(showAll, includeArchived): query TagFiles("status"), read tag blocks,
  filter/sort, return []InboxEntry{Status, To, From, Summary, Path, RequestID, Kind}.
  RequestID extracted from @ark-request: or @ark-response: tag. Kind is "request",
  "response", or "self". Comma-separated @to-project: values normalized to first entry.
- Fetch(path): verify file is indexed in microfts2, read and return full content
- Status(): return StatusInfo with file counts, total size, chunk count,
  strategy breakdown, source count, LMDB map usage (used/total/percent).
  Computes map usage from env.Info() and env.Stat(). Computes chunk
  count by summing ChunkRanges from FileInfoByID per file. Computes
  total size by summing FileLength from FileInfoByID per file. Counts
  files per strategy from StaleFiles.
- QueryTrigramCounts(query): delegate to microfts2, returns trigram counts for CLI grams command
- Init seeding: if ark.toml exists, read case_insensitive/aliases from it
- SourcesCheck(): delegate to Config.ResolveGlobs, add new sources, flag MIA, report orphans
- IsIndexable(path): find which source the path belongs to, get effective
  patterns, call Matcher.Classify. Returns true if any source would index it.

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
