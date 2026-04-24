# Sequence: embed validate

**Requirements:** R1794, R1802, R1803, R1804, R1805, R1806, R1807, R1808, R1809, R1810, R1811, R1812, R1813, R1855, R1856, R1857, R1858, R1865, R1866

Cross-reference EC/EF embedding records against FTS chunk data
to find orphans, mismatches, and gaps.

## Participants
- CLI (cmdEmbedValidate)
- DB
- Store (EC/EF records)
- microfts2 (FTS chunk index)

## Flow

```
CLI                          DB/Store                     microfts2
 |                              |                             |
 |-- withDB ------------------->|                             |
 |                              |                             |
 |  === Pass 1: scan EC records (prefix scan "EC") =========  |
 |                              |                             |
 |<-- iterate EC keys ---------|                             |
 |    parse fileID, chunkIdx    |                             |
 |    read vec dimension        |                             |
 |    accumulate:               |                             |
 |      ecByFile[fileID]++      |                             |
 |      dimCounts[dim]++        |                             |
 |                              |                             |
 |  === Pass 2: scan EF records (prefix scan "EF") =========  |
 |                              |                             |
 |<-- iterate EF keys ---------|                             |
 |    parse fileID, count       |                             |
 |    store efByFile[fileID]    |                             |
 |                              |                             |
 |  === Pass 3: scan FTS files ============================  |
 |                              |                             |
 |-- DB.Files() --------------->|-- list indexed paths ------>|
 |                              |<-- paths ------------------|
 |                              |                             |
 |  for each file:              |                             |
 |    CheckFile(path) --------->|-- fileID ------------------>|
 |    ChunkContentLens(fid) --->|                             |
 |    store ftsChunks[fid]=len  |                             |
 |                              |                             |
 |  === Check 1: orphan EC ================================  |
 |                              |                             |
 |  for fid in ecByFile:        |                             |
 |    if fid not in ftsChunks:  |                             |
 |      orphanEC += ecByFile[fid]                             |
 |    elif ecByFile has chunkIdx >= ftsChunks[fid]:           |
 |      orphanEC++              |                             |
 |                              |                             |
 |  === Check 2: EF/EC mismatch ===========================  |
 |                              |                             |
 |  for fid in efByFile:        |                             |
 |    if efByFile[fid].count != ecByFile[fid]:                |
 |      mismatch++              |                             |
 |                              |                             |
 |  === Check 3: missing EC (R1856, R1865, R1866) ===========  |
 |                              |                             |
 |  embeddable, excluded :=     |                             |
 |    AllChunkIDsPartitioned(excludePatterns)                  |
 |  for chunkID in embeddable:  |                             |
 |    if chunkID not in ecDims: |                             |
 |      missingEC++             |                             |
 |  print missingEC, len(excluded)                            |
 |                              |                             |
 |  === Check 4: orphan EF ================================  |
 |                              |                             |
 |  for fid in efByFile:        |                             |
 |    if fid not in ecByFile && fid not in ftsChunks:         |
 |      orphanEF++              |                             |
 |                              |                             |
 |  === Check 5: dimension consistency =====================  |
 |                              |                             |
 |  majorityDim = max(dimCounts)|                             |
 |  wrongDim = total - dimCounts[majorityDim]                 |
 |                              |                             |
 |  === Fix (if --fix) ====================================  |
 |                              |                             |
 |  delete orphan EC records -->|                             |
 |  delete orphan EF records -->|                             |
 |  delete wrong-dim EC ------->|                             |
 |                              |                             |
 |  === Output =============================================  |
 |                              |                             |
 |  print summary per category  |                             |
 |  if --verbose: per-file list |                             |
 |  if --fix: deletion counts   |                             |
 |  exit 0 (clean) or 1 (dirty) |                             |
```

## Notes

- Pass 1 needs per-record chunkIdx to detect out-of-range indices.
  The current Store API (ReadChunkEmbedding) reads one at a time —
  validate needs a prefix scan. New method: ScanChunkEmbeddingKeys.
- --fix deletes via RemoveFileChunkEmbeddings for whole-file orphans,
  individual EC deletes for out-of-range or wrong-dimension records.
- Missing EC records are reported but not fixed (R1808) — re-embedding
  requires a running server with a warm model.
