# Migration: lmdb-go (CGO) → bbolt (pure Go) store — ark (consumer half)

**Source survey:** `.scratch/BBOLT.md`. This is **PENDING #1a**, the consumer
half. microfts2 (#1) **already landed** (2026-06-12): it owns the store and now
exposes `DB() *bbolt.DB` instead of `Env() *lmdb.Env`, with `*bbolt.Tx` in its
public txn-carrying API. ark imports microfts2 via `~/work/go.work`, so ark
**currently does not compile** until this migration updates the consumer sites.

## Status (2026-06-12) — NOT STARTED

Spec phase. ark clean w.r.t. git except the microfts2 changes already in the
workspace.

## Problem

ark links `github.com/bmatsuo/lmdb-go` (CGO) in its own store (`store.go`,
`db.go`, `compact.go`, `extmap.go`, `indexer.go`, `verify.go`, and ~20 sites
that consume microfts2's env). It is the **last** CGO dependency in the
ecosystem; until it is gone, `CGO_ENABLED=0` and the cross-compile release sweep
(yzma R2971/R2972, PENDING #1b) are blocked.

## State B (target)

ark binds **`go.etcd.io/bbolt`**. It does **not** own the database — microfts2
does. ark's `OpenStore` takes microfts2's `*bbolt.DB` (`fts.DB()`) and opens its
own `"ark"` **bucket** inside it (was: an `ark` named DBI inside microfts2's
shared LMDB env). A `bbolt.Tx` spans all buckets, so the cross-repo atomicity
the index relies on is preserved — the ~18 sites that read microfts2's `fts`
records and ark's `ark` records in one transaction keep working, now via
`tx.Bucket("fts")` and `tx.Bucket("ark")` on the same `*bbolt.Tx`. ark builds
`CGO_ENABLED=0`. DUPSORT is absent in ark (confirmed by survey) — clean port.

## Changes

### Consuming microfts2's new API (the boundary)
- `OpenStore(fts.Env())` → `OpenStore(fts.DB())`; `Store.env *lmdb.Env` field →
  `Store.bolt *bbolt.DB`, `Store.dbi lmdb.DBI` → drop (open the `ark` bucket per
  txn via `tx.Bucket`). `db.go` opens/creates the `ark` bucket at startup.
- `db.fts.Env().View(func(txn *lmdb.Txn))` (~18 sites in db.go, locator.go,
  connections.go, connections_substrate.go, search.go, librarian.go,
  indexer.go) → `db.fts.DB().View(func(tx *bbolt.Tx))`.
- `fts.ReadCRecord(txn, chunkID)` (~12 sites) → `fts.ReadCRecord(tx, chunkID)`
  (`tx *bbolt.Tx`).
- `Store.SetChunkResolver`'s closure (`func(txn *lmdb.Txn, chunkID) []uint64`)
  → `*bbolt.Tx`; the registered resolver in db.go and its callers retype.
- `indexer.go` `RemoveCallback`/`ReindexCallback` closures → `func(tx *bbolt.Tx, …)`
  (microfts2's types changed); the txn-scoped store methods they invoke retype too.
- `db.go` stops passing `microfts2.Options{MaxDBs, MapSize}` — both fields are
  gone from microfts2's Options.

### ark's own store (`store.go`)
- `env.Update/View(func(txn *lmdb.Txn))` (~98 sites) → `bolt.Update/View(func(tx
  *bbolt.Tx))`, deriving the `ark` bucket via `tx.Bucket([]byte("ark"))`.
- `txn.Get(dbi, k)` + `lmdb.IsNotFound` (40 sites in store.go, 1 in verify.go) →
  `b.Get(k)` returning nil for absent keys. `txn.Del(dbi, k, nil)` (+IsNotFound
  guard) → `b.Delete(k)` (no error on missing; drop the guard). `txn.Put(…,0)` →
  `b.Put(k, v)`.
- Txn-scoped helpers that take `(txn *lmdb.Txn, …)` retype to `(tx *bbolt.Tx, …)`
  or take `*bbolt.Bucket`; resolve the bucket once per outer txn.
- The mmap/value-valid-only-within-txn contract is identical; the existing
  copy-out discipline transfers unchanged.

### `scanPrefix` — delete-safe rewrite (#1 correctness risk)
`scanPrefix` (store.go ~834) is the central cursor utility (~50 callers). LMDB
allows `cur.Del(0)` mid-scan with `Next` still valid; bbolt cursor deletion
during a `Seek`/`Next` walk is unsafe at page boundaries. Reimplement scanPrefix
**collect-then-delete**: walk the prefix range read-only collecting matches
(copying key bytes), invoke the per-item callback, and apply any deletes by key
(`b.Delete(k)`) **after** the walk completes. The ~15 mutation callers
(`removeChunkIDInTxn`, `UpdateTagDefs`, `DropChunkEmbeddings`, `DropEmbeddings`,
`DropHotCorrelations`, `ReplaceHotCorrelations`, `Clear*`/`Prune*` for
Discussed/SurfaceCooldown, `clearAllByPrefix`, `RemovePageContents`,
`ClearERecords`) must remain correct under the new semantics.

### `compact.go`
The standalone read-only env + `env.CopyFlag(dir, lmdb.CopyCompact)` →
`db.View(func(tx *bbolt.Tx) { tx.WriteTo(file) })` then rename, or a no-op
(bbolt needs no separate compaction). The two `SetMapSize(2<<30)` calls are
removed (bbolt has no map-size ceiling).

### `StatusInfo` (`db.go` ~2395)
`env.Info()`/`env.Stat()` (MapSize/MapUsed) have no bbolt equivalent → report
the database **file size** (`os.Stat`) instead. The `mapTotal`/`mapUsed`
StatusInfo fields become file-size based (or are renamed); `ark status` output
adjusts accordingly.

### Build & distribution
ark builds `CGO_ENABLED=0`. This unblocks yzma R2971/R2972 + the Makefile
`release` target (PENDING #1b) — out of scope here beyond confirming the
`CGO_ENABLED=0` build passes.

## Gate (before committing)
Benchmark a full `ark rebuild` + a search workload, **BBolt vs LMDB**. bbolt's
weak spot is fsync-per-commit on bulk writes; the batched write actor
(`enqueueWrite`) should neutralize it. "LMDB is just the index" — rebuildable
from files — so BBolt and LMDB can run side-by-side and be diffed.

## Supersede at source (Gaps phase)
- `specs/record-formats.md` — any "LMDB"/`DBI`/env framing → bbolt bucket; the
  `ark status -db` record listing prose.
- `specs/cli-commands.md` — `ark status` MapSize/MapUsed wording → file size;
  `ark compact` semantics if changed.
- `specs/features.md`, `specs/config.md` — storage-engine mentions.
- `design/requirements.md` — retire LMDB-specific Rn (env handle, SetMapSize,
  IsNotFound contract, CopyCompact, MapSize/MapUsed status) with replacements.
- `design/` prose — `crc-Store.md`, `crc-DB.md`, `crc-Indexer.md`,
  `crc-ExtMap.md`, the seq diagrams naming LMDB txns/env, and `CLAUDE.md`/
  `CLAUDE.local.md` build notes that cite the CGO/lmdb constraint.

## Out of scope (follow-ups)
- yzma R2971/R2972 + Makefile `release` target (PENDING #1b) — after the gate.
- microfts2's own `migration-complete` (its retirements + prose sweep) — done
  alongside this one at the end.
- Removing `-buildvcs=false` (still needed: git+fossil both present).
