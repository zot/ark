package ark

// CRC: crc-BibleRenderer.md, crc-Server.md | Test: test-BibleRender.md | R3181, R3182, R3183, R3229, R3232

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bibleRenderBlock is one prose paragraph as the publisher ships it: two
// verse-num spans inside their hBBCCCVVV wrappers, an inline onclick handler,
// a crossref popup into a sibling file, and a footnote anchor.
const bibleRenderBlock = `<p class="normal" id="v38002001"><span class="h38002001">` +
	`<span class="verse-num"><a class="pop-link" onclick="return nav.show('tc',event);">1</a></span>` +
	`I lifted up mine eyes, and there were 400 men. ` +
	`<span class="crossref"><a href="b38.crossrefs.xhtml#rr38002001.a">a</a></span></span>` +
	`<span class="h38002002"><span id="v38002002" class="verse-num"><a class="pop-link">2</a></span>` +
	`Then said I.<span class="footnote"><a href="b38.footnotes.xhtml#f1">[1]</a></span></span></p>`

// TestBibleRender_VerseSpansBecomeElements — test-BibleRender.md "verse-num
// spans become verse elements". R3181.
func TestBibleRender_VerseSpansBecomeElements(t *testing.T) {
	html := renderBibleXHTML([]byte(bibleRenderBlock))

	for _, want := range []string{`<ark-verse n="1" c="2">1</ark-verse>`, `<ark-verse n="2" c="2">2</ark-verse>`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s in:\n%s", want, html)
		}
	}
	if !strings.Contains(html, "I lifted up mine eyes") || !strings.Contains(html, "Then said I.") {
		t.Errorf("prose did not survive:\n%s", html)
	}
	// Both verses sit inside the one paragraph the publisher set them in.
	if !strings.HasPrefix(html, `<p class="ark-bible-p">`) || !strings.HasSuffix(html, `</p>`) {
		t.Errorf("paragraph structure lost:\n%s", html)
	}
}

// TestBibleRender_ApparatusAndHandlersStripped — test-BibleRender.md
// "apparatus, scripts, and handlers are stripped". R3183.
func TestBibleRender_ApparatusAndHandlersStripped(t *testing.T) {
	withScript := `<section><script src="../Scripts/nav.js"></script>` + bibleRenderBlock +
		`<p class="normal"><span class="book-name">Zechariah</span><span class="chapter-num">2</span>tail prose.</p></section>`
	html := renderBibleXHTML([]byte(withScript))

	for _, forbidden := range []string{"onclick", "<script", "nav.js", "pop-link", "href=", "crossrefs.xhtml", "footnotes.xhtml", "[1]"} {
		if strings.Contains(html, forbidden) {
			t.Errorf("publisher markup %q survived into the page:\n%s", forbidden, html)
		}
	}
	// The apparatus text goes with it — a crossref letter and a book label are
	// not scripture. The label is still *known* (it names the book for the
	// running head, R3233), so what must not survive is its appearance in the
	// reading flow, not its presence as an attribute.
	if strings.Contains(stripTagsForTest(html), "Zechariah") {
		t.Errorf("book-name text was printed in the flow:\n%s", html)
	}
	if !strings.Contains(html, `b="Zechariah"`) {
		t.Errorf("book label was dropped entirely; the running head needs it:\n%s", html)
	}
	if !strings.Contains(html, "tail prose.") || !strings.Contains(html, "Then said I.") {
		t.Errorf("prose was lost along with the apparatus:\n%s", html)
	}
}

// TestBibleRender_OnlyVerseNumSpansAreVerses — test-BibleRender.md "only
// verse-num spans are verses": recognition is over the parsed document, so a
// number in the prose is not a verse. R3183.
func TestBibleRender_OnlyVerseNumSpansAreVerses(t *testing.T) {
	html := renderBibleXHTML([]byte(bibleRenderBlock))

	if n := strings.Count(html, "<ark-verse"); n != 2 {
		t.Errorf("want exactly 2 verse elements, got %d:\n%s", n, html)
	}
	if !strings.Contains(html, "there were 400 men") {
		t.Errorf("a bare number in the prose was disturbed:\n%s", html)
	}
	if strings.Contains(html, `<ark-verse n="400"`) {
		t.Errorf("a prose number became a verse:\n%s", html)
	}
}

