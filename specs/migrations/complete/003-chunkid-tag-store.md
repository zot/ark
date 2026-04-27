# Chunkid Tag Store

Migrate ark's V/F/T tag records from fileid-keyed to chunkid-keyed.
Language: Go. Environment: ark indexer + Store + DB.Open + Inbox.

## Problem

V (tag value), F (file tags), and T (tag totals) records are keyed
on fileid. This made tag storage cheap when tags were a file-level
property, but it blocks chunk-level tag operations:

- A file with multiple chunks tagging `@food: hamburger` only
  contributes one V entry per (tag, value) — the per-chunk
  multiplicity is lost.
- `@id: UUID` (V3 standard tags) need to resolve to a section,
  not a file. Section = chunk; resolution requires chunkid keys.
- `@ext` overlay (.scratch/EXT.md) attaches tags to chunks of
  other files. Without chunkid V records, the overlay can't
  participate in normal tag queries.
- Inbox uses tag values to find request/response files; the
  `FileTagValues(fileid, tags)` reverse lookup (R1142, R1147,
  R1149) was specified but never wired up. Inbox flakiness today
  is partly that gap.

This spec migrates the store keys and rewires the indexer's
extraction pipeline to track per-chunk attribution. Backward
compatibility doesn't matter — the migration requires a rebuild.

## Record format changes

The migration changes three things: the F record key, the V record
value, and the T record count semantic. Everything else listed here
is preserved or unchanged.

### V records — value layout changes; key shape preserved

- **Key:** `V[tag]\x00[value]\x00[tvid varint]` — UNCHANGED.
- **Value:** changes from packed fileid varints to packed chunkid
  varints.
- The tvid suffix in the key stays. EV records remain tvid-keyed
  and continue to join on the same identifier — no rekey needed.

### F records — key changes from fileid to chunkid; value layout preserved

- **Old key:** `F[fileid:8][tagname]`.
- **New key:** `F[chunkid varint][tagname]`. Multi-record per
  chunkid, one record per (chunkid, tagname) pair.
- **Value:** UNCHANGED — `[count: uint32 big-endian][optional packed tvid varints]`.
  Same encoding as today; only the key prefix is different.
- Cleanup pattern (described below in "Reverse lookups") follows
  the existing tvid-trail mechanism, just keyed by chunkid instead
  of fileid.

### T records — value layout preserved; count semantic shifts

- **Key:** `T[tagname]` — UNCHANGED.
- **Value:** UNCHANGED — `[count: uint32 big-endian][optional float32 vector (3072 bytes)]`.
- **Semantic:** count shifts from "number of files containing the
  tag" to "number of (chunk, tag) pairs in F records." A file with
  3 chunks all tagging `@food: hamburger` contributes 3, not 1
  (three F records, one per chunkid). A chunk shared by 2 files
  contributes 1 (one F record). The count drives T-record cleanup
  — when it hits zero, the T entry is dropped (embedding goes with
  it, per existing `adjustTagTotal` lifecycle).

### Records NOT changed by this migration

These records keep their current shape and lifecycle. The migration
spec calls them out explicitly so future readers don't have to
guess.

- **D records** — `D[tagname][fileid:8] → description bytes`.
  Definitions are a file-level property of the defining file
  (a tag is "defined" by a file's `@tag: name -- description` line,
  not by a chunk inside that file). Unchanged. (Note: an earlier
  draft of this spec said "D[fileid]" — that was wrong; the actual
  key is tagname-first with fileid as 8-byte suffix.)
- **EV records** — `EV[tvid varint] → vector`. Tag-value compound
  embeddings join V records via tvid; since V keeps its tvid
  suffix, EV stays orthogonal to the chunkid migration. No rekey,
  no changes.
- **EC records** — `EC[chunkID varint] → vector`. Already
  chunkid-keyed since the EC-rekey migration. Unaffected.
