# Indexer
**Requirements:** R36, R37, R38, R39, R40, R41, R42, R43, R44, R117, R118, R121, R126

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
- RefreshFile(path): re-add to microfts2, remove old vectors, add new vectors,
  re-extract tags and update Store (replaces old counts)
- RefreshStale(patterns): get stale files from microfts2, optionally filter
  by patterns, refresh each one. Return list of missing files.
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
