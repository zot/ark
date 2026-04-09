package ark

// CRC: crc-Indexer.md | Test: test-Tags.md

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zot/microfts2"

	"github.com/zot/microvec"
)

func TestExtractTagsBasic(t *testing.T) {
	content := []byte("@decision: chose LMDB\n@pattern: closure-actor\nnot a @tag without colon")
	tags := ExtractTags(content)
	if tags["decision"] != 1 {
		t.Errorf("expected decision=1, got %d", tags["decision"])
	}
	if tags["pattern"] != 1 {
		t.Errorf("expected pattern=1, got %d", tags["pattern"])
	}
	if _, ok := tags["tag"]; ok {
		t.Error("tag without colon should not match")
	}
}

func TestExtractTagsMultipleOccurrences(t *testing.T) {
	content := []byte("@decision: first\nsome text\n@decision: second")
	tags := ExtractTags(content)
	if tags["decision"] != 2 {
		t.Errorf("expected decision=2, got %d", tags["decision"])
	}
}

func TestExtractTagsCaseAndHyphens(t *testing.T) {
	content := []byte("@my-tag: value\n@CamelTag: value")
	tags := ExtractTags(content)
	if tags["my-tag"] != 1 {
		t.Errorf("expected my-tag=1, got %d", tags["my-tag"])
	}
	if tags["cameltag"] != 1 {
		t.Errorf("expected cameltag=1, got %d", tags["cameltag"])
	}
}

func TestExtractTagsIgnoresEmailsAndMentions(t *testing.T) {
	content := []byte("user@example.com and @mention without colon")
	tags := ExtractTags(content)
	if len(tags) != 0 {
		t.Errorf("expected no tags, got %v", tags)
	}
}

func TestExtractTagsAdjacentAt(t *testing.T) {
	// R1320: @bar: after non-whitespace is NOT a tag — embedded in larger token
	content := []byte("foo@bar: value")
	tags := ExtractTags(content)
	if tags["bar"] != 0 {
		t.Errorf("expected bar=0 (mention, no preceding space), got %d", tags["bar"])
	}
	// But with preceding space it IS a tag
	content = []byte("foo @bar: value")
	tags = ExtractTags(content)
	if tags["bar"] != 1 {
		t.Errorf("expected bar=1 (preceded by space), got %d", tags["bar"])
	}
}

func TestExtractTagDefs(t *testing.T) {
	content := []byte("@tag: decision A choice that was made\n@tag: pattern A recurring approach\nnot a @tag: inline mention\n@tag: x\n")
	defs := ExtractTagDefs(content)
	if defs["decision"] != "A choice that was made" {
		t.Errorf("expected decision description, got %q", defs["decision"])
	}
	if defs["pattern"] != "A recurring approach" {
		t.Errorf("expected pattern description, got %q", defs["pattern"])
	}
	if _, ok := defs["inline"]; ok {
		t.Error("mid-line @tag: should not be extracted")
	}
	if _, ok := defs["x"]; ok {
		t.Error("@tag: with only name and no description should not match")
	}
}

func TestExtractTagDefsWithSeparator(t *testing.T) {
	content := []byte("@tag: decision -- A choice that was made and why\n")
	defs := ExtractTagDefs(content)
	if defs["decision"] != "-- A choice that was made and why" {
		t.Errorf("expected full description including --, got %q", defs["decision"])
	}
}

func TestExtractTagsInline(t *testing.T) {
	content := []byte("some text @decision: mid-line\n@pattern: at-start\n  indented @status: not-start")
	tags := ExtractTags(content)
	if tags["decision"] != 1 {
		t.Errorf("mid-line @decision: should be extracted, got %d", tags["decision"])
	}
	if tags["pattern"] != 1 {
		t.Errorf("expected pattern=1, got %d", tags["pattern"])
	}
	if tags["status"] != 1 {
		t.Errorf("indented @status: should be extracted, got %d", tags["status"])
	}
}

func TestExtractTagsCompound(t *testing.T) {
	content := []byte("@ref: notes/ideas.md:42 @item: interesting concept")
	tags := ExtractTags(content)
	if tags["ref"] != 1 {
		t.Errorf("expected ref=1, got %d", tags["ref"])
	}
	if tags["item"] != 1 {
		t.Errorf("expected item=1, got %d", tags["item"])
	}
}