- **EF records** — `EF[fileID varint] → sum + count`. File-level
  centroid; unaffected.
- **PC records** — `PC[fileID varint][page varint] → blob`. PDF
  page content; per-file, orthogonal to chunkid keying.
- **M, U, I, E:** records — unaffected.

### microfts2 dependencies (unchanged from prior draft)

- **C records** (microfts2 chunk reverse-index): provides
  `chunkid → []fileid` resolution via the public `FilesForChunk`
  API. The recent refcount-aware `[]FileIDCount` shape (per the
  microfts2-abi-catchup migration that just landed) is what makes
  per-chunk reference counting safe — a chunk is orphaned only
  when its refcount reaches zero across all files, at which point
  microfts2 callbacks deliver the orphaned chunkid for ark to
  clean up its F/V records.

## Schema marker

Add an I record `tag_store_version`. New format = `"1"`. Pattern
follows the existing `ec_version` I record (db.go:286, currently
`"2"`).

On `DB.Open`, after the `ec_version` block, check
`tag_store_version`. If empty or != `"1"`, refuse to start with an
actionable error: "tag store schema upgrade required — run
`ark rebuild`". Don't auto-drop V/F/T: unlike EC (which
regenerates post-reconcile and degrades gracefully), V/F/T-empty
leaves tag queries and Inbox broken until a full scan completes.
Rebuild is more honest about the cost.

`cmdRebuild` already calls `cmdInit --no-setup`, which removes
and recreates the LMDB env. That nukes V/F/T (and everything
else) wholesale — no V/F/T-prefix-specific drop is needed. After
`cmdInit --no-setup`, set `tag_store_version = "1"` unconditionally
on first Open of a new DB (alongside the `ec_version` write). New
DBs from `ark init` are also tagged "1".

Old binaries don't read the marker, so they'll happily mis-read
chunkid-keyed V records as fileid-keyed garbage. Acceptable per
"backward compat doesn't matter" — old binaries are out of
contract.

## Store API

The file-level wrappers collapse: chunkid is now the key.

```go
type ChunkTagValues struct {
    ChunkID uint64
    Values  []TagValue
}

func (s *Store) UpdateTagValues(chunkTags []ChunkTagValues) error
func (s *Store) AppendTagValues(chunkTags []ChunkTagValues) error
```

`UpdateTags(fileid, tags)` and `AppendTags(fileid, tags)` go away
— their per-chunk content is now in `ChunkTagValues`. T totals are
still sum-by-tag, but the increment is computed per-chunk during
the merged write (same data, single pass).

`UpdateTagDefs` / `AppendTagDefs` keep their fileid signature — D
records remain `D[tagname][fileid:8]`.

### Reverse lookups

- `TagValueFiles(tag, value) []uint64` — return chunkids. Inbox
  and other chunk-level consumers can resolve to fileids via
  `FilesForChunk` when they need a file path. File-level
  consumers that don't care about chunk attribution just call
  `FilesForChunk` and dedupe.
- `TagFiles(tags) []TagFileInfo` — return chunk-attributed entries
  (`{ChunkID, FileID, ...}`); callers that want file-level
  results dedupe by FileID.
- `FileTagValues(fileid, tags)` — implement with chunkid input
  internally, but keep a fileid-input wrapper for callers that
  start from a file path. Wire it into Inbox per R1142, R1147,
  R1149 — the implementation existed (store.go:1092) but was
  never called. Inbox reliability problems are partly downstream
  of this gap.

### Cleanup mechanism (orphan chunkids)

File removal no longer touches V records directly. Instead, the
microfts2 callback (`RemoveFileWithCallback`) delivers any
chunkids whose C-record refcount reached zero. For each orphaned
chunkid:

1. Scan `F[chunkid]` prefix to enumerate (tagname, count, tvids)
   entries.
2. For each tvid in each F entry, decrement the corresponding V
   record by removing the chunkid from its packed list. If a V
   record becomes empty, delete it (and orphan any EV record on
   the next embedding-validate pass).
