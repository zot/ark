package main

// CRC: test-InboxTree.md | R3404, R3406, R3407, R3408, R3410

import (
	"strings"
	"testing"
	"time"

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

// TestNewMessageStampsCreated pins R3439: new-request and new-response stamp an
// immutable @created date at creation, equal to @status-date at that moment.
// CRC: test-MessageCreated.md | R3439
func TestNewMessageStampsCreated(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		tb   *ark.TagBlock
	}{
		{"request", newRequestTags("fix-bug", "ark", "microfts2", "the issue", now)},
		{"response", newResponseTags("fix-bug", "microfts2", "ark", now)},
	}
	for _, tc := range cases {
		created, ok := tc.tb.Get("created")
		if !ok || created != "2026-09-18" {
			t.Errorf("%s: @created = %q (ok=%v), want 2026-09-18", tc.name, created, ok)
		}
		if sd, _ := tc.tb.Get("status-date"); sd != created {
			t.Errorf("%s: @created %q != @status-date %q at creation", tc.name, created, sd)
		}
	}
}