func TestExtractTagsMentionQuotes(t *testing.T) {
	// R1321: tags inside backticks are mentions
	content := []byte("see `@decision: use LMDB` for details")
	tags := ExtractTags(content)
	if tags["decision"] != 0 {
		t.Errorf("backtick-quoted @decision: should be skipped, got %d", tags["decision"])
	}
	// R1321: tags inside double quotes are mentions
	content = []byte(`she said "@note: important" today`)
	tags = ExtractTags(content)
	if tags["note"] != 0 {
		t.Errorf("double-quoted @note: should be skipped, got %d", tags["note"])
	}
	// Unquoted on same line is fine
	content = []byte("the `example` shows @decision: use LMDB")
	tags = ExtractTags(content)
	if tags["decision"] != 1 {
		t.Errorf("even-quote @decision: should be extracted, got %d", tags["decision"])
	}
}

func TestExtractTagValuesMentionFenced(t *testing.T) {
	// R1322: tags inside fenced code blocks are mentions (markdown only)
	content := []byte("text\n```\n@decision: use LMDB\n```\n@status: open\n")
	values := ExtractTagValues(content, "markdown")
	found := map[string]bool{}
	for _, v := range values {
		found[v.Tag] = true
	}
	if found["decision"] {
		t.Error("@decision inside fenced code should be skipped in markdown strategy")
	}
	if !found["status"] {
		t.Error("@status outside fence should be extracted")
	}
	// Non-markdown strategy ignores fences
	values = ExtractTagValues(content, "lines")
	found = map[string]bool{}
	for _, v := range values {
		found[v.Tag] = true
	}
	if !found["decision"] {
		t.Error("@decision inside fence should be extracted for non-markdown strategy")
	}
}

func TestExtractTagValuesMentionIndented(t *testing.T) {
	// R1323: indented code blocks in markdown
	content := []byte("normal\n    @code: example\n\t@tab: indented\n@real: tag\n")
	values := ExtractTagValues(content, "markdown")
	found := map[string]bool{}
	for _, v := range values {
		found[v.Tag] = true
	}
	if found["code"] {
		t.Error("4-space-indented @code: should be skipped in markdown")
	}
	if found["tab"] {
		t.Error("tab-indented @tab: should be skipped in markdown")
	}
	if !found["real"] {
		t.Error("@real at line start should be extracted")
	}
}

func TestTagWindowForAppend(t *testing.T) {
	data := []byte("line one\n@sta")
	// Split at position 13 (mid-tag), should back up to after "line one\n"
	w := tagWindowForAppend(data, 9)
	if string(w) != "@sta" {
		t.Errorf("expected backing up to line start, got %q", string(w))
	}

	// Boundary-split tag: old content ends mid-tag, new content completes it
	full := []byte("first line\n@status: open\nmore text")
	w = tagWindowForAppend(full, 14) // split inside "@status: open"
	tags := ExtractTags(w)
	if tags["status"] != 1 {
		t.Errorf("boundary-split tag should be found, got %d", tags["status"])
	}

	// Split exactly at newline — no back-up needed
	w = tagWindowForAppend(full, 11) // right after "first line\n"
	if string(w) != "@status: open\nmore text" {
		t.Errorf("expected no back-up at newline boundary, got %q", string(w))
	}
}

// --- Append detection integration tests ---

// testIndexer creates a microfts2.DB, microvec.DB, Store, and Indexer
// backed by a temporary LMDB environment.
func testIndexer(t *testing.T) (*Indexer, string) {
	t.Helper()
	dir := t.TempDir()

	// microfts2
	dbPath := filepath.Join(dir, "db")
	fts, err := microfts2.Create(dbPath, microfts2.Options{MaxDBs: 8})
	if err != nil {
		t.Fatal(err)
	}
	fts.AddStrategyFunc("line", microfts2.LineChunkFunc)

	// microvec (shares the LMDB env)
	vec, err := microvec.Create(fts.Env(), microvec.Options{})
	if err != nil {
		t.Fatal(err)
	}

	// Store (shares the LMDB env)
	store, err := OpenStore(fts.Env())
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		fts.Close()
	})

	idx := &Indexer{fts: fts, vec: vec, store: store}
	return idx, dir
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	fp := filepath.Join(dir, name)
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return fp
}

func TestDetectAppendBasic(t *testing.T) {
	idx, dir := testIndexer(t)

	// Add a file
	fp := writeFile(t, dir, "test.txt", "line one\nline two\n")
	fileid, err := idx.AddFile(fp, "line")
	if err != nil {
		t.Fatal(err)
	}

	// Append to file
	f, err := os.OpenFile(fp, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("line three\n")
	f.Close()

	ok, err := idx.DetectAppend(fp, fileid)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("expected append detection to return true")
	}
}

