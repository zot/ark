# Indexer
**Requirements:** R36, R37, R38, R39, R40, R41, R42, R43, R44, R117, R118, R121, R126, R360, R361, R362, R363, R364, R365, R366, R367, R368, R369

Coordinates adding, removing, and refreshing files across both
engines. microfts2 first, microvec second. Extracts tags from
file content and updates the Store.

## Knows
- fts: *microfts2.DB — trigram engine
- vec: *microvec.DB — vector engine
- store: *Store — tag storage

## Does
- AddFile(path, strategy): add to microfts2 (gets fileid + chunk offsets),
  read chunk text from file, add to microvec (fileid + chunks),
  extract tags from content, update Store tag records
- RemoveFile(path): resolve path to fileid via microfts2, remove from both,
  remove tags via Store.RemoveTags(fileid)
- RemoveByID(fileid): remove from both engines and tags by fileid
- RefreshFile(path): check for append-only change first. If append:
  use AppendChunks path. Otherwise full re-add to microfts2, remove
  old vectors, add new vectors, re-extract tags and update Store.
- RefreshStale(patterns): get stale files from microfts2, optionally filter
  by patterns, refresh each one. Return list of missing files.
- DetectAppend(path, fileid): get FileInfo from microfts2, check
  FileLength > 0, stat file for growth, hash first FileLength bytes,
  compare to stored ContentHash. Returns true if append-only.
  (Future: back-seek from last chunk for unclean boundary detection.)
- AppendFile(path, fileid, strategy): read new bytes from FileLength
  to EOF, parse last ChunkRange for base line, call AppendChunks
  with WithBaseLine/WithContentHash/WithModTime/WithFileLength.
  Remove+re-add vectors (full vector refresh). Extract tags from
  new bytes only, Store.AppendTags for incremental tag update.
- ExtractTags(content []byte): scan content with regex `@[a-zA-Z][\w-]*:`,
  return map[string]uint32 of tagname → count. Tag name is the part
  between @ and : (lowercase).

## Collaborators
- microfts2.DB: file identity, trigram indexing, staleness detection
- microvec.DB: vector embedding storage
- Store: tag record storage (T/F prefix keys)

## Sequences
- seq-add.md
- seq-server-startup.md
