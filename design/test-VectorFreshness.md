# Test Design: Vector Freshness Substrate (S records)
**Source:** crc-Store.md, specs/vector-freshness.md

## Test: stampWrite stores varint serial under S prefix
**Purpose:** validate side-index key shape and varint value encoding
**Input:** open Store, call WriteChunkEmbedding(chunkID=42, vec); inspect
the LMDB key `S` + `EC` + varint(42)
**Expected:** the key exists; its value is a varint that decodes to a
non-zero uint64
**Refs:** R2174, R2175

## Test: allocSerial advances counter monotonically
**Purpose:** I:serial counter increments by 1 per allocation
**Input:** WriteChunkEmbedding(1, vec); read I:serial. WriteChunkEmbedding(2,
vec); read I:serial again
**Expected:** second I:serial == first I:serial + 1; both > 0
**Refs:** R2176

## Test: WriteTagNameEmbedding stamps ST<tag>
**Purpose:** T-name embedding writer stamps in same txn
**Input:** WriteTagNameEmbedding("decision", vec)
**Expected:** RecordSerial([]byte{'T'}, []byte("decision")) returns
(serial, true, nil) with serial > 0
**Refs:** R2179

## Test: WriteTagValueEmbedding stamps SEV<tvid:8>
**Purpose:** EV writer stamps in same txn
**Input:** WriteTagValueEmbedding(tvid=7, vec)
**Expected:** RecordSerial([]byte("EV"), embedValueKey(7)[2:]) returns
(serial, true, nil) with serial > 0
**Refs:** R2180

## Test: WriteTagDefEmbedding stamps SED<tag><fileid:8>
**Purpose:** ED writer stamps in same txn
**Input:** WriteTagDefEmbedding("decision", fileid=42, vec)
**Expected:** RecordSerial([]byte("ED"), embedDefKey("decision", 42)[2:])
returns (serial, true, nil) with serial > 0
**Refs:** R2181

## Test: WriteChunkEmbedding stamps SEC<chunkID-varint>
**Purpose:** EC writer stamps in same txn
**Input:** WriteChunkEmbedding(chunkID=99, vec)
**Expected:** RecordSerial([]byte("EC"), chunkEmbedKey(99)[2:]) returns
(serial, true, nil) with serial > 0
**Refs:** R2182

## Test: WriteChunkEmbeddingBatch stamps all records with one shared serial
**Purpose:** batch writer allocates one serial and applies it uniformly
**Input:** WriteChunkEmbeddingBatch(chunks=[{1,vec},{2,vec},{3,vec}])
**Expected:** the three SEC entries' serials are all equal; that serial
is one greater than any pre-batch I:serial value
**Refs:** R2183

## Test: per-txn semantics across separate Update calls
**Purpose:** stamps in distinct LMDB write txns get strictly-increasing
serials
**Input:** WriteChunkEmbedding(1, vec); WriteChunkEmbedding(2, vec)
**Expected:** RecordSerial for chunk 2 > RecordSerial for chunk 1
**Refs:** R2184

## Test: re-stamping updates serial
**Purpose:** rewriting a record advances its serial
**Input:** WriteChunkEmbedding(1, vec); record s1; WriteChunkEmbedding(1,
vec2); record s2
**Expected:** s2 > s1; the SEC<1> side-index value reflects s2
**Refs:** R2184

## Test: original record values unchanged by stamping
**Purpose:** stamping does not disturb the underlying T/EV/ED/EC values
**Input:** WriteChunkEmbedding(1, vec); ReadChunkEmbedding(1)
**Expected:** ReadChunkEmbedding returns vec unchanged. Same property
holds for ReadTagNameEmbedding, ReadTagValueEmbedding,
ReadTagDefEmbedding after their respective writes.
**Refs:** R2178

## Test: DeleteChunkEmbedding drops matching SEC entry
**Purpose:** delete keeps side index in sync
**Input:** WriteChunkEmbedding(1, vec); DeleteChunkEmbedding(1)
**Expected:** RecordSerial for chunk 1 returns (0, false, nil)
**Refs:** R2185

## Test: DeleteChunkEmbeddingInTxn drops matching SEC entry
**Purpose:** in-txn delete also drops SEC
**Input:** WriteChunkEmbedding(1, vec); env.Update wrapping
DeleteChunkEmbeddingInTxn(txn, 1)
**Expected:** RecordSerial for chunk 1 returns (0, false, nil)
**Refs:** R2185

