package ark

// CRC: crc-BibleChunker.md | Test: test-BibleChunker.md | R3173, R3175, R3176, R3178, R3209, R3210, R3211, R3212, R3213, R3214, R3215, R3218, R3219, R3221, R3224, R3225

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zot/microfts2"
)

// bibleChunksOf runs the chunker and collects copies (the yielded buffers
// are documented as reusable, so a test that retains them must copy).
func bibleChunksOf(t *testing.T, src string) []microfts2.Chunk {
	t.Helper()
	var out []microfts2.Chunk
	c := &bibleChunker{}
	err := c.Chunks("/esv/OEBPS/Text/b38.00.Zechariah.text.xhtml", []byte(src), func(ch microfts2.Chunk) bool {
		out = append(out, microfts2.Chunk{
			Range:   append([]byte(nil), ch.Range...),
			Locator: append([]byte(nil), ch.Locator...),
			Content: append([]byte(nil), ch.Content...),
			Attrs:   microfts2.CopyPairs(ch.Attrs),
		})
		return true
	})
	if err != nil {
		t.Fatalf("Chunks: %v", err)
	}
	return out
}

func bibleAttr(c microfts2.Chunk, key string) (string, bool) {
	v, ok := microfts2.PairGet(c.Attrs, key)
	return string(v), ok
}

// bibleFixture is a trimmed ESV-shaped text file: one element per line, the
// publisher's `vBBCCCVVV` ids carrying identity, a pericope heading, apparatus
// spans around the prose, and a preamble paragraph that precedes any verse.
//
// Lines that become chunks:
//
//	5  preamble — prose, no verse-bearing id
//	7  chapter 2, verses 1-2 (one paragraph holding two verses)
//	8  chapter 2, verse 3
//	11 chapter 3, verse 1
const bibleFixture = `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<body>
<section epub:type="chapter">
<p class="normal">A publisher's note before chapter two.</p>
<header><p class="heading">The Vision of the Measuring Line</p></header>
<p class="no-indent" id="v38002001"><span class="h38002001"><span class="chapter-num">2</span><span class="verse-num"><a class="pop-link" onclick="return nav.show('tc',event);">1</a></span>First verse text. </span><span class="h38002002"><span id="v38002002" class="verse-num"><a class="pop-link" onclick="return nav.show('tc',event);">2</a></span>Second verse text. </span></p>
<p class="normal" id="v38002003"><span class="h38002003"><span class="verse-num"><a class="pop-link">3</a></span>Third verse, <span class="crossref"><small>&#160;</small><a href="b38.00.Zechariah.crossrefs.xhtml#rr38002003.a">a</a></span>its own paragraph.<span class="footnote"><a href="b38.00.Zechariah.footnotes.xhtml#f38002003.1">[1]</a></span> </span></p>
</section>
<section epub:type="chapter">
<p class="no-indent" id="v38003001"><span class="h38003001"><span class="book-name">Zechariah</span><span class="chapter-num">3</span><span class="verse-num"><a class="pop-link">1</a></span>Chapter three opens. </span></p>
</section>
</body>
</html>
`

// biblePoetryFixture is a trimmed Psalm: a stanza opening at
// `line-group-after-heading` and running through its `line`/`line-indent`
// paragraphs, then a second stanza.
const biblePoetryFixture = `<section epub:type="chapter">
<header><p class="heading">The Way of the Righteous</p></header>
<p class="line-group-after-heading" id="v19001001"><span class="h19001001"><span class="chapter-num">1</span><span class="verse-num"><a class="pop-link">1</a></span>Blessed is the man</span></p>
<p class="line-indent">who walks not in the counsel of the wicked;</p>
<p class="line" id="v19001002"><span class="h19001002"><span class="verse-num"><a class="pop-link">2</a></span>but his delight is in the law of the LORD,</span></p>
<p class="line-group" id="v19001003"><span class="h19001003"><span class="verse-num"><a class="pop-link">3</a></span>He is like a tree</span></p>
<p class="line-indent">planted by streams of water.</p>
</section>
`

