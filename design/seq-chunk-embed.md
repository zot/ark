# Sequence: Chunk Embedding Pipeline
**Requirements:** R1593, R1594, R1595, R1596, R1609, R1610, R1611, R1612, R1613, R1614, R1615, R1616, R1617, R1618, R1620

Post-reconcile chunk embedding. Runs after tag embedding (BatchEmbed)
completes. One model, multiple tier contexts, bucket-based dispatch.

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
      ├─ store.MissingChunkEmbeddings()  # C records without EC records     │
      │   returns []ChunkEmbedRef{FileID, ChunkIdx, Path}                   │
      │                                                                     │
      ├─ priority sort:                                                     │
      │   1. tag-bearing files (has T/F records)                            │
      │   2. non-JSONL authored content                                     │
      │   3. JSONL conversation logs                                        │
      │   skip: files matching search_exclude                               │
      │                                                                     │
      ├─ group by fileID, for each file:                                    │
      │   chunks := db.AllChunks(path)                                      │
      │   for each chunk:                                                   │
      │     tier := smallestTierFitting(len(chunk.Content))                 │
      │     if tier == nil: skip, log verbose                               │
      │     tier.bucket = append(tier.bucket, chunk)                        │
      │     if len(tier.bucket) == tier.Parallel:                           │
      │       ┌─────────────────────────────────────────┐                   │
      │       │ texts := extractContent(tier.bucket)    │  off-actor        │
      │       │ vecs := EmbedBatch(texts, tier.ctx)     │  GPU compute      │
      │       └─────────────────────────────────────────┘                   │
      │       svc(db.svc) → store.WriteChunkEmbedding(...)  # actor write   │
      │       tier.bucket = nil                                             │
      │                                                                     │
      │   after all chunks for file:                                        │
      │     update EF centroid (running sum += vec, count++)                 │
      │     svc(db.svc) → store.WriteFileCentroid(fileID, sum, count)       │
      │                                                                     │
      ├─ final flush: for each tier with non-empty bucket:                  │
      │   EmbedBatch + write (same as above)                                │
      │                                                                     │
      └─ done                                                               │
```

## File Re-Index (content changed)

```
Indexer.AddFile(path)
  │
  ├─ store.RemoveFileChunkEmbeddings(fileID)   # delete EC + EF for file
  │
  └─ (file enters MissingChunkEmbeddings on next reconcile)
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
