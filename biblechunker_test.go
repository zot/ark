package ark

// CRC: crc-BibleChunker.md | Test: test-BibleChunker.md | R3173, R3174, R3175, R3176, R3178

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/zot/microfts2"
)

// bibleChunksOf runs the chunker and collects copies (the yielded buffers
// are documented as reusable, so a test that retains them must copy).
func bibleChunksOf(t *testing.T, src string) []microfts2.Chunk {
	t.Helper()
	var out []microfts2.Chunk
	err := bibleChunker{}.Chunks("/kjv/books/zechariah.md", []byte(src), func(c microfts2.Chunk) bool {
		out = append(out, microfts2.Chunk{
			Range:   append([]byte(nil), c.Range...),
			Locator: append([]byte(nil), c.Locator...),
			Content: append([]byte(nil), c.Content...),
			Attrs:   microfts2.CopyPairs(c.Attrs),
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

// Lines:
//
//	1 # Zechariah
//	2
//	3 ## Zechariah Chapter 2
//	4 `1` first verse. `2` second verse.
//	5
//	6 `3` third verse, its own paragraph.
//	7
//	8 ## Zechariah Chapter 3
//	9 `1` chapter three opens.
const bibleFixture = "# Zechariah\n" +
	"\n" +
	"## Zechariah Chapter 2\n" +
	"`1` first verse. `2` second verse.\n" +
	"\n" +
	"`3` third verse, its own paragraph.\n" +
	"\n" +
	"## Zechariah Chapter 3\n" +
	"`1` chapter three opens.\n"

// TestBibleChunker_ParagraphBlocks — test-BibleChunker.md "one chunk per
// paragraph, with line ranges". R3173.
func TestBibleChunker_ParagraphBlocks(t *testing.T) {
	chunks := bibleChunksOf(t, bibleFixture)

	wantRanges := []string{"1-1", "3-4", "6-6", "8-9"}
	if len(chunks) != len(wantRanges) {
		t.Fatalf("got %d chunks, want %d: %s", len(chunks), len(wantRanges), bibleDump(chunks))
	}
	for i, want := range wantRanges {
		if got := string(chunks[i].Range); got != want {
			t.Errorf("chunk %d Range = %q, want %q", i, got, want)
		}
	}
	// The heading stays with the paragraph it introduces.
	if got := string(chunks[1].Content); got != "## Zechariah Chapter 2\n`1` first verse. `2` second verse.\n" {
		t.Errorf("chunk 1 content = %q", got)
	}
}

// TestBibleChunker_ChapterCarriedForward — test-BibleChunker.md "chapter is
// carried forward and absent before the first heading". R3175.
func TestBibleChunker_ChapterCarriedForward(t *testing.T) {
	chunks := bibleChunksOf(t, bibleFixture)

	if _, ok := bibleAttr(chunks[0], "chapter"); ok {
		t.Error("title chunk carries a chapter; none has been declared yet")
	}
	for _, i := range []int{1, 2} {
		if got, ok := bibleAttr(chunks[i], "chapter"); !ok || got != "2" {
			t.Errorf("chunk %d chapter = %q (present=%v), want 2", i, got, ok)
		}
	}
	if got, ok := bibleAttr(chunks[3], "chapter"); !ok || got != "3" {
		t.Errorf("chunk 3 chapter = %q (present=%v), want 3", got, ok)
	}
}

// TestBibleChunker_VerseSpan — test-BibleChunker.md "verse span covers first
// through last mark". R3176.
func TestBibleChunker_VerseSpan(t *testing.T) {
	chunks := bibleChunksOf(t, bibleFixture)

	if got, ok := bibleAttr(chunks[1], "verses"); !ok || got != "1-2" {
		t.Errorf("multi-mark block verses = %q (present=%v), want 1-2", got, ok)
	}
	if got, ok := bibleAttr(chunks[2], "verses"); !ok || got != "3" {
		t.Errorf("single-mark block verses = %q (present=%v), want 3", got, ok)
	}
	if got, ok := bibleAttr(chunks[0], "verses"); ok {
		t.Errorf("markless block carries verses = %q; want absent", got)
	}
}

// TestBibleChunker_OnlyBacktickedIntegersAreMarks — test-BibleChunker.md
// "only backtick-wrapped integers are verse marks". R3174.
func TestBibleChunker_OnlyBacktickedIntegersAreMarks(t *testing.T) {
	chunks := bibleChunksOf(t, "## Book Chapter 1\n`5` in the 400 year, `xii` of the reign, 70 elders.\n")

	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	if got, ok := bibleAttr(chunks[0], "verses"); !ok || got != "5" {
		t.Errorf("verses = %q (present=%v), want 5 — bare digits and non-integer spans are not marks", got, ok)
	}
}

// TestBibleChunker_HeadingAtAnyLevel — test-BibleChunker.md "chapter heading
// is recognized at any ATX level". R3175.
func TestBibleChunker_HeadingAtAnyLevel(t *testing.T) {
	for _, hashes := range []string{"##", "###", "#"} {
		chunks := bibleChunksOf(t, hashes+" Zechariah Chapter 2\n`1` a verse.\n")
		if len(chunks) != 1 {
			t.Fatalf("%s: got %d chunks, want 1", hashes, len(chunks))
		}
		if got, ok := bibleAttr(chunks[0], "chapter"); !ok || got != "2" {
			t.Errorf("%s heading: chapter = %q (present=%v), want 2", hashes, got, ok)
		}
	}
}

// TestBibleChunker_LocatorCoversContent — test-BibleChunker.md "locator byte
// range covers the chunk content exactly". R3173.
func TestBibleChunker_LocatorCoversContent(t *testing.T) {
	src := bibleFixture
	for i, c := range bibleChunksOf(t, src) {
		start, end, ok := microfts2.DecodeByteRangeLocator(c.Locator)
		if !ok {
			t.Fatalf("chunk %d: undecodable locator %q", i, c.Locator)
		}
		if start < 0 || end > len(src) || start > end {
			t.Fatalf("chunk %d: locator [%d,%d) out of bounds for %d bytes", i, start, end, len(src))
		}
		if got := src[start:end]; got != string(c.Content) {
			t.Errorf("chunk %d: locator slices %q, content is %q", i, got, c.Content)
		}
	}
}

// TestBibleChunker_NoTrailingNewline — test-BibleChunker.md "a final line
// without a trailing newline still chunks". R3173.
func TestBibleChunker_NoTrailingNewline(t *testing.T) {
	src := "## Book Chapter 1\n`1` an unterminated final line."
	chunks := bibleChunksOf(t, src)

	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1: %s", len(chunks), bibleDump(chunks))
	}
	if string(chunks[0].Content) != src {
		t.Errorf("content = %q, want the whole source", chunks[0].Content)
	}
	if _, end, _ := microfts2.DecodeByteRangeLocator(chunks[0].Locator); end != len(src) {
		t.Errorf("locator ends at %d, want EOF %d", end, len(src))
	}
}

// TestBibleChunker_BlankRuns — test-BibleChunker.md "runs of blank lines
// produce no empty chunks". R3173.
func TestBibleChunker_BlankRuns(t *testing.T) {
	chunks := bibleChunksOf(t, "\n\n`1` first.\n\n\n\n`2` second.\n\n\n")

	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2: %s", len(chunks), bibleDump(chunks))
	}
	for i, c := range chunks {
		if len(c.Content) == 0 {
			t.Errorf("chunk %d is empty", i)
		}
	}
}

// TestBibleChunker_ReadOnly — test-BibleChunker.md "the strategy is
// read-only". R3178.
func TestBibleChunker_ReadOnly(t *testing.T) {
	if (bibleChunker{}).IsWritable() {
		t.Error("bible strategy reports writable; annotation would be inserted into scripture")
	}
	if cs := (bibleChunker{}).CommentSyntax(); cs != "" {
		t.Errorf("CommentSyntax = %q, want empty", cs)
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
// persisted, and the CHAPTER.VERSE resolution slice reads these back from
// AllChunks. (No R3179 ref here on purpose: this test asserts that
// requirement's *precondition*, not the requirement, and a leading ref would
// report it implemented while no resolution code exists.)
// R3175, R3176.
func TestBibleChunker_AttrsSurviveIndexing(t *testing.T) {
	_, db := setupRecall(t)
	// Load-bearing, despite db.Open registering `bible` in production: this
	// harness builds the index through testIndexer, which registers only the
	// line chunker and never runs Open. Without this, AddFile below fails with
	// "unknown chunking strategy: bible".
	if err := db.indexer.fts.AddChunker(bibleStrategy, bibleChunker{}); err != nil {
		t.Fatalf("register bible strategy: %v", err)
	}

	path := filepath.Join(db.dbPath, "zechariah.md")
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