// TestBibleChunker_ProseParagraphBlocks — test-BibleChunker.md "one chunk per
// prose paragraph, verses flow within it". R3173, R3176.
func TestBibleChunker_ProseParagraphBlocks(t *testing.T) {
	chunks := bibleChunksOf(t, bibleFixture)

	wantRanges := []string{"5-5", "7-7", "8-8", "11-11"}
	if len(chunks) != len(wantRanges) {
		t.Fatalf("got %d chunks, want %d: %s", len(chunks), len(wantRanges), bibleDump(chunks))
	}
	for i, want := range wantRanges {
		if got := string(chunks[i].Range); got != want {
			t.Errorf("chunk %d Range = %q, want %q", i, got, want)
		}
	}
	// The paragraph opening at verse 1 holds verses 1 and 2; the next
	// paragraph is a separate chunk — the case that motivates block chunking.
	if got, ok := bibleAttr(chunks[1], "verses"); !ok || got != "1-2" {
		t.Errorf("multi-verse paragraph verses = %q (present=%v), want 1-2", got, ok)
	}
	if got, ok := bibleAttr(chunks[2], "verses"); !ok || got != "3" {
		t.Errorf("following paragraph verses = %q (present=%v), want 3", got, ok)
	}
}

// TestBibleChunker_PoetryStanzaIsOneChunk — test-BibleChunker.md "a poetry
// stanza is one chunk". R3212.
func TestBibleChunker_PoetryStanzaIsOneChunk(t *testing.T) {
	chunks := bibleChunksOf(t, biblePoetryFixture)

	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2 (one per stanza, not one per line): %s", len(chunks), bibleDump(chunks))
	}
	if got := string(chunks[0].Range); got != "3-5" {
		t.Errorf("first stanza Range = %q, want 3-5 — the opener plus its line run", got)
	}
	if got := string(chunks[1].Range); got != "6-7" {
		t.Errorf("second stanza Range = %q, want 6-7", got)
	}
	if got, ok := bibleAttr(chunks[0], "verses"); !ok || got != "1-2" {
		t.Errorf("first stanza verses = %q (present=%v), want 1-2", got, ok)
	}
	// The whole stanza's lines are joined into one chunk's text.
	for _, want := range []string{"Blessed is the man", "counsel of the wicked", "law of the LORD"} {
		if !strings.Contains(string(chunks[0].Content), want) {
			t.Errorf("stanza content is missing %q: %q", want, chunks[0].Content)
		}
	}
}

// TestBibleChunker_ApparatusStripped — test-BibleChunker.md "chunk text is
// prose only — apparatus stripped". R3211.
func TestBibleChunker_ApparatusStripped(t *testing.T) {
	chunks := bibleChunksOf(t, bibleFixture)

	// chunks[3] carries all three of book-name, chapter-num and verse-num;
	// chunks[2] carries a crossref and a footnote.
	if got := string(chunks[3].Content); got != "Chapter three opens." {
		t.Errorf("content = %q, want the prose alone — no book name, chapter number, or verse number", got)
	}
	if got := string(chunks[2].Content); got != "Third verse, its own paragraph." {
		t.Errorf("content = %q, want the prose alone — no crossref letter or footnote marker", got)
	}
	// Not a digit anywhere: the numbers a reader sees are apparatus.
	for _, c := range chunks {
		for _, r := range string(c.Content) {
			if r >= '0' && r <= '9' {
				t.Errorf("chunk %q carries a digit; apparatus numbers must not reach the index", c.Content)
				break
			}
		}
	}
}

// TestBibleChunker_IdentityFromIds — test-BibleChunker.md "chapter and verses
// read from the ids". R3175, R3176, R3210.
func TestBibleChunker_IdentityFromIds(t *testing.T) {
	chunks := bibleChunksOf(t, bibleFixture)

	if got, ok := bibleAttr(chunks[0], "chapter"); ok {
		t.Errorf("preamble carries chapter = %q; no verse-bearing id precedes it", got)
	}
	if got, ok := bibleAttr(chunks[0], "verses"); ok {
		t.Errorf("preamble carries verses = %q; want absent", got)
	}
	for _, i := range []int{1, 2} {
		if got, ok := bibleAttr(chunks[i], "chapter"); !ok || got != "2" {
			t.Errorf("chunk %d chapter = %q (present=%v), want 2", i, got, ok)
		}
	}
	if got, ok := bibleAttr(chunks[3], "chapter"); !ok || got != "3" {
		t.Errorf("chunk 3 chapter = %q (present=%v), want 3", got, ok)
	}
	if got, ok := bibleAttr(chunks[3], "verses"); !ok || got != "1" {
		t.Errorf("single-verse block verses = %q (present=%v), want the bare number 1", got, ok)
	}
}

