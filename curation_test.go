package ark

// CRC: crc-Curation.md | Test: test-Curation.md
// R2381, R2382, R2383, R2384, R2385

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func captureCurationLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prevOut := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})
	return &buf
}

// CRC: crc-Curation.md | R2383
func TestCurationLoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	logs := captureCurationLog(t)
	c := newCuration(dir)
	c.Load()
	if got := c.pinnedSnapshot(); len(got) != 0 {
		t.Fatalf("pinned should be empty after missing-file Load, got %d entries", len(got))
	}
	if logs.Len() != 0 {
		t.Fatalf("missing file should be silent, got log: %q", logs.String())
	}
}

// CRC: crc-Curation.md | R2382, R2384
func TestCurationSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	a := newCuration(dir)
	a.pinned = []PinnedChunk{
		{ChunkID: 11, FileID: 100, Path: "/a.md", PinnedAt: 1000},
		{ChunkID: 22, FileID: 200, Path: "/b.md", PinnedAt: 2000},
		{ChunkID: 33, FileID: 300, Path: "/c.md", PinnedAt: 3000},
	}
	a.save()

	b := newCuration(dir)
	b.Load()
	if !reflect.DeepEqual(a.pinned, b.pinnedSnapshot()) {
		t.Fatalf("round-trip mismatch:\nwrote: %#v\nread:  %#v", a.pinned, b.pinnedSnapshot())
	}
}

// CRC: crc-Curation.md | R2383, R2385
func TestCurationLoadMalformedTOML(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "curation.toml"), []byte("not toml at all === ==="), 0o644); err != nil {
		t.Fatal(err)
	}
	logs := captureCurationLog(t)
	c := newCuration(dir)
	c.Load()
	if got := c.pinnedSnapshot(); len(got) != 0 {
		t.Fatalf("malformed file should leave pinned empty, got %d entries", len(got))
	}
	if logs.Len() == 0 {
		t.Fatalf("malformed file should log a parse error")
	}
}

// CRC: crc-Curation.md | R2383
func TestCurationLoadUnknownVersion(t *testing.T) {
	dir := t.TempDir()
	body := "version = 99\n\n[[pinned]]\n  chunkID = 1\n  fileID = 1\n  path = \"/x.md\"\n  pinnedAt = 1\n"
	if err := os.WriteFile(filepath.Join(dir, "curation.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	logs := captureCurationLog(t)
	c := newCuration(dir)
	c.Load()
	if got := c.pinnedSnapshot(); len(got) != 0 {
		t.Fatalf("unknown version should leave pinned empty, got %d entries", len(got))
	}
	if logs.Len() == 0 {
		t.Fatalf("unknown version should log")
	}
}

// CRC: crc-Curation.md | R2384
func TestCurationSaveAtomicNoTmpLeftover(t *testing.T) {
	dir := t.TempDir()
	c := newCuration(dir)
	c.pinned = []PinnedChunk{{ChunkID: 7, FileID: 70, Path: "/x.md", PinnedAt: 700}}
	c.save()

	if _, err := os.Stat(filepath.Join(dir, "curation.toml")); err != nil {
		t.Fatalf("curation.toml should exist after save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "curation.toml.tmp")); !os.IsNotExist(err) {
		t.Fatalf("curation.toml.tmp should not exist after atomic save, err: %v", err)
	}
}

// CRC: crc-Curation.md | R2381
func TestCurationEmptyDbPathDisablesPersistence(t *testing.T) {
	c := newCuration("")
	c.pinned = []PinnedChunk{{ChunkID: 1}}
	logs := captureCurationLog(t)
	c.save()
	c.Load()
	if got := c.pinnedSnapshot(); len(got) != 1 || got[0].ChunkID != 1 {
		t.Fatalf("in-memory state should be untouched by Load/save with empty dbPath, got %#v", got)
	}
	if logs.Len() != 0 {
		t.Fatalf("empty dbPath should be silent: %q", logs.String())
	}
}
