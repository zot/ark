package ark

// Test: crc-Scanner.md | Seq: seq-empty-file-skip.md | R1644, R1645, R1646, R1647, R1648, R1650, R1651

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zot/microfts2"
)

// --- EmptyFiles unit tests ---

func TestEmptyFilesHasRecord(t *testing.T) {
	ef := NewEmptyFiles()
	now := time.Now()

	if ef.Has("/foo", now) {
		t.Error("unseen path should not be Has")
	}
	ef.Record("/foo", now)
	if !ef.Has("/foo", now) {
		t.Error("recorded path with same mtime should be Has")
	}
}

func TestEmptyFilesMtimeMismatch(t *testing.T) {
	ef := NewEmptyFiles()
	t1 := time.Now()
	t2 := t1.Add(time.Second)

	ef.Record("/foo", t1)
	if ef.Has("/foo", t2) {
		t.Error("different mtime should miss")
	}
	ef.Record("/foo", t2)
	if !ef.Has("/foo", t2) {
		t.Error("after re-record with new mtime, should Has")
	}
	if ef.Has("/foo", t1) {
		t.Error("old mtime should no longer Has after re-record")
	}
}

func TestEmptyFilesForget(t *testing.T) {
	ef := NewEmptyFiles()
	now := time.Now()

	ef.Record("/foo", now)
	ef.Forget("/foo")
	if ef.Has("/foo", now) {
		t.Error("after Forget, path should not be Has")
	}
}

// --- Scanner integration tests ---

// testScanner builds a Scanner with a real microfts2 DB over a tmp dir.
// The dir itself is registered as the sole source.
func testScanner(t *testing.T) (*Scanner, string, *EmptyFiles) {
	t.Helper()
	dir := t.TempDir()

	// Build Config programmatically (no ark.toml needed)
	cfg := &Config{
		Sources:        []Source{{Dir: dir}},
		DefaultInclude: []string{"*.txt", "*.pdf"},
		Dotfiles:       false,
	}
	matcher := &Matcher{}

	dbPath := filepath.Join(t.TempDir(), "db")
	fts, err := microfts2.Create(dbPath, microfts2.Options{MaxDBs: 8})
	if err != nil {
		t.Fatal(err)
	}
	fts.AddChunker("line", microfts2.FuncChunker{Fn: microfts2.LineChunkFunc})
	t.Cleanup(func() { fts.Close() })

	ef := NewEmptyFiles()
	sc := &Scanner{config: cfg, matcher: matcher, fts: fts, emptyFiles: ef}
	return sc, dir, ef
}

// R1647: a zero-byte file is reported in ScanResults.EmptyFiles.
func TestScannerDetectsEmptyFile(t *testing.T) {
	sc, dir, _ := testScanner(t)

	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, nil, 0644); err != nil {
		t.Fatal(err)
	}

	results, err := sc.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(results.EmptyFiles) != 1 || results.EmptyFiles[0] != empty {
		t.Errorf("expected EmptyFiles=[%s], got %v", empty, results.EmptyFiles)
	}
	if len(results.NewFiles) != 0 {
		t.Errorf("empty file should not appear in NewFiles, got %v", results.NewFiles)
	}
}

// R1646: an empty file already in the set with the current mtime is not re-reported.
func TestScannerSkipsKnownEmpty(t *testing.T) {
	sc, dir, _ := testScanner(t)

	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, nil, 0644); err != nil {
		t.Fatal(err)
	}

	first, err := sc.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(first.EmptyFiles) != 1 {
		t.Fatalf("first scan: expected 1 empty, got %v", first.EmptyFiles)
	}

	second, err := sc.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(second.EmptyFiles) != 0 {
		t.Errorf("second scan should skip known-empty, got %v", second.EmptyFiles)
	}
}

// R1647: an mtime change re-reports the empty file.
func TestScannerReReportsOnMtimeChange(t *testing.T) {
	sc, dir, _ := testScanner(t)

	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.Scan(); err != nil {
		t.Fatal(err)
	}

	// Advance mtime by touching the file with an explicit later time.
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(empty, later, later); err != nil {
		t.Fatal(err)
	}

	results, err := sc.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(results.EmptyFiles) != 1 {
		t.Errorf("after mtime change, expected 1 empty, got %v", results.EmptyFiles)
	}
}

// R1649: non-empty files bypass the set and go through normal NewFiles flow.
func TestScannerIgnoresNonEmptyFiles(t *testing.T) {
	sc, dir, ef := testScanner(t)

	nonEmpty := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(nonEmpty, []byte("content\n"), 0644); err != nil {
		t.Fatal(err)
	}

	results, err := sc.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(results.EmptyFiles) != 0 {
		t.Errorf("non-empty file should not appear in EmptyFiles, got %v", results.EmptyFiles)
	}
	if len(results.NewFiles) != 1 || results.NewFiles[0].Path != nonEmpty {
		t.Errorf("expected NewFiles=[%s], got %v", nonEmpty, results.NewFiles)
	}
	info, _ := os.Stat(nonEmpty)
	if ef.Has(nonEmpty, info.ModTime()) {
		t.Error("non-empty file should not be recorded in the set")
	}
}

// Forgetting: a file that was empty, then gains content, is removed from the set
// so a future truncation is re-detected.
func TestScannerForgetsWhenFileGainsContent(t *testing.T) {
	sc, dir, ef := testScanner(t)

	p := filepath.Join(dir, "doc.txt")
	if err := os.WriteFile(p, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.Scan(); err != nil {
		t.Fatal(err)
	}

	// Give the file content; mtime changes naturally.
	later := time.Now().Add(2 * time.Second)
	if err := os.WriteFile(p, []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}

	results, err := sc.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(results.NewFiles) != 1 {
		t.Errorf("expected one NewFile after content added, got %v", results.NewFiles)
	}
	info, _ := os.Stat(p)
	if ef.Has(p, info.ModTime()) {
		t.Error("file with content should be Forgotten from empty-set")
	}

	// Truncate back to zero; it should be re-detected as empty.
	later2 := later.Add(2 * time.Second)
	if err := os.WriteFile(p, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, later2, later2); err != nil {
		t.Fatal(err)
	}

	results, err = sc.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(results.EmptyFiles) != 1 || results.EmptyFiles[0] != p {
		t.Errorf("after truncation, expected EmptyFiles=[%s], got %v", p, results.EmptyFiles)
	}
}