// TestBibleChunker_HeadingsDropped — test-BibleChunker.md "editorial headings
// are dropped". R3213.
func TestBibleChunker_HeadingsDropped(t *testing.T) {
	for _, c := range bibleChunksOf(t, bibleFixture) {
		if strings.Contains(string(c.Content), "Measuring Line") {
			t.Errorf("heading text reached a chunk: %q", c.Content)
		}
		if string(c.Range) == "6-6" {
			t.Errorf("the heading became its own chunk: %q", c.Content)
		}
	}
	// A Psalter division label carries verse 1's id, so keeping it would
	// shadow the real verse-1 stanza in resolution.
	chunks := bibleChunksOf(t, "<section epub:type=\"chapter\">\n<p class=\"psalm-book\" id=\"v19001001\">Book One</p>\n<p class=\"line-group\" id=\"v19001001\"><span class=\"h19001001\">Blessed is the man</span></p>\n</section>\n")
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1 — the division label must not become a chunk: %s", len(chunks), bibleDump(chunks))
	}
}

// bibleApparatusFixture mirrors what the ESV appends to a text file after its
// scripture: the footnote and cross-reference popups in a hidden section, and
// the navigation templates in a hidden div. Both sit outside any chapter
// section, which is what disqualifies them.
const bibleApparatusFixture = `<section epub:type="chapter">
<p class="no-indent" id="v38002001"><span class="h38002001"><span class="verse-num"><a class="pop-link">1</a></span>Real scripture.</span></p>
</section>
<section class="hidden">
<aside epub:type="footnote">
<p class="note">Zech. 2:1 [1] 2:1 Or a measuring cord</p>
</aside>
<aside epub:type="footnote">
<p class="crossref"><span><span id="r38002001.a" class="crossref-popup">2:1</span></span></p>
</aside>
</section>
<div class="hide">
<p class="nav-header">ESV</p>
<p class="nav">Matthew &#8226; Mark &#8226; Luke &#8226; John</p>
</div>
`

// TestBibleChunker_ApparatusOutsideChapterSection — test-BibleChunker.md "only
// blocks inside a chapter section are chunked". The apparatus a text file
// carries alongside its scripture must not reach the index: on the ESV corpus
// it was 46% of all chunks before this rule. R3224.
func TestBibleChunker_ApparatusOutsideChapterSection(t *testing.T) {
	chunks := bibleChunksOf(t, bibleApparatusFixture)

	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1 — only the scripture block qualifies: %s",
			len(chunks), bibleDump(chunks))
	}
	if got := string(chunks[0].Content); !strings.Contains(got, "Real scripture") {
		t.Errorf("kept the wrong block: %q", got)
	}
	for _, c := range chunks {
		for _, leak := range []string{"measuring cord", "Matthew", "ESV"} {
			if strings.Contains(string(c.Content), leak) {
				t.Errorf("apparatus reached a chunk (%q): %q", leak, c.Content)
			}
		}
	}
}

// TestBibleChunker_ChapterSectionReopens — a file holds several chapter
// sections with apparatus between them, so leaving one section must not end
// the walk's willingness to chunk. R3224.
func TestBibleChunker_ChapterSectionReopens(t *testing.T) {
	src := bibleApparatusFixture + `<section epub:type="chapter">
<p class="no-indent" id="v38003001"><span class="h38003001"><span class="verse-num"><a class="pop-link">1</a></span>Later scripture.</span></p>
</section>
`
	chunks := bibleChunksOf(t, src)
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2 — the second chapter section still counts: %s",
			len(chunks), bibleDump(chunks))
	}
	if got := string(chunks[1].Content); !strings.Contains(got, "Later scripture") {
		t.Errorf("second chunk = %q, want the later scripture block", got)
	}
}

// TestBibleChunker_ChapterSectionWithCompoundEpubType — epub:type may carry
// several tokens; `chapter` is matched as one of them, not as the whole value.
// R3224.
func TestBibleChunker_ChapterSectionWithCompoundEpubType(t *testing.T) {
	src := `<section epub:type="bodymatter chapter">
<p class="no-indent" id="v38002001"><span class="h38002001"><span class="verse-num"><a class="pop-link">1</a></span>Compound type.</span></p>
</section>
`
	if n := len(bibleChunksOf(t, src)); n != 1 {
		t.Fatalf("got %d chunks, want 1 — `chapter` is a token of epub:type", n)
	}
}

