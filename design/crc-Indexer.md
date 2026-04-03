# Indexer
**Requirements:** R36, R37, R38, R39, R40, R41, R42, R43, R44, R117, R118, R121, R126, R360, R361, R362, R363, R364, R365, R366, R367, R368, R369, R385, R386, R502, R503, R505, R511, R517, R518, R519, R520, R521, R522, R751, R752, R754, R755, R756, R757, R795, R796, R797, R866, R868, R869, R870, R872, R933, R934, R935, R953, R954, R956, R873, R1009, R1018, R1019, R1021, R1022, R1037, R1038, R1103, R1104, R1105, R1106, R1113, R1114, R1115, R1116, R1117, R1118, R1119, R1120, R1121, R1122, R1123, R1124, R1125, R1126, R1127, R1128

Coordinates adding, removing, and refreshing files across both
engines. microfts2 first, microvec second. Extracts tags from
file content and updates the Store.

## Knows
- fts: *microfts2.DB — trigram engine
- vec: *microvec.DB — vector engine
- store: *Store — tag storage
- pubsub: *PubSub — notified after tag extraction (nil if no server)

## Does
- AddFile(path, strategy): add to microfts2 via AddFileWithContent with
  WithChunkCallback (R1113). Callback accumulates chunk text for microvec
  (R1116) and extracts tags/defs/values from clean chunk text (R1117,
  R1118). After return, pass chunks to microvec, merged tag data to Store.
  Eliminates splitChunks call (R1123).
- RemoveFile(path): resolve path to fileid via microfts2, remove from both,
  remove tags via Store.RemoveTags(fileid), remove V records via
  Store.RemoveTagValues(fileid) (R1105)
- RemoveByID(fileid): remove from both engines, tags, and V records by fileid
- RefreshFile(path): check for append-only change first. If append:
  use AppendChunks path. Otherwise full re-add to microfts2 via
  ReindexWithContent with WithChunkCallback (R1114). Callback
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
  with WithBaseLine/WithContentHash/WithModTime/WithFileLength and
  WithAppendChunkCallback (R1115). Remove+re-add vectors (full vector
  refresh — splitChunks retained for microvec, R1125). For tag
  extraction, back up from FileLength to previous newline to catch
  boundary-split tags, then Extract tags and tag defs from that
  widened window. Store.AppendTags, Store.AppendTagDefs, and
  Store.AppendTagValues for incremental update. (R1104)
- ExtractTags(content []byte): scan content with regex `@[a-zA-Z][\w.-]*:`,
  return map[string]uint32 of tagname → count. Tag name is the part
  between @ and : (lowercase). Matches anywhere in content (inline tags
  are valid).
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
- microvec.DB: vector embedding storage
- Store: tag record storage (T/F/V prefix keys), day-bucket storage (TD/TF keys)
- Config: schedule tag declarations for date indexing
- PubSub: notified after tag extraction (Publish call)

## Sequences
- seq-add.md
- seq-server-startup.md
- seq-pubsub.md
