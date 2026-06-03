# ExtMap
**Requirements:** R1992, R1993, R1994, R1995, R1996, R1997, R1998, R1999, R2000, R2001, R2002, R2003, R2004, R2005, R2006, R2007, R2008, R2009, R2010, R2011, R2012, R2013, R2014, R2015, R2016, R2017, R2018, R2019, R2020, R2021, R2022, R2023, R2024, R2025, R2026, R2027, R2029, R2030, R2031, R2065, R2073, R2079, R2096, R2100, R2108, R2109, R2114, R2120, R2121, R2122, R2123, R2124, R2344, R2352, R2380

Owns the in-memory state and orchestration for `@ext` routing.
Six core maps maintained alongside DB X-record writes; canonical
re-resolution flow runs from the indexer's reindex callback;
source-side cleanup runs from the orphan callback. Rebuilt at
startup by scanning X records.

Two extension maps and an error log handle overlay (tmp://)
routings whose records cannot live in LMDB. Overlay state is
session-scoped — empty at every startup, populated as overlay
sources index, dropped as overlay items disappear.

## Knows
- targetToChunk: map[uint64][]uint64 — tvid_ext → chunkids; collated
  X record contents per tvid_ext. Holds persistent and overlay
  chunkids interleaved. (R1992)
- chunkToTargets: map[uint64][]uint64 — chunkid → tvid_exts; inverse
  used by the orphan callback (R1992)
- fileidToTvids: map[uint64][]uint64 — fileid → tvid_exts; file-level
  reindex trigger (R1992)
- extByAnchor: map[string][]uint64 — BASE of the TARGET (absolutized
  path or `%UUID_VALUE`) → tvid_exts. Keyed by BASE only, not the
  full TARGET text — the narrower (anchor + modifier) is recovered
  from the tvid_ext's stored TARGET via `TvidMap.Resolve(tvid_ext)`
  and re-evaluated at resolve time. This shape covers the
  "initially unresolved → satisfiable on later target change" path
  that `fileidToTvids` cannot (target chunks with new content that
  now matches a previously-empty narrower). UUID and path BASEs
  don't collide — paths can't start with `%`. (R1992, R2380)
- unresolvedTargets: map[uint64]bool — tvid_exts whose target spec
  currently resolves to nothing (R1992, R1997)
- virtualTagCount: map[string]int — per-tag count of ext-routed
  contributions, persistent and overlay alike; sums into T-totals
  at query time (R1992, R2010, R2021)
- extSource: map[uint64]uint64 — tvid_ext → single source chunkid;
  identifies which chunk authored the @ext declaration. Used by
  ExtRoutingsForTargetChunk (render path) and CleanupSource. When
  multiple chunks share the same compound @ext text, any one is an
  acceptable source; the map holds one. (R2108)
- routedTagsByTvidExt: map[uint64][]TagValue — tvid_ext → routed
  (tag, value) pairs. Cache used by ExtTagFiles / ExtTagValueChunks
  to avoid re-reading X records or re-resolving routed_tvids on the
  tag-query hot path. (R2121, R2122)
- overlayRoutings: map[uint64]map[uint64][]uint64 — tvid_ext →
  target_chunkid → routed_tvids. In-memory parallel to X records
  for routings where `!bothPersistent`. Session-scoped. (R2013)
- overlayValues: map[string]map[string][]uint64 — tag → value →
  target_chunkids. In-memory parallel to V records for routings
  where `!bothPersistent`; multi-set semantics. Session-scoped.
  (R2014)
- overlayErrors: []OverlayError — append-only diagnostics for
  overlay routings; session-scoped. Each entry: {Time,
  SourceChunkID, SourceFileID, Severity, Message}. (R2029)

## Does
- Rebuild(db *DB): startup scan of X records to repopulate the seven
  core maps (six from R1992 plus extSource). Reads `targetToChunk`
  and `virtualTagCount` directly from X record contents; derives
  `chunkToTargets`, `fileidToTvids`, and `extByAnchor` (the latter
  via TvidMap.Resolve on each tvid_ext to recover the spec text).
  Populates `extSource` by reading `V[ext][value][tvid_ext]` once
  per tvid_ext and storing the first source chunkid. Populates
  `routedTagsByTvidExt` by decoding each X record's routed_tvids
  via TvidMap; later writes for the same tvid_ext are idempotent
  because routed pairs are a property of tvid_ext.
  `overlayRoutings`, `overlayValues`, and `overlayErrors` are
  zeroed (no on-disk source). (R1993, R2015, R2109, R2122)
- IndexExt(tvid_ext, sourceChunkID, value, sourceFileid, txn, tt):
  orchestrate one @ext routing. Steps: (1) ParseExtTarget(value);
  skip if !ok. (2) DB.ResolveExtTarget(target_spec). Empty → mark
  `unresolvedTargets[tvid_ext]` and add `extByAnchor[target_spec]`.
  (3) Self-reference check — fires regardless of overlay-ness; if
  any resolved fileid equals sourceFileid, log error and skip
  routing. (4) For each accepted target chunkid:
  bothPersistent := !IsOverlayID(sourceChunkID) &&
  !IsOverlayID(target_chunkid); allocate routed-tag tvids (persistent
  source uses `allocIDInTxn(IFieldNextTvid)` via TvidTxn, overlay
  source uses `TmpTagStore.resolveOrAlloc` / `TvidMap.AllocOverlay`);
  if bothPersistent → Store.WriteExtRecord + multi-set
  AppendChunkIDToVRecord; else → write `overlayRoutings[tvid_ext]
  [target_chunkid]` and append to `overlayValues[tag][value]`;
  update the six core maps; bump `virtualTagCount[routed_tag]` once
  per added entry. Records an overlay info entry when the routing
  takes an overlay-touched branch. (R1996, R1997, R1998, R1999,
  R2012, R2016, R2017, R2018, R2030)
- ReresolveOnReindex(fileid, addedChunkIDs, orphanedChunkIDs, txn,
  tt): canonical re-resolution flow. Step 1: collect candidate
  tvid_exts from `fileidToTvids[fileid]`, `extByAnchor[F.path]`,
  and `extByAnchor[UUID]` for each `@id: UUID` value added or
  removed in F's chunks — overlay routings are pulled in alongside
  persistent ones because the maps don't distinguish. Step 2: for
  each candidate, recover spec via TvidMap.Resolve → ParseExtTarget
  → DB.ResolveExtTarget → new chunkid set. Step 3: diff old
  (`targetToChunk[tvid_ext]`, scoped to F) vs new → Adds, Removes,
  Updates. Step 4 (Adds): per target, branch on `bothPersistent`;
  persistent → Store.WriteExtRecord + multi-set V append; overlay
  → write `overlayRoutings` entry + append to `overlayValues`; bump
  virtualTagCount. Step 5 (Updates): rewrite V record blobs
  (persistent only — overlay representations don't pack varints).
  Step 6 (Removes): per target, branch on `bothPersistent`;
  persistent → Store.RemoveChunkIDFromVRecord + Store.DeleteExtRecord;
  overlay → strike from `overlayValues` + delete `overlayRoutings`
  entry; decrement virtualTagCount; if a persistent V record empties,
  delete it and adjust T as needed. Step 7 (Empty new set): drop all
  X records for tvid_ext (persistent) AND drop `overlayRoutings[tvid_ext]`
  (overlay), mark unresolvedTargets, update extByAnchor. (R2000,
  R2001, R2002, R2003, R2004, R2005, R2006, R2026, R2027)
- CleanupSource(sourceChunkID, tvid_ext, txn, tt): source-side
  cleanup. Walks `targetToChunk[tvid_ext]` (in-memory). For each
  target_chunkid: bothPersistent := !IsOverlayID(sourceChunkID) &&
  !IsOverlayID(target_chunkid); persistent → read routed_tvids from
  X record, strike target_chunkid from each routed tag's V record
  (one occurrence), delete X record; overlay → read routed_tvids
  from `overlayRoutings[tvid_ext][target_chunkid]`, strike
  target_chunkid from `overlayValues[tag][value]` (one occurrence),
  delete `overlayRoutings` entry; decrement virtualTagCount per
  routed tag. After the loop, drop tvid_ext from targetToChunk,
  chunkToTargets, fileidToTvids, extByAnchor, unresolvedTargets,
  and overlayRoutings. **MUST run before `tt.Commit`** —
  TvidMap.Resolve is needed for spec recovery (when called from
  re-resolution paths) and the V record empties trigger
  tt.Remove(tvid_ext); reversing the order loses the spec. txn and
  tt may be nil for fully-overlay sources because no LMDB writes
  fire. (R2008, R2009, R2022, R2023, R2024, R2025)
- VirtualTagCount(tag) int: read-only accessor for T-total queries.
  Returns 0 if tag absent. Counts persistent and overlay routings
  alike. (R2010, R2021)
- VirtualTagCounts(tags []string) map[string]int: batched
  accessor under one RLock for hot-path callers. (R2010)
- ExtTagValueChunks(tag, value) []uint64: walk
  `routedTagsByTvidExt`; for each tvid_ext whose routed pairs
  contain (tag, value), append `targetToChunk[tvid_ext]` to the
  result. Covers both persistent and overlay routings under a
  single RLock. Used by Store.TagValueChunks. (R2120, R2124)
- ExtTagFiles(tags []string) []TagFileRecord: walk
  `routedTagsByTvidExt`; for each (tvid_ext, routed) where any
  routed.Tag is in `tags`, emit a TagFileRecord per
  target_chunkid in `targetToChunk[tvid_ext]`. Covers persistent
  and overlay routings in one pass. Used by Store.TagFiles. (R2120,
  R2124)
- VirtualTagNames() []string: enumerate all ext-routed tag names
  (keys of `virtualTagCount` with count > 0). Covers routings from
  inline X records and overlay (tmp://) sources. Used by tag-source
  parity in Store.ListTags / MatchTagNames. (R2344, R2352)
- VirtualTagValues(tag string) []string: enumerate distinct
  values routed for the given tag name from
  `routedTagsByTvidExt`, deduplicated. Covers persistent + overlay
  routings. Used by tag-source parity in Store.QueryTagValues /
  MatchTagValues. (R2344, R2352)
- RecordOverlayError(severity, sourceChunkID, sourceFileID,
  message): append entry to `overlayErrors`. Called by IndexExt
  and ReresolveOnReindex when they take overlay-touched branches
  or hit overlay-specific failure modes. (R2029, R2030)
- OverlayErrors() []OverlayError: return snapshot of the error
  log. Read by `ark errors --overlay --dump`. (R2030, R2031)
- ClearOverlayErrors(): reset the log. Read by `ark errors
  --overlay --clear`. (R2030, R2031)
- AddOverlayError(severity, message): append externally-supplied
  entry. Used by `ark errors --overlay --add`. (R2030, R2031)
- ExtRoutingsForTargetChunk(targetChunkID, db) []IncomingExtRouting:
  per-target render lookup. For each tvid_ext in
  `chunkToTargets[targetChunkID]`, returns the source chunkid, source
  file path, target anchor (currently always empty — anchored target
  forms are deferred), and the routed (tag, value) pairs. Branches
  on `bothPersistent` to read routed tvids from X records (LMDB)
  vs `overlayRoutings`. Self-contained — opens its own read txn via
  `db.store.env.View`. Used by Server.enrichContent to emit
  `<ark-ext-tags>` blocks. (R2065, R2073, R2079)
- AppendIsDegenerate: append-only file changes use ReresolveOnReindex
  unchanged — the diff is empty for unchanged chunks; Adds fire only
  when newly-resolvable anchors land in the appended content; Removes
  fire only when the chunker drops and replaces the previous last
  chunk. No "is this an append?" branch. (R2007)

## Types

### OverlayError (R2029)
- Time: time.Time
- SourceChunkID: uint64 (zero if externally added via AddOverlayError)
- SourceFileID: uint64 (zero if externally added)
- Severity: string ("info" or "warn")
- Message: string

### IncomingExtRouting (R2065, R2073, R2079)
- TvidExt: uint64 — the @ext tvid that produced this routing
- SourceChunkID: uint64 — chunk where the @ext declaration lives
- SourceFilePath: string — path of the file containing the source
  chunk (drives `<ark-tag externalFile="...">`)
- TargetAnchor: string — anchor portion of the target spec
  (post-`:` text); always "" for v1 because anchored target forms
  are not yet resolvable. Drives `<ark-tag externalTarget="...">`
- Routed: []TagValue — the (tag, value) pairs the ext declaration
  contributed at this target chunk

## Collaborators
- DB: ResolveExtTarget for spec → chunkid resolution; ParseExtTarget
  via the ext.go helper; chunkFileID for overlay-aware fileid lookup
- Store: X record CRUD (WriteExtRecord, ScanExtRecords,
  DeleteExtRecord); V record multi-set append/remove
  (AppendChunkIDToVRecord, RemoveChunkIDFromVRecord); T-total
  augmentation queries
- TvidMap: spec recovery via Resolve(tvid_ext); routed-tag tvid
  allocation via allocIDInTxn(IFieldNextTvid) through the
  caller-supplied TvidTxn (R1999, R2011)
- TmpTagStore: invokes CleanupSource on overlay source removal
  (per chunk, per @ext tvid the chunk contributed); also the
  routed-tag tvid allocator for overlay sources (R2017, R2023)
- Indexer: invokes IndexExt during chunk callback, ReresolveOnReindex
  from the indexed-chunk callback, CleanupSource from the orphan
  callback

## Sequences
- seq-ext-routing.md
