package main

// Test: crc-CLI.md, Test: test-ChatTranscript.md | R3035

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ucli "github.com/urfave/cli/v3"
)

// TestChatsNodeDeclaresThinkingFlags is the wiring Sentry for R3035: the urfave
// `chats` node must DECLARE --thinking / --all, or urfave rejects them
// ("flag provided but not defined") before cmdChats ever runs — the exact bug
// the renderChat unit test could not catch (it bypasses the CLI parse).
func TestChatsNodeDeclaresThinkingFlags(t *testing.T) {
	var chats *ucli.Command
	for _, c := range flatCommands() {
		if c.Name == "chats" {
			chats = c
			break
		}
	}
	if chats == nil {
		t.Fatal("chats command not found in flatCommands()")
	}
	got := map[string]bool{}
	for _, f := range chats.Flags {
		for _, n := range f.Names() {
			got[n] = true
		}
	}
	for _, want := range []string{"thinking", "all", "with-tools"} {
		if !got[want] {
			t.Errorf("chats node must declare --%s (else urfave rejects it before cmdChats)", want)
		}
	}
}

// captureStdout runs fn with os.Stdout redirected to a pipe and returns what it
// wrote. Output must stay under the pipe buffer (~64KB) — fine for these tests.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	data, _ := io.ReadAll(r)
	return string(data)
}

// TestRenderChatThinking confirms R3035: a thinking block renders only with
// --thinking (marked ✻), while text always renders.
func TestRenderChatThinking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	rec := `{"type":"assistant","message":{"content":[` +
		`{"type":"thinking","thinking":"SECRETTHOUGHT"},` +
		`{"type":"text","text":"VISIBLETEXT"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}

	// Default: text shown, thinking hidden.
	out := captureStdout(t, func() { _ = renderChat(path, false, false, 100, false) })
	if !strings.Contains(out, "VISIBLETEXT") {
		t.Errorf("text should always render; got %q", out)
	}
	if strings.Contains(out, "SECRETTHOUGHT") {
		t.Errorf("thinking must be hidden without --thinking; got %q", out)
	}

	// --thinking: thinking shown with the ✻ marker.
	out = captureStdout(t, func() { _ = renderChat(path, false, true, 100, false) })
	if !strings.Contains(out, "SECRETTHOUGHT") {
		t.Errorf("thinking should render with --thinking; got %q", out)
	}
	if !strings.Contains(out, "✻") {
		t.Errorf("thinking should carry the ✻ marker; got %q", out)
	}
}