// TestBibleChunker_IdentityIsNotTheScriptureTest — the containment rule is not
// interchangeable with "has a verse identity". A paragraph continuing a verse
// that opened earlier carries no id of its own, and is scripture. R3225.
func TestBibleChunker_IdentityIsNotTheScriptureTest(t *testing.T) {
	src := `<section epub:type="chapter">
<p class="normal">&#8220;You shall not boil a young goat in its mother's milk.</p>
<p class="psalm-title">A Psalm of David, when he fled from Absalom his son.</p>
</section>
`
	chunks := bibleChunksOf(t, src)
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2 — identity-less scripture still counts: %s",
			len(chunks), bibleDump(chunks))
	}
	for _, c := range chunks {
		if _, ok := bibleAttr(c, "chapter"); ok {
			t.Errorf("expected no chapter attribute on an identity-less block: %q", c.Content)
		}
	}
}

// TestBibleChunker_LineSpaceIsProse — `line-space` matches a `line` prefix but
// the edition styles it as a paragraph with a gap above, not as verse. Read as
// poetry it merges into the stanza above it, which is what happened to 150 of
// its 151 occurrences in the ESV. R3212.
func TestBibleChunker_LineSpaceIsProse(t *testing.T) {
	src := `<section epub:type="chapter">
<p class="line-group" id="v01001027"><span class="h01001027"><span class="verse-num"><a class="pop-link">27</a></span>So God created man in his own image.</span></p>
<p class="line-space" id="v01001028"><span class="h01001028"><span class="verse-num"><a class="pop-link">28</a></span>And God blessed them.</span></p>
</section>
`
	chunks := bibleChunksOf(t, src)
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2 — line-space opens its own prose block: %s",
			len(chunks), bibleDump(chunks))
	}
	if got := string(chunks[0].Content); strings.Contains(got, "blessed them") {
		t.Errorf("the stanza swallowed the following prose paragraph: %q", got)
	}
	if got := string(chunks[1].Content); !strings.Contains(got, "blessed them") {
		t.Errorf("second chunk = %q, want the line-space paragraph", got)
	}
}

// TestBibleChunker_OnlyTextXhtml — test-BibleChunker.md "only *.text.xhtml is
// handled": the sibling apparatus files are not classified bible, so the
// chunker never receives them. R3209.
func TestBibleChunker_OnlyTextXhtml(t *testing.T) {
	cfg := &Config{}
	per := map[string]string{"**/*.text.xhtml": bibleStrategy}

	if got := cfg.StrategyForFile("OEBPS/Text/b01.00.Genesis.text.xhtml", per); got != bibleStrategy {
		t.Errorf("text file strategy = %q, want %q", got, bibleStrategy)
	}
	for _, name := range []string{"main", "crossrefs", "footnotes", "resources"} {
		rel := "OEBPS/Text/b01.00.Genesis." + name + ".xhtml"
		if got := cfg.StrategyForFile(rel, per); got == bibleStrategy {
			t.Errorf("%s is classified %q; only *.text.xhtml is scripture", rel, got)
		}
	}
}

// TestBibleChunker_ReadOnly — test-BibleChunker.md "the strategy is
// read-only". R3178.
func TestBibleChunker_ReadOnly(t *testing.T) {
	c := &bibleChunker{}
	if c.IsWritable() {
		t.Error("bible strategy reports writable; annotation would be inserted into scripture")
	}
	if cs := c.CommentSyntax(); cs != "" {
		t.Errorf("CommentSyntax = %q, want empty", cs)
	}
}

// TestBibleBookName — test-BibleChunker.md "book-index records are written":
// the name half, which is pure. R3215.
func TestBibleBookName(t *testing.T) {
	cases := map[string]string{
		"/esv/OEBPS/Text/b43.02.John.text.xhtml":            "John",
		"/esv/OEBPS/Text/b09.01.1-Samuel.text.xhtml":        "1 Samuel",
		"/esv/OEBPS/Text/b22.00.Song-of-Solomon.text.xhtml": "Song of Solomon",
		"/esv/OEBPS/Text/b19.12.Psalm.text.xhtml":           "Psalm",
	}
	for path, want := range cases {
		if got := bibleBookName(bibleFileToken(path)); got != want {
			t.Errorf("%s → %q, want %q", path, got, want)
		}
	}
	if got := bibleFileToken("/esv/OEBPS/Text/b43.02.John.crossrefs.xhtml"); got != "" {
		t.Errorf("a non-text sibling yielded the token %q; want none", got)
	}
}

