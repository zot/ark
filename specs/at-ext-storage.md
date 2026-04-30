# `@ext` Storage Layer

`@ext: TARGET @tag1: v1 @tag2: v2 ...` declares that `@tag1: v1` and
`@tag2: v2` apply to the chunks named by TARGET, even though they are
authored in a different chunk. The parser (`specs/at-ext-parsing.md`)
splits the value; this spec describes how the routed tags reach the
search index, how the source/target relationship is tracked durably,
and how reindexing of either side keeps the index consistent.

## Storage shape

The same V/F/T machinery the chunkid migration delivered is reused.
Routed tags get V records pointing at *target* chunkids — the search
side stays simple. A new X record carries ext provenance.

### V records: multi-set, no dedup

`V[tag][value][tvid] → packed chunkid varints`. **Drop the dedup
check in `addChunkIDToVRecord`**: every contribution adds an entry,
inline and ext-routed alike. `removeVarint` removes the *first*
occurrence; the rest survive for other contributors. A chunk that
has `@food: hamburger` inline AND is ext-routed `@food: hamburger`
from one source has two entries in `V[food][hamburger][tvid_food]`.
Search-side result sets already coalesce, so duplicate chunkids are
invisible to callers; the multiplicity exists purely so that
removal of one contribution doesn't strip valid contributions from
others.

### X records: ext provenance

`X[tvid_ext][target_chunkid] → packed routed_tvid varints`.

- Key prefix is the @ext (or @link) tvid plus the target chunkid.
- Value lists the tvids of the routed tags that this @ext routing
  added to V records pointing at that target.
- One X record per (tvid_ext, target_chunkid) pair. Multiple targets
  for one tvid_ext → multiple X records, prefix-scannable by tvid_ext.
- **Chunkid-keyed, not fileid-keyed.** This is the durable bridge
  across an ark restart: if ark stops, the user edits the target file
  (deleting an `@id` line, say), and ark restarts → reindex → target
  chunk orphans. Startup scan of X records populates
  `chunkToTargets[orphan_chunkid]` so the orphan callback can find
  the routings that need cleanup. Fileid-keyed X cannot do this —
  re-resolution against the post-edit state returns empty, and the
  stale V entry for the now-orphan chunkid has no discovery path.

### F records: unchanged

`F[source_chunkid][tag] → count + packed tvids`. `F[source][ext]`
holds the @ext tag's tvid the same way any other F record holds tag
tvids. The routed tag tvids are NOT added to F[source] — they don't
belong to the source's content, they were derived during ext routing.

## In-memory ExtMap

Maintained alongside DB writes; rebuilt at startup by scanning X
records. Spec recovery for any tvid_ext is `TvidMap.Resolve(tvid_ext)`,
which returns the @ext value text — the original target spec is
embedded in that text, so no separate cache field is needed.

- `targetToChunk[tvid_ext] → []chunkid` — direct read of X records
  collated per tvid_ext. Used for re-resolution diffs.
- `chunkToTargets[chunkid] → []tvid_ext` — inverse of targetToChunk.
  Used for orphan-callback re-resolution (chunk gone → who cared)
  and search-result rendering.
- `fileidToTvids[fileid] → []tvid_ext` — file-level reindex trigger.
  Derived from each X record's target chunkid → CRecord.FileIDs.
- `extByAnchor[spec_text] → []tvid_ext` — anchor-text reverse
  lookup. Key is the literal target spec text from the @ext value
  (the UUID string, or the path string). Same map covers both —
  UUIDs and paths don't collide in practice. Handles UUID mobility
  (a UUID gains an additional location in another file) and the
  "appearing" case (target spec finally resolves once a file is
  added or `@id` is added to an existing file).
- `unresolvedTargets[tvid_ext] → true` — set of @ext tvids whose
  target spec currently resolves to nothing. Re-checked on every
  reindex that could plausibly produce a resolution.
- `virtualTagCount[tag] → int` — counter for ext-routed contributions
  per tag, used in T-total queries (see "T accounting" below).

## Indexing flow

Handled in the indexer, after `ExtractTagValues` returns the chunk's
tag values.

For each `TagValue{Tag: "ext", Value: V}`:

1. `ParseExtTarget(V)` → `(target_spec, routed_tags, ok)`. Skip if
   `!ok` (no embedded tags or empty target).
