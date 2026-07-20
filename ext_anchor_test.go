package ark

// CRC: crc-ExtMap.md | Test: test-ExtAnchor.md | R2073

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExtTargetAnchorPart — test-ExtAnchor.md "anchor part is whatever
// followed the colon". R2073.
func TestExtTargetAnchorPart(t *testing.T) {
	cases := []struct {
		target string
		want   string
	}{
		{"/a/b.md", ""},                        // bare path
		{"%some-uuid", ""},                     // bare uuid
		{"/a/b.md:3-5", "3-5"},                 // range
		{`/a/b.md:"some text"`, `"some text"`}, // string keeps its quotes
		{"/a/b.md:/re.*x/", "/re.*x/"},         // regex keeps its slashes
		{`/a/b.md[2]:"t"`, `"t"`},              // MODIFIER is base-side
		{`/a/b.md:  "padded"  `, `"padded"`},   // trimmed
	}
	for _, c := range cases {
		if got := extTargetAnchorPart(c.target); got != c.want {
			t.Errorf("extTargetAnchorPart(%q) = %q, want %q", c.target, got, c.want)
		}
	}
}

// extRoutingFixture indexes a target file and a source file whose @ext
// declares extTarget, then returns the routings incoming to the chunk
// matching wantChunkText (or the file's first chunk when it is empty).
func extRoutingFixture(t *testing.T, extTarget, wantChunkText string) []IncomingExtRouting {
	t.Helper()
	_, db := setupFileBackedRecall(t)

	targetPath := filepath.Join(db.dbPath, "target.md")
	if err := os.WriteFile(targetPath, []byte("alpha line\nbravo line\ncharlie line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.indexer.AddFile(targetPath, "line"); err != nil {
		t.Fatalf("index target: %v", err)
	}

	srcPath := filepath.Join(db.dbPath, "notes.md")
	decl := "@ext: " + targetPath + extTarget + " @note: hello\n"
	if err := os.WriteFile(srcPath, []byte(decl), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.indexer.AddFile(srcPath, "line"); err != nil {
		t.Fatalf("index source: %v", err)
	}
	if err := db.extmap.Rebuild(db); err != nil {
		t.Fatalf("ExtMap.Rebuild: %v", err)
	}

	status, err := db.fts.CheckFile(targetPath)
	if err != nil || status.FileID == 0 {
		t.Fatalf("target not indexed: %v", err)
	}
	info, err := db.fts.FileInfoByID(status.FileID)
	if err != nil || len(info.Chunks) == 0 {
		t.Fatalf("target has no chunks: %v", err)
	}

	cid := info.Chunks[0].ChunkID
	if wantChunkText != "" {
		all := db.AllChunks(targetPath)
		found := false
		for i, c := range all {
			if i < len(info.Chunks) && strings.Contains(c.Content, wantChunkText) {
				cid, found = info.Chunks[i].ChunkID, true
				break
			}
		}
		if !found {
			t.Fatalf("no chunk containing %q", wantChunkText)
		}
	}
	return db.extmap.ExtRoutingsForTargetChunk(cid, db)
}

// TestExtRoutingCarriesAnchor — test-ExtAnchor.md "a resolved anchored
// routing carries its anchor". R2073, R2376, R2377.
func TestExtRoutingCarriesAnchor(t *testing.T) {
	routings := extRoutingFixture(t, `:"bravo"`, "bravo")

	if len(routings) != 1 {
		t.Fatalf("got %d routings, want 1: %+v", len(routings), routings)
	}
	r := routings[0]
	if r.TargetAnchor != `"bravo"` {
		t.Errorf("TargetAnchor = %q, want %q (delimiters kept)", r.TargetAnchor, `"bravo"`)
	}
	if len(r.Routed) != 1 || r.Routed[0].Tag != "note" {
		t.Errorf("Routed = %+v, want one @note", r.Routed)
	}
}

// TestExtRoutingBareTargetHasNoAnchor — test-ExtAnchor.md "a bare target
// carries no anchor". R2073, R2377.
func TestExtRoutingBareTargetHasNoAnchor(t *testing.T) {
	routings := extRoutingFixture(t, "", "")

	if len(routings) != 1 {
		t.Fatalf("got %d routings, want 1: %+v", len(routings), routings)
	}
	if routings[0].TargetAnchor != "" {
		t.Errorf("TargetAnchor = %q, want empty for a bare target", routings[0].TargetAnchor)
	}
}
