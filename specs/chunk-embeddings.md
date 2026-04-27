# Chunk Embeddings

Embed every indexed chunk with nomic so semantic search can find
content by meaning. Complements trigram FTS — "how do API endpoints
work?" also matches chunks about "HTTP route handlers."

## Configurable Embedding Tiers

Different chunk sizes embed most efficiently at different context
window / parallelism settings. The user benchmarks their hardware
with `ark embed --bench chunks --ctx N --parallel N` and configures
the results in ark.toml.

```toml
tag_model = "nomic-embed-text-v1.5.Q8_0.gguf"

[[embed_tiers]]
ctx = 1024
parallel = 32

[[embed_tiers]]
ctx = 2048
parallel = 16

[[embed_tiers]]
ctx = 2048
parallel = 8

[[embed_tiers]]
ctx = 16384
parallel = 12

[[embed_tiers]]
ctx = 16384
parallel = 8
```

Each tier defines a context window and parallel sequence count. The
system derives tokens-per-sequence (`ctx / parallel`) and a byte
limit (`tokens_per_seq * 3`, ~3 bytes/token for BERT WordPiece).
Tiers are sorted by byte limit ascending at load time. Default tiers
(the five above, tuned for Steam Deck Vulkan GPU) are used when
`embed_tiers` is absent but `tag_model` is set.

Tag and query embedding use the tier with 256 tokens/seq (2048/8)
since all tag values are under 460 bytes and queries are short.

## Model and Context Lifecycle

One embedding model is loaded from `tag_model`. All tier contexts
are pre-allocated from it on first embedding use (lazy, same as
today). The model TTL timer unloads the model and all contexts when
the embedding queue is idle.

Creating a context from an already-loaded model is cheap (KV cache
allocation only). The model load (~1s) is the expensive part and
happens once.

## Storage

Chunk embeddings (EC) and file centroids (EF) are stored in the ark
LMDB subdatabase alongside existing T/EV records.

EC has one record per unique chunk content (microfts2-dedup'd —
same text shared across files gets one EC). Orphan-cleanup happens
via microfts2's removal/reindex callbacks delivering orphaned
chunkIDs that ark deletes inside the same LMDB transaction.

EF stores a running sum + count so centroid updates are O(1):
add a chunk → `sum += vec; n++`; remove → `sum -= vec; n--`; query
→ `centroid = sum / n`. Recomputed from scratch on full re-index.

Record key/value layouts: see [record-formats.md](record-formats.md)
(EC and EF sections).

## Batch Embedding Pipeline

Post-reconcile, after tag embeddings complete, the Librarian runs
`BatchEmbedChunks()`:

1. Scan for chunks missing EC records (C records exist but no
   corresponding EC record).
2. Sort by priority: tag-bearing files first, then non-JSONL authored
   content (markdown, code), then JSONL conversation logs. Files
   matching `search_exclude` are skipped entirely.
3. For each file, read chunk content via `AllChunks(path)`.
4. Route each chunk to the smallest tier whose byte limit fits the
   chunk's content length. Chunks exceeding all tiers are skipped
   (logged at verbose level).
5. When a tier's bucket reaches its parallel count, dispatch the
   batch through that tier's context via `EmbedBatch`.
6. Write resulting EC records to LMDB through the DB actor.
7. After all chunks for a file are embedded, update or create the
   EF centroid record.
8. When all files are processed, flush every bucket with remaining
   chunks — no content left unembedded.

The GPU compute happens outside the actor (same pattern as tag
embedding). Only the LMDB writes go through the actor.

## Embedding Model Mismatch

The existing model-mismatch detection (E condition records) extends
to chunk embeddings. If the configured `tag_model` changes, all
EC/EF records are stale and should be dropped on next reconcile
(same behavior as T/EV records today).

## Scale

- ~131K chunks currently, growing with sources
- 768 dims x 4 bytes = 3072 bytes per vector
- Full EC storage: ~400MB in LMDB
- EF storage: ~4K files x 3076 bytes ≈ 12MB
- Full rebuild: variable by tier, ~25-55 minutes on Steam Deck
  depending on chunk size distribution
- Incremental: per-file re-embed on content change

## Benchmark Tuning

`ark embed --bench chunks` accepts `--ctx` and `--parallel` flags to
test different configurations. It samples 200 real chunks (via
`AllChunks`, real chunker boundaries), reports batch vs single
throughput, skip rate, and chunk size distribution. Use this to find
the sweet spot for your hardware before setting `embed_tiers`.

## What This Does NOT Cover

These are deferred to separate specs after the storage pipeline is
proven:

- **Query-time search**: file centroid narrowing, chunk-level
  semantic search endpoints, integration with the three-phase
  progressive search
- **Contains-mode expansion**: wiring `<ark-search>` to EC records
  for local semantic expansion
- **Ambient Gut surfacing**: comparing new chunks against tag
  embeddings to surface relevant tags proactively