2. `db.ResolveExtTarget(target_spec)` → `[]target_chunkid`. Empty
   → mark `unresolvedTargets[tvid_ext] = true`, populate
   `extByAnchor[target_spec]`, write no X/V routed records yet.
3. **Self-reference check**: if any resolved target's fileid equals
   the *current* source's fileid, error and skip the ext routing.
   The @ext tag's V/F/T entries still land (so the user can see the
   broken @ext rendered) but no chunks are routed and no X records
   are written.
4. For each accepted target chunkid: write the X record
   `X[tvid_ext][target_chunkid] → [tvid_routed_1, ...]`. Append
   target chunkid to each routed tag's V record (multi-set append).
   Allocate routed tag tvids via the existing
   `allocIDInTxn(IFieldNextTvid)` path — same persistent counter as
   inline tags. Update in-memory ExtMap entries (targetToChunk,
   chunkToTargets, fileidToTvids, extByAnchor by anchor text).
   Increment `virtualTagCount[routed_tag_name]` once per added entry.

The @ext tag itself is stored normally (V/F/T for `("ext", V)`
against the source chunkid), exactly like any other tag — search and
rendering of @ext as text still work.

## Canonical re-resolution flow

The same flow handles every target-side and "appearing" change.
Append is a degenerate case — the diff just happens to be empty for
unchanged chunks.

When file F is reindexed (microfts2's reindex callback fires with
`(fileid, orphanedChunkIDs, addedChunkIDs)`):

1. **Collect candidate tvid_exts** from three sources:
   - `fileidToTvids[F.fileid]` — exts already routing to chunks of F.
   - `extByAnchor[F.path]` — path-anchored exts (resolved or not)
     keyed on F's path.
   - `extByAnchor[UUID]` for each `@id: UUID` value added or removed
     in F's chunks — covers UUID mobility (gained location) and the
     appearing case (newly resolvable target).
2. **Re-resolve** for each tvid_ext in the candidate set:
   `TvidMap.Resolve(tvid_ext)` recovers the @ext value text →
   `ParseExtTarget` → `db.ResolveExtTarget` → new chunkid set.
3. **Diff** new chunkid set against the old set
   (`targetToChunk[tvid_ext]`, scoped to F's chunks for the
   file-level part of the trigger):
   - **Adds** = new ∖ old
   - **Removes** = old ∖ new
   - **Updates** = unchanged chunkids whose V record blobs change as
     a side effect of other entries shifting in the same record
4. **Apply Adds**: write `X[tvid_ext][added_chunkid]`; append
   `added_chunkid` to each routed tag's V record (multi-set append);
   bump `virtualTagCount[routed_tag]`.
5. **Apply Updates**: rewrite changed V record blobs. Mostly a no-op
   for chunk-level resolution; relevant when the same V record gains
   or loses other entries from this re-resolution.
6. **Apply Removes**: strike `removed_chunkid` from each routed tag's
   V record (one occurrence — multi-set remove); decrement
   `virtualTagCount[routed_tag]`. If a V record becomes empty, delete
   it and decrement T as needed; delete `X[tvid_ext][removed_chunkid]`.
7. **Empty new set**: drop all X records for tvid_ext, mark
   `unresolvedTargets[tvid_ext] = true`, and update extByAnchor.

This handles all the failure modes:

- Target chunk content changed → orphan callback → reindex callback
  → re-resolve, find new chunk carrying the same UUID or as the new
  preamble.
- Target file deleted → reindex on remove → empty new_targets →
  unresolved.
- @ext line edited in source → source reindex → old @ext goes
  through removal flow (below) → new @ext goes through indexing flow
  above.
- Insert above target shifts first-chunk-by-position → reindex
  callback even with no chunks orphaned → path-spec re-resolves to
  the new first chunk.
- File added or `@id` added → `extByAnchor` lookup surfaces
  previously-unresolved or already-resolved-elsewhere tvid_exts.

### Append vs full reindex

No special handling. The canonical flow above already does the right
thing for appends as a degenerate case. Under append, the diff for
most tvid_exts is empty (target chunks earlier in the file are
unchanged → same chunkids). The Add branch fires when a new `@id:
UUID` lands in the appended content; the Remove branch fires only
when the chunker drops and replaces the previous last chunk. Don't
branch on "is this an append?" — run the uniform re-resolution flow.

## Source-side cleanup

When a source chunk is removed (orphan callback for that chunkid):

