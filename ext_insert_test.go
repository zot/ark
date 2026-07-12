package ark

// CRC: crc-Indexer.md | Test: test-Tags.md
//
// Internal-disposition tag insertion: the pure insertInternalTag stencils
// (markdown / bracket / indent), the capability gate, and the Sentry tests
// that re-chunk the output to prove a chunk-level tag stays with its chunk
// and a file-level tag stands as its own. Refs: R3095, R3096, R3097, R3098, R3099

import (
	"strings"
	"testing"

	"github.com/zot/microfts2"
)

// chunkOver builds a Chunk whose Locator spans [start,end) — the shape the
// accept path pre-fills from the F-record location.
func chunkOver(start, end int) microfts2.Chunk {
	return microfts2.Chunk{Locator: microfts2.EncodeByteRangeLocator(start, end)}
}

// insertInternalTag places the tag per its stencil and scope. (R3097, R3098, R3099)
func TestInsertInternalTag_Stencils(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		scope   tagScope
		comment string
		kind    stencilKind
		wantOK  bool
		want    string
	}{
		{
			name: "markdown chunk-level under heading",
			file: "## A\nbody\n", scope: tagScopeChunk, comment: "", kind: stencilMarkdown,
			wantOK: true, want: "## A\n@topic: recall\nbody\n",
		},
		{
			name: "markdown file-level top of file",
			file: "## A\nbody\n", scope: tagScopeFile, comment: "", kind: stencilMarkdown,
			wantOK: true, want: "@topic: recall\n## A\nbody\n",
		},
		{
			name: "markdown headingless prose → top of chunk",
			file: "just prose\nmore\n", scope: tagScopeChunk, comment: "", kind: stencilMarkdown,
			wantOK: true, want: "@topic: recall\njust prose\nmore\n",
		},
		{
			name: "bracket comment-wrapped inside the block",
			file: "func foo() {\n\tbody\n}\n", scope: tagScopeChunk, comment: "//", kind: stencilBracket,
			wantOK: true, want: "func foo() {\n// @topic: recall\n\tbody\n}\n",
		},
		{
			name: "indent matches the block body indentation",
			file: "def foo():\n    body1\n    body2\n", scope: tagScopeChunk, comment: "#", kind: stencilIndent,
			wantOK: true, want: "def foo():\n    # @topic: recall\n    body1\n    body2\n",
		},
		{
			name: "comment-less code chunker → external (ok=false)",
			file: "{\n  \"k\": 1\n}\n", scope: tagScopeChunk, comment: "", kind: stencilBracket,
			wantOK: false, want: "",
		},
	}
	for _, c := range cases {
		file := []byte(c.file)
		chunk := chunkOver(0, len(file))
		got, ok := insertInternalTag(file, chunk, "topic", "recall", c.scope, c.comment, c.kind)
		if ok != c.wantOK {
			t.Errorf("%s: ok=%v want %v", c.name, ok, c.wantOK)
			continue
		}
		if ok && string(got) != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, string(got), c.want)
		}
	}
}

// Empty value emits a tag-name-only line. (R3097)
func TestInsertInternalTag_EmptyValue(t *testing.T) {
	file := []byte("## A\nbody\n")
	got, ok := insertInternalTag(file, chunkOver(0, len(file)), "topic", "", tagScopeChunk, "", stencilMarkdown)
	if !ok || string(got) != "## A\n@topic:\nbody\n" {
		t.Errorf("empty value: ok=%v got %q", ok, string(got))
	}
}

// Sentry: a chunk-level markdown tag STAYS with its target chunk after a
// real re-chunk — the imperative the internal-disposition design rests on.
// (R3097)
func TestInsertInternalTag_StaysWithChunk(t *testing.T) {
	file := []byte("## A\nbody a\n\n## B\nbody b\n")
	// Capture section A's chunk (first yielded, merged heading+body).
	var secA microfts2.Chunk
	_ = microfts2.MarkdownChunkFunc("f.md", file, func(c microfts2.Chunk) bool {
		secA = c
		return false
	})
	out, ok := insertInternalTag(file, secA, "topic", "recall", tagScopeChunk, "", stencilMarkdown)
	if !ok {
		t.Fatal("insert failed")
	}
	// Re-chunk and find where the tag landed.
	var landed microfts2.Chunk
	var foundOK bool
	_ = microfts2.MarkdownChunkFunc("f.md", out, func(c microfts2.Chunk) bool {
		if strings.Contains(string(c.Content), "@topic: recall") {
			landed, foundOK = c, true
		}
		return true
	})
	if !foundOK {
		t.Fatalf("tag not found after re-chunk; bytes:\n%s", out)
	}
	if !strings.HasPrefix(string(landed.Content), "## A") || strings.Contains(string(landed.Content), "## B") {
		t.Errorf("tag did not stay with section A; landed in: %q", landed.Content)
	}
}

// Sentry: a file-level markdown tag stands as its OWN chunk (not merged into
// the first section). (R3098)
func TestInsertInternalTag_FileLevelOwnChunk(t *testing.T) {
	file := []byte("## A\nbody a\n")
	out, ok := insertInternalTag(file, microfts2.Chunk{}, "topic", "recall", tagScopeFile, "", stencilMarkdown)
	if !ok {
		t.Fatal("insert failed")
	}
	var landed microfts2.Chunk
	_ = microfts2.MarkdownChunkFunc("f.md", out, func(c microfts2.Chunk) bool {
		if strings.Contains(string(c.Content), "@topic: recall") {
			landed = c
		}
		return true
	})
	if strings.Contains(string(landed.Content), "## A") {
		t.Errorf("file-level tag merged into the heading chunk: %q", landed.Content)
	}
}

// The capability gate: the three text wrappers implement tagInserter; a bare
// (excluded) chunker does not. The composite literal also compile-checks that
// MarkdownChunker satisfies the fat interface. (R3095, R3096)
func TestTagInserterGate(t *testing.T) {
	var md any = internalTagChunker{fatChunker: microfts2.MarkdownChunker{}, kind: stencilMarkdown}
	if _, ok := md.(tagInserter); !ok {
		t.Error("markdown wrapper should implement tagInserter")
	}
	var lc any = microfts2.LineChunker{}
	if _, ok := lc.(tagInserter); ok {
		t.Error("bare LineChunker must not implement tagInserter (excluded on granularity)")
	}
}
