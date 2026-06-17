# Embed Deduplication

Prevent the batch embedding pipeline from re-embedding chunks whose
content has already been embedded. microfts2 deduplicates chunk
content — same text gets one C record with a unique chunkID, shared
across files. The ec-rekey migration moved EC records to chunkID-based
keys, so the index stores one record per unique content. But the
collection pass in `BatchEmbedChunks()` still queues duplicate GPU
work within a single invocation.

Language: Go. Environment: `librarian.go` (Librarian struct).

## History

The original version of this spec described cross-pass dedup via
in-memory high-water tracking (lastEmbedded map). That approach was
superseded by chunkID-based EC keys (ec-rekey.md) — the index lookup
`ReadChunkEmbedding(chunkID)` now skips chunks embedded in previous
passes without any in-memory state. The high-water tracking was
removed from the Librarian.

## Remaining Problem: In-Batch Dedup

The index check catches chunks embedded in *previous* runs. But
within a single `BatchEmbedChunks` call, the same chunkID can be
queued multiple times from different file references.

Pass 1 iterates every file's chunk list and checks the index for each
chunkID. For new chunks (no EC record yet), the check returns nil
and the chunk is routed to a tier bucket. Since EC records are
written in Pass 2 (after embedding), all file references to the
same chunkID see "not yet embedded" and queue it independently.

Measured: 296K chunk references across files, 157K unique chunkIDs.
On a full rebuild, ~139K chunks are embedded redundantly — same
content, wasted GPU time, silently overwritten on index write.

## Solution: Seen Set

Add an in-memory `seen map[uint64]bool` in Pass 1. Before routing
a chunk to a tier bucket, check if its chunkID is already in the
seen set. If so, skip it. Otherwise, add it and proceed.

The seen set is local to the `BatchEmbedChunks` call — no persistent
state, no cleanup needed.

### Logging

Log the dedup count alongside existing stats so the savings are
visible:

```
librarian: chunk embed: 1200 embedded, 50 skipped, 3400 deduped
```

Where "deduped" is the count of chunk references that were skipped
because another reference to the same chunkID was already queued in
this pass.
