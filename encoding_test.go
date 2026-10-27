package ark

// CRC: crc-FileEncoding.md | Test: test-FileEncoding.md

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// utf16File writes text as UTF-16LE with its byte-order mark, the way Windows
// PowerShell 5's `>` does, and returns the path.
func utf16File(t *testing.T, dir, name, text string) string {
	t.Helper()
	fp := filepath.Join(dir, name)
	if err := os.WriteFile(fp, EncodeText([]byte(text), EncodingUTF16LE), 0o644); err != nil {
		t.Fatal(err)
	}
	return fp
}

// encodingDB opens a real database, so the chunkers are registered exactly as
// production registers them, decoding wrapper included. A hand-wired fixture
// would test a registration nobody runs.
func encodingDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	if err := Init(dir, InitOpts{}); err != nil {
		t.Fatal(err)
	}
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func fileIDOf(t *testing.T, db *DB, path string) uint64 {
	t.Helper()
	st, err := db.fts.CheckFile(path)
	if err != nil || st.FileID == 0 {
		t.Fatalf("%s not indexed: %v", path, err)
	}
	return st.FileID
}

// R3656, R3657, R3661: the mark names the encoding, the UTF-8 mark is UTF-8,
// and text survives the round trip unless it is malformed.
func TestDecodeText(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		enc  FileEncoding
	}{
		{"utf-8", []byte("hello\n"), EncodingUTF8},
		{"utf-8 mark", []byte("\xEF\xBB\xBFhello\n"), EncodingUTF8},
		{"utf-16le", EncodeText([]byte("hello 😀\r\n"), EncodingUTF16LE), EncodingUTF16LE},
		{"utf-16be", EncodeText([]byte("hello 😀\r\n"), EncodingUTF16BE), EncodingUTF16BE},
	}
	for _, c := range cases {
		text, enc, ok := DecodeText(c.data)
		if enc != c.enc || !ok {
			t.Errorf("%s: enc=%s roundTrips=%v, want %s true", c.name, enc, ok, c.enc)
		}
		if !bytes.Equal(EncodeText(text, enc), c.data) {
			t.Errorf("%s: EncodeText(DecodeText(x)) != x", c.name)
		}
	}
	if text, _, _ := DecodeText(EncodeText([]byte("hello 😀"), EncodingUTF16BE)); string(text) != "hello 😀" {
		t.Errorf("utf-16be decoded to %q", text)
	}
	// An unpaired surrogate decodes to U+FFFD and cannot come back.
	lossy := []byte{0xFF, 0xFE, 0x00, 0xD8, 'a', 0x00}
	if _, _, ok := DecodeText(lossy); ok {
		t.Error("an unpaired surrogate round-tripped")
	}
}

// R3657, R3660: a UTF-16 file is indexed as text: its words are found, its
// tags read, a chunk's text is UTF-8, and its N record says so.
func TestUTF16FileIndexed(t *testing.T) {
	db := encodingDB(t)
	fp := utf16File(t, db.dbPath, "bubba.md", "# Bubba\r\n\r\n@topic: windows\r\n@tag: quokka a marsupial\r\nThe word quokkaferret.\r\n")
	if _, err := db.indexer.AddFile(fp, "markdown"); err != nil {
		t.Fatalf("AddFile: %v", err)
	}
	chunks := db.AllChunks(fp)
	var all string
	for _, c := range chunks {
		all += c.Content
	}
	if !strings.Contains(all, "quokkaferret") || strings.Contains(all, "\x00") {
		t.Errorf("chunk content is not the decoded text: %q", all)
	}
	if got := string(db.ChunkText(fp, chunks[len(chunks)-1].Range)); !strings.Contains(got, "quokkaferret") {
		t.Errorf("ChunkText = %q, want the decoded chunk", got)
	}
	tags, _ := db.TagList()
	found := false
	for _, tc := range tags {
		found = found || tc.Tag == "topic"
	}
	if !found {
		t.Errorf("@topic not indexed: %v", tags)
	}
	// A definition is a file-level read: it comes from the post-store scan of
	// the whole file, not from the chunk callback, so it sees whatever text
	// that scan is given.
	if defs, _ := db.TagDefs([]string{"quokka"}); len(defs) == 0 {
		t.Error("@tag: quokka definition not indexed; the file-level scan read the raw bytes")
	}
	rec, ok, err := db.store.ReadFileEncoding(fileIDOf(t, db, fp))
	if err != nil || !ok || rec.Encoding != EncodingUTF16LE || !rec.Writable {
		t.Errorf("N record = %+v ok=%v err=%v, want utf-16le writable", rec, ok, err)
	}
}

