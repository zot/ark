# EC Record Rekey: chunkID-based Embedding

Rekey EC records from `(fileID, chunkIdx)` to `chunkID`. One
embedding per unique content instead of one per file reference.

Language: Go. Environment: `store.go`, `librarian.go`, `indexer.go`,
`db.go`, `search.go`.

## Problem

microfts2 deduplicates chunk content — same text gets one C record
with a unique chunkID, shared across files. But EC records are keyed
by `(fileID, chunkIdx)`, so the same content gets embedded and stored
once per file reference:

- 296K chunk references across files
- 157K unique C records (chunkIDs)
- ~2x wasted GPU compute and LMDB storage

## New EC Key Format

Key: `EC` + varint(chunkID)
Value: float32 vector (768 dims = 3072 bytes)

One record per unique chunk content. The chunkID is the globally
unique identifier allocated by microfts2 when a chunk is first
indexed (dedup-aware — same content reuses the same chunkID).

## Embedding Pipeline Changes

### BatchEmbedChunks

The current pipeline scans files, checks per-file (fileID, chunkIdx)
for existing EC records, and embeds missing ones. With chunkID keys:

1. Scan files in priority order (unchanged).
2. For each file, read the F-record to get `[]FileChunkEntry{ChunkID}`.
3. For each chunkID, check if EC[chunkID] exists.
4. If not, queue the chunk for embedding.
5. After embedding, write EC[chunkID] = vector.

The high-water dedup tracking (lastEmbedded map) is no longer
needed — the chunkID check is the dedup. If a chunk was already
embedded (by any file that shares it), EC[chunkID] exists and the
check skips it in O(1).

### Chunk Cleanup on File Removal

When a file is removed or re-indexed, some chunks may lose their
last file reference. microfts2 provides callbacks for this:

- `RemoveFileWithCallback(path, fn)`: callback receives orphaned
  chunkIDs — chunks whose C records were deleted because no file
  references them anymore.
- `ReindexWithCallback(path, strategy, fn)`: callback receives both
  orphaned chunkIDs (from old version) and new chunkIDs (from new
  version).

The ark indexer wires these callbacks to delete EC records for
orphaned chunks in the same LMDB transaction. Chunks that are
removed but still referenced by other files keep their EC records.

### EF Centroids

EF records (file centroids) are still useful — search uses them for
about-mode filtering (brute-force cosine scan against all file
centroids). With chunkID-keyed EC records, the centroid computation
changes:

- Read the file's F-record to get its chunk list
- For each chunkID in the list, look up EC[chunkID]
- Average the vectors: centroid = sum(vecs) / count

EF records are recomputed:
- After BatchEmbedChunks processes a file (new embeddings written)
- During ReindexCallback (old centroid invalid, new one computed
  from new chunk list)

EF key format is unchanged: `EF` + varint(fileID). The value is
still float32 sum + uint32 count. The centroid is derived from EC
records keyed by chunkID, but cached per-file for search.

When a file is removed, its EF record is deleted (same as today).
When a file is re-indexed, its EF record is recomputed from the
new chunk list's EC records.

## Store API Changes

### Modified

- `WriteChunkEmbedding(chunkID uint64, vec []float32)`: key changes
  from (fileID, chunkIdx) to chunkID. Single argument.
- `WriteChunkEmbeddingBatch(chunks []ChunkVec)`: ChunkVec changes
  to `{ChunkID uint64, Vec []float32}`.
- `ReadChunkEmbedding(chunkID uint64)`: key changes to chunkID.
- `RemoveFileChunkEmbeddings(fileID)`: REMOVED. Replaced by
  per-chunkID deletion in the reindex/remove callbacks.
- `DeleteChunkEmbedding(chunkID uint64)`: key changes to chunkID.
  Called from callbacks for orphaned chunks.

### Unchanged

- `WriteFileCentroid`, `ReadFileCentroid`, `ScanFileCentroids`:
  EF records still keyed by fileID.
- `DropChunkEmbeddings`: drops all EC and EF records (rebuild/
  model mismatch).

### New

- `ReadChunkEmbeddings(chunkIDs []uint64) [][]float32`: batch read
  for centroid computation. One View transaction, multiple lookups.

## Indexer Wiring

### executeFullRefresh

Currently calls `ReindexWithContent` + `RemoveFileChunkEmbeddings`.
Change to `ReindexWithCallback`:

```go
fts.ReindexWithCallback(path, strategy, func(txn *lmdb.Txn, orphans, newIDs []uint64) error {
    for _, id := range orphans {
        store.DeleteChunkEmbeddingInTxn(txn, id)
    }
    return nil
}, opts...)
```

New chunkIDs are embedded in the next BatchEmbedChunks pass (not in
the callback — GPU compute doesn't belong in a transaction).

### RemoveFile / RemoveByID

Change `fts.RemoveFile(path)` to `fts.RemoveFileWithCallback(path, fn)`:

```go
fts.RemoveFileWithCallback(path, func(txn *lmdb.Txn, orphans []uint64) error {
    for _, id := range orphans {
        store.DeleteChunkEmbeddingInTxn(txn, id)
    }
    return nil
})
```

Also delete the EF centroid for the file.

### Transaction-aware Store methods

The callbacks receive a `*lmdb.Txn` from microfts2. The Store needs
methods that operate on an existing transaction rather than opening
their own. This avoids nested transactions (LMDB doesn't support
them within a single goroutine).

- `DeleteChunkEmbeddingInTxn(txn *lmdb.Txn, chunkID uint64)`
- `DeleteFileCentroidInTxn(txn *lmdb.Txn, fileID uint64)`

These use the provided txn and the Store's dbi handle.

## Validate Updates

`ark embed validate` changes:

- Orphan EC: EC records whose chunkID has no C record in microfts2.
- Missing EC: chunkIDs that have C records but no EC record.
- EF/EC consistency: EF centroid count matches the number of EC
  records resolvable from the file's F-record chunk list.
- Dimension check: unchanged.
- `--fix` deletes orphan EC records (chunkID without C record).

## Migration

On startup, detect old-format EC records (key starts with "EC" and
has two varints = fileID + chunkIdx). If found, drop all EC and EF
records. The next BatchEmbedChunks pass re-embeds everything with
the new key format.

Detection: read the first EC key. If it decodes to two varints, it's
old format. New format has one varint. This is safe because
varint-encoded chunkIDs won't collide with the two-varint pattern
at the key length level (old keys are longer).

Alternative: simpler — just bump a version counter in the I records.
Store "ec_version=2" after migration. If absent or 1, drop and
re-embed.
