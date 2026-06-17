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
- `extSource[tvid_ext] → source_chunkid` — single source chunkid per
  tvid_ext. Used by render and cleanup paths to identify which chunk
  authored the @ext declaration. When multiple chunks share the same
  compound @ext text (same tvid_ext), any of them is an acceptable
  source for rendering — the map holds one. Recovered at startup from
  the V[ext][value][tvid_ext] source-chunkid list (first entry).
- `routedTagsByTvidExt[tvid_ext] → []TagValue` — the routed (tag,
  value) pairs each tvid_ext contributes. Used by the tag-query path
  (`ExtTagFiles` / `ExtTagValueFiles`) to find which tvid_exts
  contribute a requested tag without re-reading X records or
  re-resolving routed_tvids. Populated at Rebuild from each X
  record's routed_tvids decoded via TvidMap; kept current by
  `applyIndexExt` (Adds), `applyReresolve` (Adds and Removes), and
  `CleanupSource` (drop on tvid_ext eviction).

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

`T[tag] = (index F-driven count) + virtualTagCount[tag]`, summed at
query time.

- The existing `adjustTagTotal` path stays unchanged. F-record
  insertion/removal still drives the index T count for inline
  contributions; the existing `isNew := F[chunkid][tag] == nil`
  check correctly counts each (chunkid, tag) pair once.
- `virtualTagCount[tag]` tracks the ext routings' contribution to T.
  Maintained at @ext index time (incremented per routed tag added)
  and at cleanup time (decremented per routed tag removed). Rebuilt
  at startup by scanning X records.
- T queries return `index_T + virtualTagCount[tag]`.

The ext-routed contributions don't write F records at the target
chunkid (target's F is inline-only), so the index T count doesn't see
them. The in-memory counter fills the gap.

## Routed-tag tvid allocation

Tvids stay unique across the index. Routed tags allocate from the
same persistent counter as inline tags via the existing
`allocIDInTxn(IFieldNextTvid)` path. The source's reindex txn
already has a `TvidTxn` open; the routed tag's `Lookup → allocIDInTxn
→ tt.Add` flow is identical to inline. No new TvidMap API is needed.
`AllocOverlay` stays exclusive to tmp:// content.

## Transaction boundaries

Each file reindex runs inside microfts2's per-file `db.Update`
transaction. All X record mutations and V record updates from the
ext flow go through the supplied txn, riding the same `TvidTxn`
that the @ext tag's own tvid lifecycle uses. Multi-file batches
converge correctly even when both source and target are in the same
batch — some redundant resolution work is acceptable; end state is
consistent.

## Overlay (tmp://) target routing

`tmp://` content is editable and short-lived; UUID targets in `tmp://`
files are exactly the read-write tagging case `@ext` was designed for.
A persistent file can route tags to a `tmp://` chunk, and a `tmp://`
file can route tags to either kind of target. None of these routings
may leave persistent residue: when ark exits, the overlay state is
gone, so any index record keyed by an overlay tvid or pointing at an
overlay chunkid would dangle on the next startup with no recovery
path.

### Routing scopes

Each `@ext` routing falls into one of four cases by the persistence
of its source and target. `IsOverlayID(id) = id has the high bit set`
applies to both chunkids and fileids — the microfts2 overlay counts
down from `MaxUint64`, the index counts up.

```
                target persistent      target overlay
              ┌──────────────────────┬──────────────────────┐
source        │ X + V + ExtMap maps  │ ExtMap maps only     │
persistent    │ (today's path)       │                      │
              ├──────────────────────┼──────────────────────┤
source        │ ExtMap maps only     │ ExtMap maps only     │
overlay       │                      │                      │
              └──────────────────────┴──────────────────────┘
```

`bothPersistent := !IsOverlayID(sourceChunkID) && !IsOverlayID(targetChunkID)`.
Index X and V records are written iff `bothPersistent`. Any overlay
involvement on either end keeps the routing entirely in ExtMap's
in-memory state.

### ExtMap state extensions

The six maps (`targetToChunk`, `chunkToTargets`, `fileidToTvids`,
`extByAnchor`, `unresolvedTargets`, `virtualTagCount`) accept any
uint64 chunkid or fileid; they hold persistent and overlay routings
interleaved without distinguishing. Two new pieces fill the gap left
by the missing X and V records:

- `overlayRoutings[tvid_ext][target_chunkid] → []routed_tvid` —
  in-memory parallel to X records, populated only for routings where
  `!bothPersistent`. The same data the X record would have stored,
  except session-scoped.
- `overlayValues[tag][value] → []target_chunkid` — in-memory parallel
  to V records, populated only for routings where `!bothPersistent`.
  Used by `Store.TagValueFiles` and `Store.TagFiles` to surface
  overlay-routed targets at query time. Multi-set semantics match V
  records: each contribution adds an entry; removal strikes one
  occurrence.

`Rebuild` scans X records (persistent only) to populate the six core
maps and `extSource`. For each tvid_ext encountered, it reads the
V[ext][value][tvid_ext] entry once to recover a source chunkid (first
entry in the source-chunkid list) and writes it into `extSource`.
`overlayRoutings` and `overlayValues` start empty on each session and
fill as overlay sources index.

### Indexing path

`applyIndexExt` decides per-target. For each accepted target chunkid:

1. Compute `bothPersistent`.
2. Allocate routed-tag tvids via the existing path (`allocIDInTxn`
   when source is persistent; `TmpTagStore.resolveOrAlloc` /
   `TvidMap.AllocOverlay` when source is overlay — same shared
   `TvidMap`).
3. If `bothPersistent`: write X record + multi-set-append target
   chunkid to each routed tag's V record (today's path).
