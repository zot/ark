package ark

import "testing"

// The chunk role, now derived from the transcript reader's record (R3553).
// Cases measured 2026-10-22: an agent report carries isMeta and stays "skill";
// a person's mid-turn message and a task notification also carry isMeta and are
// no longer "skill"; an unused record has no role.
func TestChatChunkRole(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{`{"type":"user","message":{"content":"hello"}}`, "human"},
		{`{"type":"assistant","message":{"content":"hi"}}`, "assistant"},
		{`{"type":"user","isMeta":true,"message":{"content":"skill stuff"}}`, "skill"},
		{`{"type":"user","isMeta":true,"origin":{"kind":"peer"},"message":{"content":"a report"}}`, "skill"},
		{`{"type":"user","isMeta":true,"origin":{"kind":"human"},"message":{"content":"mid-turn"}}`, "human"},
		{`{"type":"user","isMeta":true,"origin":{"kind":"task-notification"},"message":{"content":"done"}}`, "human"},
		{`{"type":"system","message":{}}`, ""},
		{`{"type":"progress"}`, ""},
		// Nested "type":"text" must not shadow the top-level "type":"assistant"
		{`{"parentUuid":"abc","message":{"content":[{"type":"text","text":"hello"}]},"type":"assistant"}`, "assistant"},
		{`{"message":{"content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"ok"}]},"type":"assistant"}`, "assistant"},
	}
	for _, tt := range tests {
		rec, _ := ParseTranscriptLine([]byte(tt.line), 1, nil)
		if got := chatChunkRole(rec); got != tt.want {
			t.Errorf("chatChunkRole(%s) = %q, want %q", tt.line, got, tt.want)
		}
	}
}

func TestExtractSkillName(t *testing.T) {
	tests := []struct {
		text string
		want string
	}{
		{"Base directory for this skill: /home/deck/.claude/skills/ark\n\nstuff", "ark"},
		{"Base directory for this skill: /home/deck/work/ark/.claude/skills/mini-spec\n\n# Mini-spec", "mini-spec"},
		{"No skill here", ""},
	}
	for _, tt := range tests {
		got := extractSkillName([]byte(tt.text))
		if got != tt.want {
			t.Errorf("extractSkillName(%q) = %q, want %q", tt.text[:min(40, len(tt.text))], got, tt.want)
		}
	}
}
