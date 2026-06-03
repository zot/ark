package ark

import (
	"strings"
	"testing"
)

// TestFullTextKeepsTags is the regression guard for R2913: the trigram
// index is full-text — a chunk's ark tags are indexed verbatim, so a
// search for a literal tag value finds the chunk carrying it. Before the
// content-transform rollback, index-time stripping made an all-@tag chunk
// (which strips to empty) invisible to full-text search; this test fails
// if that stripping ever returns to the index path.
// CRC: crc-Indexer.md | R2913
func TestFullTextKeepsTags(t *testing.T) {
	idx, dir := testIndexer(t)

	// A chunk whose text is nothing but an ark tag — the case that
	// stripped to empty (and vanished from the index) under the old design.
	fp := writeFile(t, dir, "note.txt", "@note: bubba\n")
	if _, err := idx.AddFile(fp, "line"); err != nil {
		t.Fatalf("AddFile: %v", err)
	}

	res, err := idx.fts.Search("bubba")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Results) == 0 {
		t.Fatal("full-text search for a literal tag value 'bubba' found nothing — " +
			"the trigram index dropped the tag text (R2913 regression)")
	}
	found := false
	for _, r := range res.Results {
		if strings.HasSuffix(r.Path, "note.txt") {
			found = true
		}
	}
	if !found {
		t.Fatalf("search for 'bubba' did not return note.txt; got %d result(s)", len(res.Results))
	}
}
