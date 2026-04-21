package ark

// Test: crc-PDFChunker.md | R1730, R1731, R1735, R1736, R1737, R1738

import (
	"testing"

	"github.com/zot/microfts2"
	"github.com/zot/pdftext"
)

// R1730, R1738: each BlockKind maps to its location suffix, counted
// per-kind per-page (1-indexed).
func TestPageChunksLocationMapping(t *testing.T) {
	blocks := []pdftext.Block{
		{Kind: pdftext.Heading, Text: "Title"},
		{Kind: pdftext.Paragraph, Text: "First para"},
		{Kind: pdftext.Paragraph, Text: "Second para"},
		{Kind: pdftext.List, Text: "item a\nitem b"},
		{Kind: pdftext.Table, Text: "a\tb\nc\td"},
		{Kind: pdftext.Irregular, Text: "Odd layout"},
		{Kind: pdftext.Salvage, Text: "degraded text"},
		{Kind: pdftext.Image, Text: ""},
	}
	got := chunkRanges(pageChunks(3, blocks))
	want := []string{
		"3/heading/1",
		"3/para/1",
		"3/para/2",
		"3/list/1",
		"3/table/1",
		"3/para/3", // Irregular folds into para
		"3/salvage/1",
		// Image is skipped
	}
	if len(got) != len(want) {
		t.Fatalf("range count mismatch: got %d, want %d\n got=%v\nwant=%v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("range[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

// R1731: Block.Caption is prepended to Text with a newline separator.
func TestPageChunksCaptionConcatenation(t *testing.T) {
	blocks := []pdftext.Block{
		{Kind: pdftext.Table, Caption: "Quarterly revenue", Text: "Q1\t100\nQ2\t120"},
		{Kind: pdftext.List, Caption: "Steps", Text: "first\nsecond"},
		{Kind: pdftext.Paragraph, Caption: "", Text: "plain paragraph"},
	}
	chunks := pageChunks(1, blocks)
	if got := string(chunks[0].Content); got != "Quarterly revenue\nQ1\t100\nQ2\t120" {
		t.Errorf("table content: got %q", got)
	}
	if got := string(chunks[1].Content); got != "Steps\nfirst\nsecond" {
		t.Errorf("list content: got %q", got)
	}
	if got := string(chunks[2].Content); got != "plain paragraph" {
		t.Errorf("paragraph content: got %q", got)
	}
}

// R1733: pages that produce no mappable blocks emit no chunks.
func TestPageChunksEmptyPageYieldsNothing(t *testing.T) {
	if got := pageChunks(5, nil); got != nil {
		t.Errorf("nil blocks: expected nil, got %v", chunkRanges(got))
	}
	if got := pageChunks(5, []pdftext.Block{{Kind: pdftext.Image}}); got != nil {
		t.Errorf("image-only page: expected nil, got %v", chunkRanges(got))
	}
}

// R1735: tag rect is the union of Block.Chars BBoxes covering the
// match byte range.
func TestExtractTagRectsFromBlockChars(t *testing.T) {
	text := "@status: open"
	chars := make([]pdftext.Char, len(text))
	for i := range text {
		chars[i] = pdftext.Char{
			Rune: rune(text[i]),
			BBox: pdftext.Rect{X: float64(i) * 5, Y: 100, W: 5, H: 12},
		}
	}
	got := extractTagRects(&pdftext.Block{Text: text, Chars: chars})
	// 13 chars, X from 0 to 65, Y=100, H=12.
	want := "status=open@0.00,100.00,65.00,12.00"
	if got != want {
		t.Errorf("extractTagRects: got %q, want %q", got, want)
	}
}

// R1735: NoRune slots (inferred whitespace / UTF-8 continuation) are
// skipped when computing the bounding box.
func TestExtractTagRectsSkipsNoRuneSlots(t *testing.T) {
	text := "@a: x"
	chars := []pdftext.Char{
		{Rune: '@', BBox: pdftext.Rect{X: 0, Y: 100, W: 5, H: 12}},
		{Rune: 'a', BBox: pdftext.Rect{X: 5, Y: 100, W: 5, H: 12}},
		{Rune: ':', BBox: pdftext.Rect{X: 10, Y: 100, W: 5, H: 12}},
		{Rune: pdftext.NoRune}, // inferred space, should be skipped
		{Rune: 'x', BBox: pdftext.Rect{X: 20, Y: 100, W: 5, H: 12}},
	}
	got := extractTagRects(&pdftext.Block{Text: text, Chars: chars})
	// X extent: 0 to 25 (skip the NoRune slot)
	want := "a=x@0.00,100.00,25.00,12.00"
	if got != want {
		t.Errorf("extractTagRects: got %q, want %q", got, want)
	}
}

// R1735, R1736: tag rects are also extracted from Block.Caption.
func TestExtractTagRectsFromCaption(t *testing.T) {
	caption := "@issue: bug"
	capChars := make([]pdftext.Char, len(caption))
	for i := range caption {
		capChars[i] = pdftext.Char{
			Rune: rune(caption[i]),
			BBox: pdftext.Rect{X: float64(i) * 6, Y: 200, W: 6, H: 10},
		}
	}
	got := extractTagRects(&pdftext.Block{Caption: caption, CaptionChars: capChars})
	want := "issue=bug@0.00,200.00,66.00,10.00"
	if got != want {
		t.Errorf("caption tag rect: got %q, want %q", got, want)
	}
}

// R1730: Salvage blocks produce PAGE/salvage/N, 1-indexed per page.
func TestPageChunksSalvageAtActualPage(t *testing.T) {
	blocks := []pdftext.Block{
		{Kind: pdftext.Paragraph, Text: "clean text"},
		{Kind: pdftext.Salvage, Text: "degraded", Confidence: 0.5},
		{Kind: pdftext.Salvage, Text: "more degraded", Confidence: 0.4},
	}
	got := chunkRanges(pageChunks(7, blocks))
	want := []string{"7/para/1", "7/salvage/1", "7/salvage/2"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("range[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

func chunkRanges(chunks []microfts2.Chunk) []string {
	out := make([]string, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, string(c.Range))
	}
	return out
}
