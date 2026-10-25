# Chunk Embeddings

Embed every indexed chunk with nomic so semantic search can find
content by meaning. Complements trigram FTS — "how do API endpoints
work?" also matches chunks about "HTTP route handlers."

## Configurable Embedding Tiers

Different chunk sizes embed most efficiently at different context
window / parallelism settings. The user benchmarks their hardware
with `ark embed bench chunks --ctx N --parallel N` and configures
the results in ark.toml.

```toml
[embedding]
model = "nomic-embed-text-v1.5.Q8_0.gguf"

[[embedding.tiers]]
ctx = 1024
parallel = 32

[[embedding.tiers]]
ctx = 2048
parallel = 16

[[embedding.tiers]]
ctx = 2048
parallel = 8

[[embedding.tiers]]
ctx = 16384
parallel = 12

[[embedding.tiers]]
ctx = 16384
parallel = 8
```

Each tier defines a context window and parallel sequence count. The
system derives tokens-per-sequence (`ctx / parallel`) and a byte
limit (`tokens_per_seq * 3`, ~3 bytes/token for BERT WordPiece).
Tiers are sorted by byte limit ascending at load time. Default tiers
(the five above, tuned for Steam Deck Vulkan GPU) are used when
`embedding.tiers` is absent but `embedding.model` is set.

Tag and query embedding use the tier with 256 tokens/seq (2048/8)
since all tag values are under 460 bytes and queries are short.

## Model and Context Lifecycle

One embedding model is loaded from `embedding.model`. All tier contexts
are pre-allocated from it on first embedding use (lazy, same as
today). The model TTL timer unloads the model and all contexts when
the embedding queue is idle.

Creating a context from an already-loaded model is cheap (KV cache
allocation only). The model load (~1s) is the expensive part and
happens once.

## Storage

Chunk embeddings (EC) and file centroids (EF) are stored in the ark
bucket alongside existing T/EV records.

EC has one record per unique chunk content (microfts2-dedup'd —
same text shared across files gets one EC). Orphan-cleanup happens
via microfts2's removal/reindex callbacks delivering orphaned
chunkIDs that ark deletes inside the same transaction.

EF stores a running sum + count: the centroid is accumulated by
summing the file's chunk vecs (`sum += vec; n++`) and read back as
`centroid = sum / n`. It is recomputed from scratch (re-summing every
chunk vec) whenever a file gains new embeddings or is re-indexed.

Record key/value layouts: see [record-formats.md](record-formats.md)
(EC and EF sections).

## Batch Embedding Pipeline

Post-reconcile, after tag embeddings complete, the Librarian runs
`BatchEmbedChunks()`:

1. Scan for chunks missing EC records (C records exist but no
   corresponding EC record).
2. Sort by priority: tag-bearing files first, then non-JSONL authored
   content (markdown, code), then JSONL conversation logs. A file is a
   conversation log by the strategy it was indexed under (`chat-jsonl`),
   the same test recall uses, not by its `.jsonl` suffix. Files
   matching `search_exclude` (by default the conversation logs; see
   [search-filtering.md](search-filtering.md)) are skipped entirely.
   `system_exclude` does not gate embedding.
3. For each file, read chunk content via `AllChunks(path)`.
   A stale chunk (one the current chunker no longer produces; see
   [chunk-context.md](chunk-context.md#a-stale-chunk)) is skipped and
   counted. It gets no embedding record, not even an empty one, so each
   pass queues it again until the file is re-chunked.
4. Route each chunk to the smallest tier whose byte limit fits the
   chunk's content length. Chunks exceeding all tiers are skipped
   (logged at verbose level).
5. When a tier's bucket reaches its parallel count, dispatch the
   batch through that tier's context via `EmbedBatch`.
6. Write resulting EC records to the index through the DB actor.
7. After all chunks for a file are embedded, update or create the
   EF centroid record.
8. When all files are processed, flush every bucket with remaining
   chunks — no content left unembedded.

The GPU compute happens outside the actor (same pattern as tag
embedding). Only the index writes go through the actor.

## Embedding Edited Files

The pass above runs after a reconcile, which happens at startup and after a
config change. A file indexed between reconciles, new or edited, is searchable
by trigram at once, so it gets its embeddings soon after as well, without
waiting for the next reconcile.

Each file joins a pending set when its index writes land, so its new chunks
already exist, unless it matches
`search_exclude` (the same skip as step 2, so the chat logs, which change every
few seconds, never touch the timer). The set is embedded once it has been
**quiet for 15 seconds**: no new file has joined it in that time. So that a
file changing every ten seconds cannot hold it off forever, the set is also
embedded at most **one minute** after its first file joined, quiet or not.

Embedding the set runs the same pass as above, limited to those files: the
tag-name and tag-value embeddings first, as after a reconcile, then steps 1 to 8
over the set's files only. The full pass checks every chunk in the index
(about 11 seconds on the dev box, 2026-10-24) to embed what is usually a
handful, which is why the set is not handed to it.

Only one embed pass runs at a time. When the set comes due while a pass is
running, it waits and is embedded as soon as that pass ends, together with any
files that joined in the meantime. The full pass after a reconcile waits the same
way rather than being skipped, and goes first; since it covers every file stored
before it began, it drops the pending and waiting files.

## NUL Bytes in Content

The tokenizer underneath embedding (llama.cpp, reached through yzma)
takes its text as a C string, so a NUL byte (`\x00`) in the content
ends the string early at the C boundary and aborts the process. Ark
replaces NUL bytes with spaces — a space, not deletion, keeps adjacent
tokens from fusing — at the single point where any embedding path
tokenizes, so no caller can hand one through: query embedding, batch
chunk embedding, and the standalone token counter are all covered by
the one guard.

When the batch chunk-embed pipeline finds a NUL byte in a chunk it
logs the offending chunk's path and id, so a malformed source file
names itself. As everywhere else, the trigram index and retrieval keep
the original bytes; only the embedding tokenizer sees the cleaned text.

## Embedding Model Mismatch

The existing model-mismatch detection (E condition records) extends
to chunk embeddings. If the configured `embedding.model` changes, all
EC/EF records are stale and should be dropped on next reconcile
(same behavior as T/EV records today).

## Scale

- ~131K chunks currently, growing with sources
- 768 dims x 4 bytes = 3072 bytes per vector
- Full EC storage: ~400MB in the index
- EF storage: ~4K files x 3076 bytes ≈ 12MB
- Full rebuild: variable by tier, ~25-55 minutes on Steam Deck
  depending on chunk size distribution
- Incremental: per-file re-embed on content change

## Benchmark Tuning

`ark embed bench chunks` accepts `--ctx` and `--parallel` flags to
test different configurations. It samples 200 real chunks (via
`AllChunks`, real chunker boundaries), reports batch vs single
throughput, skip rate, and chunk size distribution. Use this to find
the sweet spot for your hardware before setting `embedding.tiers`.

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
