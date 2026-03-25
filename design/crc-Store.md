# Store
**Requirements:** R6, R15, R45, R103, R104, R105, R106, R107, R119, R120, R121, R122, R123, R124, R125, R126, R367, R503, R504, R505, R511, R866, R867, R868, R871, R872, R873, R883, R884, R885, R886, R887, R888, R889

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

### DayBucketEntry
- Date: string — YYYYMMDD
- Start: time.Time
- End: time.Time
- Tag: string
- Summary: string — description text after date
- Path: string
- FileID: uint64
- RecurringSpec: string — empty for one-shot
- AllDay: bool

### AckEntry
- Start: time.Time — open for ..DATE entries
- End: time.Time
- Text: string — descriptive text after date

## Collaborators
- Matcher: used by DismissByPattern and ResolveByPattern

## Sequences
- seq-add.md
- seq-search.md
- seq-server-startup.md