// R3660: the record moves to the new id on a re-index and goes with the file.
func TestUTF16RecordLifecycle(t *testing.T) {
	db := encodingDB(t)
	fp := utf16File(t, db.dbPath, "life.md", "# One\r\n\r\nfirst\r\n")
	if _, err := db.indexer.AddFile(fp, "markdown"); err != nil {
		t.Fatal(err)
	}
	oldID := fileIDOf(t, db, fp)
	utf16File(t, db.dbPath, "life.md", "# Two\r\n\r\nrewritten\r\n")
	if err := db.Refresh([]string{fp}); err != nil {
		t.Fatal(err)
	}
	db.WaitWritesIdle()
	newID := fileIDOf(t, db, fp)
	if _, ok, _ := db.store.ReadFileEncoding(newID); !ok {
		t.Errorf("no N record for the re-indexed file (id %d)", newID)
	}
	if newID != oldID {
		if _, ok, _ := db.store.ReadFileEncoding(oldID); ok {
			t.Errorf("N record left behind on the old id %d", oldID)
		}
	}
	if err := db.Remove([]string{fp}); err != nil {
		t.Fatal(err)
	}
	db.WaitWritesIdle()
	if _, ok, _ := db.store.ReadFileEncoding(newID); ok {
		t.Error("N record outlived its file")
	}
}

// R3658: a UTF-16 file that grows is re-indexed whole, so the new text
// arrives decoded rather than as an appended raw tail. Two sections, so the
// last chunk starts mid-file: an append would resume from that chunk's UTF-8
// offset in the raw bytes, a window of ASCII and NULs that is valid UTF-8, so
// it would succeed with corrupt chunks instead of failing into a re-index.
func TestUTF16GrowthReindexes(t *testing.T) {
	db := encodingDB(t)
	base := "# Log\r\n\r\nfirst line\r\n\r\n# Later\r\n\r\nsecond line\r\n"
	fp := utf16File(t, db.dbPath, "grow.md", base)
	if _, err := db.indexer.AddFile(fp, "markdown"); err != nil {
		t.Fatal(err)
	}
	utf16File(t, db.dbPath, "grow.md", base+"\r\nappended wombatlyre\r\n")
	if err := db.Refresh([]string{fp}); err != nil {
		t.Fatal(err)
	}
	db.WaitWritesIdle()
	var all string
	for _, c := range db.AllChunks(fp) {
		all += c.Content
	}
	if !strings.Contains(all, "wombatlyre") || strings.Contains(all, "\x00") {
		t.Errorf("grown file's chunks = %q, want the decoded text with the new line", all)
	}
}

// R3662: an edit goes back in the file's own encoding, mark included.
func TestUTF16WriteBack(t *testing.T) {
	db := encodingDB(t)
	fp := utf16File(t, db.dbPath, "edit.md", "# Edit\r\n\r\nbody\r\n")
	if _, err := db.indexer.AddFile(fp, "markdown"); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceRegion(fp, 0, 0, []byte("prefix ")); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(fp)
	text, enc, ok := DecodeText(data)
	if enc != EncodingUTF16LE || !ok || !strings.HasPrefix(string(text), "prefix # Edit") {
		t.Errorf("after the edit: enc=%s ok=%v text=%q, want utf-16le starting with the edit", enc, ok, text)
	}
	if out, err := EncodeForFile(fp, []byte("saved\r\n")); err != nil || DetectEncoding(out) != EncodingUTF16LE {
		t.Errorf("EncodeForFile = %v, %v; want utf-16le", DetectEncoding(out), err)
	}
}