3. Decrement T totals by `count` for each tagname. Drop the T
   record if its count reaches zero.
4. Delete all F records for the orphaned chunkid.

Chunks shared across files keep their F/V/T entries until the last
file referencing them is removed (microfts2 manages the refcount
via the FileIDCount-shape C-record from the abi-catchup migration).

## chunkAccumulator

`chunkAccumulator` (indexer.go:27) shifts to per-chunk attribution:

```go
type chunkAccumulator struct {
    chunks    [][]byte
    tagValues [][]TagValue   // parallel to chunks; one slice per chunk
    defs      map[string]string  // file-level (D records)
    strategy  string
}
```

The callback (indexer.go:35) appends the per-chunk slice as one
element rather than spreading it. `chunks` and `tagValues` stay
parallel — same length, same order.

After `fts.AddFileWithContent` (or `fts.ReindexWithCallback`)
returns, zip with `FileInfoByID(fileid).Chunks` to recover
chunkids:

```go
info, _ := idx.fts.FileInfoByID(fileid)
chunkTags := make([]ChunkTagValues, len(acc.tagValues))
for i, vals := range acc.tagValues {
    chunkTags[i] = ChunkTagValues{ChunkID: info.Chunks[i].ChunkID, Values: vals}
}
store.UpdateTagValues(chunkTags)
store.UpdateTagDefs(fileid, acc.defs)
```

The `tags` accumulator field goes away — its contents are now
expressed as `len(Values)` per chunk inside `tagValues`.

## File-level consumers

`writeDateIndex` and `pubsub.PublishAndWatch` stay file-level.
Schedule entries are keyed by `(tag, value, path)` — chunkid would
be redundant. Pubsub subscribers want "tag X seen in path Y," not
a chunk address.

At each call site, flatten `acc.tagValues` (which is now
`[][]TagValue`) before passing in. A single helper:

```go
func flattenChunkTags(chunkTags [][]TagValue) []TagValue
```

keeps the file-level boundary explicit and easy to grep when
chunk-level publishing becomes a thing later. Chunk-level data
remains available in the accumulator for future per-chunk
schedule events, chunk-scoped subscriptions, or `@remove:`/`@add:`
source-chunk association (R1035/R1036).

R795/R796 (pubsub) and R866/R869/R870/R872 (schedule) remain
file-level as written — no requirement changes.

## Append path

Both append entry points use chunk callbacks instead of the
file-level `tagWindowForAppend`:

- `Indexer.AppendFile` (indexer.go:491) — direct entry.
- `executeRefresh` isAppend branch (indexer.go:327–365), with
  prep at indexer.go:312–315.

Shape:

```go
acc := newChunkAccumulator(strategy)
err := fts.AppendChunks(path, newBytes,
    microfts2.WithAppendChunkCallback(acc.callback))
// ...on success:
info, _ := fts.FileInfoByID(fileid)
n := len(acc.tagValues)
tail := info.Chunks[len(info.Chunks)-n:]
chunkTags := zipChunkTags(tail, acc.tagValues)
store.AppendTagValues(chunkTags)
store.AppendTagDefs(fileid, acc.defs)
pubsub.PublishAndWatch("", path, flattenChunkTags(acc.tagValues))
writeDateIndex(path, flattenChunkTags(acc.tagValues))
```

`tagWindowForAppend` (indexer.go:312–315, 538, definition near
line 750) goes away. Its job was to catch tags split across the
append boundary by scanning back to the previous newline. With
the chunker driving extraction, boundary handling is microfts2's
responsibility — the chunker decides whether a partial-line append
merges with the prior tail chunk (re-emitting it via callback) or
starts a new chunk. Either way the callback covers what was
emitted.

