# Store
**Requirements:** R6, R15, R45, R103, R104, R105, R106, R107, R119, R120, R121, R122, R123, R124, R125, R126, R367, R503, R504, R505, R511, R866, R867, R868, R871, R872, R873, R883, R884, R885, R886, R887, R888, R889, R911, R912, R913, R927, R928, R932, R933, R934, R935, R936, R907, R1099, R1100, R1101, R1102, R1103, R1105, R1108, R1109, R1110, R1142, R1143, R1144, R1280, R1281, R1282, R1283, R1284, R1285, R1286, R1287, R1288, R1289, R1290, R1291, R1292, R1293, R1294, R1295, R1309, R1310, R1311, R1312, R1313, R1314, R1275, R1276, R1467, R1468, R1532, R1533, R1534, R1535, R1536, R1537, R1538, R1543, R1544, R1545, R1546, R1547, R1548, R1549, R1570, R1571, R1572, R1599, R1602, R1603, R1605, R1606, R1618, R1619, R1620, R1720, R1721, R1722, R1723, R1724, R1725, R1833, R1835, R1836, R1837, R1838, R1839, R1840, R1841, R1842, R1843, R1844, R1845, R1873, R1874, R1875, R1876, R1877, R1878, R1879, R1880, R1881, R1882, R1883, R1884, R1885, R1886, R1887, R1888, R1889, R1946, R1947, R1952, R1956, R1958, R1959, R1962, R1963, R1988, R1989, R1990, R1991, R2010, R2019, R2094, R2095, R2097, R2100, R2114, R2120, R2151, R2152, R2153, R2154, R2155, R2156, R2157, R2159, R2160, R2161, R2174, R2175, R2176, R2177, R2178, R2179, R2180, R2181, R2182, R2183, R2184, R2185, R2186, R2187, R2188, R2189, R2190, R2191, R2192, R2193, R2226, R2227, R2229, R2231

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
  Within one LMDB txn: delete old D records for fileid, delete the
  fileid's ED records (one per old D), delete the fileid's matching
  SED side-index entries, write new D records. ED records for new
  (tag, fileid) pairs are written lazily by the batch-embed pass.
  (R2154, R2186)
- RemoveTagDefs(fileid): delete all D records for fileid via
  UpdateTagDefs(fileid, nil); ED records drop in the same txn. (R2155)
- AppendTagDefs(fileid, defs): add D records without removing — append path.
  ED writes do not happen synchronously; new (tag, fileid) pairs become
  visible via MissingTagDefEmbeddings on the next batch-embed pass. (R2156)
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
  a case-insensitive substring. Each match carries the chunkIDs
  decoded from the V-record value blob (TagValueMatch.ChunkIDs).
  Callers that need fileIDs resolve through filesForChunk. (R1468)

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
  vector to T record value (count:4 + vector); same txn stamps
  ST<tag> via stampWrite. (R1289, R2179)
- WriteTagValueEmbedding(tvid uint64, vec []float32): write EV[tvid]
  record; same txn stamps SEV<tvid-varint> via stampWrite. (R2180)
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
  EV records, delete all ED records, delete all HC records, and delete
  every ST*/SEV*/SED*/SHC* side-index entry (for rebuild). T-name, EV,
  ED, and HC all derive from `tag_model`, so a model swap drops them
  together. SEC* is preserved (EC is not part of DropEmbeddings).
  (R2160, R2187, R2231)

### Tag-Definition Embedding Records (R2151-R2161)
- WriteTagDefEmbedding(tag string, fileid uint64, vec []float32): write
  ED[tag][fileid:8] record. Key: `ED` + tag bytes + 8-byte big-endian
  fileid. Value: float32 vector. Same txn stamps SED<tag><fileid:8>
  via stampWrite. (R2151, R2153, R2159, R2181)
- ReadTagDefEmbedding(tag string, fileid uint64) ([]float32, error):
  read one ED record. Returns (nil, nil) when absent. (R2159)
- MissingTagDefEmbeddings() []TagDefRef: scan D prefix, return (tag,
  fileid) pairs that have a D record but no corresponding ED record.
  Used by the post-reconcile batch-embed pass. (R2157)
- DeleteTagDefEmbeddingsForFileInTxn(txn *lmdb.Txn, fileid uint64): scan
  D prefix matching the trailing 8-byte fileid, delete the parallel ED
  records and their matching SED side-index entries inside the same
  txn. Used by UpdateTagDefs/RemoveTagDefs before D records are
  deleted. (R2154, R2155, R2186)

### Hot-Correlation Records (R2226-R2231)
- WriteHotCorrelation(tag string, chunkID uint64, score float64) error:
  write HC[tag][chunkID:8] record. Key: `HC` + tag bytes + 8-byte
  big-endian chunkID. Value: 8-byte big-endian float64 score. Same
  txn stamps SHC<tag><chunkID:8> via stampWrite — the substrate
  stamp is the entry's alibi. (R2226, R2227, R2229)
