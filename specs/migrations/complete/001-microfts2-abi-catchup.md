# microfts2 ABI Catch-Up

Catch ark up to the microfts2 chunk-locator-refcount landing.

Language: Go. Environment: ark binary, microfts2 dependency.

## Problem

microfts2 landed three changes (committed 2026-04-26, see
`requests/microfts2-chunk-locator-refcount.md`):

1. **C-record fileids list refcounted.** Was `[fileid: varint]...`,
   now `[[fileid: varint] [count: varint]]...`. The Go-level type
   for `CRecord.FileIDs` changed from `[]uint64` to
   `[]microfts2.FileIDCount` (a struct with `FileID` and `Count`
   fields). This is the breaking-source-compatibility change.
2. **F-record chunk entry gained a `Locator []byte` field.** Each
   chunk entry is now `[chunkid, location, locator]`. The
   `Location` field is unchanged — `Locator` is additive. Built-in
   chunkers populate `Locator` via `EncodeByteRangeLocator(start,
   end)`.
3. **Optional `AppendAwareChunker` interface.** Chunkers may
   implement
   `AppendChunks(path, lastLocator, newBytes, yield) (replacedLast bool, err error)`
   to handle append boundaries cleanly. Built-in chunkers don't
   implement it yet (microfts2's own gap O16). Existing chunkers
   continue to work without change.

These changes ship together as one record-format break — old indices
won't parse against the new code, full reindex required.

## Impact on ark

### Source incompatibility (must fix)

`crec.FileIDs[i]` is used as `uint64` in three places:

- `search.go:416` — `fileid := crec.FileIDs[0]`, then used as
  map key in `paths[fileid]`.
- `search.go:421` — passed to `crec.FileRecord(fileid)`.
- `search.go:506–507` — iterated via `for _, fid := range crec.FileIDs`,
  used as map key in `fileIDs[fid]`.

Fix: change each site to `.FileID` accessor.

### Source-compatible (continues to work)

- `info.Chunks[i].Location` — still works; `Locator` is additive.
  Sites: `indexer.go:304`, `indexer.go:507`, `db.go:1528`,
  `librarian.go:910`, `search.go:993`.
- `idx.fts.AppendChunks(...)` — `AppendAwareChunker` is optional;
  ark's chunkers don't implement it (yet), so the call sites in
  `indexer.go:328` and `indexer.go:516` are unchanged.
- `pdfchunker.go` — implements `microfts2.FileChunker`, a different
  interface. Unaffected by chunk-locator-refcount.

### Deferred (intentional non-changes)

- **Use the new `Locator` field.** Built-in chunkers now emit
  byte-range locators on F-record chunk entries. ark could use
  these for fast random-access chunk retrieval and append-merge
  resume, but the current code uses `Location` (line range) and
  works correctly. Leave as-is until a feature drives the change
  (likely the chunkid-tag-store migration's append path, per its
  Q7).
- **Implement `AppendAwareChunker`.** Built-in chunkers'
  AppendAware implementations are microfts2's gap O16. Until those
  ship upstream, ark's append path falls through to full refresh
  on dirty markdown boundaries (per chunkid-tag-store migration's
  Q7 boundary discussion). Accept the cost for now.
- **Use the new refcount on `FileIDCount`.** Counts are populated
  by microfts2 to track how many occurrences of a chunk a file
  carries. ark currently treats fileids as a set; using counts
  correctly is part of the chunkid-tag-store migration's V/F
  rework. Leave deduped-by-FileID for this catch-up.

## Reindex required

Old indices written by the previous microfts2 won't parse against
the new code. Plan a full reindex on first run after the catch-up:

- The chunkid-tag-store migration introduces a `tag_store_version`
  I-record schema marker. Its Open-time check refuses to start with
  a stale tag store (per the chunkid migration spec).
- This catch-up doesn't introduce its own schema marker — microfts2
  manages its own format versioning. ark just needs to be rebuilt
  against the new microfts2 source and the existing
  `~/.ark/` index dropped before first run.

For two-user audience, the operational note is: `ark rebuild` after
this catch-up lands.

## Migration steps

1. **Apply source fixes in `search.go`.** Three sites: line 416,
   421, 506–507. Use `.FileID` accessor on `FileIDCount` values.
2. **Verify the build.** `make` should succeed. The unrelated
   `go.mod` warnings about `go mod tidy` are environment noise,
   not migration scope.
3. **Run the existing test suite.** Passing tests are evidence
   that the catch-up is structurally correct, even though the
   on-disk format is incompatible (tests use temp DBs).
4. **Reindex on first run.** Document in PLAN.md that this
   migration's deployment requires `ark rebuild` against the new
   microfts2.

## Records not addressed

This migration touches ark's source-level consumers of microfts2
types only. It does NOT change ark's own LMDB record formats —
those are addressed by the chunkid-tag-store migration which
depends on this one.

## Sequencing

This migration must complete before:
- `chunkid-tag-store` migration (its append path uses the new
  `Locator` field and `AppendAwareChunker` infrastructure).
- The parked `status-db` code changes (uncommitted in
  `store.go`/`db.go`) can compile and verify.
