package ark

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestSearchTagChunksPostFilterFunnel is the Sleeping-Sentry guard for R2951:
// the post-filter stack and the default search_exclude scope apply to an
// index-lookup primary (-tag/-file-tag) exactly as they do to a content-scan
// primary. Before the funnel, SearchTagChunks returned its tag-derived chunk
// set directly and silently dropped every post-filter and the default scope
// (F2). This test marches the exact bypass cases past the guard:
//
//  1. -tag guard -files '**/keep.md'  restricts to keep.md.
//  2. -tag guard -contains needle     is a strict subset (only keep.md).
//  3. -tag guard                      excludes search_exclude paths by default.
//
// If any assertion sees the unfiltered tag set again, the funnel has been
// bypassed — the sentry is asleep.
// CRC: crc-Searcher.md | Seq: seq-search.md | R2951
func TestSearchTagChunksPostFilterFunnel(t *testing.T) {
	idx, dir := testIndexer(t)

	// Three single-line ("line" chunker → one chunk per file) files, each
	// carrying @guard, so the tag primary selects all three. Only keep.md
	// matches the -files glob and carries the word "needle"; excluded.md is
	// the default-scope victim.
	for name, body := range map[string]string{
		"keep.md":     "@guard: yes needle here\n",
		"other.md":    "@guard: yes plain body\n",
		"excluded.md": "@guard: yes some body\n",
	} {
		fp := writeFile(t, dir, name, body)
		if _, err := idx.AddFile(fp, "line"); err != nil {
			t.Fatalf("AddFile %s: %v", name, err)
		}
	}
	idx.store.LoadTvidMap()

	cfg := &Config{SearchExclude: &[]string{"**/excluded.md"}}
	s := &Searcher{fts: idx.fts, store: idx.store, config: cfg}

	// Tag-primary candidate set: every chunk carrying @guard.
	p, err := ParseMatchSyntax("guard")
	if err != nil {
		t.Fatalf("ParseMatchSyntax: %v", err)
	}
	chunkSet, _ := resolvePredicateLocations(p, idx.store)
	var chunkIDs []uint64
	for cid := range chunkSet {
		chunkIDs = append(chunkIDs, cid)
	}
	if len(chunkIDs) != 3 {
		t.Fatalf("setup: expected 3 @guard chunks, got %d", len(chunkIDs))
	}

	bases := func(results []SearchResultEntry) []string {
		var out []string
		for _, r := range results {
			out = append(out, filepath.Base(r.Path))
		}
		sort.Strings(out)
		return out
	}
	eq := func(got, want []string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	// 1. -files restricts the tag set to the matching path.
	r1, err := s.SearchTagChunks(chunkIDs, nil, SearchOpts{
		K:            100,
		ChunkFilters: []ChunkFilterRow{{Polarity: "with", Mode: "files", Query: "**/keep.md"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := bases(r1); !eq(got, []string{"keep.md"}) {
		t.Errorf("R2951 -files bypass: -tag guard -files '**/keep.md' = %v, want [keep.md] "+
			"(the post-filter stack is not applied to the tag primary)", got)
	}

	// 2. -contains restricts to the chunk carrying the term — a strict subset.
	r2, err := s.SearchTagChunks(chunkIDs, nil, SearchOpts{
		K:            100,
		ChunkFilters: []ChunkFilterRow{{Polarity: "with", Mode: "contains", Query: "needle"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := bases(r2); !eq(got, []string{"keep.md"}) {
		t.Errorf("R2951 -contains bypass: -tag guard -contains needle = %v, want [keep.md]", got)
	}

	// 3. The default search_exclude scope applies with no explicit file filter.
	r3, err := s.SearchTagChunks(chunkIDs, nil, SearchOpts{K: 100})
	if err != nil {
		t.Fatal(err)
	}
	if got := bases(r3); !eq(got, []string{"keep.md", "other.md"}) {
		t.Errorf("R2951 default-scope bypass: -tag guard = %v, want [keep.md other.md] "+
			"(search_exclude '**/excluded.md' not applied to the tag primary)", got)
	}
}

// fenceFixture indexes one @guard chunk in each of fenced/, noise/, other/
// and open/, with system_exclude naming fenced/ and search_exclude naming
// noise/. It returns the searcher, every chunk ID, and the ID per directory.
// CRC: crc-Searcher.md | R3584, R3587, R3590
func fenceFixture(t *testing.T) (*Searcher, []uint64, map[string]uint64) {
	t.Helper()
	idx, dir := testIndexer(t)
	byDir := make(map[string]uint64)
	for _, d := range []string{"fenced", "noise", "other", "open"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
		fp := writeFile(t, dir, filepath.Join(d, "f.md"), "@guard: yes body of "+d+"\n") // distinct text: identical chunks dedup to one
		if _, err := idx.AddFile(fp, "line"); err != nil {
			t.Fatalf("AddFile %s: %v", fp, err)
		}
	}
	idx.store.LoadTvidMap()
	cfg := &Config{
		SystemExclude: &[]string{"**/fenced/**"},
		SearchExclude: &[]string{"**/noise/**"},
	}
	s := &Searcher{fts: idx.fts, store: idx.store, config: cfg}
	p, err := ParseMatchSyntax("guard")
	if err != nil {
		t.Fatal(err)
	}
	set, _ := resolvePredicateLocations(p, idx.store)
	var ids []uint64
	for cid := range set {
		ids = append(ids, cid)
	}
	entries, err := s.ChunksByID(ids)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		byDir[filepath.Base(filepath.Dir(e.Path))] = e.ChunkNum
	}
	if len(byDir) != 4 {
		t.Fatalf("setup: expected one chunk per dir, got %v", byDir)
	}
	return s, ids, byDir
}

func dirsOf(results []SearchResultEntry) []string {
	var out []string
	for _, r := range results {
		out = append(out, filepath.Base(filepath.Dir(r.Path)))
	}
	sort.Strings(out)
	return out
}

// TestFenceHoldsAgainstEveryFileFilter: no -files row and no structural
// filter lifts the fence, while (b) and (c) still disable the noise filter.
// Refs: R3584, R940
func TestFenceHoldsAgainstEveryFileFilter(t *testing.T) {
	s, ids, _ := fenceFixture(t)
	cases := []struct {
		name string
		opts SearchOpts
		want []string
	}{
		{"no filters", SearchOpts{K: 100}, []string{"open", "other"}},
		{"positive -files into the fence", SearchOpts{K: 100, ChunkFilters: []ChunkFilterRow{{Mode: "files", Query: "**/fenced/**", Polarity: "with"}}}, nil},
		{"structural FilterFiles", SearchOpts{K: 100, FilterFiles: []string{"**/*.md"}}, []string{"noise", "open", "other"}},
		{"structural ExcludeFiles", SearchOpts{K: 100, ExcludeFiles: []string{"**/other/**"}}, []string{"noise", "open"}},
	}
	for _, c := range cases {
		got, err := s.SearchTagChunks(ids, nil, c.opts)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		dirs := dirsOf(got)
		if strings.Join(dirs, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: got %v, want %v (a fenced/ hit means a file filter lifted the fence)", c.name, dirs, c.want)
		}
	}
}

// TestSystemLiftsOnlyTheFence: -system reaches fenced files and leaves the
// noise filter in force. Refs: R3590
func TestSystemLiftsOnlyTheFence(t *testing.T) {
	s, ids, _ := fenceFixture(t)
	got, err := s.SearchTagChunks(ids, nil, SearchOpts{K: 100, System: true})
	if err != nil {
		t.Fatal(err)
	}
	if dirs := strings.Join(dirsOf(got), ","); dirs != "fenced,open,other" {
		t.Errorf("-system: got %s, want fenced,open,other (noise must stay hidden)", dirs)
	}
}

// TestRoutedTagPassesNoiseNotFence: a routed candidate in noise/ survives,
// one in fenced/ does not, and an unrouted noise/ candidate stays hidden.
// Refs: R3587
func TestRoutedTagPassesNoiseNotFence(t *testing.T) {
	s, ids, byDir := fenceFixture(t)
	routed := map[uint64]bool{byDir["noise"]: true, byDir["fenced"]: true}
	got, err := s.SearchTagChunks(ids, routed, SearchOpts{K: 100})
	if err != nil {
		t.Fatal(err)
	}
	if dirs := strings.Join(dirsOf(got), ","); dirs != "noise,open,other" {
		t.Errorf("routed: got %s, want noise,open,other", dirs)
	}
	got, err = s.SearchTagChunks(ids, map[uint64]bool{byDir["open"]: true}, SearchOpts{K: 100})
	if err != nil {
		t.Fatal(err)
	}
	if dirs := strings.Join(dirsOf(got), ","); dirs != "open,other" {
		t.Errorf("unrouted noise: got %s, want open,other", dirs)
	}
}

// TestRoutedPrimaryChunksMatchesOnlyRoutedValues: the exempt set is routed
// values the predicate accepts, never an inline match or another tag.
// Refs: R3587
func TestRoutedPrimaryChunksMatchesOnlyRoutedValues(t *testing.T) {
	const a, b, c = 11, 12, 13
	em := &ExtMap{
		chunkToTargets: map[uint64][]uint64{a: {1}, b: {2}},
		routedTagsByTvidExt: map[uint64][]TagValue{
			1: {{Tag: "status", Value: "done"}},
			2: {{Tag: "owner", Value: "bill"}},
		},
	}
	store := &Store{extmap: em}
	for _, q := range []struct {
		pred string
		want []uint64
	}{
		{"status:done", []uint64{a}},
		{"status:open", nil},
		{"status", []uint64{a}},
	} {
		p, err := ParseMatchSyntax(q.pred)
		if err != nil {
			t.Fatal(err)
		}
		got := routedPrimaryChunks(p, store, []uint64{a, b, c})
		var ids []uint64
		for id := range got {
			ids = append(ids, id)
		}
		if len(ids) != len(q.want) || (len(ids) == 1 && ids[0] != q.want[0]) {
			t.Errorf("%s: got %v, want %v", q.pred, ids, q.want)
		}
	}
}
