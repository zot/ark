# Sequence: @ext routing and re-resolution

Covers @ext routing during file indexing, canonical re-resolution
when target-bearing files reindex, source-side cleanup when an
@ext-bearing chunk is orphaned, overlay (tmp://) source removal,
and startup rebuild.

`bothPersistent := !IsOverlayID(sourceChunkID) && !IsOverlayID(target_chunkid)`
controls whether each routing writes to the index (X + V records) or to
ExtMap's in-memory overlay state (`overlayRoutings` +
`overlayValues`). The six core maps (targetToChunk, chunkToTargets,
fileidToTvids, extByAnchor, unresolvedTargets, virtualTagCount) are
updated identically in both branches.

## Participants
- Indexer
- ExtMap
- DB
- Store
- TvidMap
- TmpTagStore
- microfts2

## Flow: indexing @ext at write time

```
Indexer (indexed-chunk callback for source chunk)
   │
   ├── ExtractTagValues(content) → []TagValue
   │
   └── for each TagValue{Tag: "ext", Value: V}:
         Indexer ──> ExtMap.IndexExt(tvid_ext, sourceChunkID, V,
                                     sourceFileid, txn, tt)
            │
            ├── ParseExtTarget(V) → (target_spec, routed_tags, ok)
            │      └── !ok → return (no-op)
            │
            ├── DB.ResolveExtTarget(target_spec) → []target_chunkid
            │      └── empty → mark unresolvedTargets[tvid_ext];
            │                    add extByAnchor[target_spec]; return
            │
            ├── self-reference check: target_chunkid → DB.chunkFileID
            │      └── any == sourceFileid → log error, skip routing
            │
            └── for each accepted target_chunkid:
                  bothPersistent := !IsOverlayID(sourceChunkID) &&
                                    !IsOverlayID(target_chunkid)
                  │
                  ├── for each routed_tag:
                  │     persistent source: tt.Lookup or
                  │       allocIDInTxn(IFieldNextTvid) → tvid_routed
                  │     overlay source: TmpTagStore.resolveOrAlloc
                  │       (TvidMap.Lookup → AllocOverlay) → tvid_routed
                  │
                  ├── if bothPersistent:
                  │     Store.WriteExtRecord(txn, tvid_ext,
                  │                          target_chunkid, routed_tvids)
                  │     for each tvid_routed:
                  │        Store.AppendChunkIDToVRecord(...)
                  │        // multi-set append — no dedup
                  │
                  ├── else:
                  │     overlayRoutings[tvid_ext][target_chunkid] =
                  │        routed_tvids
                  │     for each routed_tag:
                  │        overlayValues[tag][value] += target_chunkid
                  │     RecordOverlayError(info, sourceChunkID,
                  │        sourceFileID, "overlay routing — session-scoped")
                  │
                  └── update core maps (always):
                        targetToChunk[tvid_ext] += target_chunkid
                        chunkToTargets[target_chunkid] += tvid_ext
                        fileidToTvids[target_fileid] += tvid_ext
                        extByAnchor[target_spec] += tvid_ext
                        for each routed_tag: virtualTagCount[tag]++
```

Overlay (`tmp://`) sources run this same flow via
`Indexer.runOverlayExtRouting`, which wraps the `applyIndexExt` calls
in a read-only `db.View`: an overlay source can route to a
persistent target whose fileid resolution (`chunkFileID` →
`ReadCRecord`) is an index read needing a live txn. No writes fire
(`bothPersistent` always false), so the read-only txn and a nil
`TvidTxn` suffice. (R2915)

## Flow: canonical re-resolution on file reindex

microfts2 fires the reindex callback once per file with
(fileid, orphanedChunkIDs, addedChunkIDs).

```
Indexer (reindex callback for file F)
   │
   └── ExtMap.ReresolveOnReindex(F.fileid, addedChunkIDs,
                                  orphanedChunkIDs, txn, tt)
        │
        ├── step 1: collect candidate tvid_exts
        │      ├── fileidToTvids[F.fileid]
        │      ├── extByAnchor[F.path]
        │      └── for each addedChunkID and orphanedChunkID:
        │            read @id values → for each UUID:
        │                extByAnchor[UUID]
        │      // overlay routings appear in the same maps,
        │      // re-resolved alongside persistent ones
        │
        ├── step 2: for each candidate tvid_ext:
        │      ├── TvidMap.Resolve(tvid_ext) → (tag, value, _)
        │      │      // MUST be before tt.Commit
        │      ├── ParseExtTarget(value) → (target_spec, routed_tags, _)
        │      └── DB.ResolveExtTarget(target_spec) → new_chunkids
        │
        ├── step 3: diff old (targetToChunk[tvid_ext], scoped to F)
        │           against new_chunkids:
        │      Adds    = new ∖ old
        │      Removes = old ∖ new
        │      Updates = unchanged with V-record blob shifts
        │
        ├── step 4 (Adds): per added_chunkid:
        │      bothPersistent := !IsOverlayID(sourceChunkID for tvid_ext)
        │                        && !IsOverlayID(added_chunkid)
        │      if bothPersistent:
        │         Store.WriteExtRecord(txn, tvid_ext, added_chunkid, ...)
        │         for each routed_tvid:
        │            Store.AppendChunkIDToVRecord(...) // multi-set
        │      else:
        │         overlayRoutings[tvid_ext][added_chunkid] = routed_tvids
        │         for each routed_tag:
        │            overlayValues[tag][value] += added_chunkid
        │      virtualTagCount[tag]++ for each routed_tag
        │
        ├── step 5 (Updates):
        │      Store rewrites V record blobs whose contents shifted
        │      // persistent only — overlay representations don't pack
        │
        ├── step 6 (Removes): per removed_chunkid:
        │      bothPersistent := !IsOverlayID(sourceChunkID) &&
        │                        !IsOverlayID(removed_chunkid)
        │      if bothPersistent:
        │         for each routed_tvid:
        │            Store.RemoveChunkIDFromVRecord(...) // one occurrence
        │            if V record empty:
        │               delete it; tt.Remove(routed_tvid); decrement T
        │         Store.DeleteExtRecord(txn, tvid_ext, removed_chunkid)
        │      else:
        │         routed_tvids = overlayRoutings[tvid_ext][removed_chunkid]
        │         for each routed_tag:
        │            strike removed_chunkid from overlayValues[tag][value]
        │         delete overlayRoutings[tvid_ext][removed_chunkid]
        │      virtualTagCount[tag]-- for each routed_tag
        │
        └── step 7 (Empty new set):
              for all X[tvid_ext]: Store.DeleteExtRecord(...)
              delete overlayRoutings[tvid_ext]
              unresolvedTargets[tvid_ext] = true
              extByAnchor[target_spec] += tvid_ext
              clear targetToChunk[tvid_ext]; drop from fileidToTvids,
                 chunkToTargets
```

