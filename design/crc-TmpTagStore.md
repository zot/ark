# TmpTagStore
**Requirements:** R1941, R1942, R1943, R1944, R1945, R1949, R1950, R1951

In-memory tag overlay for `tmp://` content. Mirrors the persistent
V/F/T runtime API so callers do not branch on persistent vs tmp.
Lives for the server's lifetime; no LMDB writes, no schema marker.

## Knows
- chunkTags: map[uint64]map[string][]TagValue — chunkid → tag → values
  (mirrors persistent V records, keyed by overlay-issued chunkid)
- fileChunks: map[uint64][]uint64 — fileid → chunkids belonging to it
  (used by RemoveFile to enumerate per-file entries)
- tagCounts: map[string]int — tag name → chunk count
  (mirrors persistent T records)
- mu: sync.RWMutex — guards all maps; reads from concurrent search
  paths take RLock

## Does
- UpdateTagValues(fileid uint64, chunkTags []ChunkTagValues): replace
  all entries for the fileid. Drops existing chunkids registered to
  the fileid, decrements per-tag counts, then writes new chunk-tag
  pairs and updates the per-fileid chunk list. (R1942)
- AppendTagValues(fileid uint64, chunkTags []ChunkTagValues): add
  entries for newly emitted chunkids without touching prior chunks.
  Used by `DB.AppendTmpFile`. (R1943)
- RemoveFile(fileid uint64): drop all chunk-tag entries for the
  file, decrement per-tag counts, and clean up any tvids whose only
  remaining producer was this file. Called from `DB.RemoveTmpFile`
  before microfts2's overlay drop so the trigram and tag overlays
  fall together. (R1944, R1951)
- TagFiles(tags []string) []TagFileInfo: scan chunk-tag entries,
  return per-fileid match info for the requested tag names. Used
  by `Store.TagFiles` to contribute the overlay's results to the
  union read. (R1945)
- TagValueFiles(tag, value string) []uint64: walk entries for the
  given tag, return fileids whose chunks carry the value. Used by
  `Store.TagValueFiles`. (R1945)
- FileTagValues(fileid uint64, tags []string) []TagValue: return
  the file's values for the requested tag names. Used by inbox via
  `Store.FileTagValues`. (R1945)
- HasFile(fileid uint64) bool: true if the overlay tracks any
  chunkids for the fileid. Used by Store dispatch to decide whether
  a read needs the overlay branch.

## Origin discriminator
Overlay-issued fileids count down from `MaxUint64` and overlay-issued
chunkids likewise. The high bit (set when read as int64) marks each
record as overlay-origin. Store's read/write dispatch uses this bit
without consulting any external map. (R1950)

## Collaborators
- Store: holds a `TmpTagStore` reference and unions read results,
  dispatches writes by fileid high bit
- DB: instantiates the overlay at startup and tears it down on close

## Sequences
- seq-tmp-tag-overlay.md
