package ark

// CRC: crc-TagBlock.md

import (
	"strings"
	"testing"
)

func TestParseWellFormed(t *testing.T) {
	input := "@status: open\n@issue: foo\n@from-project: ark\n\nBody text here.\n"
	tb := ParseTagBlock([]byte(input))

	tags := tb.Tags()
	if len(tags) != 3 {
		t.Fatalf("expected 3 tags, got %d", len(tags))
	}
	if tags[0].Name != "status" || tags[0].Value != "open" {
		t.Errorf("tag 0: got %s=%s", tags[0].Name, tags[0].Value)
	}
	if tags[1].Name != "issue" || tags[1].Value != "foo" {
		t.Errorf("tag 1: got %s=%s", tags[1].Name, tags[1].Value)
	}
	if tags[2].Name != "from-project" || tags[2].Value != "ark" {
		t.Errorf("tag 2: got %s=%s", tags[2].Name, tags[2].Value)
	}
	if string(tb.Body()) != "Body text here.\n" {
		t.Errorf("body: got %q", string(tb.Body()))
	}
}

func TestParseEmpty(t *testing.T) {
	tb := ParseTagBlock([]byte{})
	if len(tb.Tags()) != 0 {
		t.Fatalf("expected 0 tags, got %d", len(tb.Tags()))
	}
	if tb.Body() != nil {
		t.Errorf("expected nil body, got %q", string(tb.Body()))
	}
}

func TestParseNoTags(t *testing.T) {
	input := "# Heading\n\nBody text\n"
	tb := ParseTagBlock([]byte(input))
	if len(tb.Tags()) != 0 {
		t.Fatalf("expected 0 tags, got %d", len(tb.Tags()))
	}
	if string(tb.Body()) != input {
		t.Errorf("body should be entire file, got %q", string(tb.Body()))
	}
}

func TestParseNoBlankSeparator(t *testing.T) {
	input := "@status: open\n# Heading\n"
	tb := ParseTagBlock([]byte(input))
	if len(tb.Tags()) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(tb.Tags()))
	}
	if string(tb.Body()) != "# Heading\n" {
		t.Errorf("body: got %q", string(tb.Body()))
	}
}

func TestSetReplacesExisting(t *testing.T) {
	input := "@status: open\n@issue: foo\n\nBody\n"
	tb := ParseTagBlock([]byte(input))
	tb.Set("status", "completed")

	result := string(tb.Render())
	expected := "@status: completed\n@issue: foo\n\nBody\n"
	if result != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, result)
	}
}

func TestSetAppendsNew(t *testing.T) {
	input := "@status: open\n\nBody\n"
	tb := ParseTagBlock([]byte(input))
	tb.Set("priority", "high")

	result := string(tb.Render())
	expected := "@status: open\n@priority: high\n\nBody\n"
	if result != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, result)
	}
}

func TestSetOnTaglessFile(t *testing.T) {
	input := "# Heading\nBody\n"
	tb := ParseTagBlock([]byte(input))
	tb.Set("status", "open")

	result := string(tb.Render())
	expected := "@status: open\n\n# Heading\nBody\n"
	if result != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, result)
	}
}

func TestGet(t *testing.T) {
	input := "@status: open\n@issue: foo\n\nBody\n"
	tb := ParseTagBlock([]byte(input))

	v, ok := tb.Get("status")
	if !ok || v != "open" {
		t.Errorf("Get(status): got %q, %v", v, ok)
	}
	_, ok = tb.Get("missing")
	if ok {
		t.Error("Get(missing) should return false")
	}
}

func TestRenderPreservesBody(t *testing.T) {
	body := "# Héading with ünïcödé\n\n```go\nfunc main() {}\n```\n"
	input := "@status: open\n\n" + body
	tb := ParseTagBlock([]byte(input))

	result := string(tb.Render())
	if !strings.HasSuffix(result, body) {
		t.Errorf("body not preserved:\nexpected suffix: %q\ngot: %q", body, result)
	}
}

func TestValidateBlankLineInBlock(t *testing.T) {
	input := "@status: open\n\n@issue: foo\n\nBody\n"
	tb := ParseTagBlock([]byte(input))
	problems := tb.Validate()

	found := false
	for _, p := range problems {
		if p.Line == 2 && strings.Contains(p.Message, "blank line") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected blank line problem at line 2, got: %v", problems)
	}
}

func TestValidateMissingSeparator(t *testing.T) {
	input := "@status: open\n# Heading\n"
	tb := ParseTagBlock([]byte(input))
	problems := tb.Validate()

	found := false
	for _, p := range problems {
		if strings.Contains(p.Message, "missing blank line") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected missing separator problem, got: %v", problems)
	}
}

func TestValidateMalformedTag(t *testing.T) {
	input := "@status:open\n\nBody\n"
	tb := ParseTagBlock([]byte(input))
	problems := tb.Validate()

	found := false
	for _, p := range problems {
		if strings.Contains(p.Message, "malformed tag") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected malformed tag problem, got: %v", problems)
	}
}

func TestScanBodyStrayTag(t *testing.T) {
	input := "@status: open\n\n@priority: high\nBody\n"
	tb := ParseTagBlock([]byte(input))
	problems := tb.ScanBody()

	found := false
	for _, p := range problems {
		if strings.Contains(p.Message, "misplaced tag") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected stray tag finding, got: %v", problems)
	}
}

func TestScanBodyHeadingTag(t *testing.T) {
	input := "@status: open\n\nBody\n## Status: completed\nmore\n"
	tb := ParseTagBlock([]byte(input))
	problems := tb.ScanBody()

	found := false
	for _, p := range problems {
		if strings.Contains(p.Message, "markdown heading") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected heading-as-tag finding, got: %v", problems)
	}
}

func TestMultipleSetPreservesOrder(t *testing.T) {
	input := "@a: 1\n@b: 2\n@c: 3\n\nBody\n"
	tb := ParseTagBlock([]byte(input))
	tb.Set("b", "X")
	tb.Set("d", "4")

	result := string(tb.Render())
	expected := "@a: 1\n@b: X\n@c: 3\n@d: 4\n\nBody\n"
	if result != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, result)
	}
}
