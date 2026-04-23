# Embed Deduplication

Prevent the batch embedding pipeline from re-embedding chunks that
were already embedded in a recent pass. Currently,
`BatchEmbedChunks()` is stateless — each invocation scans every file
from scratch. When a file changes faster than embedding completes
(e.g., a JSONL chat log growing during an active session), the
embedding pass runs in a loop: each reconcile queues a new embed pass
that re-embeds the same chunks because the EF centroid was
invalidated by the previous pass's "chunk count changed" detection.

Language: Go. Environment: `librarian.go` (Librarian struct).

## Observed Problem

The watcher triggers reconcile on every file change. Each reconcile
queues scan+refresh followed by embed. For a continuously-growing
JSONL file:

1. Reconcile N re-indexes the file (adds new chunks)
2. Embed N runs: chunk count changed since last EF, so EF is
   invalidated (written to 0). All chunks are scanned individually.
   New EC records written. New EF centroid written.
3. During embed N (~20s), the file changes again.
4. Reconcile N+1 re-indexes (adds more chunks).
5. Embed N+1 runs: chunk count changed again. EF invalidated again.
   The per-chunk scan finds existing EC records for old chunks, but
   this full scan is repeated every cycle. New chunks are embedded
   redundantly if they were already written by a concurrent pass.

Result: continuous GPU load, creeping DB size from orphan EC records,
embedding model never unloads (TTL never fires).

## Solution: In-Memory High-Water Tracking

The Librarian tracks, per fileID, the chunk count and last chunkID
from the most recent successful embed pass. This state is in-memory
only — it resets on server restart, which triggers a full scan once
(acceptable cold-start cost).

### Tracking State

Per file: `{count int, lastChunkID uint64}`.

- `count`: number of chunks in the file when last embedded.
- `lastChunkID`: the ChunkID (from microfts2 F-record) of the final
  chunk when last embedded. This detects the append boundary case
  where the last chunk was re-indexed with new content but the total
  count didn't change.

### Skip Logic

When `BatchEmbedChunks` processes a file:

1. Read the file's chunk list from FTS (already done — `ChunkContentLens`
   gives lengths, but we also need the last entry's ChunkID from the
   F-record).
2. Look up `lastEmbedded[fileID]`.
3. If `count == len(chunks) && lastChunkID == chunks[last].ChunkID`:
   skip entirely. Nothing changed.
4. If `count == len(chunks) && lastChunkID != chunks[last].ChunkID`:
   the last chunk was re-indexed (append boundary case). Re-embed
   only the last chunk (index `count-1`).
5. If `count < len(chunks) && lastChunkID == chunks[count-1].ChunkID`:
   clean append. Embed from index `count` onward. Skip all chunks
   before `count` — they're known good.
6. If `count < len(chunks) && lastChunkID != chunks[count-1].ChunkID`:
   boundary chunk changed. Embed from index `count-1` onward (re-embed
   the boundary chunk plus new chunks).
7. After embedding, update `lastEmbedded[fileID]` with the new count
   and last chunkID.

### Centroid Handling

When skipping known-good chunks (cases 4-6), the existing EF centroid
is preserved — no invalidation. The centroid accumulator is seeded
from the stored EF sum/count, and only the newly-embedded vectors
are added. This avoids the destructive "invalidate then rebuild"
cycle that currently causes the loop.

When re-embedding the boundary chunk (cases 4 and 6), the old
vector for that chunk is already included in the EF sum. Before
adding the new vector, subtract the old one: read the existing EC
record, subtract its vector from the centroid sum, then embed the
new content and add the new vector. This keeps the centroid accurate
without a full recompute. The final EF write happens once, after all
tiers have processed — never between tiers.

**Invariant: EF centroids are written only after every tier bucket
for that file has been flushed.** Tier processing is sequential (one
context alive at a time), so a fast-bucket chunk cannot race ahead
and trigger an early EF write while slow-bucket chunks for the same
file are still pending. The write queue serializes embed passes
across reconcile cycles. This invariant must hold even if tier
processing is refactored — do not parallelize tiers without
reworking the centroid accumulator.

### Accessing ChunkIDs

`BatchEmbedChunks` currently uses `ChunkContentLens(fileID)` which
returns byte lengths only. For the last-chunkID check, it needs
access to the F-record's chunk list. Add a DB method that returns
the last chunk's ID for a file, or extend the existing call path.

### Cleanup

When a file is removed from the index, its entry in the
`lastEmbedded` map should be removed. This happens naturally: the
map is in-memory, and removed files won't appear in the next scan.
Stale entries for removed files are harmless (just a few bytes of
memory) and are cleaned on restart.
