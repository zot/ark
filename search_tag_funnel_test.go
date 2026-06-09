package ark

import (
	"path/filepath"
	"sort"
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

	cfg := &Config{SearchExclude: []string{"**/excluded.md"}}
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
	r1, err := s.SearchTagChunks(chunkIDs, SearchOpts{
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
	r2, err := s.SearchTagChunks(chunkIDs, SearchOpts{
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
	r3, err := s.SearchTagChunks(chunkIDs, SearchOpts{K: 100})
	if err != nil {
		t.Fatal(err)
	}
	if got := bases(r3); !eq(got, []string{"keep.md", "other.md"}) {
		t.Errorf("R2951 default-scope bypass: -tag guard = %v, want [keep.md other.md] "+
			"(search_exclude '**/excluded.md' not applied to the tag primary)", got)
	}
}
