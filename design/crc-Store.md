# Store
**Requirements:** R6, R15, R45, R103, R104, R105, R106, R107, R119, R120, R121, R122, R123, R124, R125, R126, R367, R503, R504, R505, R511, R866, R867, R868, R871, R872, R873, R883, R884, R885, R886, R887, R888, R889, R911, R912, R913, R927, R928, R932, R933, R934, R935, R936, R907, R1099, R1100, R1101, R1102, R1103, R1105, R1108, R1109, R1110, R1142, R1143, R1144, R1280, R1289, R1275, R1276, R1281, R1282, R1283, R1284, R1285, R1286, R1287, R1288, R1290, R1291, R1292, R1293, R1294, R1295

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
- GetSettings(): read ark settings (I key)
- PutSettings(settings): write ark settings
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
- GetScheduleConfig() string: read stored [schedule] section from
  settings record (I prefix). (R927, R928)
- PutScheduleConfig(serialized string): write [schedule] section to
  settings record. (R927, R932)
- RecordCounts(): scan all keys in ark subdatabase, count by prefix byte,
  return map[byte]int64. Single LMDB View transaction. (R907)
- UpdateTagValues(fileid, values []TagValue): replace V records for
  fileid. Within one LMDB txn: scan all V keys, remove fileid from
  any value blobs, delete empty keys. Then for each new (tag, value),
  append fileid varint to the value blob (or create new key).
  (R1099, R1100, R1101, R1103)
- AppendTagValues(fileid, values []TagValue): add V records without
  removing — append path. For each (tag, value), append fileid varint
  to existing value blob or create new key. (R1104)
- RemoveTagValues(fileid): scan all V keys, remove fileid from any
  value blobs, delete keys whose blob becomes empty. (R1105)
- QueryTagValues(tag, prefix string) []TagValueCount: prefix scan
  V[tag]\x00[prefix], decode varint count from each value blob.
  Return {value, count} pairs. (R1108, R1109)
- TagValueFiles(tag, value string) []uint64: direct key lookup
  V[tag]\x00[value], decode varints. (R1110)
- FileTagValues(fileid uint64, tags []string) map[string]string:
  for each requested tag, scan V[tag]\x00 entries, check if fileid
  is in the varint list, return first matching value per tag.
  (R1142, R1143)

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
  next tag-value-id from `I` prefix (`next_tvid` setting).
- AllocTagNameID() uint64: atomically increment and return the
  next tag-name-id from `I` prefix (`next_tnid` setting).

### Embedding Records (R1289-R1294)
- WriteTagNameEmbedding(tnid uint64, vec []float32): write ET record
- WriteTagValueEmbedding(tvid uint64, vec []float32): write EV record
- ReadTagNameEmbedding(tnid uint64) ([]float32, error): read ET record
- ReadTagValueEmbedding(tvid uint64) ([]float32, error): read EV record
- ScanTagNameEmbeddings() (map[uint64][]float32, error): scan all ET records
- ScanTagValueEmbeddings() (map[uint64][]float32, error): scan all EV records
- MissingTagNameEmbeddings() []uint64: T records with IDs lacking ET records
- MissingTagValueEmbeddings() []uint64: V records with IDs lacking EV records
- DropEmbeddings(): delete all ET and EV records (for rebuild)

## Collaborators
- Matcher: used by DismissByPattern and ResolveByPattern

## Sequences
- seq-add.md
- seq-search.md
- seq-server-startup.md
- seq-tag-embed.md