1. Existing F→V cleanup runs: `F[source][ext]` gives `tvid_ext`,
   `removeVarint(V[ext][value][tvid_ext], source)`. If empty, drop
   V record, decrement T, `tt.Remove(tvid_ext)`. **Order matters**:
   the re-resolution paths must call `TvidMap.Resolve(tvid_ext)`
   BEFORE `tt.Commit` drops the tvid, otherwise the spec recovery
   fails.
2. **For each tvid_ext that source contributed**: prefix-scan
   `X[tvid_ext]` → enumerate (target_chunkid, routed_tvids) pairs.
   For each pair: for each routed_tvid, strike target_chunkid from
   `V[..routed_tvid..]` (one occurrence — multi-set remove);
   decrement `virtualTagCount[routed_tag]`. Drop the X record.
3. Update ExtMap: drop tvid_ext from targetToChunk, chunkToTargets,
   fileidToTvids, extByAnchor, unresolvedTargets.

The "for each tvid_ext" set is derived from `F[source][ext]`'s tvid
list, which the existing cleanup already reads.

## Self-reference rejection

Resolved target chunkids carry their own fileid (resolvable via
`ReadCRecord`). The indexer knows the source's fileid (the file it
is currently indexing). If any resolved target's fileid equals the
source's, reject with an error and skip the ext routing entirely
(the @ext tag's V/F/T records still land — user-visible — but no
chunks are routed).

The check exists because (a) self-referencing ext inside the same
indexing transaction races microfts2's chunkid allocation, (b) the
common case for users wanting "annotate this file" is a different
file's `@ext`, and (c) v2 with anchor-cached deferred resolution
could revisit if the use case appears.

## T accounting under multi-set V

`T[tag] = (LMDB F-driven count) + virtualTagCount[tag]`, summed at
query time.

- The existing `adjustTagTotal` path stays unchanged. F-record
  insertion/removal still drives the LMDB T count for inline
  contributions; the existing `isNew := IsNotFound(F[chunkid][tag])`
  check correctly counts each (chunkid, tag) pair once.
- `virtualTagCount[tag]` tracks the ext routings' contribution to T.
  Maintained at @ext index time (incremented per routed tag added)
  and at cleanup time (decremented per routed tag removed). Rebuilt
  at startup by scanning X records.
- T queries return `LMDB_T + virtualTagCount[tag]`.

The ext-routed contributions don't write F records at the target
chunkid (target's F is inline-only), so the LMDB T count doesn't see
them. The in-memory counter fills the gap.

## Routed-tag tvid allocation

Tvids stay unique across the index. Routed tags allocate from the
same persistent counter as inline tags via the existing
`allocIDInTxn(IFieldNextTvid)` path. The source's reindex txn
already has a `TvidTxn` open; the routed tag's `Lookup → allocIDInTxn
→ tt.Add` flow is identical to inline. No new TvidMap API is needed.
`AllocOverlay` stays exclusive to tmp:// content.

## Transaction boundaries

Each file reindex runs inside microfts2's per-file `env.Update`
transaction. All X record mutations and V record updates from the
ext flow go through the supplied txn, riding the same `TvidTxn`
that the @ext tag's own tvid lifecycle uses. Multi-file batches
converge correctly even when both source and target are in the same
batch — some redundant resolution work is acceptable; end state is
consistent.

## Out of scope (deferred)

- **Advanced anchor forms** (`path:line`, `path:"string"`,
  `path:/regex/`, `path[N]:anchor`, `path^:anchor`). v1
  `ResolveExtTarget` only knows bare UUID and bare path; the
  re-resolution machinery is form-agnostic and will inherit anchors
  for free when they ship.
- **UUID:LOCATION asymmetry** — when anchor forms ship,
  `UUID:LOCATION` should function the same as `PATH:LOCATION`: the
  UUID resolves only to a fileid (via the chunk carrying `@id:
  UUID`), and the LOCATION is file-relative, not chunk-relative. So
  bare UUID is chunk-level and UUID+LOCATION is file-level — they
  look symmetric in syntax but differ in resolution semantics.
  Documented for the future, not implemented in v1. See
  `.scratch/EXT-V-F.md` for the full treatment.
- **Rendering**: corner chip + search-result indicator + ExtEntry
  metadata expansion (`TargetFileID`, `TargetPath`). Belongs to a
  later roadmap point.
- **Content-hash fallback** for renamed @link paths.

## Out of scope (won't ship)

- Self-referencing @ext within the same file (rejected as error;
  permanent answer).
