# Sequence: @ext routing and re-resolution

Covers @ext routing during file indexing, canonical re-resolution
when target-bearing files reindex, and source-side cleanup when an
@ext-bearing chunk is orphaned.

## Participants
- Indexer
- ExtMap
- DB
- Store
- TvidMap
- microfts2

## Flow: indexing @ext at write time

```
Indexer (indexed-chunk callback for source chunk)
   │
   ├── ExtractTagValues(content) → []TagValue
   │
   └── for each TagValue{Tag: "ext", Value: V}:
         Indexer ──> ExtMap.IndexExt(tvid_ext, V, sourceFileid, txn, tt)
            │
            ├── ParseExtTarget(V) → (target_spec, routed_tags, ok)
            │      └── !ok → return (no-op)
            │
            ├── DB.ResolveExtTarget(target_spec) → []target_chunkid
            │      └── empty → mark unresolvedTargets[tvid_ext];
            │                    add extByAnchor[target_spec]; return
            │
            ├── self-reference check: target_chunkid → CRecord.FileIDs
            │      └── any == sourceFileid → log error, skip routing
            │
            └── for each accepted target_chunkid:
                  ├── for each routed_tag:
                  │     tt.Lookup or allocIDInTxn(IFieldNextTvid) → tvid_routed
                  │
                  ├── Store.WriteExtRecord(txn, tvid_ext, target_chunkid,
                  │                         routed_tvids)
                  │
                  ├── for each tvid_routed:
                  │     Store.AppendChunkIDToVRecord(txn, target_chunkid,
                  │                                   tag, value, tvid_routed)
                  │     // multi-set append — no dedup
                  │     virtualTagCount[tag]++
                  │
                  └── update ExtMap maps:
                        targetToChunk[tvid_ext] += target_chunkid
                        chunkToTargets[target_chunkid] += tvid_ext
                        fileidToTvids[target_fileid] += tvid_ext
                        extByAnchor[target_spec] += tvid_ext
```

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
        ├── step 4 (Adds):
        │      for each added_chunkid:
        │         Store.WriteExtRecord(txn, tvid_ext, added_chunkid, ...)
        │         for each routed_tvid:
        │            Store.AppendChunkIDToVRecord(...) // multi-set
        │            virtualTagCount[tag]++
        │
        ├── step 5 (Updates):
        │      Store rewrites V record blobs whose contents shifted
        │
        ├── step 6 (Removes):
        │      for each removed_chunkid:
        │         for each routed_tvid:
        │            Store.RemoveChunkIDFromVRecord(...) // one occurrence
        │            virtualTagCount[tag]--
        │            if V record empty:
        │               delete it; tt.Remove(routed_tvid); decrement T
        │         Store.DeleteExtRecord(txn, tvid_ext, removed_chunkid)
        │
        └── step 7 (Empty new set):
              for all X[tvid_ext]:
                 Store.DeleteExtRecord(...)
              unresolvedTargets[tvid_ext] = true
              extByAnchor[target_spec] += tvid_ext
              clear targetToChunk[tvid_ext], drop from fileidToTvids,
                 chunkToTargets
```

Append is the degenerate case: most candidates' diffs are empty,
Adds fire only when newly-resolvable anchors land in appended
content, Removes fire only when the chunker drops and replaces the
previous last chunk. No special branch.

## Flow: source-side cleanup (source chunk orphaned)

```
Indexer (orphan callback for source_chunkid)
   │
   ├── existing F→V cleanup:
   │      F[source_chunkid][ext] → tvid_ext list
   │      for each tvid_ext:
   │         removeVarint(V[ext][value][tvid_ext], source_chunkid)
   │         if V record empty: delete; tt.Remove(tvid_ext); decrement T
   │
   └── for each tvid_ext: ExtMap.CleanupSource(tvid_ext, txn, tt)
        // MUST run before tt.Commit — TvidMap.Resolve is needed
        │
        ├── Store.ScanExtRecords(tvid_ext) → []ExtRouting
        │      // each: (target_chunkid, []routed_tvid)
        │
        ├── for each routing:
        │      for each routed_tvid:
        │         Store.RemoveChunkIDFromVRecord(...) // one occurrence
        │         virtualTagCount[tag]--
        │         if V record empty: delete; tt.Remove(routed_tvid); T--
        │      Store.DeleteExtRecord(txn, tvid_ext, target_chunkid)
        │
        └── drop tvid_ext from all six maps
              (targetToChunk, chunkToTargets, fileidToTvids,
               extByAnchor, unresolvedTargets, virtualTagCount entries)
```

## Flow: startup rebuild

```
DB.Open
   │
   └── ExtMap.Rebuild(store)
        │
        ├── Store.ScanAllExtRecords() → iterator of (tvid_ext,
        │                                  target_chunkid, routed_tvids)
        │
        └── for each X record:
              targetToChunk[tvid_ext] += target_chunkid
              chunkToTargets[target_chunkid] += tvid_ext

              CRecord(target_chunkid).FileIDs → target_fileid
              fileidToTvids[target_fileid] += tvid_ext

              TvidMap.Resolve(tvid_ext) → (_, value, _)
              ParseExtTarget(value) → (target_spec, _, _)
              extByAnchor[target_spec] += tvid_ext

              for each routed_tvid:
                 TvidMap.Resolve(routed_tvid) → (tag, _, _)
                 virtualTagCount[tag]++
```