`refreshPrep.tagValues`, `prep.tags`, `prep.defs` were file-level
pre-extracted values. With chunk-callback-driven extraction during
execute, they go away. Prep keeps `path`, `strategy`, `oldID`,
`isAppend`, `data`, `newBytes`, `baseLine`, `fullHash`, `fileSize`,
`modTime`. Tag extraction moves on-actor. Cost: small — append by
definition has small new content; `ExtractTagValues` on a few
chunk-strings is sub-millisecond. The pre-extraction was a
marginal optimization that the chunkid migration makes
incompatible — accept the cost.

The vector path is unchanged. `splitChunks(prep.data, ...)` and
`vec.AddFile(fileid, allChunks)` stay file-level — vectors are
file-scoped.

If `AppendChunks` returns an error, `executeRefresh` falls through
to `executeFullRefresh`, which uses its own fresh accumulator.
The append accumulator is discarded — clean.

## Append boundary case (gap O12)

The append boundary problem (a tag split across the seam, e.g.
`"@stat"` already indexed, `"us:"` arriving as new bytes) is
solved by microfts2's chunk-locator + `AppendAwareChunker` work
(landed and committed in microfts2 as of 2026-04-26; see
`~/work/microfts2/requests/chunk-locator-refcount.md`).

microfts2 now carries:
- `Locator []byte` per-occurrence on F records,
- `EncodeByteRangeLocator(start, end)` populated by built-in
  chunkers,
- An optional `AppendAwareChunker` interface:

```go
AppendChunks(path string, lastLocator []byte, newBytes []byte,
    yield func(Chunk) bool) (replacedLast bool, err error)
```

When a chunker implements this, it decides whether the first new
chunk should merge with (replacing) the last existing chunk;
`replacedLast=true` triggers F-record last-entry drop + C-record
fileid decrement + cascade. The boundary tag gets re-emitted
through the callback as part of the merged chunk — exactly what
ark needs.

Built-in chunkers' AppendAwareChunker implementations are
deferred in microfts2 (their gap O16). Until those ship, ark's
append path still hits the dirty-boundary case for markdown.
Interim: keep the current fall-through-to-full-refresh on dirty
boundaries (R386). Markdown appends pay the full-reindex cost
until microfts2's per-chunker resume protocols land. The chunkid
migration is independently valuable; the full-refresh fallback is
correct, just slower.

A reindex is required regardless — record formats are
incompatible. The `tag_store_version` check from this spec
piggybacks on the microfts2 reindex requirement.

## Sequencing prerequisites

The microfts2-abi-catchup migration (completed
`specs/migrations/complete/001-microfts2-abi-catchup.md`) is the
prerequisite for this work. ark's source now consumes the
refcount-aware `FileIDCount` C-record shape and the new
`AppendAwareChunker` infrastructure, both of which this migration
relies on for the cleanup mechanism (orphan chunkid callbacks)
and the append path.

## Order

1. Schema marker: `tag_store_version` I record + Open-time check.
2. Store API: `ChunkTagValues`, new signatures, drop
   `UpdateTags`/`AppendTags`. F-record key construction shifts
   to `F[chunkid varint][tagname]`; value layout
   (`[count][optional tvids]`) preserved.
3. `chunkAccumulator` shape change + callback shift.
4. AddFile + executeFullRefresh: zip with `FileInfoByID` after
   indexing.
5. Append path: `WithAppendChunkCallback`, drop
   `tagWindowForAppend`, drop prep tag/def/value pre-extraction.
6. `flattenChunkTags` helper at file-level call sites
   (`writeDateIndex`, `pubsub.PublishAndWatch`).
7. Reverse lookups: `TagValueFiles` returns chunkids;
   `TagFiles` returns chunk-attributed entries; `FileTagValues`
   wired into Inbox per R1142/R1147/R1149.
8. Cleanup wiring: orphan-chunkid callback consumer that
   enumerates F/V/T per the cleanup mechanism above.
9. Rebuild + tag_store_version write on init.
