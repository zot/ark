# Sequence: tvid map and transaction overlay
**Requirements:** R1953–R1969

Covers TvidMap startup load, persistent indexing through a TvidTxn,
and tmp:// overlay tvid allocation.

## Participants
- DB
- Store
- TvidMap
- TvidTxn
- index (db.Update / db.View)
- TmpTagStore

## Flow: Startup load (R1958)

```
DB.Open()
  ├── Store.Open(env)
  ├── tvids := NewTvidMap()
  ├── tvids.LoadFromStore(store)
  │     └── db.View:
  │           scanPrefix(V) → for each (tag, value, tvid):
  │             entries[tvid]    = {tag, value, OriginPersistent}
  │             byPair[{t,v}]    = tvid
  └── store.tvids = tvids
        // server now safe to accept traffic
```

## Flow: Persistent write — UpdateTagValues happy path (R1959–R1963)

```
Store.UpdateTagValues(chunkTags)
  └── db.Update(func(txn) error {
        tt := store.tvids.Begin()
        defer func() { if !committed { tt.Abort() } }()

        for ct in persistent:
          writeChunkTagValuesInTxn(txn, tt, ct)
            └── for each (tag, value):
                  fullKey, _, tvid := findVRecord(txn, tag, value)
                  if fullKey == nil:
                      tvid := allocIDInTxn(txn, IFieldNextTvid)
                      txn.Put(V[tag][value][tvid], chunkid varint)
                      tt.Add(tvid, tag, value, OriginPersistent)
                  else:
                      append chunkid to existing V blob
                      // tvid already in live map (from startup or
                      // a previous commit); no Add needed

        commit txn → tt.Commit()
            └── live map gains the new tvids under write lock
      })
```

If `writeChunkTagValuesInTxn` returns an error, the deferred
`tt.Abort` discards the overlay. `db.Update` rolls back the
txn. The live map never sees the in-flight tvids. (R1962, R1969)

## Flow: Persistent removal — orphan-chunk cleanup (R1963)

```
microfts2 RemovedChunkCallback(txn, chunkID)
  └── Store.RemoveTagValuesInTxn(txn, chunkID)
        └── tt := store.tvids.Begin()  // NB: nested under microfts2's
                                       // db.Update — same one-writer
                                       // invariant holds

            removeChunkIDInTxn(txn, tt, chunkID)
              ├── scan F[chunkID] → tvidSet
              ├── decrement T totals, drop F records
              └── scan V prefix:
                    if parseVKey(k).tvid in tvidSet:
                      newBlob := removeVarint(blob, chunkID)
                      if len(newBlob) == 0:
                          cur.Del()
                          tt.Remove(tvid)   // V record gone
                      else:
                          txn.Put(k, newBlob)

            on success: tt.Commit()
            on error:   tt.Abort()
```

`tt.Remove` only fires when the V record is fully deleted. If the
record still has chunkids, the tvid stays in the live map — only the
chunkid is removed.

## Flow: tmp:// write — overlay tvid allocation (R1964–R1967)

```
Store.UpdateTagValues(chunkTags)
  └── partitionChunkTags → overlay map[fileid][]ChunkTagValues
        └── for each fileid:
              TmpTagStore.UpdateTagValues(fileid, chunkTags)
                └── for each (chunkID, []TagValue):
                      tvids := []
                      for each (tag, value) in TagValue:
                          tvid, ok := store.tvids.Lookup(tag, value)
                          if !ok:
                              tvid = store.tvids.AllocOverlay(tag, value)
                          tvids = append(tvids, tvid)
                      chunkEntry.tvids = tvids
                      // store per-chunk tvid list, not (tag, value)
```

`AllocOverlay` decrements `nextOverlay` from `MaxUint64`. The high bit
(set when read as int64) marks the tvid as overlay-issued — same
discriminator pattern as overlay chunkids and fileids. (R1965)

`Lookup` finds either persistent tvids (loaded at startup) or
previously-allocated overlay tvids. The first overlay producer of a
new (tag, value) creates `OriginOverlay`; subsequent producers just
reuse it.

## Flow: tmp:// removal — origin-aware tvid cleanup (R1967)

```
TmpTagStore.RemoveFile(fileID)
  ├── enumerate fileChunks[fileID] → chunk tvid lists
  ├── for each chunkID:
  │     drop chunk entry, decrement tag counts
  │     for each tvid in entry.tvids:
  │         if tvid no longer referenced by any tmp:// chunk:
  │             entry := store.tvids.entries[tvid]
  │             if entry.Origin == OriginOverlay:
  │                 delete(store.tvids.entries, tvid)
  │                 delete(store.tvids.byPair, {t,v})
  │             // OriginPersistent: leave it; the index still owns it
  └── delete fileChunks[fileID]
```

`OriginPersistent` tvids stay in the live map across `tmp://` removals
because the corresponding V record is still on disk. Only tvids
*born* in the overlay get GC'd when their last overlay producer
disappears.

## Flow: Read path (no txn)

```
caller has tvid t (e.g. from F record, or ext map in EXT.md step 7)
  └── store.tvids.Resolve(t)
        ├── RLock
        ├── entry := entries[t]
        └── return entry.Tag, entry.Value, true
```

No transaction needed. Reads see only committed state. Reads
inside a write txn must use `tt.Resolve` instead so they see
in-flight allocations.

## Notes

- Only one TvidTxn is ever live (write-actor invariant). Commit and
  Abort don't need cross-txn coordination beyond TvidMap's own RWMutex.
- `Store.AllocTagValueID` keeps allocating from `I[next_tvid]` for
  persistent V records — no change. `TvidMap.AllocOverlay` is a
  separate counter for overlay-issued tvids only.
- `ScanVRecordTvids` becomes `Snapshot` filtered to `OriginPersistent`
  entries (or just the whole map if callers don't care about origin).
- The ext map (EXT.md step 7) consumes this resolver: ext entries
  store tvids and call `Resolve` for display, with no V-record scan.
