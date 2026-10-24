package main

// Test: test-StaleChunk.md | R3572

import (
	"strings"
	"testing"

	"github.com/zot/ark"
	"github.com/zot/microfts2"
)

// R3572: under --wrap a stale chunk's element holds the placeholder; the
// JSONL form carries "missing": true with empty content.
func TestEmitChunkResult_StaleChunk(t *testing.T) {
	res := ark.ChunkFetchResult{Chunks: []microfts2.ChunkResult{
		{Path: "/a/b.md", Range: "1-1", Content: "alpha one\n"},
		{Path: "/a/b.md", Range: "2-2", Index: 1, Missing: true},
	}}
	wrapped := captureStdout(t, func() { emitChunkResult(res, "", "", "", "chunk") })
	if strings.Count(wrapped, "older chunking rules") != 1 || !strings.Contains(wrapped, "alpha one") {
		t.Errorf("--wrap output: want the live text and one placeholder, got %q", wrapped)
	}
	jsonl := captureStdout(t, func() { emitChunkResult(res, "", "", "", "") })
	if !strings.Contains(jsonl, `"missing":true`) || strings.Contains(jsonl, "older chunking rules") {
		t.Errorf("JSONL output: want the missing flag and no placeholder, got %q", jsonl)
	}
}