- ReadHotCorrelations(tag string) ([]HotCorrelation, error): scan
  HC prefix for one tag, return slice of {ChunkID, Score}. Order is
  undefined — caller sorts. (R2226)
- DeleteHotCorrelation(tag string, chunkID uint64) error: delete one
  HC record and its matching SHC side-index entry in the same txn.
  Used by the sweep's phase-4 displace path. (R2229)
- ReplaceHotCorrelations(tag string, entries []HotCorrelation) error:
  delete all HC entries for tag, write the supplied slice, all in
  one LMDB write txn. Each new entry is stamped with the txn's
  serial. Used by the sweep's phase-3 tag rebuild. Single shared
  serial per call (per-tag txn). (R2229, R2238)
- DropHotCorrelations() error: delete every HC* record and every
  SHC* side-index entry. Called by DropEmbeddings. (R2231)

### Chunk Embedding Records (R1833-R1845)
- WriteChunkEmbedding(chunkID uint64, vec []float32): write EC[chunkID]
  record. Key: `EC` + varint(chunkID). Value: float32 vector. Same txn
  stamps SEC<chunkID-varint> via stampWrite. (R1836, R2182)
- WriteChunkEmbeddingBatch(chunks []ChunkVec): batch write. ChunkVec is
  {ChunkID uint64, Vec []float32}. Single allocSerial at the top of
  the callback; every batch record's SEC<...> entry is stamped with
  that one serial via stampWriteWith. (R1837, R2183)
- ReadChunkEmbedding(chunkID uint64) ([]float32, error): read one EC
  record by chunkID. (R1838)
- ReadChunkEmbeddings(chunkIDs []uint64) [][]float32: batch read EC
  records for centroid computation. One View transaction. (R1842)
- DeleteChunkEmbedding(chunkID uint64): delete one EC record and its
  matching SEC side-index entry in the same txn. (R1839, R2185)
- DeleteChunkEmbeddingInTxn(txn *lmdb.Txn, chunkID uint64): delete one
  EC record and its matching SEC side-index entry using an existing
  transaction. For microfts2 callbacks. (R1840, R2185)
- WriteFileCentroid(fileID uint64, sum []float32, count uint32): write
  EF[fileID] record. Unchanged key format. (R1835)
- ReadFileCentroid(fileID uint64) (sum []float32, count uint32, err error):
  read one EF record.
- DeleteFileCentroidInTxn(txn *lmdb.Txn, fileID uint64): delete one EF
  record using an existing transaction. For microfts2 callbacks. (R1841)
- ScanFileCentroids() (map[uint64][]float32, error): scan EF prefix, return
  fileID → centroid vector (sum / count).
- DropChunkEmbeddings(): delete all EC and EF prefix records, and
  delete every SEC* side-index entry alongside the EC sweep. EF
  has no side-index. (R1844, R2193)
- ScanChunkEmbeddingKeys() map[uint64]int: prefix scan EC keys, returns
  chunkID → vector dimension. Used by embed validate. (R1845)

### Vector Freshness Substrate (S records, R2174-R2193)
- allocSerial(txn *lmdb.Txn) (uint64, error): unexported. Read the
  `I:serial` counter, advance by 1, write back, return the new value.
  Sourced from an I-record (not lmdb.Txn.ID()) because compact-copy
  may reset mt_txnid; the I-record sits in the active B-tree and is
  preserved by every compact-copy. Counter never resets over the
  database's lifetime. (R2176, R2177)
- stampWriteWith(txn *lmdb.Txn, prefix, key []byte, serial uint64) error:
  unexported. Write the side-index entry `S<prefix><key>` →
  varint(serial). Caller is responsible for the original record's
  txn.Put. Used by WriteChunkEmbeddingBatch to stamp every batch
  record with one shared serial. (R2174, R2175)
- stampWrite(txn *lmdb.Txn, prefix, key []byte) error: unexported
  convenience wrapper. Calls allocSerial, then stampWriteWith.
  Used by the four single-record Write*Embedding methods. (R2174-R2176)
- deleteStamp(txn *lmdb.Txn, prefix, key []byte) error: unexported.
  Delete `S<prefix><key>` from the side index. No-op if absent.
  Used by the embedding-record delete paths to keep the side index
  in sync. (R2185, R2186)
- RecordSerial(prefix, key []byte) (serial uint64, found bool, err error):
  return the stamped serial of the record at (prefix + key). found
  is false iff no S-entry exists for that key. (R2188)
