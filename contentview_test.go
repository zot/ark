package ark

// CRC: crc-Server.md | Seq: seq-content-fetching.md | Test: test-ContentView.md

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// addIndexed writes a file into the test source dir and indexes it with the
// given strategy, returning its path. Companion to indexLine (which is
// single-line and line-strategy only) for the multi-chunk render cases.
func addIndexed(t *testing.T, db *DB, name, content, strategy string) string {
	t.Helper()
	fp := filepath.Join(db.dbPath, name)
	if err := os.WriteFile(fp, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.indexer.AddFile(fp, strategy); err != nil {
		t.Fatalf("AddFile %s (%s): %v", name, strategy, err)
	}
	return fp
}

// getContentView drives the real handler through httptest and returns the
// body. The stand-in templates are `{{.Content}}`, so the body is exactly
// what the render assembled.
func getContentView(t *testing.T, srv *Server, path, query string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/content"+path+query, nil)
	rec := httptest.NewRecorder()
	srv.handleContentView(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /content%s%s → %d: %s", path, query, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// chunkDivCount counts emitted <div class="ark-chunk"> wrappers.
func chunkDivCount(html string) int {
	return strings.Count(html, `<div class="ark-chunk"`)
}

const mdSample = "# Heading\n\nfirst paragraph\n\n## Second\n\nsecond paragraph\n"

// TestContentView_MarkdownFullFile — test-ContentView.md "markdown full-file
// renders one chunk div per chunk". R1160-R1164, R2415, R2065.
func TestContentView_MarkdownFullFile(t *testing.T) {
	srv, db, _ := setupContentView(t)
	path := addIndexed(t, db, "doc.md", mdSample, "line")

	chunks := db.AllChunks(path)
	if len(chunks) < 2 {
		t.Fatalf("want a multi-chunk fixture, got %d chunks", len(chunks))
	}

	html := getContentView(t, srv, path, "")

	if got := chunkDivCount(html); got != len(chunks) {
		t.Errorf("chunk divs = %d, want %d (one per chunk)", got, len(chunks))
	}
	if !strings.Contains(html, "<h1") {
		t.Error("markdown was not rendered through goldmark (no <h1>)")
	}
	if strings.Contains(html, `data-chunkid=""`) {
		t.Error("a chunk div has an empty data-chunkid; the pin button needs it")
	}
	if strings.Contains(html, `data-fileid=""`) {
		t.Error("a chunk div has an empty data-fileid; the pin button needs it")
	}
	for _, ch := range chunks {
		if !strings.Contains(html, `data-range="`+ch.Range+`"`) {
			t.Errorf("no chunk div for range %q", ch.Range)
		}
	}
}

// TestContentView_MarkdownSingleChunk — test-ContentView.md "markdown single
// chunk honors ?range=". R1423-R1425, R2415.
func TestContentView_MarkdownSingleChunk(t *testing.T) {
	srv, db, _ := setupContentView(t)
	path := addIndexed(t, db, "doc.md", mdSample, "line")

	chunks := db.AllChunks(path)
	if len(chunks) < 2 {
		t.Fatalf("want a multi-chunk fixture, got %d chunks", len(chunks))
	}
	first, last := chunks[0], chunks[len(chunks)-1]

	html := getContentView(t, srv, path, "?range="+first.Range)

	if got := chunkDivCount(html); got != 1 {
		t.Errorf("chunk divs = %d, want exactly 1 for a ranged view", got)
	}
	if !strings.Contains(html, `data-range="`+first.Range+`"`) {
		t.Errorf("chunk div does not carry the requested range %q", first.Range)
	}
	want := db.ChunkIDByLocation(path, first.Range)
	if want == 0 {
		t.Fatal("fixture chunk has no chunk ID")
	}
	if !strings.Contains(html, `data-chunkid="`+strconv.FormatUint(want, 10)+`"`) {
		t.Errorf("chunk div does not carry chunk ID %d", want)
	}
	if body := strings.TrimSpace(last.Content); body != "" && strings.Contains(html, body) {
		t.Error("a ranged view rendered content from another chunk")
	}
}

// TestContentView_PlainChunked — test-ContentView.md "non-markdown chunked
// file escapes rather than renders". R1495-R1496, R1499.
func TestContentView_PlainChunked(t *testing.T) {
	srv, db, _ := setupContentView(t)
	path := addIndexed(t, db, "notes.txt", "# not a heading\na < b\nlast line\n", "line")

	chunks := db.AllChunks(path)
	if len(chunks) == 0 {
		t.Fatal("fixture produced no chunks")
	}

	html := getContentView(t, srv, path, "")

	if got := chunkDivCount(html); got != len(chunks) {
		t.Errorf("chunk divs = %d, want %d", got, len(chunks))
	}
	if strings.Contains(html, "<h1") {
		t.Error("plain text was rendered as markdown; it must be escaped, not converted")
	}
	if !strings.Contains(html, "# not a heading") {
		t.Error("literal markdown syntax did not survive escaping")
	}
	if !strings.Contains(html, "a &lt; b") {
		t.Error("HTML-special character was not escaped")
	}
}

// TestContentView_PlainSingleChunk — test-ContentView.md "non-markdown single
// chunk is wrapped for the pin button". R2415, R2417.
func TestContentView_PlainSingleChunk(t *testing.T) {
	srv, db, _ := setupContentView(t)
	path := addIndexed(t, db, "notes.txt", "# not a heading\na < b\nlast line\n", "line")

	chunks := db.AllChunks(path)
	if len(chunks) == 0 {
		t.Fatal("fixture produced no chunks")
	}
	rng := chunks[0].Range

	html := getContentView(t, srv, path, "?range="+rng)

	if got := chunkDivCount(html); got != 1 {
		t.Errorf("chunk divs = %d, want exactly 1 wrapping div", got)
	}
	if !strings.Contains(html, `data-range="`+rng+`"`) {
		t.Errorf("wrapping div does not carry the requested range %q", rng)
	}
	if cid := db.ChunkIDByLocation(path, rng); cid != 0 &&
		!strings.Contains(html, `data-chunkid="`+strconv.FormatUint(cid, 10)+`"`) {
		t.Errorf("wrapping div does not carry chunk ID %d", cid)
	}
}

// TestContentView_UnindexedFallback — test-ContentView.md "unindexed markdown
// falls back to a whole-file render". R1160-R1164.
func TestContentView_UnindexedFallback(t *testing.T) {
	srv, db, _ := setupContentView(t)
	path := filepath.Join(db.dbPath, "loose.md")
	if err := os.WriteFile(path, []byte(mdSample), 0o644); err != nil {
		t.Fatal(err)
	}

	html := getContentView(t, srv, path, "")

	if !strings.Contains(html, "<h1") {
		t.Error("unindexed markdown was not rendered")
	}
	if got := chunkDivCount(html); got != 0 {
		t.Errorf("chunk divs = %d, want 0 for an unindexed file", got)
	}
}