Append is the degenerate case: most candidates' diffs are empty,
Adds fire only when newly-resolvable anchors land in appended
content, Removes fire only when the chunker drops and replaces the
previous last chunk. No special branch.

## Flow: persistent source-side cleanup (source chunk orphaned)

```
Indexer (orphan callback for persistent source_chunkid)
   │
   ├── existing F→V cleanup:
   │      F[source_chunkid][ext] → tvid_ext list
   │      for each tvid_ext:
   │         removeVarint(V[ext][value][tvid_ext], source_chunkid)
   │         if V record empty: delete; tt.Remove(tvid_ext); decrement T
   │
   └── for each tvid_ext: ExtMap.CleanupSource(source_chunkid,
                                               tvid_ext, txn, tt)
        // MUST run before tt.Commit — TvidMap.Resolve is needed
        │
        ├── for each target_chunkid in targetToChunk[tvid_ext]:
        │      bothPersistent := !IsOverlayID(source_chunkid) &&
        │                        !IsOverlayID(target_chunkid)
        │      if bothPersistent:
        │         routed_tvids = Store.ReadExtRecord(txn, tvid_ext,
        │                                            target_chunkid)
        │         for each routed_tvid:
        │            Store.RemoveChunkIDFromVRecord(...) // one occurrence
        │            if V empty: delete; tt.Remove(routed_tvid); T--
        │         Store.DeleteExtRecord(txn, tvid_ext, target_chunkid)
        │      else:
        │         routed_tvids = overlayRoutings[tvid_ext][target_chunkid]
        │         for each routed_tag:
        │            strike target_chunkid from overlayValues[tag][value]
        │         delete overlayRoutings[tvid_ext][target_chunkid]
        │      virtualTagCount[tag]-- for each routed_tag
        │
        └── drop tvid_ext from all relevant maps
              (targetToChunk, chunkToTargets, fileidToTvids,
               extByAnchor, unresolvedTargets, overlayRoutings)
```

## Flow: overlay source removal

Triggered when TmpTagStore drops a chunk (RemoveFile or RemoveChunk).
Every routing whose source is overlay has bothPersistent=false, so
no index writes fire.

```
TmpTagStore.dropChunkLocked(chunkID)
   │
   ├── entry = chunks[chunkID]
   │
   ├── if entry.tvids["ext"] non-empty:
   │      for each tvid_ext in entry.tvids["ext"]:
   │         ExtMap.CleanupSource(chunkID, tvid_ext, nil, nil)
   │            // txn and tt are nil — every routing is overlay-touched,
   │            // CleanupSource takes the overlay branch per target
   │
   └── (existing) drop chunk from chunks map, decrement counts, etc.
```

## Flow: startup rebuild

```
DB.Open
   │
   └── ExtMap.Rebuild(db)
        │
        ├── zero overlayRoutings, overlayValues, overlayErrors
        │      (no persistent source for these — session-scoped)
        │
        ├── Store.ScanAllExtRecords() → iterator of (tvid_ext,
        │                                  target_chunkid, routed_tvids)
        │
        └── for each X record:
              targetToChunk[tvid_ext] += target_chunkid
              chunkToTargets[target_chunkid] += tvid_ext

              DB.chunkFileID(target_chunkid) → target_fileid
              fileidToTvids[target_fileid] += tvid_ext

              TvidMap.Resolve(tvid_ext) → (_, value, _)
              ParseExtTarget(value) → (target_spec, _, _)
              extByAnchor[target_spec] += tvid_ext

              for each routed_tvid:
                 TvidMap.Resolve(routed_tvid) → (tag, _, _)
                 virtualTagCount[tag]++
```

## Flow: query unification

`Store.TagValueChunks(tag, value)` and `Store.TagFiles(tags)` union
three sources without coordination:

```
Store.TagValueChunks(tag, value)
   │
   ├── persistent index:
   │      prefix scan V[tag]\x00[value]\x00 → []chunkid
   │
   ├── TmpTagStore.TagValueChunks(tag, value) → []chunkid
   │      // overlay-direct content
   │
   └── ExtMap.OverlayTagValueFiles(tag, value) → []chunkid
          // overlay-routed @ext targets

   // chunkids do not collide across the three sources
   // (overlay chunkids count down, persistent up; routed targets
   // are uint64-keyed alongside)
```
