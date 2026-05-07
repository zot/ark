# Vector Freshness Substrate

Per-record transaction-serial side index. Stamps every embedding-record
write with the LMDB transaction's ID so corpus-wide derived
computations (the hot-correlations sweep in Phase 1E, EF centroid
freshness, etc.) can identify which records changed since the last
pass instead of recomputing from scratch.

This spec covers only the **substrate** — the side-index storage and
the read/write helpers. No clients, no caches. Phase 1E layers the
hot-correlations engine on top.

Language: Go. Environment: ark server, LMDB via
`github.com/bmatsuo/lmdb-go` v1.8.0.

## Context

Once `ED` records populate (Phase 1A) and the orphan-detection sweep
ships (Phase 1E), one full corpus pass is ~270 × 48K × 5μs ≈ 65 s.
Chunk-against-chunk goes quadratic at 48K × 48K ≈ 3.25 hours.
Recomputation has to amortize.

The amortization key is "what changed since last sweep?" — answered
by a per-record stamp. LMDB already assigns each write transaction a
monotonic ID via `txn.ID()` (returns `uintptr`, sourced from
`mdb_txn_id`). Stamping every record written in a txn with that
txn's ID gives a per-transaction monotonic serial: records written
together share a serial, records written later have a strictly
greater serial.

## Side Index

A new prefix `S` (single byte, mnemonic "Serial") holds the side
index:

```
S + <original-prefix-bytes> + <original-key-tail>  →  varint-encoded uint64
```

Examples:

```
ST<tagname>                  → serial of last T-name embedding write
SEV<tvid-varint>             → serial of last EV write
SED<tagname><fileid:8>       → serial of last ED write
SEC<chunkID-varint>          → serial of last EC write
```

Single-byte `S` was chosen over the originally-sketched `TS`. `T` is
already allocated (`prefixTagTotal`) with variable-length tagname
suffixes — any tagname starting with `S` would collide with a `TS*`
key in cursor scans. `S` is unallocated and disjoint from every
existing prefix's first byte.

The value is varint-encoded (`binary.PutUvarint` /
`binary.Uvarint`). Typical txn counts at this corpus age fit in 3-4
bytes; a fully-loaded uint64 still fits in 10. Byte-comparison sort
order does not match numeric order under varint, but the side index
is keyed by `S<...>` and walks happen in key order, never in
serial-value order — `since N` filtering is a numeric comparison done
after decode, regardless of encoding.

The serial comes from a maintained counter in an I record
(`I:serial`), not from `txn.ID()`. ark compacts its database at
startup via `mdb_env_copy(MDB_CP_COMPACT)`, and LMDB's compact-copy
may reset `mt_txnid` on the destination. An I-record counter sits in
the live B-tree, gets preserved by every compact-copy, and is
monotonic across the database's entire lifetime regardless of LMDB's
internal txn bookkeeping.

The original record's value is **unchanged**. Existing readers
(`bytesToFloat32`, etc.) work without modification — the side index
is purely additive.

### Why a side index, not embedded in the value

Three considerations weighed against splicing the serial into each
record's value:

- **Regularity across record types.** Every stamped record uses the
  same `S` + original-key shape and the same 8-byte value. Stamping
  is a generic helper that doesn't know what kind of record it's
  stamping; the same scheme extends to F, X, schedule, or any other
  prefix later. Value-embedding is per-record-type bespoke — T's
  serial would sit after `count:4`, EV/EC/ED's at offset 0, and each
  future record type would have to re-decide its layout.
- **No reader changes.** Existing readers (`bytesToFloat32(v[4:])`
  for T, `bytesToFloat32(v)` for EV/EC/ED) keep working unchanged.
  Value-embedding would touch every read call site plus add a
  length-discrimination helper to handle pre-substrate records
  during a migration window.
- **Diagnostics.** All serials live under one prefix. Walking the
  full set is one cursor scan over `S`; `ark status -db` reports it
  as one row. Value-embedding would require per-record-type walkers.
- **Compact-copy renumbering is local.** Because all serials live
  under one prefix, the startup compact-copy can run a one-shot
  pass that subtracts the minimum serial from every S record and
  resets the I-record counter to `(max - min + 1)`. Keeps varint
  widths small over many compactions. Value-embedding would require
  touching every embedding-record value to do the same. (Optional
  enhancement; not required for correctness.)