// setupBibleDB wires a test DB the way DB.Open does for the bible strategy:
// the chunker bound to the DB, registered with microfts2, mirrored into
// chunkerByName, and reachable by the indexer's flush.
func setupBibleDB(t *testing.T) *DB {
	t.Helper()
	_, db := setupRecall(t)
	// Load-bearing: this harness builds the index through testIndexer, which
	// never runs Open — the call that registers `bible` in production.
	db.bibleChunker = newBibleChunker(db)
	if err := db.indexer.fts.AddChunker(bibleStrategy, db.bibleChunker); err != nil {
		t.Fatalf("register bible strategy: %v", err)
	}
	if db.chunkerByName == nil {
		db.chunkerByName = map[string]any{}
	}
	db.chunkerByName[bibleStrategy] = db.bibleChunker
	db.indexer.bibleChunker = db.bibleChunker
	db.config.Sources = []Source{{Dir: db.dbPath, Strategies: map[string]string{"**/*.text.xhtml": bibleStrategy}}}
	return db
}

// TestBibleChunker_BookIndexWritten — test-BibleChunker.md "book-index records
// are written, one per chapter". R3214, R3215.
func TestBibleChunker_BookIndexWritten(t *testing.T) {
	db := setupBibleDB(t)

	path := filepath.Join(db.dbPath, "b38.00.1-Samuel.text.xhtml")
	if err := os.WriteFile(path, []byte(bibleFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.indexer.AddFile(path, bibleStrategy); err != nil {
		t.Fatalf("index: %v", err)
	}

	// The fixture holds chapters 2 and 3; the hyphen in the filename token
	// becomes a space in the key.
	for _, chapter := range []int{2, 3} {
		got, err := db.store.ReadBookIndex(db.dbPath, "1 Samuel", chapter)
		if err != nil {
			t.Fatalf("ReadBookIndex(%d): %v", chapter, err)
		}
		if got != path {
			t.Errorf("chapter %d → %q, want %q", chapter, got, path)
		}
	}
	// Chapter 1 is not in the file, so it has no record.
	if got, _ := db.store.ReadBookIndex(db.dbPath, "1 Samuel", 1); got != "" {
		t.Errorf("chapter 1 → %q, want no record — the file holds no chapter 1", got)
	}
	// The raw filename token is not the address form.
	if got, _ := db.store.ReadBookIndex(db.dbPath, "1-Samuel", 2); got != "" {
		t.Errorf("hyphenated token resolved to %q; the key holds the spaced name", got)
	}
}

// TestBibleChunker_ActivateForSource — test-BibleChunker.md "ActivateForSource
// registers the entry and runs the guard". R3218, R3219.
func TestBibleChunker_ActivateForSource(t *testing.T) {
	dir := t.TempDir()
	c := &bibleChunker{}

	registered := map[string]string{}
	register := func(pattern, strategy string) error {
		registered[pattern] = strategy
		return nil
	}

	if err := c.ActivateForSource(&Source{Dir: dir}, register); err != nil {
		t.Fatalf("ActivateForSource on a clean source: %v", err)
	}
	want := filepath.Join(dir, "BIBLE") + "/**"
	if registered[want] != bibleStrategy {
		t.Fatalf("registered %v, want %q → %q", registered, want, bibleStrategy)
	}
	// The entry is the absolute form and is confined to its own source.
	m := &Matcher{Dotfiles: true}
	if !m.Match(want, filepath.Join(dir, "BIBLE", "1 Samuel"), "", false) {
		t.Errorf("%q does not match a virtual book address under its source", want)
	}
	if m.Match(want, "/elsewhere/BIBLE/John", "", false) {
		t.Errorf("%q matches another source's path; the prefix is what makes a global entry safe", want)
	}

	// A real BIBLE path collides with the reserved namespace.
	collide := t.TempDir()
	if err := os.Mkdir(filepath.Join(collide, "BIBLE"), 0o755); err != nil {
		t.Fatal(err)
	}
	registered = map[string]string{}
	err := c.ActivateForSource(&Source{Dir: collide}, register)
	if err == nil {
		t.Fatal("a real BIBLE path activated silently; want an error naming the collision")
	}
	if !strings.Contains(err.Error(), "BIBLE") {
		t.Errorf("error %q does not name the colliding path", err)
	}
	if len(registered) != 0 {
		t.Errorf("registered %v despite the collision; want nothing", registered)
	}
}

// TestBibleChunker_CollisionIsAnnouncedAndClears — test-BibleChunker.md "a
// colliding source is announced durably, and the announcement clears". R3219.
func TestBibleChunker_CollisionIsAnnouncedAndClears(t *testing.T) {
	db := setupBibleDB(t)
	reserved := filepath.Join(db.dbPath, "BIBLE")
	if err := os.Mkdir(reserved, 0o755); err != nil {
		t.Fatal(err)
	}

	sources := len(db.config.Sources)
	if err := db.activateSourceChunkers(db.config); err == nil {
		t.Fatal("a colliding source activated without error")
	}
	recs, err := db.store.ReadERecords()
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := recs[ECondSourceActivation]
	if !ok {
		t.Fatalf("no %s record; the failure would live only in the log: %v", ECondSourceActivation, recs)
	}
	if !strings.Contains(string(payload), db.dbPath) {
		t.Errorf("record %s does not name the offending source", payload)
	}
	// The source stays configured — dropping it would make DiffConfig report a
	// sources change on every boot and could trip the catastrophe check.
	if len(db.config.Sources) != sources {
		t.Errorf("the source was removed from the config (%d → %d)", sources, len(db.config.Sources))
	}

	// Fixing the collision clears the condition on the next config load, with
	// no dismissal step.
	if err := os.Remove(reserved); err != nil {
		t.Fatal(err)
	}
	if err := db.activateSourceChunkers(db.config); err != nil {
		t.Fatalf("activation after the fix: %v", err)
	}
	recs, _ = db.store.ReadERecords()
	if _, still := recs[ECondSourceActivation]; still {
		t.Errorf("%s survived the fix; a stale condition outlives its problem", ECondSourceActivation)
	}
}

// TestBibleChunker_ReconcileBookIndex — test-BibleChunker.md "a source that
// stops being scripture loses its book-index records". R3221.
func TestBibleChunker_ReconcileBookIndex(t *testing.T) {
	db := setupBibleDB(t)
	keep, drop := "/scripture/esv", "/scripture/kjv"

	for _, src := range []string{keep, drop} {
		if err := db.store.WriteBookIndex(src, "John", 3, src+"/John.text.xhtml"); err != nil {
			t.Fatal(err)
		}
	}

	if err := db.bibleChunker.ReconcileBookIndex([]*Source{{Dir: keep}}); err != nil {
		t.Fatalf("ReconcileBookIndex: %v", err)
	}
	if got, _ := db.store.ReadBookIndex(keep, "John", 3); got == "" {
		t.Error("an active source lost its records")
	}
	if got, _ := db.store.ReadBookIndex(drop, "John", 3); got != "" {
		t.Errorf("a source that no longer declares the strategy kept %q", got)
	}

	// The empty case is the one that matters: a removed source gets no
	// per-source hook call, so nothing else would ever notice its records.
	if err := db.bibleChunker.ReconcileBookIndex(nil); err != nil {
		t.Fatalf("ReconcileBookIndex(nil): %v", err)
	}
	if got, _ := db.store.ReadBookIndex(keep, "John", 3); got != "" {
		t.Errorf("records survived a config with no bible source at all: %q", got)
	}
}

func bibleDump(chunks []microfts2.Chunk) string {
	s := ""
	for i, c := range chunks {
		s += fmt.Sprintf("\n  [%d] %s %s", i, c.Range, c.Content)
	}
	return s
}

// TestBibleChunker_AttrsSurviveIndexing — test-BibleChunker.md "attributes
// survive indexing": the pure tests above would all pass even if nothing
// persisted, and resolution (R3179) reads these back from AllChunks.
// R3175, R3176.
func TestBibleChunker_AttrsSurviveIndexing(t *testing.T) {
	db := setupBibleDB(t)

	path := filepath.Join(db.dbPath, "b38.00.Zechariah.text.xhtml")
	if err := os.WriteFile(path, []byte(bibleFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.indexer.AddFile(path, bibleStrategy); err != nil {
		t.Fatalf("index: %v", err)
	}

	all := db.AllChunks(path)
	if len(all) != 4 {
		t.Fatalf("AllChunks returned %d chunks, want 4", len(all))
	}
	chapter, hasChapter := microfts2.PairGet(all[1].Attrs, "chapter")
	verses, hasVerses := microfts2.PairGet(all[1].Attrs, "verses")
	if !hasChapter || string(chapter) != "2" {
		t.Errorf("round-tripped chapter = %q (present=%v), want 2", chapter, hasChapter)
	}
	if !hasVerses || string(verses) != "1-2" {
		t.Errorf("round-tripped verses = %q (present=%v), want 1-2", verses, hasVerses)
	}
}