// TestBibleRender_NumberlessVerseAnchored — test-BibleRender.md "a chapter's
// first verse is addressable though it has no number". The edition prints a
// chapter-num drop cap in place of verse 1's number, so without this verse 1
// of every chapter is the one verse nothing can address, and a routing aimed
// at it resolves and then has nowhere to render. R3222.
func TestBibleRender_NumberlessVerseAnchored(t *testing.T) {
	// A chapter opening exactly as the ESV ships it: the <p> carries no id,
	// verse 1's h-span holds a book-name and a chapter-num and no verse-num,
	// and verse 2 has an ordinary verse-num.
	opening := `<p class="no-indent"><span class="h01001001"> ` +
		`<span class="book-name"><a href="b01.main.xhtml">GENESIS</a></span>` +
		`<span class="chapter-num"> 1 </span>In the beginning. </span>` +
		`<span class="h01001002"><span id="v01001002" class="verse-num"><a>2</a></span>The earth was without form. </span></p>`

	html := renderBibleXHTML([]byte(opening))

	if !strings.Contains(html, `<ark-verse n="1" c="1"></ark-verse>`) {
		t.Errorf("verse 1 got no anchor; it is unaddressable:\n%s", html)
	}
	if !strings.Contains(html, `<ark-verse n="2" c="1">2</ark-verse>`) {
		t.Errorf("verse 2 lost its numbered element:\n%s", html)
	}
	if n := strings.Count(html, `<ark-verse n="1"`); n != 1 {
		t.Errorf("verse 1 anchored %d times, want exactly 1:\n%s", n, html)
	}
	// The anchor is inside the paragraph, where the verse's text begins.
	if strings.Index(html, `<ark-verse n="1"`) < strings.Index(html, "<p ") {
		t.Errorf("anchor escaped its paragraph:\n%s", html)
	}
	// A verse that does carry a number gets no extra empty anchor.
	if strings.Contains(html, `<ark-verse n="2" c="1"></ark-verse>`) {
		t.Errorf("a numbered verse was double-anchored:\n%s", html)
	}

	// The identity may be repeated by the <p> and its inner span; anchoring
	// must still happen once.
	repeated := `<p class="normal" id="v01002001"><span class="h01002001">` +
		`<span class="chapter-num"> 2 </span>Thus the heavens were finished. </span></p>`
	got := renderBibleXHTML([]byte(repeated))
	if n := strings.Count(got, `<ark-verse n="1"`); n != 1 {
		t.Errorf("repeated identity anchored %d times, want 1:\n%s", n, got)
	}
}

// TestBibleRender_HeadingsNotRendered — R3213: an editorial heading is dropped
// from the page as well as from the index, so the two never disagree.
func TestBibleRender_HeadingsNotRendered(t *testing.T) {
	html := renderBibleXHTML([]byte(`<header><p class="heading">The Vision</p></header>` + bibleRenderBlock))

	if strings.Contains(html, "The Vision") {
		t.Errorf("editorial heading reached the page:\n%s", html)
	}
}

// TestBibleLineSlice — the render side reads the file, not the chunk, because
// a bible chunk's stored content is stripped prose. R3181.
func TestBibleLineSlice(t *testing.T) {
	got := bibleLineSlice([]byte(biblePoetryFixture), "3-5")
	if !strings.Contains(string(got), "Blessed is the man") ||
		!strings.Contains(string(got), "law of the LORD") {
		t.Errorf("3-5 did not slice the whole stanza:\n%s", got)
	}
	if strings.Contains(string(got), "He is like a tree") {
		t.Errorf("3-5 overran into the next stanza:\n%s", got)
	}
	for _, bad := range []string{"", "5", "abc", "3-999", "0-2", "5-3"} {
		if bibleLineSlice([]byte(biblePoetryFixture), bad) != nil {
			t.Errorf("label %q sliced something; want nil so the caller falls back", bad)
		}
	}
}

