package ark

// Test: test-StaleChunk.md | R3571-R3575

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zot/microfts2"
)

// staleFixture indexes three one-line chunks, then shortens the file on disk
// without re-indexing, so the current chunker produces only the first. The
// second and third stay in the index and come back from GetChunks Missing.
// It checks that, so a change in microfts2's range matching fails here as a
// fixture problem rather than as a wrong verdict in the test using it.
//
// Its chunker stores attrs, so the stale entries carry some and a test can see
// whether they leak: line 1 is chapter 2, the stale lines chapter 1 (verses
// 1-9), the shape chunkForVerse reads.
func staleFixture(t *testing.T, db *DB) (fp string, info microfts2.FRecord) {
	t.Helper()
	if err := db.fts.AddChunker(staleStrategy, microfts2.FuncChunker{Fn: lineWithAttrs}); err != nil {
		t.Fatal(err)
	}
	fp = filepath.Join(db.dbPath, "stale.txt")
	if err := os.WriteFile(fp, []byte("alpha one\nbeta two\ngamma three\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fileID, err := db.indexer.AddFile(fp, staleStrategy)
	if err != nil {
		t.Fatal(err)
	}
	info, err = db.fts.FileInfoByID(fileID)
	if err != nil || len(info.Chunks) != 3 {
		t.Fatalf("fixture invalid: want 3 indexed chunks, got %d (%v)", len(info.Chunks), err)
	}
	if err := os.WriteFile(fp, []byte("alpha one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	all := db.AllChunks(fp)
	if len(all) != 3 || all[0].Missing || !all[1].Missing || !all[2].Missing {
		t.Fatalf("fixture invalid: want chunk 1 present and chunks 2-3 missing, got %+v", all)
	}
	if len(all[2].Attrs) == 0 {
		t.Fatal("fixture invalid: the stale entry carries no stored attrs, so no test can see them leak")
	}
	return fp, info
}

const staleStrategy = "line-attrs"

// lineWithAttrs is the line chunker plus verse attrs: chapter 2 for line 1,
// chapter 1 for every later line.
func lineWithAttrs(path string, content []byte, yield func(microfts2.Chunk) bool) error {
	first := true
	return microfts2.LineChunkFunc(path, content, func(c microfts2.Chunk) bool {
		chapter := "1"
		if first {
			chapter, first = "2", false
		}
		c.Attrs = []microfts2.Pair{{Key: []byte("chapter"), Value: []byte(chapter)}, {Key: []byte("verses"), Value: []byte("1-9")}}
		return yield(c)
	})
}

// R3571: a missing chunk's content and stored attrs are not used.
func TestStaleChunk_ReadAsAbsent(t *testing.T) {
	_, db := setupRecall(t)
	fp, info := staleFixture(t, db)
	live, stale := info.Chunks[0], info.Chunks[2]

	if db.ChunkMissing(fp, live.Location) {
		t.Error("ChunkMissing reports the live chunk missing")
	}
	if !db.ChunkMissing(fp, stale.Location) {
		t.Error("ChunkMissing does not report the stale chunk")
	}
	if db.ChunkMissing(fp, "no-such-range") {
		t.Error("ChunkMissing reports a range the file never had")
	}
	if attrs := db.ChunkAttrs(fp, stale.Location); attrs != nil {
		t.Errorf("ChunkAttrs returned a stale chunk's attrs: %v", attrs)
	}
	if got := db.chunkForVerse(fp, info, 1, 3); got != nil {
		t.Errorf("chunkForVerse placed a reference by a stale chunk's attrs: %v", got)
	}
	fetched, err := db.FetchChunkContent(fp, live.Location, "", 0, 2)
	if err != nil || len(fetched.Chunks) != 3 {
		t.Fatalf("FetchChunkContent: %v, %d chunks", err, len(fetched.Chunks))
	}
	if fetched.Chunks[0].Attrs == nil {
		t.Fatal("fixture invalid: the live chunk's attrs are not fetched, so their absence on a stale one proves nothing")
	}
	if a := fetched.Chunks[2].Attrs; a != nil {
		t.Errorf("ark chunks would emit a stale chunk's attrs: %v", a)
	}
	if _, ok := db.chunkContent(stale.ChunkID); ok {
		t.Error("chunkContent answered for a stale chunk")
	}
	if _, ok := db.chunkContent(live.ChunkID); !ok {
		t.Error("chunkContent lost the live chunk")
	}
	// An empty stale chunk must not satisfy a regex that matches empty text.
	// The positive control proves the target reaches the regex branch.
	if got := db.ResolveExtTarget(fp+`:/alpha/`, ""); len(got) != 1 || got[0] != live.ChunkID {
		t.Fatalf("fixture invalid: the ext regex target does not resolve the live chunk (%v)", got)
	}
	for _, cid := range db.ResolveExtTarget(fp+`:/^$/`, "") {
		if cid != live.ChunkID {
			t.Errorf("ext regex anchor matched stale chunk %d", cid)
		}
	}
	res, err := db.ChunkStats(nil, nil, func(s string) int { return len(s) })
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Rows[0].Count; got != 1 {
		t.Errorf("ChunkStats counted %d chunks, want 1: a stale chunk was measured", got)
	}
	if _, err := db.SuggestExtLocator(stale.ChunkID); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Errorf("SuggestExtLocator on a stale chunk: want a stale error, got %v", err)
	}
}

// R3572: one placeholder text; the HTML form says the same thing.
func TestStaleChunk_Placeholder(t *testing.T) {
	p := StaleChunkPlaceholder("/a/b.md")
	if !strings.Contains(p, "`ark refresh --force /a/b.md`") {
		t.Errorf("placeholder lacks the repair command: %q", p)
	}
	if q := StaleChunkPlaceholder("/a/my notes.md"); !strings.Contains(q, "'/a/my notes.md'") {
		t.Errorf("a path with a space is not quoted: %q", q)
	}
	h := staleChunkHTML("/a/b.md")
	if !strings.Contains(h, "<code>ark refresh --force /a/b.md</code>") || !strings.Contains(h, "older chunking rules") {
		t.Errorf("HTML placeholder differs from the text: %q", h)
	}
}

// R3573: a search hit whose text is unavailable is dropped, not returned empty.
func TestStaleChunk_SearchFillDrops(t *testing.T) {
	_, db := setupRecall(t)
	fp, info := staleFixture(t, db)
	in := []SearchResultEntry{
		{Path: fp, Range: info.Chunks[0].Location, ChunkNum: info.Chunks[0].ChunkID},
		{Path: fp, Range: info.Chunks[2].Location, ChunkNum: info.Chunks[2].ChunkID},
	}
	out, err := db.FillChunks(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].ChunkNum != info.Chunks[0].ChunkID {
		t.Fatalf("FillChunks kept %d results (%+v), want only the live chunk", len(out), out)
	}
}

// R3573: a recall hit whose content was asked for and cannot be read is dropped.
func TestStaleChunk_RecallDropsUnavailable(t *testing.T) {
	l, db := setupRecall(t)
	query, _ := indexLine(t, db, "query.txt", "what should we cook tonight")
	if err := db.store.WriteChunkEmbedding(query, vecFrom(1, 0, 0, 0)); err != nil {
		t.Fatal(err)
	}
	_, info := staleFixture(t, db)
	stale := info.Chunks[2].ChunkID
	if err := db.store.WriteChunkEmbedding(stale, vecFrom(1, 0.01, 0, 0)); err != nil {
		t.Fatal(err)
	}
	opts := RecallOpts{K: 5, KeepTagless: true}
	plain, err := l.Recall([]ConnectionsInput{{ChunkID: query}}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !scopeLegContains(plain, stale) {
		t.Fatal("fixture invalid: recall without content does not surface the stale chunk, so this test cannot see it dropped")
	}
	opts.IncludeContent = true
	withContent, err := l.Recall([]ConnectionsInput{{ChunkID: query}}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if scopeLegContains(withContent, stale) {
		t.Fatal("recall with content returned the stale chunk instead of dropping it")
	}
}

// R3574: the content view keeps a stale chunk's div and fills it with the
// placeholder; a PDF page draws no overlay for one.
func TestStaleChunk_ContentView(t *testing.T) {
	_, db := setupRecall(t)
	fp, info := staleFixture(t, db)
	cr := &contentRender{rdb: db, path: fp, strategy: staleStrategy}
	body := cr.chunkedBody(db.AllChunks(fp))
	if n := strings.Count(body, `<div class="ark-chunk"`); n != 3 {
		t.Errorf("chunked view has %d chunk divs, want 3: the file lost its shape", n)
	}
	if n := strings.Count(body, "older chunking rules"); n != 2 {
		t.Errorf("chunked view shows %d placeholders, want 2", n)
	}
	if !strings.Contains(body, "alpha one") {
		t.Error("chunked view lost the live chunk's text")
	}

	md := &contentRender{srv: &Server{db: db}, rdb: db, path: fp}
	if body := md.markdownBody(); strings.Count(body, "older chunking rules") != 2 || !strings.Contains(body, "alpha one") {
		t.Errorf("markdown view: want the live text and 2 placeholders, got %q", body)
	}
	// A single-chunk view of a stale chunk (the handler sets stale when
	// ChunkMissing answers for its ?range=) shows the placeholder, in both
	// render kinds.
	md.isChunk, md.stale, md.rangeParam = true, true, info.Chunks[2].Location
	if body := md.markdownBody(); !strings.Contains(body, "older chunking rules") {
		t.Errorf("markdown single-chunk view of a stale chunk: %q", body)
	}
	if body := md.unchunkedBody(); !strings.Contains(body, "older chunking rules") {
		t.Errorf("plain single-chunk view of a stale chunk: %q", body)
	}

	staleRect := "9,9,99,99"
	pdf := renderPdfChunksByPage([]microfts2.ChunkResult{{
		Path: fp, Range: "1", Index: 0, Missing: true,
		Attrs: []microfts2.Pair{{Key: []byte("page"), Value: []byte("1")}, {Key: []byte("page_size"), Value: []byte("600,800")}, {Key: []byte("rect"), Value: []byte(staleRect)}},
	}}, fp, 0, db)
	if strings.Contains(pdf, staleRect) {
		t.Error("PDF page view placed an overlay from a stale chunk's rect")
	}
}

// R3575: the embedding pass leaves a stale chunk queued, with no sentinel.
func TestStaleChunk_EmbedAction(t *testing.T) {
	if _, k := embedChunkAction(microfts2.ChunkResult{Missing: true}, "markdown"); k != embedChunkStale {
		t.Errorf("a missing chunk got action %d, want stale (no record)", k)
	}
	if _, k := embedChunkAction(microfts2.ChunkResult{Content: "@status: open\n"}, "markdown"); k != embedChunkTagOnly {
		t.Errorf("an all-tag chunk got action %d, want the tag-only sentinel", k)
	}
	if text, k := embedChunkAction(microfts2.ChunkResult{Content: "plain words"}, "markdown"); k != embedChunkText || text != "plain words" {
		t.Errorf("a plain chunk got (%q, %d), want its text embedded", text, k)
	}
}
