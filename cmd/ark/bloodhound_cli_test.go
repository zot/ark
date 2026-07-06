package main

// CRC: crc-CLITree.md | Test: test-BloodhoundCLI.md

import (
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
