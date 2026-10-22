package main

// CRC: crc-LuhmannCLI.md | Test: test-LuhmannCLI.md

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeTranscript writes JSONL lines shaped as Claude Code writes a subagent
// transcript: a tool call is a tool_use block inside an assistant record's
// message.content, and usage is message.usage. Nothing sits at the top level.
func writeTranscript(t *testing.T, closeCalled bool, contextTokens int) string {
	t.Helper()
	cmd := "~/.ark/ark connections recall next 1"
	if closeCalled {
		cmd = "~/.ark/ark connections recall close wm-s-1 --nonce 1"
	}
	recs := []map[string]any{
		{"type": "assistant", "message": map[string]any{
			"role": "assistant",
			"content": []any{map[string]any{"type": "tool_use", "id": "t1", "name": "Bash",
				"input": map[string]any{"command": cmd}}},
			"usage": map[string]any{"cache_creation_input_tokens": 0, "cache_read_input_tokens": contextTokens},
		}},
		{"type": "user", "message": map[string]any{
			"role":    "user",
			"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": "ok"}},
		}},
	}
	path := filepath.Join(t.TempDir(), "agent-x.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range recs {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// R2796: a secretary that closed its hunt and stopped early is quit-early, not
// a crash, and tokens_at_close is the R2777 value. Measured 2026-10-22: the
// classifier read top-level tool_result and usage, which Claude Code never
// writes, so every bloodhound pool exit was labelled a crash.
func TestClassifySubagentExitReadsTheTranscriptShape(t *testing.T) {
	arkDir := t.TempDir()
	cases := []struct {
		name   string
		closed bool
		tokens int
		want   string
	}{
		{"closed early", true, 25000, "quit-early"},
		{"closed at the limit", true, 10_000_000, "healthy"},
		{"never closed", false, 25000, "crash"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifySubagentExit(arkDir, writeTranscript(t, tc.closed, tc.tokens), 1)
			if got.Label != tc.want {
				t.Errorf("label = %q, want %q", got.Label, tc.want)
			}
			if got.TokensAtClose != tc.tokens {
				t.Errorf("tokens_at_close = %d, want %d (the R2777 value)", got.TokensAtClose, tc.tokens)
			}
		})
	}
}
