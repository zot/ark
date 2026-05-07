package ark

// CRC: crc-Store.md | Test: test-Store.md, test-Tags.md

import (
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
