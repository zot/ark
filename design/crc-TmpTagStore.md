# TmpTagStore
**Requirements:** R1941, R1942, R1943, R1944, R1945, R1949, R1950, R1951, R1964, R1966, R1967

In-memory tag overlay for `tmp://` content. Mirrors the persistent
V/F/T runtime API so callers do not branch on persistent vs tmp.
Lives for the server's lifetime; no LMDB writes, no schema marker.

## Knows
- chunks: map[uint64]*chunkTagEntry — chunkid → entry. Each entry
  holds the chunk's fileid and a list of tvids resolved from the
  shared `TvidMap` (R1964); read-side methods convert tvids back to
  `(tag, value)` via `TvidMap.Resolve`.
- fileChunks: map[uint64][]uint64 — fileid → chunkids belonging to it
  (used by RemoveFile to enumerate per-file entries)
- tagCounts: map[string]int — tag name → chunk count
  (mirrors persistent T records)
- tvids: *TvidMap — shared resolver; consulted via Lookup/AllocOverlay
  on writes and Resolve on reads
- mu: sync.RWMutex — guards all maps; reads from concurrent search
  paths take RLock

## Does
- UpdateTagValues(fileid uint64, chunkTags []ChunkTagValues): replace
  all entries for the fileid. Drops existing chunkids registered to
  the fileid, decrements per-tag counts, resolves each `(tag, value)`
  to a tvid (`TvidMap.Lookup`, fall back to `AllocOverlay`), and
  writes new per-chunk tvid lists. (R1942, R1966)
- AppendTagValues(fileid uint64, chunkTags []ChunkTagValues): add
  entries for newly emitted chunkids without touching prior chunks.
  Resolves tvids the same way. Used by `DB.AppendTmpFile`. (R1943,
  R1966)
- RemoveFile(fileid uint64): drop all chunk-tag entries for the
  file, decrement per-tag counts, and clean up overlay-only tvids
  whose last `tmp://` producer was this file (`OriginOverlay`
  entries deleted from `TvidMap`; `OriginPersistent` left alone).
  Called from `DB.RemoveTmpFile` before microfts2's overlay drop
  so the trigram and tag overlays fall together. (R1944, R1951,
  R1967)
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
- TvidMap: shared `tvid → (tag, value)` resolver; consulted on every
  write (Lookup → AllocOverlay) and read (Resolve)

## Sequences
- seq-tmp-tag-overlay.md
- seq-tvid-overlay.md
