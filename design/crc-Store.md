# Store
**Requirements:** R6, R15, R45, R103, R104, R105, R106, R107, R119, R120, R121, R122, R123, R124, R125, R126, R367, R503, R504, R505, R511, R866, R867, R868, R871, R872, R873, R883, R884, R885, R886, R887, R888, R889, R911, R912, R913, R927, R928, R932, R933, R934, R935, R936, R907, R1099, R1100, R1101, R1102, R1103, R1105, R1108, R1109, R1110, R1142, R1143, R1144, R1280, R1281, R1282, R1283, R1284, R1285, R1286, R1287, R1288, R1289, R1290, R1291, R1292, R1293, R1294, R1295, R1309, R1310, R1311, R1312, R1313, R1314, R1275, R1276, R1467, R1468, R1532, R1533, R1534, R1535, R1536, R1537, R1538, R1543, R1544, R1545, R1546, R1547, R1548, R1549, R1570, R1571, R1572, R1599, R1602, R1603, R1605, R1606, R1618, R1619, R1620, R1720, R1721, R1722, R1723, R1724, R1725, R1833, R1835, R1836, R1837, R1838, R1839, R1840, R1841, R1842, R1843, R1844, R1845

Ark's own LMDB subdatabase. Manages missing files, unresolved files,
ark-level settings, and tag tracking.

## Knows
- env: *lmdb.Env — shared LMDB environment
- dbi: lmdb.DBI — ark subdatabase handle

## Does
- Open(env): open the ark subdatabase
- AddMissing(fileid, path, lastSeen): store missing file record (M prefix)
- RemoveMissing(fileid): remove missing record
- ListMissing(): all missing file records
- AddUnresolved(path, dir): store unresolved file record (U prefix)
- RemoveUnresolved(path): remove unresolved record
- ListUnresolved(): all unresolved file records
- CleanUnresolved(): remove entries for files no longer on disk
- DismissByPattern(patterns): remove missing records matching patterns
- ResolveByPattern(patterns): remove unresolved records matching patterns
- iGet(name): read a single I record string value. Returns "" if not found. (R1537, R1538)
- iPut(name, value): write a single I record string value. (R1537, R1538)
- iDel(name): delete a single I record. (R1537)
- iGetCounter(name): read a uint64 counter I record. Returns 0 if not found. (R1538)
- iSetCounter(name, value): write a uint64 counter I record. (R1538)
- WriteConfig(cfg *Config): write all Config fields to per-name I records.
  Scalars as strings, compounds as JSON. (R1532, R1534, R1535, R1539)
- ReadConfig(): read all known I record names, reconstruct a Config struct.
  Returns nil if no I records exist (fresh DB). (R1532, R1540)
- WriteERecord(name string, payload any): write E + name → JSON payload. (R1543)
- ReadERecords(): scan E prefix, return map[name]json.RawMessage. (R1544)
- DeleteERecord(name string): remove one E record. (R1545)
- ClearERecords(): delete all E prefix records. (R1542, R1545)
- UpdateTags(fileid, tags): replace all F records for fileid, recompute T totals.
  tags is map[string]uint32 (tagname → count in file).
  Within one LMDB txn: delete old F records for fileid, write new F records,
  recompute T totals from all F records for affected tagnames.
- RemoveTags(fileid): delete all F records for fileid, decrement T totals
- ListTags(): scan T prefix, return all tagname/count pairs
- TagCounts(tags []string): look up T records for specific tags
- TagFiles(tags []string): scan F prefix for matching tags,
  return fileid + count per file. Caller resolves fileid to path/size.
- TagContext(tags []string): for each F record match, read file content
  and extract lines containing the tag — return tag-to-end-of-line text
- AppendTags(fileid, tags): add to existing F record counts and T totals
  without replacing — used by append-only indexing path
- UpdateTagDefs(fileid, defs): replace all D records for fileid, write new ones.
  defs is map[string]string (tagname → description).
  Within one LMDB txn: delete old D records for fileid, write new D records.