The cost is one I-record read+write per txn (allocating the serial)
plus one extra `txn.Put` per stamped record (writing the S-entry),
and roughly 1 MB more on disk at current corpus size (~20-30 bytes
per S record × ~52K records, vs. 3-4 bytes × 52K for value-embedding
with varint). The write cost is modest in a txn that's already open;
the storage delta is under 1% of the embedding records' own
footprint.

## Stamping

Every write to one of the four embedding methods writes a parallel
`S*` entry in the **same LMDB transaction**:

- `Store.WriteTagNameEmbedding(tag, vec)` → stamps `ST<tag>`.
- `Store.WriteTagValueEmbedding(tvid, vec)` → stamps `SEV<tvid-varint>`.
- `Store.WriteTagDefEmbedding(tag, fileid, vec)` → stamps
  `SED<tag><fileid:8>`.
- `Store.WriteChunkEmbedding(chunkID, vec)` → stamps
  `SEC<chunkID-varint>`.
- `Store.WriteChunkEmbeddingBatch(chunks)` → stamps `SEC<...>` once
  per chunk, all sharing the batch's single allocated serial.

Stamping is performed by three internal helpers:

```go
// allocSerial reads the I-record serial counter, advances it by 1,
// writes back, and returns the value to use for the current txn's
// stamps. Multiple records stamped within one txn share one
// serial — the caller calls allocSerial once and passes the
// returned value to every stampWriteWith call in that txn.
func allocSerial(txn *lmdb.Txn) (uint64, error)

// stampWriteWith writes the S-side-index entry for (prefix + key)
// using a caller-supplied serial. Caller is responsible for the
// original record's txn.Put.
func stampWriteWith(txn *lmdb.Txn, prefix, key []byte, serial uint64) error

// stampWrite is the convenience wrapper for single-record callers:
// allocates a serial via allocSerial and stamps in one call.
func stampWrite(txn *lmdb.Txn, prefix, key []byte) error
```

All three are unexported. The single-record `Write*Embedding`
methods (`WriteTagNameEmbedding`, `WriteTagValueEmbedding`,
`WriteTagDefEmbedding`, `WriteChunkEmbedding`) call `stampWrite`
after their existing `txn.Put` for the value.
`WriteChunkEmbeddingBatch` calls `allocSerial` once at the top of
its callback and `stampWriteWith` in its loop, so all batch records
share one serial.

### Per-txn semantics

All records stamped within one LMDB write transaction share a single
serial — "records that moved together carry the same mark." Across
transactions, serials are strictly monotonic.

## Deletion

When a stamped record is deleted, its S-entry is deleted in the same
txn. Three call sites:

- `Store.DeleteChunkEmbedding(chunkID)` and
  `Store.DeleteChunkEmbeddingInTxn(txn, chunkID)` drop the matching
  `SEC<chunkID>` entry.
