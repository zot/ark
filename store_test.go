package ark

// CRC: crc-Store.md | Test: test-Store.md, test-Tags.md

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bmatsuo/lmdb-go/lmdb"
)

// testStore creates a temporary LMDB env and Store for testing.
func testStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	env, err := lmdb.NewEnv()
	if err != nil {
		t.Fatal(err)
	}
	if err := env.SetMaxDBs(8); err != nil {
		t.Fatal(err)
	}
	if err := env.SetMapSize(1 << 20); err != nil {
		t.Fatal(err)
	}
	if err := env.Open(dir, 0, 0644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.Close() })

	store, err := OpenStore(env)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// --- Missing file tests ---

func TestAddAndListMissing(t *testing.T) {
	s := testStore(t)
	now := time.Now()
	if err := s.AddMissing(42, "/foo/bar.md", now); err != nil {
		t.Fatal(err)
	}
	records, err := s.ListMissing()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].FileID != 42 {
		t.Errorf("expected fileid 42, got %d", records[0].FileID)
	}
	if records[0].Path != "/foo/bar.md" {
		t.Errorf("expected path /foo/bar.md, got %q", records[0].Path)
	}
}

func TestRemoveMissing(t *testing.T) {
	s := testStore(t)
	if err := s.AddMissing(42, "/foo/bar.md", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveMissing(42); err != nil {
		t.Fatal(err)
	}
	records, err := s.ListMissing()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records, got %d", len(records))
	}
}

// --- Unresolved file tests ---

func TestAddAndListUnresolved(t *testing.T) {
	s := testStore(t)
	if err := s.AddUnresolved("/foo/mystery.dat", "/foo"); err != nil {
		t.Fatal(err)
	}
	records, err := s.ListUnresolved()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Path != "/foo/mystery.dat" {
		t.Errorf("expected path /foo/mystery.dat, got %q", records[0].Path)
	}
	if records[0].Dir != "/foo" {
		t.Errorf("expected dir /foo, got %q", records[0].Dir)
	}
}

func TestCleanUnresolvedRemovesGoneFiles(t *testing.T) {
	s := testStore(t)
	// Create a temp file, add it, then delete it
	dir := t.TempDir()
	tmpFile := filepath.Join(dir, "temp.dat")
	if err := os.WriteFile(tmpFile, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := s.AddUnresolved(tmpFile, dir); err != nil {
		t.Fatal(err)
	}
	os.Remove(tmpFile)
	if err := s.CleanUnresolved(); err != nil {
		t.Fatal(err)
	}
	records, err := s.ListUnresolved()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records after clean, got %d", len(records))
	}
}

func TestCleanUnresolvedKeepsExistingFiles(t *testing.T) {
	s := testStore(t)
	dir := t.TempDir()
	tmpFile := filepath.Join(dir, "exists.dat")
	if err := os.WriteFile(tmpFile, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := s.AddUnresolved(tmpFile, dir); err != nil {
		t.Fatal(err)
	}
	if err := s.CleanUnresolved(); err != nil {
		t.Fatal(err)
	}
	records, err := s.ListUnresolved()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Errorf("expected 1 record, got %d", len(records))
	}
}

func TestDismissByPattern(t *testing.T) {
	s := testStore(t)
	m := &Matcher{Dotfiles: true}
	s.AddMissing(1, "/foo/a.md", time.Now())
	s.AddMissing(2, "/foo/b.md", time.Now())
	s.AddMissing(3, "/foo/c.txt", time.Now())

	dismissed, err := s.DismissByPattern([]string{"*.md"}, m)
	if err != nil {
		t.Fatal(err)
	}
	if len(dismissed) != 2 {
		t.Errorf("expected 2 dismissed, got %d", len(dismissed))
	}
	remaining, _ := s.ListMissing()
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining, got %d", len(remaining))
	}
	if remaining[0].Path != "/foo/c.txt" {
		t.Errorf("expected c.txt remaining, got %q", remaining[0].Path)
	}
}

func TestResolveByPattern(t *testing.T) {
	s := testStore(t)
	m := &Matcher{Dotfiles: true}
	s.AddUnresolved("/foo/x.dat", "/foo")
	s.AddUnresolved("/foo/y.dat", "/foo")
	s.AddUnresolved("/foo/z.md", "/foo")

	if err := s.ResolveByPattern([]string{"*.dat"}, m); err != nil {
		t.Fatal(err)
	}
	remaining, _ := s.ListUnresolved()
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining, got %d", len(remaining))
	}
	if remaining[0].Path != "/foo/z.md" {
		t.Errorf("expected z.md remaining, got %q", remaining[0].Path)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	s := testStore(t)
	cfg := &Config{Dotfiles: true, TagModel: "nomic.gguf"}
	if err := s.WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("ReadConfig returned nil")
	}
	if got.Dotfiles != true {
		t.Error("expected dotfiles=true")
	}
	if got.TagModel != "nomic.gguf" {
		t.Errorf("expected tag_model=nomic.gguf, got %q", got.TagModel)
	}
}

