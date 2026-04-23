# Sequence: Chunk Embedding Pipeline
**Requirements:** R1593, R1594, R1595, R1596, R1609, R1610, R1611, R1612, R1613, R1614, R1615, R1616, R1617, R1618, R1620, R1817, R1818, R1819, R1820, R1822, R1823, R1824, R1825, R1826, R1827, R1828, R1829, R1830, R1831, R1832

Post-reconcile chunk embedding. Runs after tag embedding (BatchEmbed)
completes. One model, multiple tier contexts, bucket-based dispatch.
High-water tracking prevents re-embedding already-processed chunks.

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
      │                                                                     │
      ├─ for each file:                                                     │
      │   chunkLens := fts.ChunkContentLens(fileID)                         │
      │   lastChunkID := fts.FileInfoByID(fileID).Chunks[last].ChunkID     │
      │   prev := lastEmbedded[fileID]                                      │
      │                                                                     │
      │   ┌─ Case 1 (R1823): count==prev.count && lastID==prev.lastID      │
      │   │  → SKIP entirely                                                │
      │   │                                                                 │
      │   ├─ Case 2 (R1824): count==prev.count && lastID!=prev.lastID      │
      │   │  → re-embed chunk[count-1] only                                 │
      │   │  → subtract old EC vector from centroid (R1829)                 │
      │   │                                                                 │
      │   ├─ Case 3 (R1825): count>prev.count && lastID==chunks[prev-1].ID │
      │   │  → embed from prev.count onward (clean append)                  │
      │   │                                                                 │
      │   └─ Case 4 (R1826): count>prev.count && lastID!=chunks[prev-1].ID │
      │      → embed from prev.count-1 onward (boundary changed)            │
      │      → subtract old EC vector for boundary chunk (R1829)            │
      │                                                                     │
      │   seed centroid from stored EF sum/count (R1828)                    │
      │   route chunks to tier buckets by byte size                         │
      │                                                                     │
      ├─ Pass 2: embed one tier at a time (sequential, R1830)               │
      │   for each tier with non-empty bucket:                              │
      │     ctx := createTierCtx(tier)                                      │
      │     for batches of tier.Parallel:                                   │
      │       ┌────────────────────────────────────────┐                    │
      │       │ texts := readChunkContent(batch)       │ off-actor          │
      │       │ vecs := embedWithCtx(ctx, texts)       │ GPU compute        │
      │       └────────────────────────────────────────┘                    │
      │       store.WriteChunkEmbeddingBatch(cvs)                           │
      │       addToCentroid(fileID, vecs)                                   │
      │     ctx.Close()                                                     │
      │                                                                     │
      ├─ Write EF centroids (once, after ALL tiers, R1830)                  │
      │   for each file with centroid accumulator:                          │
      │     store.WriteFileCentroid(fileID, sum, count)                     │
      │                                                                     │
      ├─ Update lastEmbedded[fileID] for each processed file (R1827)        │
      │                                                                     │
      └─ done                                                               │
```

## High-Water State (R1817-R1821)

```
type embedState struct {
    count       int      // chunks at last embed
    lastChunkID uint64   // ChunkID of final chunk at last embed
}

Librarian.lastEmbedded map[uint64]embedState

- in-memory only, resets on restart (R1820)
- updated after successful embed pass per file (R1827)
- stale entries for removed files harmless (R1821)
```

## File Re-Index (content changed)

```
Indexer.AddFile(path)
  │
  ├─ store.RemoveFileChunkEmbeddings(fileID)   # delete EC + EF for file
  │
  └─ next BatchEmbedChunks: lastEmbedded entry stale
     → count or lastChunkID won't match → re-embed as needed
```

## Model Mismatch

```
Server startup
  │
  ├─ detect tag_model changed (E condition "model_mismatch")
  │
  ├─ store.DropEmbeddings()           # existing: T vectors + EV records
  ├─ store.DropChunkEmbeddings()      # new: EC + EF records
  │
  └─ next reconcile: lastEmbedded is empty (restart cleared it)
     → full scan, all files processed
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
