package ark

// Test: crc-PDFChunker.md | R1661, R1662, R1663, R1664

import (
	"testing"

	"github.com/zot/microfts2"
)

// R1661: lines with non-whitespace text are kept.
// R1661: lines whose entire text is whitespace (space, tab, newline, NBSP) are dropped.
func TestFilterBlankLinesDropsWhitespaceOnly(t *testing.T) {
	lines := []pdfLine{
		{Text: "February 28, 2026", Y: 780},
		{Text: " ", Y: 765},          // ONLYOFFICE-style blank separator
		{Text: "Dear Team,", Y: 750},
		{Text: "\t  \n", Y: 735},     // mixed whitespace
		{Text: "Body line", Y: 720},
		{Text: "", Y: 705},           // empty
	}
	got := filterBlankLines(lines)
	if len(got) != 3 {
		t.Fatalf("expected 3 content lines, got %d", len(got))
	}
	want := []string{"February 28, 2026", "Dear Team,", "Body line"}
	for i, l := range got {
		if l.Text != want[i] {
			t.Errorf("line %d: expected %q, got %q", i, want[i], l.Text)
		}
	}
}

func TestFilterBlankLinesEmpty(t *testing.T) {
	if got := filterBlankLines(nil); len(got) != 0 {
		t.Errorf("nil input: expected empty result, got %v", got)
	}
	if got := filterBlankLines([]pdfLine{}); len(got) != 0 {
		t.Errorf("empty input: expected empty result, got %v", got)
	}
}

// R1663: with blank separators removed, paragraph gap exceeds 1.5× dominant
// spacing → buildPageChunks emits separate paragraph chunks.
func TestBuildPageChunksRecognizesParagraphsWithBlankSeparators(t *testing.T) {
	// Simulate ONLYOFFICE shape: body lines 15 pts apart, each paragraph
	// separated by a line of a single space at its own Y.
	lines := []pdfLine{
		{Text: "First paragraph line 1", X: 72, Y: 780, FontSize: 12, Height: 12, Width: 120},
		{Text: "First paragraph line 2", X: 72, Y: 765, FontSize: 12, Height: 12, Width: 120},
		{Text: " ", X: 72, Y: 750, FontSize: 12, Height: 12, Width: 5},
		{Text: "Second paragraph line 1", X: 72, Y: 735, FontSize: 12, Height: 12, Width: 120},
		{Text: "Second paragraph line 2", X: 72, Y: 720, FontSize: 12, Height: 12, Width: 120},
		{Text: " ", X: 72, Y: 705, FontSize: 12, Height: 12, Width: 5},
		{Text: "Third paragraph", X: 72, Y: 690, FontSize: 12, Height: 12, Width: 120},
	}
	chunks := buildPageChunks(1, lines, nil)

	// Count paragraph chunks (range = "1/para/N")
	paraCount := 0
	for _, c := range chunks {
		if string(c.Range) == "1/para/1" || string(c.Range) == "1/para/2" || string(c.Range) == "1/para/3" {
			paraCount++
		}
	}
	if paraCount != 3 {
		t.Errorf("expected 3 paragraph chunks after blank-line filter, got %d; chunks: %v",
			paraCount, chunkRanges(chunks))
	}
	// Verify blank lines are not in content
	for _, c := range chunks {
		if string(c.Content) == " " || string(c.Content) == "" {
			t.Errorf("blank-line content leaked into chunk %s: %q", c.Range, c.Content)
		}
	}
}

// Sanity check: without blank separators between paragraphs, content stays one paragraph.
func TestBuildPageChunksGroupsUniformSpacing(t *testing.T) {
	lines := []pdfLine{
		{Text: "Line one", X: 72, Y: 780, FontSize: 12, Height: 12, Width: 60},
		{Text: "Line two", X: 72, Y: 765, FontSize: 12, Height: 12, Width: 60},
		{Text: "Line three", X: 72, Y: 750, FontSize: 12, Height: 12, Width: 70},
	}
	chunks := buildPageChunks(1, lines, nil)
	paraCount := 0
	for _, c := range chunks {
		if string(c.Range) == "1/para/1" {
			paraCount++
		}
	}
	if paraCount != 1 {
		t.Errorf("expected 1 paragraph, got %d; chunks: %v", paraCount, chunkRanges(chunks))
	}
}

func chunkRanges(chunks []microfts2.Chunk) []string {
	out := make([]string, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, string(c.Range))
	}
	return out
}
