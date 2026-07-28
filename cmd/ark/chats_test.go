package main

// CRC: test-ChatTranscript.md | R3035, R3242, R3243

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	ucli "github.com/urfave/cli/v3"
	"github.com/zot/ark"
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

// TestResolveChatFilesDirectPath confirms R3242: an argument naming an existing
// file resolves to exactly that file — no index lookup, no projects walk — so
// any on-disk JSONL renders, wherever it lives.
func TestResolveChatFilesDirectPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // hermetic: empty ~/.claude/projects/ walk
	path := filepath.Join(t.TempDir(), "anywhere.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := resolveChatFiles([]string{path})
	if !slices.Equal(files, []string{path}) {
		t.Errorf("existing file should resolve to itself; got %v", files)
	}
}

// TestResolveChatFilesGlobFallback confirms R1050 + R3243: a non-file argument
// falls back to the basename glob under ~/.claude/projects/; an argument that
// yields nothing leaves the result empty, and a mixed call keeps the existing
// file in argument order.
func TestResolveChatFilesGlobFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // hermetic walk root
	projects := filepath.Join(home, ".claude", "projects", "p")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	indexed := filepath.Join(projects, "session-abc.jsonl")
	if err := os.WriteFile(indexed, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	direct := filepath.Join(t.TempDir(), "local.jsonl")
	if err := os.WriteFile(direct, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if files := resolveChatFiles([]string{"no-such-*.jsonl"}); len(files) != 0 {
		t.Errorf("no-match pattern should resolve to nothing; got %v", files)
	}
	files := resolveChatFiles([]string{direct, "*abc*.jsonl", "no-such-*.jsonl"})
	want := []string{direct, indexed}
	if !slices.Equal(files, want) {
		t.Errorf("mixed args should resolve in argument order %v; got %v", want, files)
	}
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
	out := captureStdout(t, func() { _ = renderChat(path, ark.ChatRenderOptions{}) })
	if !strings.Contains(out, "VISIBLETEXT") {
		t.Errorf("text should always render; got %q", out)
	}
	if strings.Contains(out, "SECRETTHOUGHT") {
		t.Errorf("thinking must be hidden without --thinking; got %q", out)
	}

	// --thinking: thinking shown with the ✻ marker.
	out = captureStdout(t, func() { _ = renderChat(path, ark.ChatRenderOptions{WithThinking: true}) })
	if !strings.Contains(out, "SECRETTHOUGHT") {
		t.Errorf("thinking should render with --thinking; got %q", out)
	}
	if !strings.Contains(out, "✻") {
		t.Errorf("thinking should carry the ✻ marker; got %q", out)
	}
}
