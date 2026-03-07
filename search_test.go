package ark

// CRC: crc-Searcher.md | Test: test-Searcher.md, test-ChunkRetrieval.md

import "testing"

func TestValidateSearchFlagsContainsAndRegexMutuallyExclusive(t *testing.T) {
	err := validateSearchFlags(SearchOpts{Contains: "foo", Regex: "bar"})
	if err == nil {
		t.Error("expected error for both --contains and --regex")
	}
}

func TestValidateSearchFlagsAcceptsContainsAlone(t *testing.T) {
	err := validateSearchFlags(SearchOpts{Contains: "foo"})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateSearchFlagsAcceptsRegexAlone(t *testing.T) {
	err := validateSearchFlags(SearchOpts{Regex: "foo"})
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
	// chunkNumForRange uses ChunkRanges from microfts2.FileInfo
	// The merge/intersect logic requires live microfts2, tested via
	// integration tests.
}
