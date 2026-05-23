package ark

// CRC: crc-Store.md | Test: test-Store.md, test-Tags.md

import (
	"encoding/binary"
	"errors"
	"math"
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

// --- Hot Correlation (HC) tests ---
// CRC: crc-Store.md | Test: test-HotCorrelations.md

// TestHC_WriteReadRoundTrip — write/read recovers the score, S-substrate
// stamps the record alongside.
// Refs: R2226, R2227, R2229
func TestHC_WriteReadRoundTrip(t *testing.T) {
	s := testStore(t)
	if err := s.WriteHotCorrelation("priority", 42, 0.85); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadHotCorrelations("priority")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ChunkID != 42 {
		t.Fatalf("expected one entry for chunk 42, got %+v", got)
	}
	if math.Abs(got[0].Score-0.85) > 1e-9 {
		t.Errorf("score round-trip: got %v want 0.85", got[0].Score)
	}
	// Stamp should exist with non-zero serial.
	key := hotCorrKey("priority", 42)
	serial, found, err := s.RecordSerial([]byte(prefixHotCorrelation), key[len(prefixHotCorrelation):])
	if err != nil {
		t.Fatal(err)
	}
	if !found || serial == 0 {
		t.Errorf("expected SHC stamp present and non-zero, got found=%v serial=%d", found, serial)
	}
}

// TestHC_ReadEmpty — no entries returns nil/empty, no error.
func TestHC_ReadEmpty(t *testing.T) {
	s := testStore(t)
	got, err := s.ReadHotCorrelations("absent")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %+v", got)
	}
}

// TestHC_DeleteRemovesValueAndStamp — DeleteHotCorrelation clears both
// the HC record and the SHC stamp.
// Refs: R2229
func TestHC_DeleteRemovesValueAndStamp(t *testing.T) {
	s := testStore(t)
	s.WriteHotCorrelation("priority", 42, 0.85)
	if err := s.DeleteHotCorrelation("priority", 42); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ReadHotCorrelations("priority")
	if len(got) != 0 {
		t.Errorf("expected entry gone, got %+v", got)
	}
	key := hotCorrKey("priority", 42)
	_, found, err := s.RecordSerial([]byte(prefixHotCorrelation), key[len(prefixHotCorrelation):])
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Errorf("expected stamp gone after delete")
	}
}

// TestHC_ReplaceAtomic — ReplaceHotCorrelations clears existing entries
// for a tag and writes the batch in one txn; all batch entries share a
// single serial.
// Refs: R2229, R2238
func TestHC_ReplaceAtomic(t *testing.T) {
	s := testStore(t)
	s.WriteHotCorrelation("priority", 1, 0.5)
	s.WriteHotCorrelation("priority", 2, 0.6)

	batch := []HotCorrelation{
		{ChunkID: 10, Score: 0.9},
		{ChunkID: 11, Score: 0.8},
		{ChunkID: 12, Score: 0.7},
	}
	if err := s.ReplaceHotCorrelations("priority", batch); err != nil {
		t.Fatal(err)
	}

	got, err := s.ReadHotCorrelations("priority")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 entries after replace, got %d (%+v)", len(got), got)
	}
	gotIDs := map[uint64]float64{}
	for _, e := range got {
		gotIDs[e.ChunkID] = e.Score
	}
	for _, want := range batch {
		if math.Abs(gotIDs[want.ChunkID]-want.Score) > 1e-9 {
			t.Errorf("missing or wrong score for chunk %d: got %v want %v", want.ChunkID, gotIDs[want.ChunkID], want.Score)
		}
	}
	if _, ok := gotIDs[1]; ok {
		t.Errorf("old entry chunk=1 not cleared")
	}

	// Verify all three new entries share a single serial.
	serials := []uint64{}
	for _, e := range batch {
		key := hotCorrKey("priority", e.ChunkID)
		serial, found, err := s.RecordSerial([]byte(prefixHotCorrelation), key[len(prefixHotCorrelation):])
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			t.Errorf("stamp missing for chunk %d", e.ChunkID)
		}
		serials = append(serials, serial)
	}
	for i := 1; i < len(serials); i++ {
		if serials[i] != serials[0] {
			t.Errorf("expected shared serial across batch, got %v", serials)
			break
		}
	}
}

// TestHC_ReplaceEmpty — ReplaceHotCorrelations with an empty slice
// clears the tag's HC entries.
// Refs: R2229
func TestHC_ReplaceEmpty(t *testing.T) {
	s := testStore(t)
	s.WriteHotCorrelation("priority", 1, 0.5)
	s.WriteHotCorrelation("priority", 2, 0.6)
	if err := s.ReplaceHotCorrelations("priority", nil); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ReadHotCorrelations("priority")
	if len(got) != 0 {
		t.Errorf("expected empty after replace-with-nil, got %+v", got)
	}
}

