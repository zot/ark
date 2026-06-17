# TvidMap
**Requirements:** R1953, R1954, R1955, R1956, R1957, R1958, R1959, R1960, R1961, R1962, R1963, R1965, R1968, R1969

In-memory `tvid → (tag, value, origin)` resolver shared by `Store`
(index V/F records) and `TmpTagStore` (`tmp://` overlay). Loaded once
at startup from V records; maintained at indexing time via a
transaction overlay that mirrors the index's commit/abort semantics.

## Knows
- entries: map[uint64]tvidEntry — tvid → {tag, value, origin}
  (the live map; reads use RLock)
- byPair: map[tvidKey]uint64 — `(tag, value)` → tvid for reverse
  lookup. Same locking as `entries`.
- mu: sync.RWMutex — guards entries and byPair
- nextOverlay: uint64 — overlay tvid counter, decremented from
  `MaxUint64`; protected by mu (write lock)

## Does
- Resolve(tvid) (tag, value string, ok bool): O(1) read of the live
  map under RLock. Used by every callsite that has a tvid and needs
  the (tag, value). (R1954)
- Lookup(tag, value) (tvid uint64, ok bool): O(1) reverse lookup via
  byPair under RLock. Lets persistent and overlay write paths reuse
  an existing tvid without scanning V records. (R1955)
- Snapshot() map[uint64]TagAlt: copy of the live map for diagnostics.
  `Store.ScanVRecordTvids` becomes a thin wrapper. (R1956)
- LoadFromStore(s *Store): one-time V-prefix scan during DB.Open;
  registers each tvid with `OriginPersistent`. The only V-prefix scan
  needed for tvid resolution per process lifetime. (R1958)
- AllocOverlay(tag, value) uint64: allocate a fresh overlay tvid
  (decrements `nextOverlay`), insert with `OriginOverlay`, return.
  Used by `TmpTagStore` write paths when `Lookup` finds nothing.
  (R1965)
- Begin() *TvidTxn: returns a fresh txn-scoped overlay struct. Only
  one TvidTxn is ever live (write-actor invariant), so no contention.
  (R1959)

## TvidEntry
- Tag, Value: string — the tag-value pair
- Origin: TvidOrigin — `OriginPersistent` (loaded from V record) or
  `OriginOverlay` (allocated for tmp:// content). Set at first
  registration; persistent always wins if both producers exist.
  (R1957)

## TvidTxn (overlay)
Scoped to one write transaction. Reads inside the txn must use
TvidTxn.Resolve (not the live map) so they see in-flight allocations
and removals.

- Add(tvid, tag, value, origin): record an addition in the overlay.
  No mutation of the live map. (R1960)
- Remove(tvid): record a removal in the overlay. Live-map readers
  outside the txn still see it; readers via TvidTxn.Resolve do not.
  (R1960)
- Resolve(tvid) (tag, value string, ok bool): consult overlay first
  (added entries visible, removed entries hidden); fall through to
  the live TvidMap. (R1961)
- Commit(): take TvidMap.mu write lock, merge added/removed entries
  into the live map, release. (R1962)
- Abort(): drop the overlay struct; live map untouched. (R1962)

## Crash and abort safety
- write txn aborts → caller calls TvidTxn.Abort → overlay
  discarded → live map unchanged. Next startup reloads from V
  records (which also rolled back). (R1969)
- commit succeeds → caller calls TvidTxn.Commit → live map
  reflects the durable state.
- Process death mid-write: write txn rolls back; live map is in
  process memory and dies with the process; next startup runs
  LoadFromStore again. (R1969)

## Collaborators
- Store: owns the TvidMap, calls LoadFromStore on Open, calls
  Begin/Commit/Abort around `db.Update` blocks that touch V records.
  Calls `tt.Add` from `addChunkIDToVRecord` and `tt.Remove` from
  `removeChunkIDInTxn` when V records are deleted entirely. (R1963)
- TmpTagStore: calls Lookup before AllocOverlay when writing per-chunk
  entries; resolves tvids back to (tag, value) on read.

## Sequences
- seq-tvid-overlay.md