- WalkRecordsSinceSerial(prefix []byte, since uint64,
  fn func(originalKey []byte, serial uint64) error) error: walk the
  `S<prefix>` side index in key order, varint-decode each entry's
  serial, call fn for each entry whose serial is strictly greater
  than `since`. fn receives the original record's full key (with
  the leading `S` stripped, so the original prefix bytes lead) and
  the decoded serial. A non-nil error from fn stops iteration and
  is returned by the call. (R2189, R2190)

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

### Tmp tag overlay union (R1946, R1947, R1952, R2019)
- TagFiles, TagValueFiles, FileTagValues union persistent LMDB
  results with `TmpTagStore` results before returning. Callers stay
  unaware of the tmp:// distinction.
- TagFiles and TagValueFiles add two ExtMap legs covering routed
  contributions: `ExtMap.ExtTagFiles` / `ExtMap.ExtTagValueFiles`
  walk `routedTagsByTvidExt` and `targetToChunk` in one pass to
  surface every persistent and overlay ext-routed target chunk that
  carries a requested tag. Without these, F records (which never
  land at the target chunk per R1991) leave routed targets invisible
  to tag queries. The four sources (persistent F, TmpTagStore
  overlay-direct, ExtMap persistent-routed, ExtMap overlay-routed)
  union without coordination — chunkids do not collide. (R2019,
  R2120, R2124)
- UpdateTagValues, AppendTagValues, RemoveTagValues dispatch each
  fileid by its high bit (set when interpreted as int64): persistent
  fileids go to LMDB, overlay-issued fileids (counting down from
  `MaxUint64`) go to TmpTagStore.
- FileTagValues is the call inbox uses to resolve message tags
  without re-reading file content; tmp:// messages flow through the
  same path.

### Tvid map and overlay (R1956, R1958, R1959, R1962, R1963)
- tvids: *TvidMap — owned by Store; loaded once during DB.Open via
  `tvids.LoadFromStore(s)` (V-prefix scan, OriginPersistent).
- Each `env.Update` block touching V records calls `tvids.Begin()`
  for a fresh `TvidTxn`, then `Commit` on success or `Abort` on
  error/panic. The write-actor invariant guarantees only one txn is
  ever live.
- `addChunkIDToVRecord` calls `tt.Add(tvid, tag, value, OriginPersistent)`
  on every newly-allocated tvid. `removeChunkIDInTxn` calls
  `tt.Remove(tvid)` whenever it deletes a V record entirely.
- `ScanVRecordTvids` becomes a thin wrapper over `tvids.Snapshot()`
  for diagnostics.
- `addChunkIDToVRecord` is a **multi-set append** — no dedup check.
  Each contribution (inline or ext-routed) writes its own varint
  entry. `removeVarint` removes the first occurrence so other
  contributors survive when one is cleaned up. (R1988)

### Ext provenance records (R1989-R1991, R2010)
- WriteExtRecord(txn *lmdb.Txn, tvid_ext, target_chunkid uint64,
  routed_tvids []uint64): write `X[tvid_ext][target_chunkid] →
  packed routed_tvid varints`. One X record per (tvid_ext,
  target_chunkid) pair. (R1989)
- ScanExtRecords(tvid_ext uint64) []ExtRouting: prefix-scan
  X[tvid_ext], decode each (target_chunkid, []routed_tvid) pair.
  Used by source-side cleanup and re-resolution. (R1989, R1990)
- DeleteExtRecord(txn *lmdb.Txn, tvid_ext, target_chunkid uint64):
  delete one X record. (R1989)
- ScanAllExtRecords() — used by ExtMap.Rebuild at startup to
  repopulate the six in-memory maps. (R1990, R1993)
- The X key shape is **chunkid-keyed, not fileid-keyed**: the
  durable bridge across an ark restart so the orphan callback can
  find routings to clean up after offline edits. (R1990)
- F records stay inline-only; routed-tag tvids are NOT added to any
  target chunk's F record. F[source][ext] holds the @ext tag's tvid
  the same way any other F record holds tag tvids. (R1991)
- TagCounts(tags []string): T-total query augmented with
  `ExtMap.VirtualTagCount(tag)`; returns `LMDB_T[tag] +
  virtualTagCount[tag]`. (R2010)

## Collaborators
- Matcher: used by DismissByPattern and ResolveByPattern
- TmpTagStore: in-memory tag overlay for tmp:// fileids; consulted
  by the read union and the write dispatcher
- TvidMap: shared `tvid → (tag, value, origin)` resolver; loaded
  from V records at startup, maintained via TvidTxn during writes
- ExtMap: in-memory routing state for @ext; consulted by TagCounts
  for T-total augmentation and driven by Indexer for X-record
  CRUD and V-record multi-set ops

## Sequences
- seq-add.md
- seq-search.md
- seq-server-startup.md
- seq-tag-embed.md
- seq-chunk-embed.md
- seq-tvid-overlay.md