// R3661: a file that would not survive re-encoding is indexed and never written.
func TestUTF16LossyNotWritable(t *testing.T) {
	db := encodingDB(t)
	fp := filepath.Join(db.dbPath, "lossy.md")
	lossy := append(EncodeText([]byte("# Lossy\r\n\r\nword "), EncodingUTF16LE), 0x00, 0xD8, 'z', 0x00, '\r', 0x00, '\n', 0x00)
	if err := os.WriteFile(fp, lossy, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.indexer.AddFile(fp, "markdown"); err != nil {
		t.Fatalf("AddFile: %v", err)
	}
	if rec, ok, _ := db.store.ReadFileEncoding(fileIDOf(t, db, fp)); !ok || rec.Writable {
		t.Errorf("N record = %+v ok=%v, want not writable", rec, ok)
	}
	if _, _, err := ReadEditableText(fp); !errors.Is(err, errNotWritable) {
		t.Errorf("ReadEditableText err = %v, want errNotWritable", err)
	}
	if err := db.ReplaceRegion(fp, 0, 0, []byte("x")); err == nil {
		t.Error("ReplaceRegion wrote a file that does not round-trip")
	}
	if got, _ := os.ReadFile(fp); !bytes.Equal(got, lossy) {
		t.Error("the lossy file's bytes changed")
	}
}

// R3659: an unwrapped chunker's UTF-8 failure names the file's encoding.
func TestExplainEncoding(t *testing.T) {
	split := errors.New(`chunk "1-2" contains invalid UTF-8 in x.md`)
	got := explainEncoding(split, EncodeText([]byte("hi"), EncodingUTF16LE), "chat-jsonl")
	if !strings.Contains(got.Error(), "utf-16le") || !strings.Contains(got.Error(), "chat-jsonl chunker reads UTF-8 only") || !errors.Is(got, split) {
		t.Errorf("explained = %v", got)
	}
	if got := explainEncoding(split, []byte("hi"), "chat-jsonl"); got != split {
		t.Errorf("a UTF-8 file's error changed: %v", got)
	}
}

// encodingServer serves db's content and editor routes the way production
// registers them, with stand-in templates that show the editor flag and the
// rendered content.
func encodingServer(t *testing.T, db *DB) *http.ServeMux {
	t.Helper()
	db.config.Sources = []Source{{Dir: db.dbPath}} // the content routes serve only files under a source
	htmlDir := filepath.Join(db.dbPath, "html")
	if err := os.MkdirAll(htmlDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"content-markdown.html", "content-plain.html"} {
		if err := os.WriteFile(filepath.Join(htmlDir, name), []byte(`hide={{.HideToggle}} {{.Content}}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	srv := &Server{db: db}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /fetch/", srv.handleContentFetch)
	mux.HandleFunc("GET /content/", srv.handleContentView)
	mux.HandleFunc("GET /raw/", srv.handleContentRaw)
	mux.HandleFunc("POST /file/save", srv.handleSave)
	mux.HandleFunc("POST /tags/set", srv.handleSetTags)
	return mux
}

func serve(t *testing.T, mux *http.ServeMux, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// R3663, R3661: /fetch and the view serve decoded text, /raw the bytes, and a
// file that does not round-trip is shown without the editor.
func TestUTF16ContentRoutes(t *testing.T) {
	db := encodingDB(t)
	mux := encodingServer(t, db)
	fp := utf16File(t, db.dbPath, "view.md", "# View\r\n\r\nquokkaferret\r\n")
	if _, err := db.indexer.AddFile(fp, "markdown"); err != nil {
		t.Fatal(err)
	}
	u := ContentURLPath(fp)
	if got := serve(t, mux, "GET", "/fetch"+u, "").Body.String(); !strings.Contains(got, "quokkaferret") {
		t.Errorf("/fetch = %q, want the decoded text", got)
	}
	if got := serve(t, mux, "GET", "/content"+u, "").Body.String(); !strings.Contains(got, "quokkaferret") || !strings.Contains(got, "hide=false") {
		t.Errorf("/content = %q, want the decoded text with the editor", got)
	}
	if got := serve(t, mux, "GET", "/raw"+u, "").Body.Bytes(); !bytes.HasPrefix(got, bomUTF16LE) {
		t.Errorf("/raw does not start with the file's mark: % x", got[:min(4, len(got))])
	}
	lossy := filepath.Join(db.dbPath, "lossy.md")
	if err := os.WriteFile(lossy, append(EncodeText([]byte("# L\r\n\r\nword "), EncodingUTF16LE), 0x00, 0xD8), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.indexer.AddFile(lossy, "markdown"); err != nil {
		t.Fatal(err)
	}
	if got := serve(t, mux, "GET", "/content"+ContentURLPath(lossy), "").Body.String(); !strings.Contains(got, "hide=true") {
		t.Errorf("lossy /content = %q, want no editor", got)
	}
}

// R3662: an editor save and a tag-block write keep the file UTF-16.
func TestUTF16SaveAndSetTags(t *testing.T) {
	db := encodingDB(t)
	mux := encodingServer(t, db)
	fp := utf16File(t, db.dbPath, "save.md", "# Save\r\n\r\nbody\r\n")
	if _, err := db.indexer.AddFile(fp, "markdown"); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"path": fp, "content": "# Save\r\n\r\nedited body\r\n"})
	if rec := serve(t, mux, "POST", "/file/save", string(body)); rec.Code != 200 {
		t.Fatalf("save → %d: %s", rec.Code, rec.Body.String())
	}
	data, _ := os.ReadFile(fp)
	if text, enc, _ := DecodeText(data); enc != EncodingUTF16LE || !strings.Contains(string(text), "edited body") {
		t.Errorf("after save: enc=%s text=%q", enc, text)
	}
	body, _ = json.Marshal(map[string]any{"path": fp, "tags": map[string]string{"status": "open"}})
	if rec := serve(t, mux, "POST", "/tags/set", string(body)); rec.Code != 200 {
		t.Fatalf("set-tags → %d: %s", rec.Code, rec.Body.String())
	}
	data, _ = os.ReadFile(fp)
	if text, enc, _ := DecodeText(data); enc != EncodingUTF16LE || !strings.Contains(string(text), "@status: open") {
		t.Errorf("after set-tags: enc=%s text=%q", enc, text)
	}
}

// R3658, the server's path: a running server re-indexes through
// prepareScan (RefreshAsync, IndexPathsAsync), not prepareRefresh, and a grown
// UTF-16 file must be re-indexed whole there too.
func TestUTF16GrowthReindexesAsync(t *testing.T) {
	db := encodingDB(t)
	base := "# Log\r\n\r\nfirst line\r\n\r\n# Later\r\n\r\nsecond line\r\n"
	fp := utf16File(t, db.dbPath, "grow.md", base)
	if _, err := db.indexer.AddFile(fp, "markdown"); err != nil {
		t.Fatal(err)
	}
	chunkText := func() string {
		var all string
		for _, c := range db.AllChunks(fp) {
			all += c.Content
		}
		return all
	}
	before := chunkText()
	db.config.Sources = []Source{{Dir: db.dbPath, Include: PatternSpec{Add: []string{"*.md"}}}}
	utf16File(t, db.dbPath, "grow.md", base+"\r\nappended wombatlyre\r\n")
	// The freshness check reads the modification time first, and two writes
	// this close can share one; a real edit comes later.
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(fp, later, later); err != nil {
		t.Fatal(err)
	}
	if err := SyncVoid(db, func(db *DB) error { return db.RefreshAsync() }); err != nil {
		t.Fatal(err)
	}
	all := before
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if st, err := db.fts.CheckFile(fp); err == nil && st.Status == "fresh" {
			if all = chunkText(); all != before {
				break
			}
		}
	}
	if !strings.Contains(all, "wombatlyre") || strings.Contains(all, "\x00") {
		t.Errorf("grown file's chunks after an async refresh = %q, want the decoded text with the new line", all)
	}
}

// R3664: the `lines` strategy and ark's own chat-log chunker read UTF-16.
func TestUTF16EveryTextChunker(t *testing.T) {
	db := encodingDB(t)
	cases := []struct{ name, strategy, text, word string }{
		{"notes.txt", "lines", "plain text line\r\nthe word dugongnote\r\n", "dugongnote"},
		{"log.jsonl", ChatStrategy, `{"type":"user","message":{"role":"user","content":"hello quokkachat"}}` + "\n", "quokkachat"},
	}
	for _, c := range cases {
		fp := utf16File(t, db.dbPath, c.name, c.text)
		if _, err := db.indexer.AddFile(fp, c.strategy); err != nil {
			t.Errorf("%s (%s): %v", c.name, c.strategy, err)
			continue
		}
		var all string
		for _, ch := range db.AllChunks(fp) {
			all += ch.Content
		}
		if !strings.Contains(all, c.word) || strings.Contains(all, "\x00") {
			t.Errorf("%s (%s) chunks = %q, want the decoded text", c.name, c.strategy, all)
		}
	}
}

// R3664: the scripture chunker yields the same chunks from a UTF-16 copy of a
// book as from the UTF-8 original.
func TestUTF16BibleChunker(t *testing.T) {
	want := bibleChunksOf(t, bibleFixture)
	got := bibleChunksOf(t, string(EncodeText([]byte(bibleFixture), EncodingUTF16LE)))
	if len(got) != len(want) {
		t.Fatalf("UTF-16 copy gave %d chunks, UTF-8 %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i].Content, want[i].Content) || !bytes.Equal(got[i].Range, want[i].Range) || !bytes.Equal(got[i].Locator, want[i].Locator) {
			t.Errorf("chunk %d differs: %q vs %q", i, got[i].Content, want[i].Content)
		}
	}
}
