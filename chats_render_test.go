package ark

// CRC: crc-CLI.md | Test: test-ChatTranscript.md | R1045, R1047, R1049, R3035, R3553

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// One transcript holding every case the renderer distinguishes: a user turn of
// two text blocks (one per line), a sidechain turn, an assistant turn with
// text, thinking, narration and a tool call, a tool-result turn, a turn end.
const renderFixture = `{"type":"user","origin":{"kind":"human"},"message":{"role":"user","content":"first question"}}
{"type":"user","isSidechain":true,"message":{"role":"user","content":"SIDECHAIN"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"the answer"},{"type":"thinking","thinking":"THOUGHT"},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls -la"}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"out"}]}}
{"type":"system","subtype":"turn_duration"}
{"type":"user","origin":{"kind":"human"},"message":{"role":"user","content":[{"type":"text","text":"line one"},{"type":"text","text":"line two"}]}}
`

func renderFixtureWith(t *testing.T, opts ChatRenderOptions) string {
	t.Helper()
	var sb strings.Builder
	if err := RenderChatLines(&sb, strings.NewReader(renderFixture), opts); err != nil {
		t.Fatal(err)
	}
	return sb.String()
}

// The renderer's output for each option, exactly. Before the transcript reader
// moved it (2026-10-22) only --thinking was tested; injecting a space join, a
// dropped sidechain filter and an unconditional tool line turned nothing red.
func TestRenderChatOutput(t *testing.T) {
	base := "❯ first question\n\n● the answer\n\n❯ line one\n  line two\n\n"
	if got := renderFixtureWith(t, ChatRenderOptions{}); got != base {
		t.Errorf("default render:\n%q\nwant\n%q", got, base)
	}
	tools := renderFixtureWith(t, ChatRenderOptions{WithTools: true})
	if !strings.Contains(tools, "  ⚙ Bash ls -la\n") {
		t.Errorf("--with-tools lacks the tool line:\n%s", tools)
	}
	if strings.Contains(renderFixtureWith(t, ChatRenderOptions{}), "⚙") {
		t.Error("tool line shown without --with-tools")
	}
	if all := renderFixtureWith(t, ChatRenderOptions{Sidechain: true}); !strings.Contains(all, "❯ SIDECHAIN") {
		t.Errorf("--all should show sidechain turns:\n%s", all)
	}
	if strings.Contains(base, "SIDECHAIN") || strings.Contains(base, "THOUGHT") {
		t.Error("default render shows sidechain or thinking")
	}
}

// A session's turn count is every user and assistant record outside sidechains:
// tool-result carriers included, turn ends not. (TestReadChatLabel covers the
// label and the sidechain rule.)
func TestReadChatLabelCountsTurns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(renderFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	label, turns := readChatLabel(path)
	if label != "first question" {
		t.Errorf("label = %q, want the first user turn", label)
	}
	if turns != 4 {
		t.Errorf("turns = %d, want 4 (two person turns, one assistant, one tool-result carrier)", turns)
	}
}
