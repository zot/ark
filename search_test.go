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

// A one-chunk corpus is searchable — the property ark requires (R3401),
// asserted against the dependency that now delivers it.
//
// This is a `sleeping-sentry`: microfts2.FilterByRatio abstains below two
// chunks by its own contract (its R674), and ark neither owns nor pins that
// code. A permissive dependency returning a wrong-but-valid answer is a guard
// asleep at the gate, so the check keeps stomping past the post. It has
// already woken once — it went red the moment microfts2 fixed this upstream,
// which is how ark learned the fix had landed at all.
//
// The failure it guards is total and silent: every trigram of every query
// discarded means no candidates rather than no matches, and a lone note naming
// Neil Armstrong simply cannot be found by searching for Armstrong.
// CRC: crc-Searcher.md | Seq: seq-search.md | R3401
func TestTrigramFilterKeepsSingleChunkCorpusSearchable(t *testing.T) {
	tg := []microfts2.TrigramCount{{Trigram: 1, Count: 1}}
	// minCount 0 deliberately: with a live cost clause this would pass even if
	// the below-two-chunks guard were gone, and the guard is what is under test.
	kept := microfts2.FilterByRatio(trigramRatio, 0)(tg, 1)
	if len(kept) != 1 {
		t.Errorf("one-chunk corpus kept %d of 1 trigrams, want 1 — every query on it returns nothing, "+
			"which reads as an empty index rather than an empty result (R3401)", len(kept))
	}
}

// The filter still DISCRIMINATES at low ratios — the other half of the sentry,
// and the half ark got wrong.
//
// Ark briefly shipped its own copy of the filter with the threshold floored at
// one, arguing the floor was provably scoped to the one-chunk case because
// int(total × 0.50) >= 1 for total >= 2. The proof holds only at ratio 0.50,
// and the function took the ratio as a parameter: the floor actually fires
// whenever ratio < 1/totalChunks, so at 50 chunks and ratio 0.0 it kept
// trigrams the contract says to skip. Refuted by measurement in microfts2's
// response (2026-08-05), not by anything ark ran.
//
// Ark's own guard could not have caught it: it swept totalChunks 2..200 with
// the ratio pinned at 0.50 — varying the axis where the claim was safe while
// holding fixed the one where it broke. That is `leading-the-witness` in a
// test rather than in a grep, and it is why this case pins the ratio low
// instead of sweeping the corpus size.
// CRC: crc-Searcher.md | R3401
func TestTrigramFilterDiscriminatesAtLowRatios(t *testing.T) {
	tg := []microfts2.TrigramCount{{Trigram: 1, Count: 1}}
	for _, ratio := range []float64{0.0, 0.01} {
		if kept := microfts2.FilterByRatio(ratio, 0)(tg, 50); len(kept) != 0 {
			t.Errorf("FilterByRatio(%v) at totalChunks=50 kept a Count=1 trigram (2%% of the corpus); "+
				"the contract says skip, and keeping it is the defect ark's own floor introduced", ratio)
		}
	}
}

// A trigram cheap enough to scan is kept whatever its share of the corpus
// (R3402). This is the case that motivated the cost clause: on a 10-chunk
// corpus a trigram in 6 of them is over the 50% bar, and skipping it saves a
// 6-posting scan while potentially costing the whole answer.
//
// A `sleeping-sentry` on the same dependency as its neighbours — ark supplies
// the number, microfts2 owns the rule, and ark pins neither.
//
// The control matters: with minCount 0 the same trigram MUST be skipped, or
// this proves only that something returned a non-empty slice. The pair is what
// distinguishes the cost clause from the clause simply not running.
// CRC: crc-Searcher.md | Seq: seq-search.md | R3402
func TestTrigramFilterKeepsCheapPostings(t *testing.T) {
	tg := []microfts2.TrigramCount{{Trigram: 1, Count: 6}}

	if kept := microfts2.FilterByRatio(trigramRatio, trigramMinCount)(tg, 10); len(kept) != 1 {
		t.Errorf("a 6-posting trigram on a 10-chunk corpus was skipped (kept %d of 1); "+
			"skipping it saves a 6-posting scan and can cost the entire answer (R3402)", len(kept))
	}

	// Control: without the cost clause the ratio alone still discards it.
	if kept := microfts2.FilterByRatio(trigramRatio, 0)(tg, 10); len(kept) != 0 {
		t.Errorf("control: with minCount 0 the ratio must still skip 6 of 10 chunks, but kept %d — "+
			"the assertion above then proves nothing about the cost clause", len(kept))
	}
}

// The cost clause is inert where it should be: above minCount/ratio chunks it
// cannot change a verdict, which is the bound R3402 rests on and the reason
// 1,000 is chosen rather than profiled.
//
// Deliberately swept across counts on BOTH sides of the threshold. Ark's
// retired R3401 guard swept only the axis where its claim was safe, and that
// is exactly why it never rang; a single count above the threshold would pass
// here even if the clause were applied in the wrong direction.
// CRC: crc-Searcher.md | R3402
func TestTrigramCostClauseInertAboveItsReach(t *testing.T) {
	const total = 4000 // > trigramMinCount/trigramRatio (2000)
	for _, count := range []int{1, 500, 1999, 2001, 3000, 3999} {
		tg := []microfts2.TrigramCount{{Trigram: 1, Count: count}}
		withClause := microfts2.FilterByRatio(trigramRatio, trigramMinCount)(tg, total)
		pureRatio := microfts2.FilterByRatio(trigramRatio, 0)(tg, total)
		if len(withClause) != len(pureRatio) {
			t.Errorf("count=%d at totalChunks=%d: cost clause kept %d, pure ratio kept %d — "+
				"the clause must be inert above minCount/ratio (R3402)",
				count, total, len(withClause), len(pureRatio))
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