## Test: UpdateTagDefs drops fileid's SED entries
**Purpose:** delByFileid extension cleans the side index alongside D and ED
**Input:** UpdateTagDefs(fileid=10, {"a":"x","b":"y"});
WriteTagDefEmbedding for both (a,10) and (b,10);
UpdateTagDefs(fileid=10, {"c":"z"})
**Expected:** RecordSerial for SED("a",10) and SED("b",10) both return
(0, false, nil); SED("c",10) is absent until its ED is written by the
batch-embed pass
**Refs:** R2186

## Test: DropEmbeddings drops ST*/SEV*/SED* and leaves SEC* alone
**Purpose:** model-swap drop covers stamped tag-side records, not chunks
**Input:** populate T-name embedding (ST), EV (SEV), ED (SED), and EC
(SEC); call DropEmbeddings()
**Expected:** ST, SEV, SED side-index entries all gone; every SEC entry
still present with its original serial
**Refs:** R2187

## Test: DropChunkEmbeddings drops SEC*
**Purpose:** rebuild's EC drop covers stamped chunk-side records
**Input:** WriteChunkEmbeddingBatch with several chunks; call
DropChunkEmbeddings()
**Expected:** every SEC entry gone
**Refs:** R2193

## Test: RecordSerial returns (0,false,nil) for never-stamped key
**Purpose:** found bool distinguishes absent from stamped-with-zero
**Input:** RecordSerial([]byte("EC"), chunkEmbedKey(999)[2:]) with no
prior write
**Expected:** (0, false, nil)
**Refs:** R2188

## Test: WalkRecordsSinceSerial since=0 visits all stamped EC records
**Purpose:** baseline walk surfaces every stamped record under the prefix
**Input:** WriteChunkEmbedding for chunks 1, 2, 3;
WalkRecordsSinceSerial([]byte("EC"), 0, fn collecting keys)
**Expected:** fn called three times, with original keys for all three
chunks; each reported serial is > 0
**Refs:** R2189

## Test: WalkRecordsSinceSerial since=N skips serials <= N
**Purpose:** since filter is strict-greater, not ≥
**Input:** WriteChunkEmbedding(1) → s1; WriteChunkEmbedding(2) → s2;
WalkRecordsSinceSerial([]byte("EC"), s1, fn)
**Expected:** fn called once, with chunk 2's key and serial s2
**Refs:** R2189

## Test: WalkRecordsSinceSerial fn-error stops iteration
**Purpose:** non-nil error from fn halts the walk and propagates
**Input:** stamp three EC records; WalkRecordsSinceSerial fn returns a
sentinel error on the first call
**Expected:** WalkRecordsSinceSerial returns the sentinel error; fn was
called exactly once
**Refs:** R2190

## Test: counter survives DropEmbeddings
**Purpose:** R2177 — counter is monotonic across drops
**Input:** WriteTagNameEmbedding to advance the counter; record I:serial;
DropEmbeddings(); WriteTagNameEmbedding again; record I:serial
**Expected:** post-drop I:serial > pre-drop I:serial; new ST entries
carry serials > any pre-drop serial
**Refs:** R2177

## Test: counter survives DropChunkEmbeddings
**Purpose:** R2177 — counter is monotonic across rebuild's EC drop
**Input:** WriteChunkEmbedding to advance counter; record I:serial;
DropChunkEmbeddings(); WriteChunkEmbedding again
**Expected:** post-drop I:serial > pre-drop I:serial; new SEC entries
carry serials > any pre-drop serial
**Refs:** R2177

## Test: substrate does not backfill pre-existing records
**Purpose:** R2192 — records written before the substrate landed have no
S-entry until next write
**Input:** simulate by writing an EC record's value via raw txn.Put
(bypassing WriteChunkEmbedding) so no S-entry is written; call
RecordSerial for that chunk; call WalkRecordsSinceSerial
**Expected:** RecordSerial returns (0, false, nil); the walk does not
visit that chunk
**Refs:** R2192

## Test: no tombstones — deleted records leave no S residue
**Purpose:** R2191 — sentinel-value tombstones are not introduced
**Input:** WriteChunkEmbedding(1); DeleteChunkEmbedding(1);
WalkRecordsSinceSerial since=0
**Expected:** the walk does not visit chunk 1; RecordSerial returns
(0, false, nil); no special tombstone value is observable
**Refs:** R2191
