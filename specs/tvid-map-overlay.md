# Tvid Map and Transaction Overlay

A live in-memory map `tvid → (tag, value)` covering every `(tag, value)`
pair the index has seen, persistent and `tmp://` alike. Loaded at
startup from V records, maintained by an indexing-time overlay that
mirrors the index's transaction semantics.

This spec is point 3 of the external-tags roadmap (`.scratch/EXT.md`).
It is the foundation for fast tvid resolution and for the ext map
(points 6-9), which needs `(target chunkid, source chunkid, tvid)`
without re-scanning V records.

## Why an in-memory map

V records key tvids into the trailing varint of `V[tag]\0[value]\0[tvid]`.
Resolving a tvid back to its `(tag, value)` today requires a full scan of
the V prefix (`Store.ScanVRecordTvids`). The tag-value cardinality is
small (≈1221 values today, comfortably small for several orders of
magnitude of growth), so a full map fits in memory. Resolution becomes
a constant-time read.

The map also unifies persistent and `tmp://` tvids. Persistent V
records carry tvids today; `TmpTagStore` does not (R1951, deferred).
With a shared resolver, both code paths look up tvids the same way and
inbox/search readers stop branching on origin.

## Why a transaction overlay

Tvid allocations happen inside write transactions (the write
actor's `db.Update`). If a transaction aborts, the I[next_tvid]
counter rolls back with it, but a naively-mutated in-memory map would
keep the doomed tvid. Mirroring the index's MVCC semantics — writes
accumulate in a scoped overlay struct, visible to reads inside the
transaction, merged into the live map on commit, discarded on abort —
keeps the map honest under crashes and aborts and matches what the
write actor already does for the index itself. Also, uncommitted changes
are only visible within the transaction until the actual commit.

## Surface

A new collaborator `TvidMap` owned by `Store`:

- `TvidMap.Resolve(tvid uint64) (tag, value string, ok bool)` —
  returns the `(tag, value)` for a tvid, or `ok=false` if unknown.
  Reads the live map under RLock.
- `TvidMap.Lookup(tag, value string) (tvid uint64, ok bool)` —
  reverse lookup for callers that have a `(tag, value)` and want the
  existing tvid without an index scan. Optional sibling map.
- `TvidMap.Snapshot() map[uint64]TagAlt` — returns a copy for
  diagnostics (`ScanVRecordTvids` becomes a thin wrapper).

A new collaborator `TvidTxn` scoped to one write transaction:

- `TvidTxn.Add(tvid uint64, tag, value string, origin TvidOrigin)` —
  registers a tvid in the overlay. Subsequent `Resolve` within the
  same txn returns it.
- `TvidTxn.Remove(tvid uint64)` — marks a tvid for removal on commit.
  Live-map reads inside the txn skip it; reads outside still see it
  until commit.
- `TvidTxn.Resolve(tvid uint64) (tag, value string, ok bool)` —
  consults the overlay first, then the live map. Used by code running
  inside the txn (e.g., orphan-chunk cleanup that resolves tvids it
  is about to remove).
- `TvidTxn.Commit()` — merges added/removed entries into the live map
  under write lock.
- `TvidTxn.Abort()` — discards the overlay.

`TvidOrigin` is `OriginPersistent` or `OriginOverlay`. It identifies
where the tvid was first introduced; readers do not branch on origin,
but `RemoveFile` for `tmp://` content uses it to drop tvids whose only
producer was overlay content.

Each entry's origin is stored alongside `(tag, value)` so the live map
keeps it as authoritative metadata. A persistent tvid that later
acquires an overlay producer keeps `OriginPersistent` — origin is
where the tvid was born, not who currently uses it.

## Startup load

`DB.Open` (after `Store` is wired but before the server accepts
traffic) calls `Store.LoadTvidMap()`, which scans V records via
`ScanVRecordTvids` semantics and populates the live map with
`OriginPersistent` entries. Cost is one V-prefix scan, performed once
per process lifetime.

## Transaction integration

The write actor's `db.Update` blocks become:

```go
return s.db.Update(func(txn *bbolt.Tx) error {
    tt := s.tvids.Begin()
    err := writeChunkTagValuesInTxn(txn, tt, ...)
    if err != nil {
        tt.Abort()
        return err
    }
    tt.Commit()
    return nil
})
```

`Begin()` returns a fresh `TvidTxn`. `addChunkIDToVRecord` allocates a
tvid via `allocIDInTxn` as today, then calls `tt.Add(tvid, tag, value,
OriginPersistent)`. `removeChunkIDInTxn` already iterates V records
to remove chunkids; if it deletes a V record entirely it calls
`tt.Remove(tvid)` so the live map drops the orphan.

`Commit` merges into the live map under write lock. `Abort` is called
on error and on panic via `defer`. Only one write txn runs at a time
(write-actor invariant), so only one `TvidTxn` is ever live; no
overlay-merge contention.

Reads outside any write txn use `TvidMap.Resolve` directly.

## Tmp:// integration (R1951)

`TmpTagStore.AppendTagValues` and friends currently store
`(tag, value)` strings on each chunk entry. After this change:

1. Each `(tag, value)` is reconciled with the shared `TvidMap`.
   `Lookup(tag, value)` returns the existing tvid if any. Otherwise
   `TvidMap.AllocOverlay(tag, value)` allocates a new tvid with
   `OriginOverlay` and registers it in the live map.
2. Per-chunk entries store tvids; `(tag, value)` is resolved on read
   via `TvidMap.Resolve`. This removes the per-chunk string duplication
   and aligns the overlay's runtime shape with the persistent store.
3. `RemoveFile` enumerates the file's tvids; for each, it removes the
   chunk's contribution. If a tvid loses its last `tmp://` producer
   AND its origin is `OriginOverlay`, it is dropped from the live map.
   `OriginPersistent` tvids persist regardless — the index record still
   owns them.

Overlay tvid allocation uses a separate counter from the
`I[next_tvid]` index counter. The natural choice is to count down from `MaxUint64`,
mirroring the chunkid/fileid overlay convention: the high bit (set
when read as int64) marks a tvid as overlay-issued. This makes the
overlay/persistent distinction available without consulting any
external map, and guarantees no collision with the persistent counter that
counts up from 1.

## Lifetime and recovery

- The map lives in process memory. Server restart performs the V-scan
  load again. No persistence beyond V records themselves.
- No schema marker, no version check, no `ark rebuild` interaction.
  The V records are the source of truth; the map is a view.
- Crash safety: if the process dies mid-write, the index rolls back the
  write txn. The next startup reloads from V records. Overlay entries
  for that aborted txn never enter the live map because `Commit` was
  never called.

## Out of scope

- The ext map (target chunkid → ext entries). Built on top of this
  spec but lives in `EXT.md` step 7.
- Reverse lookup tables for `(tag, value) → tvid` beyond the
  occasional `Lookup` call. The forward map is the one that matters.
- Tag-name embedding (T records). T keys are the tag name itself; no
  tvid involved. Out of scope.
- Tvid retirement / GC sweeps. The live map mirrors V records; an
  empty V record is already cleaned up by `removeChunkIDInTxn`, which
  calls `tt.Remove(tvid)`. No background sweep needed.