func TestDetectAppendModified(t *testing.T) {
	idx, dir := testIndexer(t)

	fp := writeFile(t, dir, "test.txt", "line one\nline two\n")
	fileid, err := idx.AddFile(fp, "line")
	if err != nil {
		t.Fatal(err)
	}

	// Modify (not append — change existing content)
	os.WriteFile(fp, []byte("changed\nline two\nline three\n"), 0644)

	ok, err := idx.DetectAppend(fp, fileid)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expected append detection to return false for modified file")
	}
}

func TestDetectAppendShrunk(t *testing.T) {
	idx, dir := testIndexer(t)

	fp := writeFile(t, dir, "test.txt", "line one\nline two\nline three\n")
	fileid, err := idx.AddFile(fp, "line")
	if err != nil {
		t.Fatal(err)
	}

	// Shrink the file
	os.WriteFile(fp, []byte("line one\n"), 0644)

	ok, err := idx.DetectAppend(fp, fileid)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expected append detection to return false for shrunk file")
	}
}

func TestAppendFileUpdatesIndex(t *testing.T) {
	idx, dir := testIndexer(t)

	// Add file with tags
	fp := writeFile(t, dir, "test.txt", "@decision: first choice\n")
	fileid, err := idx.AddFile(fp, "line")
	if err != nil {
		t.Fatal(err)
	}

	// Verify initial state
	info, _ := idx.fts.FileInfoByID(fileid)
	if len(info.Chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(info.Chunks))
	}

	// Append content with a new tag
	f, _ := os.OpenFile(fp, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString("@pattern: new pattern\n")
	f.Close()

	// Run append
	if err := idx.AppendFile(fp, fileid, "line"); err != nil {
		t.Fatal(err)
	}

	// Verify FTS has 2 chunks now
	info, _ = idx.fts.FileInfoByID(fileid)
	if len(info.Chunks) != 2 {
		t.Fatalf("expected 2 chunks after append, got %d", len(info.Chunks))
	}

	// Verify tags were appended (not replaced)
	tags, _ := idx.store.ListTags()
	m := make(map[string]uint32)
	for _, tc := range tags {
		m[tc.Tag] = tc.Count
	}
	if m["decision"] != 1 {
		t.Errorf("expected decision=1, got %d", m["decision"])
	}
	if m["pattern"] != 1 {
		t.Errorf("expected pattern=1, got %d", m["pattern"])
	}
}

func TestRefreshFileUsesAppendPath(t *testing.T) {
	idx, dir := testIndexer(t)

	fp := writeFile(t, dir, "test.txt", "line one\n@tag: value\n")
	_, err := idx.AddFile(fp, "line")
	if err != nil {
		t.Fatal(err)
	}

	// Append
	f, _ := os.OpenFile(fp, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString("line three\n@other: thing\n")
	f.Close()

	// RefreshFile should detect append and use the fast path
	if err := idx.RefreshFile(fp, "line"); err != nil {
		t.Fatal(err)
	}

	// Check that both old and new tags exist
	tags, _ := idx.store.ListTags()
	m := make(map[string]uint32)
	for _, tc := range tags {
		m[tc.Tag] = tc.Count
	}
	if m["tag"] != 1 {
		t.Errorf("expected tag=1, got %d", m["tag"])
	}
	if m["other"] != 1 {
		t.Errorf("expected other=1, got %d", m["other"])
	}
}

func TestRefreshFileFallsBackToFullReindex(t *testing.T) {
	idx, dir := testIndexer(t)

	fp := writeFile(t, dir, "test.txt", "@old: tag\nline two\n")
	_, err := idx.AddFile(fp, "line")
	if err != nil {
		t.Fatal(err)
	}

	// Modify existing content (not append)
	os.WriteFile(fp, []byte("@new: tag\nline two\nline three\n"), 0644)

	// RefreshFile should fall back to full reindex
	if err := idx.RefreshFile(fp, "line"); err != nil {
		t.Fatal(err)
	}

	// Old tag should be gone, new tag should exist
	tags, _ := idx.store.ListTags()
	m := make(map[string]uint32)
	for _, tc := range tags {
		m[tc.Tag] = tc.Count
	}
	if _, ok := m["old"]; ok {
		t.Error("old tag should be gone after full reindex")
	}
	if m["new"] != 1 {
		t.Errorf("expected new=1, got %d", m["new"])
	}
}
