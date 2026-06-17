# Tmp Tag Overlay

Tags extracted from `tmp://` content live in an in-memory overlay
that mirrors the runtime shape of the persistent V/F/T tag store.
Tag-aware reads (search filters, `ark tag files`, `FileTagValues`,
inbox) union the persistent store with the overlay so callers do
not branch on `tmp://`.

This spec is paired with [tmp-documents.md](tmp-documents.md), which
covers `tmp://` add/remove/search at large. Tag extraction was
reserved there as "tracked in memory alongside the overlay"; this
spec pins the structure.

## Why an overlay rather than persistent records

`tmp://` documents live for the server's lifetime — no disk, no
recovery, no schema versioning. The persistent V/F/T records carry
durability scaffolding (index keys, schema markers, rebuild
protocols) that `tmp://` content does not need. Mirroring the
runtime API surface gives us code symmetry without inheriting
storage concerns.

## Surface

The overlay is owned by `DB`. It mirrors the persistent tag store's
runtime API for the tag types ark exposes today:

- `TmpTagStore.UpdateTagValues(chunkTags []ChunkTagValues)` —
  per-chunk attribution, same shape as the persistent store.
- `TmpTagStore.AppendTagValues(chunkTags)` — for
  `AppendTmpFile`.
- `TmpTagStore.RemoveFile(fileid)` — drop the file's V/F entries
  and decrement T counters.
- `TmpTagStore.TagFiles(tags) []TagFileInfo` — overlay-only
  contribution for tag-name queries.
- `TmpTagStore.TagValueFiles(tag, value) []uint64` — overlay-only
  contribution for tag-name+value queries.
- `TmpTagStore.FileTagValues(fileid, tags) []TagValue` — per-file
  read used by inbox.

`Store` (the persistent face) gains a thin union layer for the
read methods listed above: results from the index and `TmpTagStore` are
merged before return. Write-side dispatch routes by id at the
call boundary: chunkid-keyed writes (`UpdateTagValues`,
`AppendTagValues`, per-chunk `RemoveTagValues`) split groups by
chunkid high bit; the file-level cleanup
(`RemoveFileTagValues(fileid)`, called from `DB.RemoveTmpFile`)
splits by fileid high bit. Both ids count down from `MaxUint64`
in the overlay, so the high bit (set when read as int64) is the
discriminator. Callers stay tmp-unaware.

## Tag extraction flow

`AddTmpFile`, `UpdateTmpFile`, and `AppendTmpFile` extract tags via
microfts2's `WithIndexedChunkCallback`, the same callback the
persistent path uses. The callback fires once per genuinely-new
chunk (hash-dedup miss) in chunk order; the chunkAccumulator
collects per-chunk tag values. After the call returns, the
accumulator's chunk-tag pairs are written to `TmpTagStore` via
`UpdateTagValues` (full add/update) or `AppendTagValues` (append).

`RemoveTmpFile` clears the overlay's entries for that fileid before
the microfts2 overlay drops its records.

### Overlay callback caveats

For overlay-fired `IndexedChunk` values, `CRecord.Txn()` and
`CRecord.DB()` return nil — there is no transaction context.
`ChunkID`, `Hash`, `ContentLen`, `Attrs`, `FileIDs`, and
`Trigrams` are populated; lookups that traverse the CRecord into
the index (e.g., `CRecord.FileRecord`) will fail. The chunkAccumulator
pattern is unaffected because it keys by chunkid and reads only
populated fields.

Overlay chunkids count down from `MaxUint64`. Combined with
overlay fileids counting down from `MaxUint64` (read as negative
when treated as int64), the high bit serves as a per-record origin
discriminator independent of any external map.

## Tvid integration

The persistent tag store assigns tvids to `(tag, value)` pairs.
The same tvid table covers tmp:// content — overlay entries live
in the same map. Each tvid is annotated with its origin so
`RemoveTmpFile` cleans up tvids that were introduced solely by
`tmp://` content. This keeps the resolver shape single-pathed for
all callers (subpoint 3's tvid map).

## Inbox is the canary caller

`FileTagValues` (R1142, R1147, R1149) is currently orphaned — no
Go caller, even though inbox needs it. Wiring inbox to
`FileTagValues` is part of this spec rather than a follow-up:
shipping the overlay without exercising the unified read path
would let an index-only inbox slip in. By the time this spec is
implemented, `ark message inbox` resolves messages from both
persistent and tmp:// sources via the same call.

## Lifetime and recovery

- The overlay lives in process memory. Server restart is full
  reset.
- No schema marker. No version check. No `ark rebuild` interaction.
- Persistent tag-aware operations (rebuild, refresh, scan) do not
  read or write the overlay.

## Out of scope

- Schedule logs and pubsub events from tmp:// content remain
  governed by `specs/scheduling.md` and the watchdog flow in
  `pubsub.go`. Those producers already write into `tmp://` paths;
  this spec does not change them.
- The `OnlyIfTmp` 204-shortcut described in `tmp-documents.md` is
  half-wired and orthogonal to tag extraction. Not addressed here.
- Tag definitions (D records) for tmp:// content. Tag definitions
  are a vocabulary concern, not a per-file index concern, and a
  tmp:// document is unlikely to be the canonical source of a new
  tag definition. Defer until a use case appears.