4. Else: write `overlayRoutings[tvid_ext][target_chunkid] = routed_tvids`;
   append target chunkid to `overlayValues[tag][value]` for each
   routed tag (multi-set, no dedup, mirroring V record semantics).
5. Either way: update the six maps (`targetToChunk`, `chunkToTargets`,
   `fileidToTvids`, `extByAnchor`, `virtualTagCount`).

The self-reference check still fires on every routing regardless of
overlay-ness — a `tmp://` file's `@ext` cannot self-target.

### Query unification

`Store.TagValueFiles(tag, value)` and `Store.TagFiles(tags)` union
four legs:

1. **F records** — inline contributions, source-chunk-keyed.
2. **`TmpTagStore`** — overlay-direct (tags inline on `tmp://` chunks).
3. **ExtMap, persistent ext-routed** — `ExtMap.ExtTagFiles(tags)` /
   `ExtMap.ExtTagValueFiles(tag, value)` walk an in-memory cache
   (`routedTagsByTvidExt[tvid_ext] → []TagValue`) and
   `targetToChunk[tvid_ext]` to surface every persistent target chunk
   carrying any of the requested tags. F records intentionally never
   land at the target chunk (R1991), so without this leg the target
   side is invisible to tag queries.
4. **ExtMap, overlay-routed** — same maps, same accessor; overlay
   target chunks appear in `targetToChunk` because the maps don't
   distinguish persistent from overlay. The persistent-vs-overlay
   distinction lives in the X record / `overlayRoutings` write side,
   not here.

The four legs union without coordination — chunkids do not collide
across the sources. The historical pair
`ExtMap.OverlayTagFiles` / `ExtMap.OverlayTagValueFiles` is replaced
by `ExtMap.ExtTagFiles` / `ExtMap.ExtTagValueFiles`; the new pair
covers both persistent and overlay routings in one walk.

The cache is populated by `Rebuild` (from on-disk X records'
routed_tvids decoded via TvidMap), maintained alongside writes by
`applyIndexExt` and `applyReresolve` (Adds add to the cache,
Removes prune it when the tvid_ext loses its last target),
and torn down by `CleanupSource` (drop the cache entry as the
tvid_ext leaves every map).

T-totals: `virtualTagCount[tag]` already counts every routed
contribution (overlay or persistent), so the existing
`index_T[tag] + virtualTagCount[tag]` formula stays correct without
modification.

### Cleanup paths

Two trigger surfaces:

**Persistent source orphan callback** (microfts2 fires per chunkid).
Existing F→V cleanup runs and yields the source's `tvid_ext` list. For
each `tvid_ext`, call `ExtMap.CleanupSource(sourceChunkID, tvidExt,
txn, tt)`.

**Overlay source removal** (`TmpTagStore.RemoveFile` /
`RemoveChunk`). Before TmpTagStore drops the chunk, enumerate its
`tvids["ext"]` to find the `tvid_ext` set the chunk contributed. For
each `tvid_ext`, call `ExtMap.CleanupSource(sourceChunkID, tvidExt,
nil, nil)` — txn and TvidTxn are unused for overlay sources because
no index writes can fire (every routing for an overlay source has
`bothPersistent=false`).

`CleanupSource(sourceChunkID, tvidExt, txn, tt)` walks
`targetToChunk[tvidExt]` (in-memory). For each target_chunkid:

