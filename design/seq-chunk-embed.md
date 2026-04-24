# Sequence: Chunk Embedding Pipeline
**Requirements:** R1593, R1594, R1595, R1596, R1609, R1610, R1611, R1612, R1613, R1614, R1615, R1616, R1617, R1618, R1620, R1830, R1831, R1833, R1835, R1846, R1847, R1848, R1849, R1852, R1854, R1862, R1863, R1864

Post-reconcile chunk embedding. Runs after tag embedding (BatchEmbed)
completes. One model, multiple tier contexts, bucket-based dispatch.
EC records keyed by chunkID (one per unique content). In-batch seen
set prevents the same chunkID from being queued multiple times when
shared across files (R1862, R1863).

## Model Load (first use)

```
Librarian.ensureModel()
  │
  ├─ LoadModel(modelPath)                    # ~1s, one-time
  │
  ├─ model.NewContext(2048/8)                # default ctx for tags/queries
  │
  ├─ for each tier in config.EmbedTiers:
  │    model.NewContext(tier.ctx, tier.parallel)   # tier contexts
  │
  └─ start TTL timer
```

## BatchEmbedChunks (post-reconcile)

```
Server.doReconcile
  └─ enqueueWrite ──────────────────────────────────────────────────────────┐
                                                                            │
  Write goroutine:                                                          │
    BatchEmbed()        # tags first                                        │
    BatchEmbedChunks()  # then chunks                                       │
      │                                                                     │
      ├─ collect files (priority sort, exclude search_exclude)              │
      │   seen := map[uint64]bool{}              # in-batch dedup (R1862)   │
      │                                                                     │
      ├─ for each file:                                                     │
      │   finfo := fts.FileInfoByID(fileID)     # F-record with chunk list  │
      │   for each entry in finfo.Chunks:                                   │
      │     if seen[entry.ChunkID]: skip (deduped, R1863)                   │
      │     ec := store.ReadChunkEmbedding(entry.ChunkID)                   │
      │     if ec != nil: skip (already embedded, R1847)                    │
      │     seen[entry.ChunkID] = true                                      │
      │     route to tier bucket by chunk byte size                         │
      │                                                                     │
      ├─ Pass 2: embed one tier at a time (sequential, R1830)               │
      │   for each tier with non-empty bucket:                              │
      │     ctx := createTierCtx(tier)                                      │
      │     for batches of tier.Parallel:                                   │
      │       ┌────────────────────────────────────────┐                    │
      │       │ texts := readChunkContent(batch)       │ off-actor          │
      │       │ vecs := embedWithCtx(ctx, texts)       │ GPU compute        │
      │       └────────────────────────────────────────┘                    │
      │       store.WriteChunkEmbeddingBatch(cvs)  # keyed by chunkID      │
      │     ctx.Close()                                                     │
      │                                                                     │
      ├─ Recompute EF centroids (once, after ALL tiers, R1848)              │
      │   for each file that had missing chunks:                            │
      │     chunkIDs := finfo.Chunks[*].ChunkID                            │
      │     vecs := store.ReadChunkEmbeddings(chunkIDs)                    │
      │     centroid := average(vecs)                                       │
      │     store.WriteFileCentroid(fileID, sum, count)                     │
      │                                                                     │
      └─ done                                                               │
```

## Chunk Cleanup on File Removal (R1850, R1852, R1853)

```
Indexer.RemoveFile(path)
  │
  ├─ fts.RemoveFileWithCallback(path, func(txn, orphans) {
  │     for each orphanedChunkID:
  │       store.DeleteChunkEmbeddingInTxn(txn, chunkID)
  │     store.DeleteFileCentroidInTxn(txn, fileID)
  │  })
  │
  └─ remove tags, tag defs, tag values, page contents
```

## Chunk Cleanup on Re-Index (R1849, R1852, R1854)

```
Indexer.executeFullRefresh(prep)
  │
  ├─ fts.ReindexWithCallback(path, strategy, func(txn, orphans, newIDs) {
  │     for each orphanedChunkID:
  │       store.DeleteChunkEmbeddingInTxn(txn, chunkID)
  │     store.DeleteFileCentroidInTxn(txn, fileID)  # will be recomputed
  │  })
  │
  ├─ (newIDs embedded in next BatchEmbedChunks pass, R1854)
  │
  └─ update tags, tag defs, tag values
```

## Model Mismatch / Migration (R1859, R1860)

```
Server startup
  │
  ├─ check ec_version I record
  │   if absent or "1": drop all EC + EF, set ec_version = "2"
  │
  ├─ detect tag_model changed (E condition "model_mismatch")
  │   store.DropEmbeddings()           # T vectors + EV records
  │   store.DropChunkEmbeddings()      # EC + EF records
  │
  └─ next reconcile re-embeds everything
```

## Tier Routing

```
chunk (1200 bytes)
  │
  ├─ tier 1: 1024/32 → max 96 bytes    SKIP
  ├─ tier 2: 2048/16 → max 384 bytes   SKIP
  ├─ tier 3: 2048/8  → max 768 bytes   SKIP
  ├─ tier 4: 16384/12 → max 4095 bytes HIT → bucket[4]
  └─ (tier 5: 16384/8 → max 6144 bytes not reached)
```