- RemoveTagDefs(fileid): delete all D records for fileid
- AppendTagDefs(fileid, defs): add D records without removing — append path
- ListTagDefs(tags []string): scan D prefix, return definitions.
  If tags is empty, return all. Otherwise filter to requested tags.
  Returns (tagname, description, fileid) triples.
- WriteDayBuckets(fileid uint64, entries []DayBucketEntry): write
  TD keys for each day spanned by each entry, write TF reverse index.
  Cleans old entries first via ClearDayBuckets. (R866, R871, R872)
- ClearDayBuckets(fileid uint64): read TF|fileid to get date list,
  delete all TD|date|fileid|* entries, delete TF|fileid. (R871, R872)
- QueryDayBuckets(startDate, endDate string) []DayBucketEntry: seek
  TD|startDate, scan to TD|endDate, return all entries. (R867)
- ParseAcks(content []byte, tag string) []AckEntry: extract @ack:
  tags from the same chunk as the given tag, parse dates and ranges.
  (R883, R884, R885, R886, R887, R888)
- WriteDayBucketsWithAcks(fileid uint64, entries []DayBucketEntry,
  acks []AckEntry): same as WriteDayBuckets but cross-references
  ack entries against event dates, setting Acked/AckText on matching
  DayBucketEvents before writing. (R933, R934, R935)
- GetScheduleConfig() string: read stored schedule config from
  I record "schedule_config". (R927, R928, R1572)
- PutScheduleConfig(serialized string): write schedule config to
  I record "schedule_config". (R927, R932, R1572)
- RecordCounts(): scan all keys in ark subdatabase, count by prefix byte,
  return map[byte]int64. Single LMDB View transaction. (R907)
- UpdateTagValues(fileid, values []TagValue): replace V records for
  fileid. Within one LMDB txn: read F records for fileid to get
  existing tvids, remove fileid from exactly those V records (targeted
  cleanup via R1312-R1314), delete empty V keys. Then for each new
  (tag, value), look up existing V record by prefix scan
  V[tag]\x00[value]\x00 to get tvid, or allocate new tvid if none
  exists. Write V[tag]\x00[value]\x00[tvid] with fileid appended.
  Update F record with new tvids. (R1099, R1100, R1101, R1103, R1281,
  R1309, R1311, R1312, R1313)
- AppendTagValues(fileid, values []TagValue): add V records without
  removing — append path. For each (tag, value), prefix scan to find
  existing tvid or allocate new. Append fileid varint to value blob.
  Append tvids to F record value. (R1104, R1281, R1311)
- RemoveTagValues(fileid): read F records for fileid to get tvids,
  remove fileid from exactly those V records. Delete V keys whose
  blob becomes empty. (R1105, R1312, R1313, R1314)
- QueryTagValues(tag, prefix string) []TagValueCount: prefix scan
  V[tag]\x00[prefix], parse key to extract value (between first and
  last null separators) and tvid (after last null). Decode varint
  count from value blob. Return {value, count} pairs. (R1108, R1109)
- TagValueFiles(tag, value string) []uint64: prefix scan
  V[tag]\x00[value]\x00, decode varints from value blob of the one
  matching record. (R1110, R1309)
- FileTagValues(fileid uint64, tags []string) map[string]string:
  for each requested tag, scan V[tag]\x00 entries, parse value from
  key (between first and last null), check if fileid is in the varint
  list, return first matching value per tag. (R1142, R1143)
- MatchTagNames(tokens []string) []string: scan T records, return
  tag names where every token is a case-insensitive substring of the
  name. Linear scan — T record set is small. (R1467)
- MatchTagValues(tag string, tokens []string) []TagValueMatch: scan
  V records for a given tag name, return values where every token is
  a case-insensitive substring. Each result includes the value string
  and its file ID list. (R1468)

### DayBucketEvent (R911, R912)
- Start: time.Time
- End: time.Time
- Summary: string — description text after date
- AllDay: bool
- Acked: bool — true if @ack: covers this date
- AckText: string — descriptive text from the @ack: entry

### DayBucketEntry (R866, R911)
- Date: string — YYYYMMDD
- Tag: string
- Path: string
- FileID: uint64
- Events: []DayBucketEvent — JSON array, multiple per day

