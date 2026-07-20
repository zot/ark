package ark

// CRC: crc-BibleRenderer.md, crc-Server.md | Test: test-BibleRender.md | R3181, R3182, R3183

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBibleRender_VerseMarksBecomeElements — test-BibleRender.md "verse marks
// become verse elements". R3181.
func TestBibleRender_VerseMarksBecomeElements(t *testing.T) {
	html := renderBibleMarkdownForContent(
		[]byte("`1` I lifted up mine eyes. `2` Then said I.\n"), "/kjv/books/zechariah.md")

	for _, want := range []string{
		`<ark-verse n="1"><code>1</code></ark-verse>`,
		`<ark-verse n="2"><code>2</code></ark-verse>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s in:\n%s", want, html)
		}
	}
	if !strings.Contains(html, "I lifted up mine eyes.") {
		t.Errorf("prose did not survive:\n%s", html)
	}
}

// TestBibleRender_OnlyNumericMarks — test-BibleRender.md "only numeric marks
// are verses". R3183.
func TestBibleRender_OnlyNumericMarks(t *testing.T) {
	html := renderBibleMarkdownForContent(
		[]byte("`3` of the reign of `xii`, see `someFunc()` for detail.\n"), "/kjv/books/x.md")

	if !strings.Contains(html, `<ark-verse n="3">`) {
		t.Errorf("numeric mark did not become a verse:\n%s", html)
	}
	if strings.Count(html, "<ark-verse") != 1 {
		t.Errorf("want exactly one verse element, got %d:\n%s", strings.Count(html, "<ark-verse"), html)
	}
	for _, want := range []string{"<code>xii</code>", "someFunc()"} {
		if !strings.Contains(html, want) {
			t.Errorf("ordinary inline code was disturbed; missing %q in:\n%s", want, html)
		}
	}
}

// TestBibleRender_FencedCodeIsNotAVerse — test-BibleRender.md "a numeric span
// inside a fenced code block is not a verse": the case a pass over rendered
// HTML could not distinguish. R3183.
func TestBibleRender_FencedCodeIsNotAVerse(t *testing.T) {
	html := renderBibleMarkdownForContent(
		[]byte("```\n42\n```\n\n`7` a real verse mark.\n"), "/kjv/books/x.md")

	fence := html[:strings.Index(html, "<p>")]
	if strings.Contains(fence, "<ark-verse") {
		t.Errorf("fenced code became a verse:\n%s", fence)
	}
	if !strings.Contains(html, `<ark-verse n="7">`) {
		t.Errorf("prose mark did not become a verse:\n%s", html)
	}
}

// TestInsertVerseExtBlocks — test-BibleRender.md "ext blocks land in their own
// verse". R3182.
func TestInsertVerseExtBlocks(t *testing.T) {
	html := `<p>` +
		`<ark-verse n="1"><code>1</code></ark-verse> one ` +
		`<ark-verse n="2"><code>2</code></ark-verse> two ` +
		`<ark-verse n="3"><code>3</code></ark-verse> three</p>`

	got := insertVerseExtBlocks(html, map[int]string{2: `<ark-ext-tags>X</ark-ext-tags>`})

	if !strings.Contains(got, `<ark-verse n="2"><code>2</code><ark-ext-tags>X</ark-ext-tags></ark-verse>`) {
		t.Errorf("block did not land inside verse 2:\n%s", got)
	}
	if !strings.Contains(got, `<ark-verse n="1"><code>1</code></ark-verse>`) ||
		!strings.Contains(got, `<ark-verse n="3"><code>3</code></ark-verse>`) {
		t.Errorf("unmapped verses were disturbed:\n%s", got)
	}
	if same := insertVerseExtBlocks(html, nil); same != html {
		t.Errorf("empty map changed the html:\n%s", same)
	}
}

// setupBibleView wires the content-view harness for a bible file: registers
// the strategy (this harness never runs db.Open), indexes the fixture, and
// returns the server, db and path.
func setupBibleView(t *testing.T) (*Server, *DB, string) {
	t.Helper()
	srv, db, _ := setupContentView(t)
	if err := db.indexer.fts.AddChunker(bibleStrategy, bibleChunker{}); err != nil {
		t.Fatalf("register bible strategy: %v", err)
	}
	path := filepath.Join(db.dbPath, "zechariah.md")
	if err := os.WriteFile(path, []byte(bibleFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.indexer.AddFile(path, bibleStrategy); err != nil {
		t.Fatalf("index bible file: %v", err)
	}
	return srv, db, path
}

// routeExt authors an @ext line in a source file and indexes it, so the
// routing resolves the way an indexed declaration does.
func routeExt(t *testing.T, db *DB, target, tag, value string) {
	t.Helper()
	src := filepath.Join(db.dbPath, "notes-"+tag+".md")
	if err := os.WriteFile(src, []byte("@ext: "+target+" @"+tag+": "+value+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.indexer.AddFile(src, "line"); err != nil {
		t.Fatalf("index @ext source: %v", err)
	}
	if err := db.extmap.Rebuild(db); err != nil {
		t.Fatalf("ExtMap.Rebuild: %v", err)
	}
}

// TestBibleRender_RoutingAtItsVerse — test-BibleRender.md "a verse-targeted
// routing renders at its verse": the point of #41. R3182.
func TestBibleRender_RoutingAtItsVerse(t *testing.T) {
	srv, db, path := setupBibleView(t)
	routeExt(t, db, path+":2.1", "note", "at verse one")

	html := getContentView(t, srv, path, "")

	i := strings.Index(html, `<ark-verse n="1">`)
	if i < 0 {
		t.Fatalf("no verse 1 element:\n%s", html)
	}
	end := strings.Index(html[i:], "</ark-verse>")
	if end < 0 {
		t.Fatalf("verse 1 element unterminated:\n%s", html)
	}
	if inside := html[i : i+end]; !strings.Contains(inside, "at verse one") {
		t.Errorf("routed tag is not inside verse 1; verse element was:\n%s\nfull:\n%s", inside, html)
	}
}

// TestBibleRender_RoutingWithoutVerseStaysAtParagraph — test-BibleRender.md "a
// routing with no verse stays at the paragraph". R3182.
func TestBibleRender_RoutingWithoutVerseStaysAtParagraph(t *testing.T) {
	srv, db, path := setupBibleView(t)
	routeExt(t, db, path, "note", "whole file")

	html := getContentView(t, srv, path, "")

	if !strings.Contains(html, "whole file") {
		t.Fatalf("bare-target routing was dropped entirely:\n%s", html)
	}
	// It must not have been placed inside any verse element.
	for _, seg := range strings.Split(html, `<ark-verse `)[1:] {
		if end := strings.Index(seg, "</ark-verse>"); end >= 0 &&
			strings.Contains(seg[:end], "whole file") {
			t.Errorf("bare-target routing was placed inside a verse:\n%s", seg[:end])
		}
	}
}