- `bothPersistent := !IsOverlayID(sourceChunkID) && !IsOverlayID(target_chunkid)`
- If `bothPersistent`: read routed_tvids from the X record; strike
  target_chunkid from each routed tag's V record (one occurrence);
  delete the X record.
- Else: read routed_tvids from `overlayRoutings[tvidExt][target_chunkid]`;
  strike target_chunkid from `overlayValues[tag][value]` (one
  occurrence); delete the `overlayRoutings` entry.
- Decrement `virtualTagCount[tag]` per routed tag.

After the loop, drop `tvidExt` from `targetToChunk`,
`chunkToTargets`, `fileidToTvids`, `extByAnchor`,
`unresolvedTargets`, and `overlayRoutings`. The walk-the-maps shape
replaces today's `Store.ScanExtRecords` walk; X records are read
once per persistent target during the walk, not enumerated up front.

### Re-resolution

The canonical re-resolution flow is unchanged in shape. `applyReresolve`
gains the same per-target `bothPersistent` branch on Adds and Removes:
persistent targets touch X / V; overlay targets touch
`overlayRoutings` / `overlayValues`. Updates (V record blob shifts
caused by other entries) only apply to persistent targets — overlay
representations don't pack varints, so there is nothing to "shift."

When microfts2's reindex callback fires for a persistent file, the
candidate set still comes from `fileidToTvids[F.fileid]` plus
`extByAnchor[F.path]` plus `extByAnchor[UUID]` for added/removed
`@id` values — overlay routings appear in those maps under the same
keys, so they are re-resolved alongside persistent ones.

### Overlay-aware `chunkFileID`

`DB.chunkFileID(txn, chunkID)` today reads only persistent C records
(`fts.ReadCRecord`). For overlay chunkids it must branch to
`Store.filesForChunk(chunkID)`, which already routes overlay
chunkids to `TmpTagStore.FilesForChunk` via the resolver wired in
`DB.Open`. Without this, ExtMap cannot determine the fileid of an
overlay target during `Rebuild` (a no-op for overlay since they
don't appear in X records, but defensive) or during indexing
self-reference checks (a `tmp://` source routing to a `tmp://`
target needs to know both fileids to compare).

An overlay source can also route to a **persistent** target (the
source-overlay/target-persistent scope cell). There `chunkFileID`
takes the persistent branch (`fts.ReadCRecord`), which needs a live
read transaction to resolve the target's fileid (for the
self-reference check and `fileidToTvids`). So the overlay indexing
path opens a **read-only** transaction and threads it down: "an
overlay source writes no index records" (`bothPersistent` always
false) does not mean it touches no index — the persistent-target
fileid read still needs a txn. A read-only `db.View` suffices and
mirrors the self-contained read in `ExtRoutingsForTargetChunk`.
`CleanupSource` keeps its nil txn: for an overlay source it branches
on `bothPersistent` before any index access, so it never does a
persistent read.

### Overlay error log

Overlay routings are best-effort: their target may vanish before the
user notices, and their resolution dies with the session. The user
needs visibility into what overlay routings happened, what failed,
and what was skipped. ExtMap holds an in-memory error log for these
diagnostics:

- `overlayErrors []OverlayError` — append-only ring or unbounded
  slice (size cap to be decided at design time).
- Each entry: `{Time, SourceChunkID, SourceFileID, Severity,
  Message}`. Severity is `info` (informational, e.g., "@ext routes
  to tmp:// target — session-scoped") or `warn` (e.g., "self-reference
  in overlay routing rejected", "target resolution returned empty").

ExtMap exposes:

- `RecordOverlayError(severity, sourceChunkID, sourceFileID, message)`
  — append entry. Called by `applyIndexExt` and `applyReresolve`
  when they take overlay-affecting branches.
- `OverlayErrors() []OverlayError` — read snapshot.
- `ClearOverlayErrors()` — reset the log.
- `AddOverlayError(severity, message)` — externally-supplied entry
  (used by `ark errors --add`).

These are wired to the planned `ark errors` CLI command (PLAN.md
V2.5) for `dump`, `clear`, and `add` operations against the overlay
log. Persistent error records (a separate concept on the same
roadmap item) are out of scope here.

### What stays the same

- `extByAnchor` keys on the literal anchor text regardless of
  resolution outcome — overlay-source candidates appear in
  `extByAnchor[F.path]` / `extByAnchor[UUID]` and are re-resolved
  by file-change events identically to persistent ones.
- `unresolvedTargets` membership is overlay-agnostic.
- Self-reference rejection still fires for any source/target pair
  with the same fileid.
- T-totals query as `index_T[tag] + virtualTagCount[tag]`.

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
