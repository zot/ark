package main

// CRC: test-InboxTree.md | R3404, R3406, R3407, R3408, R3410

import (
	"strings"
	"testing"

	"github.com/zot/ark"
)

// captureStdout is defined in chats_test.go (same package).

// TestInboxNodeNestsFilteredResponse pins R3406/R3407: a completed response —
// which the default row view hides — still nests under its open request, with
// the bookmark stated in words. This is the miss the feature exists to fix.
func TestInboxNodeNestsFilteredResponse(t *testing.T) {
	req := &ark.InboxEntry{
		Status: "open", From: "microfts2", To: "mini-spec",
		Summary: "harvest scope", RequestID: "X", StatusDate: "2026-08-06",
		Kind: "request",
	}
	resp := &ark.InboxEntry{
		Status: "completed", From: "mini-spec", To: "microfts2",
		RequestID: "X", StatusDate: "2026-09-16", ResponseHandled: "",
		Kind: "response",
	}
	out := captureStdout(t, func() { printInboxNode(req, resp) })

	for _, want := range []string{
		"- open  microfts2 → mini-spec  harvest scope  (2026-08-06)",
		"- response: completed  mini-spec → microfts2  (2026-09-16)",
		"microfts2 has not handled this response yet",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\ngot:\n%s", want, out)
		}
	}
}

// TestInboxNodeNoResponse pins R3408: an unanswered request says so, rather
// than reading like a response that failed to render.
func TestInboxNodeNoResponse(t *testing.T) {
	req := &ark.InboxEntry{
		Status: "open", From: "ui-engine", To: "ark",
		Summary: "svc ordering", StatusDate: "2026-08-01", Kind: "request",
	}
	out := captureStdout(t, func() { printInboxNode(req, nil) })
	if !strings.Contains(out, "- no response") {
		t.Errorf("output missing %q\ngot:\n%s", "- no response", out)
	}
}

// TestInboxEmptyHintNamesProjects pins R3410: an over-narrow filter echoes the
// filter and lists the projects present, sorted, so a minispec/mini-spec typo
// is correctable rather than a silent empty result.
func TestInboxEmptyHintNamesProjects(t *testing.T) {
	entries := []ark.InboxEntry{
		{From: "mini-spec", To: "microfts2"},
		{From: "ark", To: "microfts2"},
	}
	out := captureStdout(t, func() { printInboxEmptyHint(entries, "", "", "minispec") })

	for _, want := range []string{
		"no messages match (--from minispec)",
		"from-projects present: ark, mini-spec",
		"to-projects present: microfts2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\ngot:\n%s", want, out)
		}
	}
}
