package ark

// CRC: crc-Server.md | Seq: seq-content-fetching.md | Test: test-ContentView.md

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
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

// getContentView drives the real handler through the route it is registered
// on, with the URL built the way every builder builds it (R3654, R3655), and
// returns the body. The stand-in templates are `{{.Content}}`, so the body is
// exactly what the render assembled. Calling the handler directly with
// "/content"+path once hid a Windows defect: the handler accepted
// `/contentC:/…`, which no route delivers.
func getContentView(t *testing.T, srv *Server, path, query string) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /content/", srv.handleContentView)
	target := "/content" + ContentURLPath(path) + query
	req := httptest.NewRequest("GET", target, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET %s → %d: %s", target, rec.Code, rec.Body.String())
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

// TestContentView_PencilOnlyWhereSaveWorks — test-ContentView.md "the pencil
// is offered only where save accepts the file". R3665.
func TestContentView_PencilOnlyWhereSaveWorks(t *testing.T) {
	db := encodingDB(t)
	mux := encodingServer(t, db)
	indexed := filepath.Join(db.dbPath, "kept.md")
	loose := filepath.Join(db.dbPath, "loose.md")
	for _, p := range []string{indexed, loose} {
		if err := os.WriteFile(p, []byte(mdSample), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.indexer.AddFile(indexed, "markdown"); err != nil {
		t.Fatal(err)
	}
	if got := serve(t, mux, "GET", "/content"+ContentURLPath(indexed), "").Body.String(); !strings.Contains(got, "hide=false") {
		t.Errorf("indexed file: %q, want the pencil", got[:min(40, len(got))])
	}
	if got := serve(t, mux, "GET", "/content"+ContentURLPath(loose), "").Body.String(); !strings.Contains(got, "hide=true") {
		t.Errorf("unindexed file: %q, want no pencil, since save refuses it", got[:min(40, len(got))])
	}
}

// TestSave_RefusalIsLogged — test-ContentView.md "a refused save is logged".
// R1087, R3668.
func TestSave_RefusalIsLogged(t *testing.T) {
	db := encodingDB(t)
	mux := encodingServer(t, db)
	loose := filepath.Join(db.dbPath, "loose.md")
	if err := os.WriteFile(loose, []byte(mdSample), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	body, _ := json.Marshal(map[string]string{"path": loose, "content": "changed"})
	rec := serve(t, mux, "POST", "/file/save", string(body))
	if rec.Code != http.StatusForbidden {
		t.Errorf("save of an unindexed file → %d, want 403", rec.Code)
	}
	if data, _ := os.ReadFile(loose); string(data) != mdSample {
		t.Errorf("refused save changed the file: %q", data)
	}
	if got := buf.String(); !strings.Contains(got, "save refused") || !strings.Contains(got, loose) {
		t.Errorf("log = %q, want the refusal with its path", got)
	}
}
