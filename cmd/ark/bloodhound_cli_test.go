package main

// CRC: crc-CLITree.md | Test: test-BloodhoundCLI.md

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRenderFindingsMarkdown confirms the curated JSONL renders as a locator
// list — one `- ` + "`path:range`" + note line per finding, the chunk excerpt as
// a blockquote when present (R3037).
func TestRenderFindingsMarkdown(t *testing.T) {
	data := `{"path":"patterns/actor-queue-ordering.md","range":"1-40","note":"the core pattern","chunk":"queue the operation behind the message"}
{"path":"closure-actor.md","range":"12-30","note":"ChanSvc actor via closures"}`
	out := renderFindingsMarkdown([]byte(data))

	if !strings.Contains(out, "- `patterns/actor-queue-ordering.md:1-40` — the core pattern") {
		t.Errorf("missing first locator line:\n%s", out)
	}
	if !strings.Contains(out, "> queue the operation behind the message") {
		t.Errorf("chunk should render as a blockquote:\n%s", out)
	}
	if !strings.Contains(out, "- `closure-actor.md:12-30` — ChanSvc actor via closures") {
		t.Errorf("missing second locator line:\n%s", out)
	}
	if strings.Contains(out, "{") || strings.Contains(out, "\"path\"") {
		t.Errorf("output should be markdown, not raw JSON:\n%s", out)
	}
}

// TestRenderFindingsMarkdownEmpty confirms an empty result renders a single "no
// findings" line, not blank output (R3037).
func TestRenderFindingsMarkdownEmpty(t *testing.T) {
	if out := renderFindingsMarkdown([]byte("")); strings.TrimSpace(out) != "no findings" {
		t.Errorf("empty result = %q, want a 'no findings' line", out)
	}
}

// TestRenderFindingsMarkdownSkipsMalformed confirms a line that does not parse as
// a finding is skipped, not fatal — the render stays defensive (R3037).
func TestRenderFindingsMarkdownSkipsMalformed(t *testing.T) {
	data := `{"path":"a.md","range":"1-2","note":"good"}
this is not json`
	out := renderFindingsMarkdown([]byte(data))
	if !strings.Contains(out, "- `a.md:1-2` — good") {
		t.Errorf("valid finding should still render:\n%s", out)
	}
	if strings.Contains(out, "not json") {
		t.Errorf("malformed line should be skipped, not emitted:\n%s", out)
	}
}

// TestResolveClue confirms the clue comes from positional args or --file (with
// "-" reading stdin), and that the two are mutually exclusive (R3046).
func TestResolveClue(t *testing.T) {
	if got, err := resolveClue([]string{"find", "the", "thing"}, "", nil); err != nil || got != "find the thing" {
		t.Errorf("positional: got %q err %v", got, err)
	}
	fp := filepath.Join(t.TempDir(), "clue.md")
	if err := os.WriteFile(fp, []byte("para one\n\npara two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveClue(nil, fp, nil); err != nil || got != "para one\n\npara two" {
		t.Errorf("file: got %q err %v", got, err)
	}
	if got, err := resolveClue(nil, "-", strings.NewReader("heredoc clue")); err != nil || got != "heredoc clue" {
		t.Errorf("stdin: got %q err %v", got, err)
	}
	if _, err := resolveClue([]string{"x"}, fp, nil); err == nil {
		t.Error("positional + --file together should error (mutually exclusive)")
	}
}

// TestBuildSearchPayload confirms the payload is metadata-first (scope/depth/want,
// then curate:false only for --raw) with the clue body last and its paragraph
// breaks intact (R3044, R3046).
func TestBuildSearchPayload(t *testing.T) {
	clue := "idea one\n\nidea two"
	p := buildSearchPayload(clue, "specs", "investigate", "passages", false, nil, nil)
	if !strings.HasPrefix(p, "scope: specs\ndepth: investigate\nwant: passages\n") {
		t.Errorf("metadata should lead:\n%q", p)
	}
	if strings.Contains(p, "curate:") {
		t.Errorf("no curate marker without --raw:\n%q", p)
	}
	if !strings.HasSuffix(strings.TrimRight(p, "\n"), clue) {
		t.Errorf("clue body (with blank-line break) should be last:\n%q", p)
	}
	if pr := buildSearchPayload(clue, "all", "lookup", "passages", true, nil, nil); !strings.Contains(pr, "curate: false\n") {
		t.Errorf("--raw should add curate:false:\n%q", pr)
	}
}
