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

func TestSettingsRoundTrip(t *testing.T) {
	s := testStore(t)
	settings := ArkSettings{Dotfiles: true}
	if err := s.PutSettings(settings); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.Dotfiles != true {
		t.Error("expected dotfiles=true")
	}
}

// --- Tag tests (Store-level) ---

func TestUpdateTagsAndListTags(t *testing.T) {
	s := testStore(t)
	if err := s.UpdateTags(1, map[string]uint32{"decision": 2, "pattern": 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateTags(2, map[string]uint32{"decision": 1}); err != nil {
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
	if m["decision"] != 3 {
		t.Errorf("expected decision=3, got %d", m["decision"])
	}
	if m["pattern"] != 1 {
		t.Errorf("expected pattern=1, got %d", m["pattern"])
	}
}

func TestUpdateTagsReplaces(t *testing.T) {
	s := testStore(t)
	if err := s.UpdateTags(1, map[string]uint32{"decision": 2}); err != nil {
		t.Fatal(err)
	}
	// Replace: decision gone, pattern added
	if err := s.UpdateTags(1, map[string]uint32{"pattern": 1}); err != nil {
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
	if _, ok := m["decision"]; ok {
		t.Error("decision should be gone after replace")
	}
	if m["pattern"] != 1 {
		t.Errorf("expected pattern=1, got %d", m["pattern"])
	}
}

func TestRemoveTags(t *testing.T) {
	s := testStore(t)
	s.UpdateTags(1, map[string]uint32{"decision": 2})
	s.UpdateTags(2, map[string]uint32{"decision": 1})

	if err := s.RemoveTags(1); err != nil {
		t.Fatal(err)
	}
	tags, err := s.ListTags()
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(tags))
	}
	if tags[0].Tag != "decision" || tags[0].Count != 1 {
		t.Errorf("expected decision=1, got %s=%d", tags[0].Tag, tags[0].Count)
	}
}

func TestTagFiles(t *testing.T) {
	s := testStore(t)
	s.UpdateTags(1, map[string]uint32{"decision": 2})
	s.UpdateTags(2, map[string]uint32{"decision": 1, "pattern": 3})

	records, err := s.TagFiles([]string{"decision"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	// Records are sorted by fileid (LMDB key order)
	m := make(map[uint64]uint32)
	for _, r := range records {
		m[r.FileID] = r.Count
	}
	if m[1] != 2 {
		t.Errorf("expected fileid=1 count=2, got %d", m[1])
	}
	if m[2] != 1 {
		t.Errorf("expected fileid=2 count=1, got %d", m[2])
	}
}

func TestTagCounts(t *testing.T) {
	s := testStore(t)
	s.UpdateTags(1, map[string]uint32{"decision": 2, "pattern": 1})

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
	if m["decision"] != 2 {
		t.Errorf("expected decision=2, got %d", m["decision"])
	}
	if m["nonexistent"] != 0 {
		t.Errorf("expected nonexistent=0, got %d", m["nonexistent"])
	}
}