// TestHC_DropClearsAllAndStamps — DropHotCorrelations sweeps every HC
// and SHC record across all tags.
// Refs: R2231
func TestHC_DropClearsAllAndStamps(t *testing.T) {
	s := testStore(t)
	s.WriteHotCorrelation("priority", 1, 0.5)
	s.WriteHotCorrelation("priority", 2, 0.6)
	s.WriteHotCorrelation("status", 3, 0.7)
	if err := s.DropHotCorrelations(); err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"priority", "status"} {
		got, _ := s.ReadHotCorrelations(tag)
		if len(got) != 0 {
			t.Errorf("expected %s empty after drop, got %+v", tag, got)
		}
	}
	// No SHC entries either.
	visited := 0
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, serialKey([]byte(prefixHotCorrelation), nil), func(_ *lmdb.Cursor, _, _ []byte) error {
			visited++
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if visited != 0 {
		t.Errorf("expected no SHC stamps after drop, got %d", visited)
	}
}

// TestHC_DropEmbeddingsCascadesToHC — DropEmbeddings clears HC and SHC
// alongside T/EV/ED and their stamps.
// Refs: R2231
func TestHC_DropEmbeddingsCascadesToHC(t *testing.T) {
	s := testStore(t)
	// Populate enough state that DropEmbeddings has work to do.
	if err := s.UpdateTagDefs(10, map[string]string{"priority": "ranking"}); err != nil {
		t.Fatal(err)
	}
	s.WriteTagDefEmbedding("priority", 10, fakeVec(1))
	s.WriteHotCorrelation("priority", 100, 0.9)
	s.WriteHotCorrelation("priority", 101, 0.85)

	if err := s.DropEmbeddings(); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ReadHotCorrelations("priority")
	if len(got) != 0 {
		t.Errorf("expected HC empty after DropEmbeddings, got %+v", got)
	}
	visited := 0
	err := s.env.View(func(txn *lmdb.Txn) error {
		return scanPrefix(txn, s.dbi, serialKey([]byte(prefixHotCorrelation), nil), func(_ *lmdb.Cursor, _, _ []byte) error {
			visited++
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if visited != 0 {
		t.Errorf("expected no SHC stamps after DropEmbeddings, got %d", visited)
	}
}

// TestHC_TagBoundary — entries for tag "ab" don't bleed into reads for
// tag "a" (or vice versa). Variable-length tag prefix matching is
// length-bounded by chunkID width.
func TestHC_TagBoundary(t *testing.T) {
	s := testStore(t)
	s.WriteHotCorrelation("a", 1, 0.5)
	s.WriteHotCorrelation("ab", 2, 0.6)

	gotA, _ := s.ReadHotCorrelations("a")
	if len(gotA) != 1 || gotA[0].ChunkID != 1 {
		t.Errorf(`expected tag "a" to return one entry chunk=1, got %+v`, gotA)
	}
	gotAB, _ := s.ReadHotCorrelations("ab")
	if len(gotAB) != 1 || gotAB[0].ChunkID != 2 {
		t.Errorf(`expected tag "ab" to return one entry chunk=2, got %+v`, gotAB)
	}
}

// --- Discussed-tag (RD) tests ---
// Test: test-Discussed.md

// TestDiscussed_AddListRoundTrip verifies AddDiscussed writes an RD
// record with the expected key shape and 8-byte unix-nanos value, and
// ListDiscussed reads it back. R2648, R2650, R2651
func TestDiscussed_AddListRoundTrip(t *testing.T) {
	s := testStore(t)
	before := time.Now()
	if err := s.AddDiscussed("sess-1", "topic", "messaging"); err != nil {
		t.Fatal(err)
	}
	after := time.Now()

	got, err := s.ListDiscussed("sess-1", 0, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	if got[0].Tag != "topic" || got[0].Value != "messaging" {
		t.Errorf("unexpected entry: %+v", got[0])
	}
	if got[0].Timestamp.Before(before) || got[0].Timestamp.After(after.Add(time.Second)) {
		t.Errorf("timestamp %v outside [%v, %v]", got[0].Timestamp, before, after)
	}

	// Verify the key layout: "RD" + "sess-1" + \x00 + "topic" + \x00 + "messaging"
	expectedKey := append([]byte("RD"), []byte("sess-1\x00topic\x00messaging")...)
	err = s.env.View(func(txn *lmdb.Txn) error {
		v, getErr := txn.Get(s.dbi, expectedKey)
		if getErr != nil {
			t.Errorf("expected key %q present: %v", expectedKey, getErr)
			return nil
		}
		if len(v) != 8 {
			t.Errorf("expected 8-byte value, got %d", len(v))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestDiscussed_BareTagEncoding verifies a bare @name argument (no
// value) encodes with an empty trailing value segment. R2648
func TestDiscussed_BareTagEncoding(t *testing.T) {
	s := testStore(t)
	if err := s.AddDiscussed("sess-1", "topic", ""); err != nil {
		t.Fatal(err)
	}
	expectedKey := append([]byte("RD"), []byte("sess-1\x00topic\x00")...)
	err := s.env.View(func(txn *lmdb.Txn) error {
		_, getErr := txn.Get(s.dbi, expectedKey)
		if getErr != nil {
			t.Errorf("expected bare-tag key %q present: %v", expectedKey, getErr)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.ListDiscussed("sess-1", 0, 24*time.Hour)
	if len(got) != 1 || got[0].Tag != "topic" || got[0].Value != "" {
		t.Errorf("expected one bare entry, got %+v", got)
	}
}

// TestDiscussed_AddReAddOverwritesTimestamp verifies re-adding the
// same (session, tag, value) updates the timestamp rather than
// creating a duplicate. R2650
func TestDiscussed_AddReAddOverwritesTimestamp(t *testing.T) {
	s := testStore(t)
	if err := s.AddDiscussed("sess-1", "topic", "messaging"); err != nil {
		t.Fatal(err)
	}
	first, _ := s.ListDiscussed("sess-1", 0, 24*time.Hour)
	time.Sleep(2 * time.Millisecond)
	if err := s.AddDiscussed("sess-1", "topic", "messaging"); err != nil {
		t.Fatal(err)
	}
	second, _ := s.ListDiscussed("sess-1", 0, 24*time.Hour)
	if len(second) != 1 {
		t.Fatalf("expected 1 entry after re-add, got %d", len(second))
	}
	if !second[0].Timestamp.After(first[0].Timestamp) {
		t.Errorf("expected timestamp to advance: first=%v second=%v",
			first[0].Timestamp, second[0].Timestamp)
	}
}

// TestDiscussed_ListScope verifies ListDiscussed returns only the
// requested session's entries. R2651
func TestDiscussed_ListScope(t *testing.T) {
	s := testStore(t)
	s.AddDiscussed("sess-A", "topic", "messaging")
	s.AddDiscussed("sess-A", "ext", "tagdefs")
	s.AddDiscussed("sess-B", "topic", "auth")

	a, _ := s.ListDiscussed("sess-A", 0, 24*time.Hour)
	if len(a) != 2 {
		t.Errorf("expected 2 entries for sess-A, got %d", len(a))
	}
	b, _ := s.ListDiscussed("sess-B", 0, 24*time.Hour)
	if len(b) != 1 || b[0].Value != "auth" {
		t.Errorf("expected 1 entry for sess-B (auth), got %+v", b)
	}
}

// TestDiscussed_LazyTTL verifies entries past their TTL are skipped on
// read but not deleted. R2659
func TestDiscussed_LazyTTL(t *testing.T) {
	s := testStore(t)
	// Write an entry with a backdated timestamp by going below the API.
	oldKey := discussedKey("sess-1", "topic", "messaging")
	oldVal := make([]byte, 8)
	binary.BigEndian.PutUint64(oldVal, uint64(time.Now().Add(-25*time.Hour).UnixNano()))
	err := s.env.Update(func(txn *lmdb.Txn) error {
		return txn.Put(s.dbi, oldKey, oldVal, 0)
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := s.ListDiscussed("sess-1", 0, 24*time.Hour)
	if len(got) != 0 {
		t.Errorf("expected 0 entries (TTL expired), got %d", len(got))
	}

	// Raw record still present (lazy expiry only).
	err = s.env.View(func(txn *lmdb.Txn) error {
		_, gerr := txn.Get(s.dbi, oldKey)
		if gerr != nil {
			t.Errorf("expected RD record still present pre-prune, got %v", gerr)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// ttl=0 disables expiry — entry resurfaces.
	got, _ = s.ListDiscussed("sess-1", 0, 0)
	if len(got) != 1 {
		t.Errorf("expected 1 entry with ttl=0, got %d", len(got))
	}
}

// TestDiscussed_SinceFilter verifies --since DUR drops entries older
// than NOW - DUR. R2651
func TestDiscussed_SinceFilter(t *testing.T) {
	s := testStore(t)
	old := discussedKey("sess-1", "topic", "old")
	recent := discussedKey("sess-1", "topic", "recent")
	oldVal := make([]byte, 8)
	binary.BigEndian.PutUint64(oldVal, uint64(time.Now().Add(-2*time.Hour).UnixNano()))
	recentVal := make([]byte, 8)
	binary.BigEndian.PutUint64(recentVal, uint64(time.Now().Add(-10*time.Minute).UnixNano()))
	err := s.env.Update(func(txn *lmdb.Txn) error {
		if e := txn.Put(s.dbi, old, oldVal, 0); e != nil {
			return e
		}
		return txn.Put(s.dbi, recent, recentVal, 0)
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := s.ListDiscussed("sess-1", 30*time.Minute, 24*time.Hour)
	if len(got) != 1 || got[0].Value != "recent" {
		t.Errorf("expected only 'recent', got %+v", got)
	}
}

// TestDiscussed_SkipsMalformedValue verifies RD records whose value
// isn't 8 bytes are treated as expired and skipped on read. R2663
func TestDiscussed_SkipsMalformedValue(t *testing.T) {
	s := testStore(t)
	bad := discussedKey("sess-1", "topic", "messaging")
	err := s.env.Update(func(txn *lmdb.Txn) error {
		return txn.Put(s.dbi, bad, []byte{1, 2, 3, 4}, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.ListDiscussed("sess-1", 0, 24*time.Hour)
	if len(got) != 0 {
		t.Errorf("expected malformed entry skipped, got %+v", got)
	}
}

// TestDiscussed_ClearScope verifies ClearDiscussed removes only the
// requested session's entries. R2652
func TestDiscussed_ClearScope(t *testing.T) {
	s := testStore(t)
	s.AddDiscussed("sess-A", "topic", "messaging")
	s.AddDiscussed("sess-A", "ext", "tagdefs")
	s.AddDiscussed("sess-B", "topic", "auth")

	count, err := s.ClearDiscussed("sess-A")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("expected deleted count 2, got %d", count)
	}
	a, _ := s.ListDiscussed("sess-A", 0, 24*time.Hour)
	if len(a) != 0 {
		t.Errorf("expected sess-A empty after clear, got %+v", a)
	}
	b, _ := s.ListDiscussed("sess-B", 0, 24*time.Hour)
	if len(b) != 1 {
		t.Errorf("expected sess-B intact (1 entry), got %d", len(b))
	}
}

// TestDiscussed_PruneCrossSession verifies prune deletes expired
// entries across all sessions and returns the count. R2653
func TestDiscussed_PruneCrossSession(t *testing.T) {
	s := testStore(t)
	old := time.Now().Add(-25 * time.Hour)
	recent := time.Now().Add(-1 * time.Hour)
	writeStamped := func(session, tag, value string, ts time.Time) {
		k := discussedKey(session, tag, value)
		v := make([]byte, 8)
		binary.BigEndian.PutUint64(v, uint64(ts.UnixNano()))
		s.env.Update(func(txn *lmdb.Txn) error {
			return txn.Put(s.dbi, k, v, 0)
		})
	}
	writeStamped("sess-A", "topic", "old1", old)
	writeStamped("sess-A", "topic", "recent1", recent)
	writeStamped("sess-B", "topic", "old2", old)
	writeStamped("sess-C", "ext", "recent2", recent)

	deleted, err := s.PruneDiscussed(24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Errorf("expected 2 deletions, got %d", deleted)
	}

	a, _ := s.ListDiscussed("sess-A", 0, 0)
	if len(a) != 1 || a[0].Value != "recent1" {
		t.Errorf("expected only recent1 in sess-A, got %+v", a)
	}
	b, _ := s.ListDiscussed("sess-B", 0, 0)
	if len(b) != 0 {
		t.Errorf("expected sess-B empty, got %+v", b)
	}
	c, _ := s.ListDiscussed("sess-C", 0, 0)
	if len(c) != 1 {
		t.Errorf("expected sess-C intact, got %+v", c)
	}
}

// TestDiscussed_PruneZeroTTLNoOp verifies ttl=0 deletes nothing (the
// "never expire" semantic). R2653, R2659
func TestDiscussed_PruneZeroTTLNoOp(t *testing.T) {
	s := testStore(t)
	old := discussedKey("sess-1", "topic", "old")
	oldVal := make([]byte, 8)
	binary.BigEndian.PutUint64(oldVal, uint64(time.Now().Add(-1000*time.Hour).UnixNano()))
	err := s.env.Update(func(txn *lmdb.Txn) error {
		return txn.Put(s.dbi, old, oldVal, 0)
	})
	if err != nil {
		t.Fatal(err)
	}

	deleted, err := s.PruneDiscussed(0)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Errorf("expected zero deletions with ttl=0, got %d", deleted)
	}
	got, _ := s.ListDiscussed("sess-1", 0, 0)
	if len(got) != 1 {
		t.Errorf("expected entry preserved with ttl=0, got %+v", got)
	}
}