func TestIRecordRoundTrip(t *testing.T) {
	s := testStore(t)
	if err := s.IPut("test_key", "test_value"); err != nil {
		t.Fatal(err)
	}
	got, err := s.IGet("test_key")
	if err != nil {
		t.Fatal(err)
	}
	if got != "test_value" {
		t.Errorf("expected test_value, got %q", got)
	}

	// Delete
	if err := s.IDel("test_key"); err != nil {
		t.Fatal(err)
	}
	got, err = s.IGet("test_key")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("expected empty after delete, got %q", got)
	}
}

func TestERecordRoundTrip(t *testing.T) {
	s := testStore(t)
	payload := map[string]string{"stored": "old", "current": "new"}
	if err := s.WriteERecord("model_mismatch", payload); err != nil {
		t.Fatal(err)
	}
	records, err := s.ReadERecords()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 E record, got %d", len(records))
	}
	if _, ok := records["model_mismatch"]; !ok {
		t.Error("expected model_mismatch E record")
	}

	// Clear
	if err := s.ClearERecords(); err != nil {
		t.Fatal(err)
	}
	records, err = s.ReadERecords()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 E records after clear, got %d", len(records))
	}
}

// --- Tag tests (Store-level, chunkid-keyed) ---

// ctv builds a ChunkTagValues with `count` tag occurrences per name, all
// with empty value (no V records). Useful for T/F-only tests.
func ctv(chunkID uint64, tags map[string]int) ChunkTagValues {
	var values []TagValue
	for tag, n := range tags {
		for i := 0; i < n; i++ {
			values = append(values, TagValue{Tag: tag})
		}
	}
	return ChunkTagValues{ChunkID: chunkID, Values: values}
}

func TestUpdateTagValuesAndListTags(t *testing.T) {
	s := testStore(t)
	// Chunk 1 carries two `decision` occurrences and one `pattern`.
	if err := s.UpdateTagValues([]ChunkTagValues{ctv(1, map[string]int{"decision": 2, "pattern": 1})}); err != nil {
		t.Fatal(err)
	}
	// Chunk 2 carries one `decision`.
	if err := s.UpdateTagValues([]ChunkTagValues{ctv(2, map[string]int{"decision": 1})}); err != nil {
		t.Fatal(err)
	}
	tags, err := s.ListTags()
	if err != nil {
		t.Fatal(err)
	}
	m := make(map[string]uint32)
	for _, tc := range tags {
		m[tc.Tag] = tc.Count
	}
	// T = number of distinct (chunkID, tag) pairs:
	//   decision: chunks 1 and 2 → 2
	//   pattern:  chunk 1 → 1
	if m["decision"] != 2 {
		t.Errorf("expected decision=2 (two chunks), got %d", m["decision"])
	}
	if m["pattern"] != 1 {
		t.Errorf("expected pattern=1, got %d", m["pattern"])
	}
}

func TestRemoveTagValuesDecrements(t *testing.T) {
	s := testStore(t)
	s.UpdateTagValues([]ChunkTagValues{ctv(1, map[string]int{"decision": 2})})
	s.UpdateTagValues([]ChunkTagValues{ctv(2, map[string]int{"decision": 1})})

	if err := s.RemoveTagValues(1); err != nil {
		t.Fatal(err)
	}
	tags, err := s.ListTags()
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 {
		t.Fatalf("expected 1 tag remaining, got %d", len(tags))
	}
	if tags[0].Tag != "decision" || tags[0].Count != 1 {
		t.Errorf("expected decision=1 after removing chunk 1, got %s=%d", tags[0].Tag, tags[0].Count)
	}
}

