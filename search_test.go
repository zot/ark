package ark

// CRC: crc-Searcher.md | Test: test-Searcher.md, test-ChunkRetrieval.md

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zot/microfts2"
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

// A one-chunk corpus is searchable. The unfloored threshold is
// int(1 × 0.50) = 0, and every real trigram occurs at least once, so
// `count <= 0` discarded all of them and every query returned no candidates —
// silently, and indistinguishably from an empty index. R3401
// CRC: crc-Searcher.md | Seq: seq-search.md | R3401
func TestTrigramFilterKeepsSingleChunkCorpusSearchable(t *testing.T) {
	tg := []microfts2.TrigramCount{{Trigram: 1, Count: 1}}
	kept := filterByRatioFloored(trigramRatio)(tg, 1)
	if len(kept) != 1 {
		t.Errorf("one-chunk corpus kept %d of 1 trigrams, want 1 — every query on it returns nothing", len(kept))
	}
	// The unfloored rule is what the fix replaces; asserting it still
	// discards everything keeps this test honest about what changed.
	if base := microfts2.FilterByRatio(trigramRatio)(tg, 1); len(base) != 0 {
		t.Errorf("microfts2.FilterByRatio kept %d trigrams at totalChunks=1; this test's premise is stale", len(base))
	}
}

// The floor is claimed to be provably scoped to the one-chunk case, because
// int(total × 0.50) is already >= 1 for total >= 2. That claim is load-bearing
// — it is the whole argument for the fix being safe — so it is tested rather
// than merely asserted in prose. Widening the floor (max(2, ...)) turns this
// red, which is the point. R3401
// CRC: crc-Searcher.md | R3401
func TestTrigramFilterFloorIsNoOpAboveOneChunk(t *testing.T) {
	floored := filterByRatioFloored(trigramRatio)
	base := microfts2.FilterByRatio(trigramRatio)
	for total := 2; total <= 200; total++ {
		var tg []microfts2.TrigramCount
		for c := 1; c <= total; c++ {
			tg = append(tg, microfts2.TrigramCount{Trigram: uint32(c), Count: c})
		}
		got, want := floored(tg, total), base(tg, total)
		if len(got) != len(want) {
			t.Fatalf("totalChunks=%d: floored kept %d, FilterByRatio kept %d — the floor is not a no-op above one chunk",
				total, len(got), len(want))
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("totalChunks=%d: trigram %d differs (%+v vs %+v)", total, i, got[i], want[i])
			}
		}
	}
}

// End to end, through a real index: one file, one chunk, and the term is
// found. This is the case a new ark hits on its very first search, where an
// empty answer reads as "the index is broken". R3401
// CRC: crc-Searcher.md | Seq: seq-search.md | R3401
func TestSearchFindsContentInSingleChunkCorpus(t *testing.T) {
	ftsDir := filepath.Join(t.TempDir(), "fts")
	if err := os.MkdirAll(ftsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fts, err := microfts2.Create(IndexPath(ftsDir), microfts2.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fts.Close() })
	if err := fts.AddChunker("line", microfts2.FuncChunker{Fn: microfts2.LineChunkFunc}); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(fts.DB())
	if err != nil {
		t.Fatal(err)
	}

	moon := filepath.Join(t.TempDir(), "moon.md")
	if err := os.WriteFile(moon, []byte("Neil Armstrong walked on the moon\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := &Indexer{fts: fts, store: store}
	if _, err := idx.AddFile(moon, "line"); err != nil {
		t.Fatal(err)
	}

	// Guard the guard: the whole point is that this corpus is exactly one
	// chunk. At two the threshold reaches 1 on its own and the test would
	// pass without the fix.
	info, err := fts.FileInfoByID(1)
	if err != nil || len(info.Chunks) != 1 {
		t.Fatalf("fixture is not a one-chunk corpus: err=%v chunks=%d", err, len(info.Chunks))
	}

	fr, err := fts.Search("Armstrong", defaultSearchOpts(nil, "", SearchOpts{})...)
	if err != nil {
		t.Fatal(err)
	}
	if len(fr.Results) != 1 || fr.Results[0].Path != moon {
		t.Errorf("search Armstrong on a one-chunk corpus found %d results (%v), want %q",
			len(fr.Results), fr.Results, moon)
	}
}
