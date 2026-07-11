package ark

// CRC: crc-Indexer.md | Test: test-ChunkerRanges.md | R3078

import (
	"bytes"
	"testing"

	"github.com/zot/microfts2"
)

// TestChunkRangeLocationsColonFree is a Sleeping Sentry for the
// specs/chunkers.md contract: chunk-range Locations contain no bare colon,
// which is what lets `path:range` split unambiguously at the LAST colon
// (so a colon in the path or a tmp:// scheme is handled). If a chunker ever
// emits a colon in a Range, this fails loudly — and *that* is when we reach
// for a source/extension-aware splitter. PDF is covered by construction
// (PAGE/KIND/N — slashes, no colon) and not indexed here (needs a real PDF).
func TestChunkRangeLocationsColonFree(t *testing.T) {
	text := []byte("line one\nline two\n\n## Heading\n\npara here\n\tindented line\n")
	jsonl := []byte(`{"type":"user","message":{"role":"user","content":"hi there"}}` + "\n")

	// lines covers the N-M form shared by the line, bracket, and indent
	// chunkers; markdown and chat-jsonl are the other registered forms.
	chunkers := []struct {
		name string
		fn   func(path string, content []byte, yield func(microfts2.Chunk) bool) error
		in   []byte
	}{
		{"lines", microfts2.LineChunkFunc, text},
		{"markdown", microfts2.MarkdownChunker{}.Chunks, text},
		{"chat-jsonl", JSONLChunkFunc, jsonl},
	}
	for _, ch := range chunkers {
		got := 0
		if err := ch.fn("f", ch.in, func(c microfts2.Chunk) bool {
			got++
			if bytes.ContainsRune(c.Range, ':') {
				t.Errorf("%s: chunk range %q contains a colon — breaks path:range last-colon splitting (specs/chunkers.md contract)", ch.name, c.Range)
			}
			return true
		}); err != nil {
			t.Errorf("%s: chunk err: %v", ch.name, err)
		}
		if got == 0 {
			t.Errorf("%s: produced no chunks (sample too small?)", ch.name)
		}
	}
}