func TestTagFilesChunkAttributed(t *testing.T) {
	s := testStore(t)
	s.UpdateTagValues([]ChunkTagValues{ctv(1, map[string]int{"decision": 2})})
	s.UpdateTagValues([]ChunkTagValues{ctv(2, map[string]int{"decision": 1, "pattern": 3})})

	records, err := s.TagFiles([]string{"decision"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records (one per chunk), got %d", len(records))
	}
	// Without a chunkResolver, FileID is 0; ChunkID carries the truth.
	m := make(map[uint64]uint32)
	for _, r := range records {
		m[r.ChunkID] = r.Count
	}
	if m[1] != 2 {
		t.Errorf("expected chunkID=1 count=2, got %d", m[1])
	}
	if m[2] != 1 {
		t.Errorf("expected chunkID=2 count=1, got %d", m[2])
	}
}

func TestTagCounts(t *testing.T) {
	s := testStore(t)
	s.UpdateTagValues([]ChunkTagValues{ctv(1, map[string]int{"decision": 2, "pattern": 1})})

	counts, err := s.TagCounts([]string{"decision", "nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 2 {
		t.Fatalf("expected 2 results, got %d", len(counts))
	}
	m := make(map[string]uint32)
	for _, c := range counts {
		m[c.Tag] = c.Count
	}
	// One chunk carries decision → T[decision]=1.
	if m["decision"] != 1 {
		t.Errorf("expected decision=1 (one chunk), got %d", m["decision"])
	}
	if m["nonexistent"] != 0 {
		t.Errorf("expected nonexistent=0, got %d", m["nonexistent"])
	}
}

// --- AppendTagValues tests ---

func TestAppendTagValuesIdempotentForSameChunkID(t *testing.T) {
	s := testStore(t)
	s.UpdateTagValues([]ChunkTagValues{ctv(1, map[string]int{"decision": 2, "pattern": 1})})

	// Re-appending the same chunkid+tags is idempotent (chunkids are
	// content-hashed: same content → same chunkid → same extraction).
	if err := s.AppendTagValues([]ChunkTagValues{ctv(1, map[string]int{"decision": 2, "pattern": 1})}); err != nil {
		t.Fatal(err)
	}

	tags, _ := s.ListTags()
	m := make(map[string]uint32)
	for _, tc := range tags {
		m[tc.Tag] = tc.Count
	}
	if m["decision"] != 1 {
		t.Errorf("expected decision=1 (one chunk, idempotent), got %d", m["decision"])
	}
	if m["pattern"] != 1 {
		t.Errorf("expected pattern=1, got %d", m["pattern"])
	}
}

func TestAppendTagValuesNewChunk(t *testing.T) {
	s := testStore(t)
	if err := s.AppendTagValues([]ChunkTagValues{ctv(99, map[string]int{"fresh": 5})}); err != nil {
		t.Fatal(err)
	}

	tags, _ := s.ListTags()
	if len(tags) != 1 || tags[0].Tag != "fresh" || tags[0].Count != 1 {
		t.Errorf("expected fresh=1 (one chunk carries the tag), got %v", tags)
	}
}

// --- ED record tests (R2151-R2162) ---
// CRC: crc-Store.md | Test: test-TagDefEmbed.md

// fakeVec returns a deterministic 768-dim vector seeded by tag.
// The first byte is set so different inputs produce different vectors;
// the rest stays zero, which is fine for round-trip equality tests.
func fakeVec(seed byte) []float32 {
	v := make([]float32, 768)
	v[0] = float32(seed)
	return v
}

func vecEqual(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestED_WriteAndRead validates ED key/value round-trip per fileid.
// Refs: R2151, R2153, R2159
func TestED_WriteAndRead(t *testing.T) {
	s := testStore(t)
	want := fakeVec(7)
	if err := s.WriteTagDefEmbedding("decision", 42, want); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadTagDefEmbedding("decision", 42)
	if err != nil {
		t.Fatal(err)
	}
	if !vecEqual(got, want) {
		t.Errorf("round-trip mismatch")
	}
	missing, err := s.ReadTagDefEmbedding("decision", 99)
	if err != nil {
		t.Fatalf("read absent: %v", err)
	}
	if missing != nil {
		t.Errorf("expected nil for absent fileid, got %v", missing)
	}
}

// TestED_MissingFindsDWithoutED checks the batch-embed discovery path.
// Refs: R2157
func TestED_MissingFindsDWithoutED(t *testing.T) {
	s := testStore(t)
	if err := s.UpdateTagDefs(10, map[string]string{"decision": "choosing tools"}); err != nil {
		t.Fatal(err)
	}
	missing, err := s.MissingTagDefEmbeddings()
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || missing[0].Tag != "decision" || missing[0].FileID != 10 {
		t.Fatalf("expected one missing pair (decision,10), got %+v", missing)
	}
	if missing[0].Description != "choosing tools" {
		t.Errorf("description not carried back: %q", missing[0].Description)
	}
	if err := s.WriteTagDefEmbedding("decision", 10, fakeVec(1)); err != nil {
		t.Fatal(err)
	}
	missing2, err := s.MissingTagDefEmbeddings()
	if err != nil {
		t.Fatal(err)
	}
	if len(missing2) != 0 {
		t.Errorf("expected empty after write, got %+v", missing2)
	}
}

// TestED_UpdateTagDefsDropsED verifies ED lifecycle mirrors D on replace.
// Refs: R2154
func TestED_UpdateTagDefsDropsED(t *testing.T) {
	s := testStore(t)
	if err := s.UpdateTagDefs(10, map[string]string{"a": "x", "b": "y"}); err != nil {
		t.Fatal(err)
	}
	s.WriteTagDefEmbedding("a", 10, fakeVec(1))
	s.WriteTagDefEmbedding("b", 10, fakeVec(2))

	if err := s.UpdateTagDefs(10, map[string]string{"c": "z"}); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.ReadTagDefEmbedding("a", 10); v != nil {
		t.Error("expected ED(a,10) gone")
	}
	if v, _ := s.ReadTagDefEmbedding("b", 10); v != nil {
		t.Error("expected ED(b,10) gone")
	}
	missing, _ := s.MissingTagDefEmbeddings()
	if len(missing) != 1 || missing[0].Tag != "c" || missing[0].FileID != 10 {
		t.Errorf("expected (c,10) missing, got %+v", missing)
	}
}

// TestED_UpdateTagDefsScopedByFileid ensures ED deletion is scoped to the
// fileid being replaced — other files' ED records survive.
// Refs: R2154
func TestED_UpdateTagDefsScopedByFileid(t *testing.T) {
	s := testStore(t)
	s.UpdateTagDefs(10, map[string]string{"a": "x"})
	s.UpdateTagDefs(20, map[string]string{"a": "x"})
	s.WriteTagDefEmbedding("a", 10, fakeVec(1))
	s.WriteTagDefEmbedding("a", 20, fakeVec(2))

	if err := s.UpdateTagDefs(10, nil); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.ReadTagDefEmbedding("a", 10); v != nil {
		t.Error("expected ED(a,10) cleared")
	}
	v, _ := s.ReadTagDefEmbedding("a", 20)
	if !vecEqual(v, fakeVec(2)) {
		t.Error("ED(a,20) should survive — different fileid")
	}
}

// TestED_RemoveTagDefsDropsED — removal path drops both D and ED.
// Refs: R2155
func TestED_RemoveTagDefsDropsED(t *testing.T) {
	s := testStore(t)
	s.UpdateTagDefs(7, map[string]string{"decision": "d"})
	s.WriteTagDefEmbedding("decision", 7, fakeVec(3))

	if err := s.RemoveTagDefs(7); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.ReadTagDefEmbedding("decision", 7); v != nil {
		t.Error("expected ED gone after RemoveTagDefs")
	}
	missing, _ := s.MissingTagDefEmbeddings()
	for _, m := range missing {
		if m.Tag == "decision" && m.FileID == 7 {
			t.Error("removed pair should not appear in MissingTagDefEmbeddings")
		}
	}
}

// TestED_AppendTagDefsPreservesED — append leaves existing ED intact.
// Refs: R2156
func TestED_AppendTagDefsPreservesED(t *testing.T) {
	s := testStore(t)
	s.UpdateTagDefs(10, map[string]string{"a": "x"})
	want := fakeVec(5)
	s.WriteTagDefEmbedding("a", 10, want)

	if err := s.AppendTagDefs(10, map[string]string{"b": "y"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ReadTagDefEmbedding("a", 10)
	if !vecEqual(got, want) {
		t.Error("ED(a,10) should survive AppendTagDefs")
	}
	missing, _ := s.MissingTagDefEmbeddings()
	if len(missing) != 1 || missing[0].Tag != "b" || missing[0].FileID != 10 {
		t.Errorf("expected only (b,10) missing, got %+v", missing)
	}
}

// TestED_DropEmbeddingsClearsED — model swap drops ED with EV and T-vec.
// Refs: R2160
func TestED_DropEmbeddingsClearsED(t *testing.T) {
	s := testStore(t)
	s.UpdateTagDefs(1, map[string]string{"a": "x", "b": "y"})
	s.WriteTagDefEmbedding("a", 1, fakeVec(1))
	s.WriteTagDefEmbedding("b", 1, fakeVec(2))

	if err := s.DropEmbeddings(); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.ReadTagDefEmbedding("a", 1); v != nil {
		t.Error("expected ED(a,1) cleared by DropEmbeddings")
	}
	if v, _ := s.ReadTagDefEmbedding("b", 1); v != nil {
		t.Error("expected ED(b,1) cleared by DropEmbeddings")
	}
	// D records survive — DropEmbeddings only touches embedding records.
	defs, _ := s.ListTagDefs(nil)
	if len(defs) != 2 {
		t.Errorf("expected D records preserved, got %d", len(defs))
	}
}

// TestED_RecordCountsListsED — RecordCounts surfaces "ED" prefix so
// `ark status -db` can count them.
// Refs: R2162
func TestED_RecordCountsListsED(t *testing.T) {
	s := testStore(t)
	s.WriteTagDefEmbedding("decision", 1, fakeVec(1))
	s.WriteTagDefEmbedding("pattern", 2, fakeVec(2))

	counts, err := s.RecordCounts()
	if err != nil {
		t.Fatal(err)
	}
	st, ok := counts["ED"]
	if !ok {
		t.Fatalf("expected ED prefix in RecordCounts, got %v", counts)
	}
	if st.Count != 2 {
		t.Errorf("expected 2 ED records, got %d", st.Count)
	}
}

// --- Vector Freshness Substrate (S records) ---
// CRC: crc-Store.md | Test: test-VectorFreshness.md

// readISerial reads the I:serial counter directly. 0 if absent.
func readISerial(t *testing.T, s *Store) uint64 {
	t.Helper()
	v, err := s.IGetCounter("serial")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestS_StampVarintUnderSPrefix validates side-index key shape and varint
// encoding.
// Refs: R2174, R2175
func TestS_StampVarintUnderSPrefix(t *testing.T) {
	s := testStore(t)
	if err := s.WriteChunkEmbedding(42, fakeVec(1)); err != nil {
		t.Fatal(err)
	}
	ecKey := chunkEmbedKey(42)
	tail := ecKey[len(prefixEmbedChunk):]
	serial, found, err := s.RecordSerial([]byte(prefixEmbedChunk), tail)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected SEC entry to be present")
	}
	if serial == 0 {
		t.Errorf("expected serial > 0, got 0")
	}
}

// TestS_AllocSerialMonotonic validates that the I:serial counter increments
// by 1 per write txn.
// Refs: R2176
func TestS_AllocSerialMonotonic(t *testing.T) {
	s := testStore(t)
	if err := s.WriteChunkEmbedding(1, fakeVec(1)); err != nil {
		t.Fatal(err)
	}
	first := readISerial(t, s)
	if err := s.WriteChunkEmbedding(2, fakeVec(2)); err != nil {
		t.Fatal(err)
	}
	second := readISerial(t, s)
	if second != first+1 {
		t.Errorf("expected counter to advance by 1, got %d -> %d", first, second)
	}
}

// TestS_WriteTagNameEmbeddingStamps validates ST<tag> stamping.
// Refs: R2179
func TestS_WriteTagNameEmbeddingStamps(t *testing.T) {
	s := testStore(t)
	if err := s.WriteTagNameEmbedding("decision", fakeVec(1)); err != nil {
		t.Fatal(err)
	}
	serial, found, err := s.RecordSerial([]byte{byte(prefixTagTotal)}, []byte("decision"))
	if err != nil {
		t.Fatal(err)
	}
	if !found || serial == 0 {
		t.Errorf("expected ST entry with serial > 0, got found=%v serial=%d", found, serial)
	}
}

// TestS_WriteTagValueEmbeddingStamps validates SEV<tvid-varint> stamping.
// Refs: R2180
func TestS_WriteTagValueEmbeddingStamps(t *testing.T) {
	s := testStore(t)
	if err := s.WriteTagValueEmbedding(7, fakeVec(1)); err != nil {
		t.Fatal(err)
	}
	tail := embedValueKey(7)[len(prefixEmbedValue):]
	serial, found, err := s.RecordSerial([]byte(prefixEmbedValue), tail)
	if err != nil {
		t.Fatal(err)
	}
	if !found || serial == 0 {
		t.Errorf("expected SEV entry with serial > 0, got found=%v serial=%d", found, serial)
	}
}

// TestS_WriteTagDefEmbeddingStamps validates SED<tag><fileid:8> stamping.
// Refs: R2181
func TestS_WriteTagDefEmbeddingStamps(t *testing.T) {
	s := testStore(t)
	if err := s.WriteTagDefEmbedding("decision", 42, fakeVec(1)); err != nil {
		t.Fatal(err)
	}
	tail := embedDefKey("decision", 42)[len(prefixEmbedDef):]
	serial, found, err := s.RecordSerial([]byte(prefixEmbedDef), tail)
	if err != nil {
		t.Fatal(err)
	}
	if !found || serial == 0 {
		t.Errorf("expected SED entry with serial > 0, got found=%v serial=%d", found, serial)
	}
}

// TestS_WriteChunkEmbeddingStamps validates SEC<chunkID-varint> stamping.
// Refs: R2182
func TestS_WriteChunkEmbeddingStamps(t *testing.T) {
	s := testStore(t)
	if err := s.WriteChunkEmbedding(99, fakeVec(1)); err != nil {
		t.Fatal(err)
	}
	tail := chunkEmbedKey(99)[len(prefixEmbedChunk):]
	serial, found, err := s.RecordSerial([]byte(prefixEmbedChunk), tail)
	if err != nil {
		t.Fatal(err)
	}
	if !found || serial == 0 {
		t.Errorf("expected SEC entry with serial > 0, got found=%v serial=%d", found, serial)
	}
}

// TestS_BatchSharesOneSerial validates per-txn semantics for batch writes.
// Refs: R2183
func TestS_BatchSharesOneSerial(t *testing.T) {
	s := testStore(t)
	pre := readISerial(t, s)
	if err := s.WriteChunkEmbeddingBatch([]ChunkVec{
		{ChunkID: 1, Vec: fakeVec(1)},
		{ChunkID: 2, Vec: fakeVec(2)},
		{ChunkID: 3, Vec: fakeVec(3)},
	}); err != nil {
		t.Fatal(err)
	}
	var seen []uint64
	for _, id := range []uint64{1, 2, 3} {
		tail := chunkEmbedKey(id)[len(prefixEmbedChunk):]
		serial, found, err := s.RecordSerial([]byte(prefixEmbedChunk), tail)
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			t.Fatalf("missing SEC entry for chunk %d", id)
		}
		seen = append(seen, serial)
	}
	if seen[0] != seen[1] || seen[1] != seen[2] {
		t.Errorf("expected all batch records to share one serial, got %v", seen)
	}
	if seen[0] != pre+1 {
		t.Errorf("expected batch serial = pre+1 (%d), got %d", pre+1, seen[0])
	}
}

// TestS_CrossTxnMonotonic validates strictly-increasing serials across
// distinct write txns.
// Refs: R2184
func TestS_CrossTxnMonotonic(t *testing.T) {
	s := testStore(t)
	if err := s.WriteChunkEmbedding(1, fakeVec(1)); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteChunkEmbedding(2, fakeVec(2)); err != nil {
		t.Fatal(err)
	}
	tail1 := chunkEmbedKey(1)[len(prefixEmbedChunk):]
	tail2 := chunkEmbedKey(2)[len(prefixEmbedChunk):]
	s1, _, _ := s.RecordSerial([]byte(prefixEmbedChunk), tail1)
	s2, _, _ := s.RecordSerial([]byte(prefixEmbedChunk), tail2)
	if !(s2 > s1) {
		t.Errorf("expected s2 > s1, got s1=%d s2=%d", s1, s2)
	}
}

// TestS_RestampUpdatesSerial validates that rewriting a record advances its
// serial.
// Refs: R2184
func TestS_RestampUpdatesSerial(t *testing.T) {
	s := testStore(t)
	if err := s.WriteChunkEmbedding(1, fakeVec(1)); err != nil {
		t.Fatal(err)
	}
	tail := chunkEmbedKey(1)[len(prefixEmbedChunk):]
	first, _, _ := s.RecordSerial([]byte(prefixEmbedChunk), tail)
	if err := s.WriteChunkEmbedding(1, fakeVec(2)); err != nil {
		t.Fatal(err)
	}
	second, _, _ := s.RecordSerial([]byte(prefixEmbedChunk), tail)
	if !(second > first) {
		t.Errorf("expected second > first, got first=%d second=%d", first, second)
	}
}

// TestS_OriginalValuesUnchanged validates that stamping does not disturb the
// underlying record values.
// Refs: R2178
func TestS_OriginalValuesUnchanged(t *testing.T) {
	s := testStore(t)
	want := fakeVec(7)
	if err := s.WriteChunkEmbedding(1, want); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadChunkEmbedding(1)
	if err != nil {
		t.Fatal(err)
	}
	if !vecEqual(got, want) {
		t.Errorf("EC round-trip mismatch")
	}
	if err := s.WriteTagDefEmbedding("a", 1, want); err != nil {
		t.Fatal(err)
	}
	got2, err := s.ReadTagDefEmbedding("a", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !vecEqual(got2, want) {
		t.Errorf("ED round-trip mismatch")
	}
	if err := s.WriteTagValueEmbedding(7, want); err != nil {
		t.Fatal(err)
	}
	got3, err := s.ReadTagValueEmbedding(7)
	if err != nil {
		t.Fatal(err)
	}
	if !vecEqual(got3, want) {
		t.Errorf("EV round-trip mismatch")
	}
}

// TestS_DeleteChunkEmbeddingDropsSEC validates side-index cleanup on delete.
// Refs: R2185
func TestS_DeleteChunkEmbeddingDropsSEC(t *testing.T) {
	s := testStore(t)
	if err := s.WriteChunkEmbedding(1, fakeVec(1)); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteChunkEmbedding(1); err != nil {
		t.Fatal(err)
	}
	tail := chunkEmbedKey(1)[len(prefixEmbedChunk):]
	_, found, err := s.RecordSerial([]byte(prefixEmbedChunk), tail)
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Error("expected SEC entry to be gone after delete")
	}
}

// TestS_UpdateTagDefsDropsSED validates the delByFileid extension covers SED.
// Refs: R2186
func TestS_UpdateTagDefsDropsSED(t *testing.T) {
	s := testStore(t)
	if err := s.UpdateTagDefs(10, map[string]string{"a": "x", "b": "y"}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTagDefEmbedding("a", 10, fakeVec(1)); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTagDefEmbedding("b", 10, fakeVec(2)); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateTagDefs(10, map[string]string{"c": "z"}); err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"a", "b"} {
		tail := embedDefKey(tag, 10)[len(prefixEmbedDef):]
		_, found, _ := s.RecordSerial([]byte(prefixEmbedDef), tail)
		if found {
			t.Errorf("expected SED(%s,10) to be gone", tag)
		}
	}
}

// TestS_DropEmbeddingsClearsTagSideStamps validates the model-swap drop covers
// ST*/SEV*/SED* but leaves SEC* alone.
// Refs: R2187
func TestS_DropEmbeddingsClearsTagSideStamps(t *testing.T) {
	s := testStore(t)
	// populate all four
	s.WriteTagNameEmbedding("t1", fakeVec(1))
	s.WriteTagValueEmbedding(7, fakeVec(2))
	s.WriteTagDefEmbedding("d1", 10, fakeVec(3))
	s.WriteChunkEmbedding(99, fakeVec(4))

	if err := s.DropEmbeddings(); err != nil {
		t.Fatal(err)
	}

	if _, found, _ := s.RecordSerial([]byte{byte(prefixTagTotal)}, []byte("t1")); found {
		t.Error("expected ST(t1) gone")
	}
	tailEV := embedValueKey(7)[len(prefixEmbedValue):]
	if _, found, _ := s.RecordSerial([]byte(prefixEmbedValue), tailEV); found {
		t.Error("expected SEV(7) gone")
	}
	tailED := embedDefKey("d1", 10)[len(prefixEmbedDef):]
	if _, found, _ := s.RecordSerial([]byte(prefixEmbedDef), tailED); found {
		t.Error("expected SED(d1,10) gone")
	}
	tailEC := chunkEmbedKey(99)[len(prefixEmbedChunk):]
	if _, found, _ := s.RecordSerial([]byte(prefixEmbedChunk), tailEC); !found {
		t.Error("expected SEC(99) preserved by DropEmbeddings")
	}
}

// TestS_DropChunkEmbeddingsClearsSEC validates the rebuild EC drop covers SEC*.
// Refs: R2193
func TestS_DropChunkEmbeddingsClearsSEC(t *testing.T) {
	s := testStore(t)
	if err := s.WriteChunkEmbeddingBatch([]ChunkVec{
		{ChunkID: 1, Vec: fakeVec(1)},
		{ChunkID: 2, Vec: fakeVec(2)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.DropChunkEmbeddings(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint64{1, 2} {
		tail := chunkEmbedKey(id)[len(prefixEmbedChunk):]
		if _, found, _ := s.RecordSerial([]byte(prefixEmbedChunk), tail); found {
			t.Errorf("expected SEC(%d) gone", id)
		}
	}
}

// TestS_RecordSerialAbsent validates the found=false case.
// Refs: R2188
func TestS_RecordSerialAbsent(t *testing.T) {
	s := testStore(t)
	tail := chunkEmbedKey(999)[len(prefixEmbedChunk):]
	serial, found, err := s.RecordSerial([]byte(prefixEmbedChunk), tail)
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Error("expected found=false for never-stamped key")
	}
	if serial != 0 {
		t.Errorf("expected serial=0 for absent, got %d", serial)
	}
}

// TestS_WalkSinceZeroVisitsAll validates the baseline walk.
// Refs: R2189
func TestS_WalkSinceZeroVisitsAll(t *testing.T) {
	s := testStore(t)
	for _, id := range []uint64{1, 2, 3} {
		if err := s.WriteChunkEmbedding(id, fakeVec(byte(id))); err != nil {
			t.Fatal(err)
		}
	}
	visited := map[uint64]uint64{}
	err := s.WalkRecordsSinceSerial([]byte(prefixEmbedChunk), 0, func(originalKey []byte, serial uint64) error {
		// originalKey starts with "EC"
		id, _ := binary.Uvarint(originalKey[len(prefixEmbedChunk):])
		visited[id] = serial
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(visited) != 3 {
		t.Errorf("expected 3 visits, got %d (%v)", len(visited), visited)
	}
	for _, id := range []uint64{1, 2, 3} {
		if visited[id] == 0 {
			t.Errorf("expected serial > 0 for chunk %d", id)
		}
	}
}

// TestS_WalkSinceFiltersStrictGreater validates the > since filter.
// Refs: R2189
func TestS_WalkSinceFiltersStrictGreater(t *testing.T) {
	s := testStore(t)
	if err := s.WriteChunkEmbedding(1, fakeVec(1)); err != nil {
		t.Fatal(err)
	}
	tail1 := chunkEmbedKey(1)[len(prefixEmbedChunk):]
	s1, _, _ := s.RecordSerial([]byte(prefixEmbedChunk), tail1)
	if err := s.WriteChunkEmbedding(2, fakeVec(2)); err != nil {
		t.Fatal(err)
	}
	visited := map[uint64]uint64{}
	err := s.WalkRecordsSinceSerial([]byte(prefixEmbedChunk), s1, func(originalKey []byte, serial uint64) error {
		id, _ := binary.Uvarint(originalKey[len(prefixEmbedChunk):])
		visited[id] = serial
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(visited) != 1 {
		t.Errorf("expected 1 visit, got %d (%v)", len(visited), visited)
	}
	if _, ok := visited[2]; !ok {
		t.Error("expected chunk 2 to be visited")
	}
}

// TestS_WalkPropagatesError validates fn-error early-stop.
// Refs: R2190
func TestS_WalkPropagatesError(t *testing.T) {
	s := testStore(t)
	for _, id := range []uint64{1, 2, 3} {
		s.WriteChunkEmbedding(id, fakeVec(byte(id)))
	}
	sentinel := errors.New("stop")
	calls := 0
	err := s.WalkRecordsSinceSerial([]byte(prefixEmbedChunk), 0, func(_ []byte, _ uint64) error {
		calls++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel, got %v", err)
	}
	if calls != 1 {
		t.Errorf("expected exactly 1 call before stop, got %d", calls)
	}
}

// TestS_CounterSurvivesDropEmbeddings validates monotonicity across drop.
// Refs: R2177
func TestS_CounterSurvivesDropEmbeddings(t *testing.T) {
	s := testStore(t)
	if err := s.WriteTagNameEmbedding("t1", fakeVec(1)); err != nil {
		t.Fatal(err)
	}
	pre := readISerial(t, s)
	if err := s.DropEmbeddings(); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTagNameEmbedding("t2", fakeVec(2)); err != nil {
		t.Fatal(err)
	}
	post := readISerial(t, s)
	if !(post > pre) {
		t.Errorf("expected counter to advance past drop, got pre=%d post=%d", pre, post)
	}
	serial, found, _ := s.RecordSerial([]byte{byte(prefixTagTotal)}, []byte("t2"))
	if !found || !(serial > pre) {
		t.Errorf("expected new ST entry serial > pre (%d), got found=%v serial=%d", pre, found, serial)
	}
}

// TestS_CounterSurvivesDropChunkEmbeddings validates monotonicity across the
// rebuild EC drop.
// Refs: R2177
func TestS_CounterSurvivesDropChunkEmbeddings(t *testing.T) {
	s := testStore(t)
	if err := s.WriteChunkEmbedding(1, fakeVec(1)); err != nil {
		t.Fatal(err)
	}
	pre := readISerial(t, s)
	if err := s.DropChunkEmbeddings(); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteChunkEmbedding(1, fakeVec(2)); err != nil {
		t.Fatal(err)
	}
	post := readISerial(t, s)
	if !(post > pre) {
		t.Errorf("expected counter to advance past drop, got pre=%d post=%d", pre, post)
	}
	tail := chunkEmbedKey(1)[len(prefixEmbedChunk):]
	serial, _, _ := s.RecordSerial([]byte(prefixEmbedChunk), tail)
	if !(serial > pre) {
		t.Errorf("expected new SEC serial > pre (%d), got %d", pre, serial)
	}
}

// TestS_NoBackfill validates that pre-substrate records (raw txn.Put bypassing
// the stamping writers) have no S-entry until next write touches them.
// Refs: R2192
func TestS_NoBackfill(t *testing.T) {
	s := testStore(t)
	// Simulate a pre-substrate write — bypass WriteChunkEmbedding.
	err := s.env.Update(func(txn *lmdb.Txn) error {
		return txn.Put(s.dbi, chunkEmbedKey(42), float32ToBytes(fakeVec(1)), 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	tail := chunkEmbedKey(42)[len(prefixEmbedChunk):]
	if _, found, _ := s.RecordSerial([]byte(prefixEmbedChunk), tail); found {
		t.Error("expected found=false for pre-substrate record")
	}
	visited := 0
	s.WalkRecordsSinceSerial([]byte(prefixEmbedChunk), 0, func(_ []byte, _ uint64) error {
		visited++
		return nil
	})
	if visited != 0 {
		t.Errorf("expected walk to skip pre-substrate record, got %d visits", visited)
	}
}

// TestS_NoTombstones validates that delete leaves no observable residue.
// Refs: R2191
func TestS_NoTombstones(t *testing.T) {
	s := testStore(t)
	if err := s.WriteChunkEmbedding(1, fakeVec(1)); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteChunkEmbedding(1); err != nil {
		t.Fatal(err)
	}
	visited := 0
	err := s.WalkRecordsSinceSerial([]byte(prefixEmbedChunk), 0, func(_ []byte, _ uint64) error {
		visited++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if visited != 0 {
		t.Errorf("expected walk to find nothing after delete, got %d visits", visited)
	}
}