- `Store.UpdateTagDefs` (when replacing a fileid's D records, drops
  the fileid's old ED records) drops the matching `SED<tag><fileid>`
  entries — the existing `delByFileid` loop is extended to walk the
  `SED` prefix as well.
- `Store.DropEmbeddings` (model-swap drop of T+EV+ED) drops every
  `ST*`, `SEV*`, and `SED*` entry. EC is not part of `DropEmbeddings`
  and `SEC*` is left intact.
- `Store.DropChunkEmbeddings` (rebuild's EC+EF drop) drops every
  `SEC*` entry alongside the EC delete. EF is not stamped, so no
  side-index entries are involved on its side.

Tombstone serials (sentinel values that mark "deleted") are *not*
introduced. Lookup-then-delete is fine at this scale; revisit only if
deletion churn becomes the bottleneck.

## Read API

Two new public methods on `Store`:

```go
// RecordSerial returns the txn serial of the last write to the
// record at (prefix + key). The found bool distinguishes "no
// S-entry" (never stamped) from "stamped with serial 0" — both
// return serial=0, but only stamped records return found=true.
func (s *Store) RecordSerial(prefix, key []byte) (serial uint64, found bool, err error)

// WalkRecordsSinceSerial walks the S<prefix> side index in key
// order, calling fn for every entry whose stamped serial > since.
// fn receives the original record's key (S<prefix><tail> with the
// leading 'S' byte stripped) and the stamped serial. Stop iteration
// by returning a non-nil error from fn.
func (s *Store) WalkRecordsSinceSerial(
    prefix []byte,
    since uint64,
    fn func(originalKey []byte, serial uint64) error,
) error
```

Callers pass the original prefix bytes (e.g., `[]byte("EC")` or
`[]byte{byte(prefixTagTotal)}`). The substrate prepends `S`.

`originalKey` is the full original record key (including the prefix
bytes). Callers can hand it directly to existing readers.

### Cold-start vs incremental

`WalkRecordsSinceSerial` only visits records that have an S-entry.
Records written before the substrate landed have no S-entry until
the next write touches them. Clients doing a from-scratch sweep
should walk the data prefix directly (e.g., the existing
`scanPrefix` over `EC`), record `last_sweep_serial = currentSerial`
on completion, and switch to `WalkRecordsSinceSerial` for incremental
refresh thereafter. The substrate does not backfill.

## Lifecycle Interactions

- **`ark rebuild`** drops all embedding records and rewrites them
  fresh. S-entries are dropped alongside (rebuild's existing reset
  path handles this — every prefix that gets cleared loses its
  matching `S*` entries because the four `Write*Embedding` methods
  stamp on each rewrite).
- **Model swap (`tag_model` change)** triggers `DropEmbeddings`,
  which drops T+EV+ED + their `S*` side-index entries together.
  Next batch-embed pass repopulates with fresh serials from the
  swap-time txn onward.
- **Crash mid-write** is handled by LMDB's transactional guarantees:
  either the value write and the S-entry both land, or neither does.
  No half-stamped records.
- **Compact-copy at startup** (`mdb_env_copy(MDB_CP_COMPACT)`)
  preserves the I-record counter and every S-record because both
  live in the active B-tree. The counter never resets, regardless
  of whether LMDB's internal `mt_txnid` does. A future enhancement
  may renumber serials during compact-copy (subtract the minimum
  serial from every S record, reset the counter to `max - min + 1`)
  to keep varint widths small over many compactions; not required
  for correctness.

## What This Does Not Do

- **No HC cache.** The hot-correlations cache and its incremental
  sweep are Phase 1E. This substrate provides only the freshness
  signal those caches will read.
- **No client of the API.** `WalkRecordsSinceSerial` and
  `RecordSerial` are exported for Phase 1E onward. No code path in
  Phase 1C calls them outside tests.
- **No EF / file-centroid stamping.** Phase 1C stamps the four named
  writers. EF centroid freshness, `BatchEmbed` dedup, and search
  snapshot consistency are noted in `.scratch/VECTOR-FRESHNESS.md`
  as future generalizations and are out of scope.
- **No tombstones.** Deleted records' S-entries are removed
  alongside the value; clients reconcile cache rows by lookup. No
  sentinel values.
- **No backfill.** Records that exist before the substrate lands
  have no S-entry until the next write. Clients handle cold-start
  by walking the data prefix directly the first time.
- **No status display.** `ark status -db` does not list the `S`
  prefix specially — `RecordCounts` will report it generically as
  any other prefix is reported.

## Storage Scale

Per S record at current corpus age: ~3-4 byte varint value + S key
(1 byte 'S' + 1-2 prefix bytes + key tail) + LMDB B-tree node
overhead. Roughly 17-30 bytes per record on average; ~52K records ≈
1-1.5 MB total. The varint encoding saves ~5 bytes per record vs.
fixed 8-byte values (~260 KB at current scale, scales linearly as
more record types adopt stamping). Negligible against the 6.8 MB EF,
4.0 MB EV, 141 MB EC budgets already in use.

## Performance

- **Write path (single record):** one I-record read + one I-record
  write to allocate the serial, plus one `txn.Put` for the S-record,
  on top of the existing value put. The I-record is small (single
  key, single B-tree page) and stays in LMDB's page cache after the
  first access. At LMDB's typical 5–20 μs per put, single-record
  writes pay roughly one extra put's worth of latency.
- **Write path (batch):** one allocation up front, then one extra
  `txn.Put` per record (the S-stamp). A 256-chunk batch is one
  alloc + 256 value puts + 256 stamp puts, all in one txn.
- **Read path (RecordSerial):** one `txn.Get` against the S-side
  index plus a varint decode. Sub-millisecond.
- **Read path (WalkRecordsSinceSerial):** one cursor scan over
  `S<prefix>`, varint-decoding each value and filtering by `>
  since`. Linear in the number of side-index entries for that
  prefix; ~50–100 ns per entry for decode + comparison + callback
  dispatch.
