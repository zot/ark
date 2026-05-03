# Sequence: ark tag verify

`ark tag verify [--repair] [--scope SCOPE]`

```
CLI                TagVerify       Store          DB            ExtMap
 |                    |              |             |              |
 | parse flags        |              |             |              |
 | (--repair, scope)  |              |             |              |
 |------------------->|              |             |              |
 |                    | scope == ext or all?       |              |
 |                    |------------------------------------------->|
 |                    | iterate F records carrying `ext` tag     |
 |                    |------------->|             |              |
 |                    |              | F[*][ext]   |              |
 |                    |<-------------| (chunk, tvid_ext) pairs    |
 |                    |              |             |              |
 |                    | for each tvid_ext:        |              |
 |                    |   ParseExtTarget(value)   |              |
 |                    |   ResolveExtTarget(target)|              |
 |                    |---------------------------->|              |
 |                    |                            |              |
 |                    |   compare to ExtRecord(tvid_ext, *)        |
 |                    |------------->|             |              |
 |                    |              | X[tvid_ext]→{chunkID, []routed_tvids} |
 |                    |<-------------|             |              |
 |                    |   emit drift issues (missing, stale, orphan, routed) |
 |                    |              |             |              |
 |                    |   verifyExtMapConsistency():               |
 |                    |     extByAnchor / targetToChunk / chunkToTargets    |
 |                    |     / fileidToTvids vs X record set       |
 |                    |--------------------------------------------|
 |                    |                            |              |
 |                    | scope == tag-totals or all?               |
 |                    | iterate T records          |              |
 |                    |------------->|             |              |
 |                    |              | T[tag] → stored count      |
 |                    |              | recompute from V multi-set sizes      |
 |                    |              | + extmap.virtualTagCount[tag]         |
 |                    |<-------------|             |              |
 |                    | emit drift issues          |              |
 |                    |              |             |              |
 | --repair?          |              |             |              |
 |                    | open write txn (single)   |              |
 |                    |---------------------------->|              |
 |                    | for each ext issue:       |              |
 |                    |   WriteExtRecord / DeleteExtRecord       |
 |                    |   addChunkIDToVRecord / removeOneChunkIDFromVRecord  |
 |                    |------------->|             |              |
 |                    |              |             |              |
 |                    | for each tag-total drift: |              |
 |                    |   rewrite T value         |              |
 |                    |------------->|             |              |
 |                    |              |             |              |
 |                    | ExtMap.Rebuild(db) (after X records consistent)     |
 |                    |--------------------------------------------|
 |                    |                            |              |
 |                    | commit txn                |              |
 |                    |---------------------------->|              |
 |                    |                            |              |
 |<-------------------| summary, exit code        |              |
 |                    |                            |              |
 v                    v                            v              v
```

Notes:

- Read-only mode skips the write-txn block entirely. The walks
  use a read txn for F/V/T/X access.
- `repair` runs all corrections in a single write transaction.
  Partial repair (a fixable issue followed by an unfixable one)
  surfaces via the per-issue report and exit code 1.
- `ExtMap.Rebuild` runs only after the X record set has been
  made consistent — it scans X records to repopulate maps.
  Order matters: rebuilding before X is consistent would just
  re-derive the broken state.
