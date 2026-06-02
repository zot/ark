package ark

// CRC: crc-Searcher.md | Test: test-Searcher.md, test-ChunkRetrieval.md

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateSearchFlagsContainsAndRegexCompose(t *testing.T) {
	err := validateSearchFlags(SearchOpts{Contains: "foo", Regex: []string{"bar"}})
	if err != nil {
		t.Errorf("--contains and --regex should compose, got error: %v", err)
	}
}

func TestValidateSearchFlagsAcceptsContainsAlone(t *testing.T) {
	err := validateSearchFlags(SearchOpts{Contains: "foo"})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateSearchFlagsAcceptsRegexAlone(t *testing.T) {
	err := validateSearchFlags(SearchOpts{Regex: []string{"foo"}})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExtractByRange(t *testing.T) {
	lines := []string{"chunk0", "chunk1", "chunk2", ""}

	// Line 1
	got := extractByRange(lines, "1-1")
	if got != "chunk0\n" {
		t.Errorf("range 1-1: expected %q, got %q", "chunk0\n", got)
	}

	// Lines 2-3
	got = extractByRange(lines, "2-3")
	if got != "chunk1\nchunk2\n" {
		t.Errorf("range 2-3: expected %q, got %q", "chunk1\nchunk2\n", got)
	}

	// Invalid range
	got = extractByRange(lines, "bad")
	if got != "" {
		t.Errorf("bad range: expected empty, got %q", got)
	}
}

func TestParseRange(t *testing.T) {
	s, e := parseRange("5-10")
	if s != 5 || e != 10 {
		t.Errorf("expected 5,10 got %d,%d", s, e)
	}
	s, e = parseRange("bad")
	if s != 0 || e != 0 {
		t.Errorf("expected 0,0 for bad range, got %d,%d", s, e)
	}
}

func TestChunkNumForRange(t *testing.T) {
	// chunkNumForRange uses Chunks from microfts2.FRecord
	// The merge/intersect logic requires live microfts2, tested via
	// integration tests.
}

// TestMatchFilesGlob_ExpandsTilde guards R950 for the filter-stack
// `-files` matcher: a leading `~/` must expand so `'~/.claude/projects/**'`
// matches, identically to the absolute form. (The bug it regress-guards:
// the tilde reached the matcher literally and matched nothing.)
func TestMatchFilesGlob_ExpandsTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	paths := map[uint64]string{
		1: filepath.Join(home, ".claude/projects/abc/session.jsonl"),
		2: "/home/other/work/notes.md",
		3: filepath.Join(home, ".ark/schedule/log.jsonl"),
	}

	got := matchFilesGlob("~/.claude/projects/**", paths)
	if !got[1] || got[2] || got[3] {
		t.Errorf("tilde glob: got %v, want only fileID 1", got)
	}

	// Absolute equivalent matches the same single file.
	abs := matchFilesGlob(filepath.Join(home, ".claude/projects/**"), paths)
	if !abs[1] || len(abs) != 1 {
		t.Errorf("absolute glob: got %v, want only fileID 1", abs)
	}

	// Basename glob (no tilde) still matches on filepath.Base.
	base := matchFilesGlob("*.md", paths)
	if !base[2] || len(base) != 1 {
		t.Errorf("basename glob: got %v, want only fileID 2", base)
	}
}
