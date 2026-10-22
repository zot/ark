package ark

// CRC: crc-DB.md | Test: test-TranscriptReader.md | R240, R1507, R2271, R3553

import (
	"strings"
	"testing"

	"github.com/zot/microfts2"
)

// The chat chunker's rules, on lines in the shapes Claude Code writes: a turn's
// text, thinking and narration are indexed one per line; tool calls and tool
// results are not; records ark does not use (an attachment) are not; a partial
// trailing line is indexed raw until it completes. Before the chunker moved onto
// the transcript reader (2026-10-22) nothing tested the text rules: indexing
// tool results, or dropping thinking, turned nothing red.
func TestChatChunkerText(t *testing.T) {
	content := strings.Join([]string{
		`{"type":"user","origin":{"kind":"human"},"timestamp":"2026-10-22T10:00:00Z","message":{"role":"user","content":"a question"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"weighing"},{"type":"text","text":"an answer"},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"SECRETCOMMAND"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"TOOLOUTPUT"}]}}`,
		`{"type":"attachment","attachment":{"content":"HARNESSNOISE"}}`,
		`{"type":"user","origin":{"kind":"human"},"message":{"role":"user","content":"trail`, // no newline: still being written
	}, "\n")
	var chunks []microfts2.Chunk
	if err := JSONLChunkFunc("x.jsonl", []byte(content), func(c microfts2.Chunk) bool {
		chunks = append(chunks, c)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range chunks {
		got = append(got, string(c.Range)+" "+string(c.Content))
	}
	want := []string{
		"1-1 a question",
		"2-2 weighing\nan answer",
		`5-5 {"type":"user","origin":{"kind":"human"},"message":{"role":"user","content":"trail`,
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("chunks:\n%q\nwant\n%q", got, want)
	}
	attr := func(c microfts2.Chunk, k string) string {
		for _, a := range c.Attrs {
			if string(a.Key) == k {
				return string(a.Value)
			}
		}
		return ""
	}
	if attr(chunks[0], "role") != "human" || attr(chunks[0], "timestamp") == "" {
		t.Errorf("first chunk attrs = %v", chunks[0].Attrs)
	}
	if attr(chunks[1], "role") != "assistant" {
		t.Errorf("assistant chunk role = %q", attr(chunks[1], "role"))
	}
	if r := attr(chunks[2], "role"); r != "" {
		t.Errorf("a partial line has no role, got %q", r)
	}
}
