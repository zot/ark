# Indexer
**Requirements:** R36, R37, R38, R39, R40, R41, R42, R43, R44

Coordinates adding, removing, and refreshing files across both
engines. microfts2 first, microvec second.

## Knows
- fts: *microfts2.DB — trigram engine
- vec: *microvec.DB — vector engine

## Does
- AddFile(path, strategy): add to microfts2 (gets fileid + chunk offsets),
  read chunk text from file, add to microvec (fileid + chunks)
- RemoveFile(path): resolve path to fileid via microfts2, remove from both
- RemoveByID(fileid): remove from both engines by fileid
- RefreshFile(path): re-add to microfts2, remove old vectors, add new vectors
- RefreshStale(patterns): get stale files from microfts2, optionally filter
  by patterns, refresh each one. Return list of missing files.

## Collaborators
- microfts2.DB: file identity, trigram indexing, staleness detection
- microvec.DB: vector embedding storage

## Sequences
- seq-add.md
- seq-server-startup.md
