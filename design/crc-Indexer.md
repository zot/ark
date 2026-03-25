# Indexer
**Requirements:** R36, R37, R38, R39, R40, R41, R42, R43, R44, R117, R118, R121, R126, R360, R361, R362, R363, R364, R365, R366, R367, R368, R369, R385, R386, R502, R503, R505, R511, R517, R518, R519, R520, R521, R522, R751, R752, R754, R755, R756, R757, R795, R796, R797, R866, R868, R869, R870, R872

Coordinates adding, removing, and refreshing files across both
engines. microfts2 first, microvec second. Extracts tags from
file content and updates the Store.

## Knows
- fts: *microfts2.DB — trigram engine
- vec: *microvec.DB — vector engine
- store: *Store — tag storage
- pubsub: *PubSub — notified after tag extraction (nil if no server)

## Does
- AddFile(path, strategy): add to microfts2 (gets fileid + chunk offsets),
  read chunk text from file, add to microvec (fileid + chunks),
  extract tags from content, update Store tag records,
  extract tag definitions, update Store D records
- RemoveFile(path): resolve path to fileid via microfts2, remove from both,
  remove tags via Store.RemoveTags(fileid)
- RemoveByID(fileid): remove from both engines and tags by fileid
- RefreshFile(path): check for append-only change first. If append:
  use AppendChunks path. Otherwise full re-add to microfts2, remove
  old vectors, add new vectors, re-extract tags and tag defs, update Store.
- RefreshStale(patterns): get stale files from microfts2, optionally filter
  by patterns, refresh each one in parallel. Worker pool (NumCPU goroutines)
  reads+chunks+extracts per file. ChanSvc actor serializes all LMDB writes —
  workers send closures capturing prepared data. Errors skip the file and
  log a warning. Return list of missing files.
- DetectAppend(path, fileid): get FileInfo from microfts2, check
  FileLength > 0, stat file for growth, hash first FileLength bytes,
  compare to stored ContentHash. Returns true if append-only.
  (Future: back-seek from last chunk for unclean boundary detection.)
- AppendFile(path, fileid, strategy): read new bytes from FileLength
  to EOF, parse last ChunkRange for base line, call AppendChunks
  with WithBaseLine/WithContentHash/WithModTime/WithFileLength.
  Remove+re-add vectors (full vector refresh). For tag extraction,
  back up from FileLength to previous newline to catch boundary-split
  tags, then Extract tags and tag defs from that widened window.
  Store.AppendTags and Store.AppendTagDefs for incremental update.
- ExtractTags(content []byte): scan content with regex `@[a-zA-Z][\w.-]*:`,
  return map[string]uint32 of tagname → count. Tag name is the part
  between @ and : (lowercase). Matches anywhere in content (inline tags
  are valid).
- ExtractTagDefs(content []byte): scan content for `@tag: <name> <description>`
  lines. First word after `@tag:` is the tag name, rest is description.
  Returns map[string]string (tagname → description).
- WriteDateIndex(fileid uint64, path string, content []byte, config *Config):
  for each tag in content that matches a schedule tag in config, parse the
  date value via EventScheduler.ParseDateValue, discretize into day buckets,
  write via Store.WriteDayBuckets. Also parses @ack: entries in the same
  chunk and writes past-event day buckets. Called from AddFile and
  RefreshFile after tag extraction. (R866, R868, R869, R870, R872)

## Collaborators
- microfts2.DB: file identity, trigram indexing, staleness detection
- microvec.DB: vector embedding storage
- Store: tag record storage (T/F prefix keys), day-bucket storage (TD/TF keys)
- Config: schedule tag declarations for date indexing
- PubSub: notified after tag extraction (Publish call)

## Sequences
- seq-add.md
- seq-server-startup.md
- seq-pubsub.md
