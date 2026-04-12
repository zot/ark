package ark

import "testing"

func TestExtractJSONLRole(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{`{"type":"user","message":{"content":"hello"}}`, "human"},
		{`{"type":"assistant","message":{"content":"hi"}}`, "assistant"},
		{`{"type":"user","isMeta":true,"message":{"content":"skill stuff"}}`, "skill"},
		{`{"type":"system","message":{}}`, ""},
		{`{"type":"progress"}`, ""},
		// Nested "type":"text" must not shadow the top-level "type":"assistant"
		{`{"parentUuid":"abc","message":{"content":[{"type":"text","text":"hello"}]},"type":"assistant"}`, "assistant"},
		{`{"message":{"content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"ok"}]},"type":"assistant"}`, "assistant"},
	}
	for _, tt := range tests {
		got := extractJSONLRole([]byte(tt.line))
		if got != tt.want {
			t.Errorf("extractJSONLRole(%q) = %q, want %q", tt.line, got, tt.want)
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