### AckEntry
- Start: time.Time — open for ..DATE entries
- End: time.Time
- Text: string — descriptive text after date

### Tag Value ID Allocation (R1280-R1284)
- AllocTagValueID() uint64: atomically increment and return the
  next tag-value-id from I record "next_tvid" counter. (R1536, R1572)

### Embedding Records (R1289-R1294)
- WriteTagNameEmbedding(tag string, vec []float32): append embedding
  vector to T record value (count:4 + vector). (R1289)
- WriteTagValueEmbedding(tvid uint64, vec []float32): write EV[tvid] record
- ReadTagNameEmbedding(tag string) ([]float32, error): read vector from
  T record (nil if len(value) == 4, i.e. count only)
- ReadTagValueEmbedding(tvid uint64) ([]float32, error): read EV record
- ScanTagNameEmbeddings() (map[string][]float32, error): scan T records
  with len(value) > 4, return tag → vector
- ScanTagValueEmbeddings() (map[uint64][]float32, error): scan EV records
- ScanVRecordTvids() (map[uint64]TagAlt, error): scan V prefix, parse tvid
  from each key's trailing bytes. Returns tvid → {tag, value} mapping. (R1310)
- MissingTagNameEmbeddings() []string: T records where len(value) == 4
- MissingTagValueEmbeddings() []uint64: scan V records for tvids, return
  those without corresponding EV records. (R1292)
- DropEmbeddings(): strip vectors from T records (keep count), delete all
  EV records (for rebuild)

### Chunk Embedding Records (R1833-R1845)
- WriteChunkEmbedding(chunkID uint64, vec []float32): write EC[chunkID]
  record. Key: `EC` + varint(chunkID). Value: float32 vector. (R1836)
- WriteChunkEmbeddingBatch(chunks []ChunkVec): batch write. ChunkVec is
  {ChunkID uint64, Vec []float32}. (R1837)
- ReadChunkEmbedding(chunkID uint64) ([]float32, error): read one EC
  record by chunkID. (R1838)
- ReadChunkEmbeddings(chunkIDs []uint64) [][]float32: batch read EC
  records for centroid computation. One View transaction. (R1842)
- DeleteChunkEmbedding(chunkID uint64): delete one EC record. (R1839)
- DeleteChunkEmbeddingInTxn(txn *lmdb.Txn, chunkID uint64): delete one
  EC record using an existing transaction. For microfts2 callbacks. (R1840)
- WriteFileCentroid(fileID uint64, sum []float32, count uint32): write
  EF[fileID] record. Unchanged key format. (R1835)
- ReadFileCentroid(fileID uint64) (sum []float32, count uint32, err error):
  read one EF record.
- DeleteFileCentroidInTxn(txn *lmdb.Txn, fileID uint64): delete one EF
  record using an existing transaction. For microfts2 callbacks. (R1841)
- ScanFileCentroids() (map[uint64][]float32, error): scan EF prefix, return
  fileID → centroid vector (sum / count).
- DropChunkEmbeddings(): delete all EC and EF prefix records. (R1844)
- ScanChunkEmbeddingKeys() map[uint64]int: prefix scan EC keys, returns
  chunkID → vector dimension. Used by embed validate. (R1845)

### Page Content Records (R1720-R1725)
- WritePageContent(fileID uint64, page uint32, blob []byte): write
  PC[fileID][page] record. Key: `PC` + varint(fileID) + varint(page).
  Value: zstd-compressed concatenation of chunk texts on that page
  (null-byte separated). (R1720, R1721, R1722)
- ReadPageContent(fileID uint64, page uint32) ([]byte, error): read one PC
  record. Returns ErrNotFound semantics via (nil, nil) when the record
  is absent so callers can detect missing-blob and fall back.
- RemovePageContents(fileID uint64): prefix-scan PC + varint(fileID),
  delete all matching records. Called before re-indexing a file
  (R1724) and from the file-removal path (R1725).

## Collaborators
- Matcher: used by DismissByPattern and ResolveByPattern

## Sequences
- seq-add.md
- seq-search.md
- seq-server-startup.md
- seq-tag-embed.md
- seq-chunk-embed.md