// TestInsertVerseExtBlocks — test-BibleRender.md "ext blocks land in their own
// verse". R3182.
func TestInsertVerseExtBlocks(t *testing.T) {
	html := `<p class="ark-bible-p">` +
		`<ark-verse n="1">1</ark-verse> one ` +
		`<ark-verse n="2">2</ark-verse> two ` +
		`<ark-verse n="3">3</ark-verse> three</p>`

	got := insertVerseExtBlocks(html, map[int]string{2: `<ark-ext-tags>X</ark-ext-tags>`})

	if !strings.Contains(got, `<ark-verse n="2">2<ark-ext-tags>X</ark-ext-tags></ark-verse>`) {
		t.Errorf("block did not land inside verse 2:\n%s", got)
	}
	if !strings.Contains(got, `<ark-verse n="1">1</ark-verse>`) ||
		!strings.Contains(got, `<ark-verse n="3">3</ark-verse>`) {
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
	db.bibleChunker = newBibleChunker(db)
	if err := db.indexer.fts.AddChunker(bibleStrategy, db.bibleChunker); err != nil {
		t.Fatalf("register bible strategy: %v", err)
	}
	db.indexer.bibleChunker = db.bibleChunker

	path := filepath.Join(db.dbPath, "b38.00.Zechariah.text.xhtml")
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

// TestBibleRender_ContentViewIntermediatesXHTML — the content view reaches the
// bible renderer at all. A bible file is XHTML, so the strategy's content type
// is "text"; without the isBible arm in handleContentView it would render as
// an escaped <pre> blob and no verse would ever be addressable. R3181, R3183.
func TestBibleRender_ContentViewIntermediatesXHTML(t *testing.T) {
	srv, _, path := setupBibleView(t)

	html := getContentView(t, srv, path, "")

	if !strings.Contains(html, `<ark-verse n="1"`) {
		t.Errorf("content view emitted no verse elements:\n%s", html)
	}
	if strings.Contains(html, "onclick") || strings.Contains(html, "pop-link") {
		t.Errorf("publisher markup reached the content view:\n%s", html)
	}
	if strings.Contains(html, "&lt;p class=") {
		t.Errorf("the XHTML was escaped as text rather than intermediated:\n%s", html)
	}
}

// TestBibleRender_RangedViewIntermediates — the `?range=` single-chunk view
// takes the same path. It is the case most likely to regress: that view
// replaces cr.data with the chunk's *stored* text, which for a bible chunk is
// stripped prose with no verse marks in it, so the renderer must still reach
// the file's own markup for those lines. R3181.
func TestBibleRender_RangedViewIntermediates(t *testing.T) {
	srv, db, path := setupBibleView(t)

	chunks := db.AllChunks(path)
	if len(chunks) < 2 {
		t.Fatalf("fixture indexed %d chunks", len(chunks))
	}
	// chunks[1] is the paragraph spanning verses 1-2.
	html := getContentView(t, srv, path, "?range="+chunks[1].Range)

	if !strings.Contains(html, `<ark-verse n="2"`) {
		t.Errorf("ranged bible view lost its verse elements:\n%s", html)
	}
	if strings.Contains(html, "onclick") {
		t.Errorf("ranged bible view leaked publisher markup:\n%s", html)
	}
}

// TestBibleRender_RoutingAtItsVerse — test-BibleRender.md "a verse-targeted
// routing renders at its verse": the point of the feature. R3182.
func TestBibleRender_RoutingAtItsVerse(t *testing.T) {
	srv, db, path := setupBibleView(t)
	routeExt(t, db, path+":2.1", "note", "at verse one")

	html := getContentView(t, srv, path, "")

	i := strings.Index(html, `<ark-verse n="1"`)
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

// TestBibleRender_VerseCarriesChapter — a text file spans several chapters, so
// a verse number alone does not identify a verse within the page. R3229.
func TestBibleRender_VerseCarriesChapter(t *testing.T) {
	twoChapters := `<p class="normal" id="v01001003"><span class="h01001003"><span class="verse-num"><a>3</a></span>Let there be light.</span></p>` +
		`<p class="normal" id="v01002003"><span class="h01002003"><span class="verse-num"><a>3</a></span>And God blessed the seventh day.</span></p>`

	html := renderBibleXHTML([]byte(twoChapters))

	for _, want := range []string{`<ark-verse n="3" c="1">`, `<ark-verse n="3" c="2">`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s — the two verse 3s are indistinguishable:\n%s", want, html)
		}
	}
	// `n` must stay first: insertVerseExtBlocks finds a verse by scanning for
	// the literal `<ark-verse n="`, so the order is load-bearing.
	if strings.Contains(html, `<ark-verse c=`) {
		t.Errorf("chapter was written before the verse number, which breaks insertVerseExtBlocks:\n%s", html)
	}
}

// TestBibleRender_VerseChapterSurvivesExtInsertion — the chapter attribute must
// not break the routing placement that scans for the verse tag. R3229, R3182.
func TestBibleRender_VerseChapterSurvivesExtInsertion(t *testing.T) {
	html := `<p class="ark-bible-p">` +
		`<ark-verse n="1" c="2">1</ark-verse> one ` +
		`<ark-verse n="2" c="2">2</ark-verse> two</p>`

	got := insertVerseExtBlocks(html, map[int]string{2: `<ark-ext-tags>X</ark-ext-tags>`})

	if !strings.Contains(got, `<ark-verse n="2" c="2">2<ark-ext-tags>X</ark-ext-tags></ark-verse>`) {
		t.Errorf("routing was not placed inside the chaptered verse:\n%s", got)
	}
	if !strings.Contains(got, `<ark-verse n="1" c="2">1</ark-verse>`) {
		t.Errorf("an unrouted verse was disturbed:\n%s", got)
	}
}

// stripTagsForTest removes every tag, leaving only the text a reader sees —
// so an assertion can tell "printed in the flow" from "carried as an
// attribute".
func stripTagsForTest(html string) string {
	var b strings.Builder
	depth := 0
	for _, r := range html {
		switch {
		case r == '<':
			depth++
		case r == '>' && depth > 0:
			depth--
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// TestBibleRender_ChapterNumberShown — the chapter number is the one piece of
// the publisher's apparatus the *page* keeps, because it is the structure a
// reader navigates by rather than reference apparatus they can lose. The
// chunker still strips it, so the index is unaffected. R3232.
func TestBibleRender_ChapterNumberShown(t *testing.T) {
	opening := `<p class="no-indent"><span class="h01002001"> ` +
		`<span class="book-name"><a href="b01.main.xhtml">GENESIS</a></span>` +
		`<span class="chapter-num"> 2 </span>Thus the heavens were finished. </span></p>`

	html := renderBibleXHTML([]byte(opening))

	if !strings.Contains(html, `<ark-chapter n="2" b="GENESIS">2</ark-chapter>`) {
		t.Errorf("chapter marker missing or malformed:\n%s", html)
	}
	// The number the edition prints is the element's text, trimmed.
	if strings.Contains(html, "> 2 <") {
		t.Errorf("chapter number kept the publisher's padding:\n%s", html)
	}
	// The book label is not printed in the flow — only carried.
	if strings.Contains(stripTagsForTest(html), "GENESIS") {
		t.Errorf("book label was printed in the reading flow:\n%s", html)
	}

	// The chunker's view is unchanged: chunk text stays prose-only (R3211).
	chunks := bibleChunksOf(t, "<section epub:type=\"chapter\">\n"+opening+"\n</section>\n")
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	if got := string(chunks[0].Content); got != "Thus the heavens were finished." {
		t.Errorf("chunk text = %q; the chapter number must not reach the index", got)
	}
}
