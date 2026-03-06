package ark

// CRC: crc-Indexer.md | Test: test-Tags.md

import "testing"

func TestExtractTagsBasic(t *testing.T) {
	content := []byte("@decision: chose LMDB\n@pattern: closure-actor\nnot a @tag without colon")
	tags := ExtractTags(content)
	if tags["decision"] != 1 {
		t.Errorf("expected decision=1, got %d", tags["decision"])
	}
	if tags["pattern"] != 1 {
		t.Errorf("expected pattern=1, got %d", tags["pattern"])
	}
	if _, ok := tags["tag"]; ok {
		t.Error("tag without colon should not match")
	}
}

func TestExtractTagsMultipleOccurrences(t *testing.T) {
	content := []byte("@decision: first\nsome text\n@decision: second")
	tags := ExtractTags(content)
	if tags["decision"] != 2 {
		t.Errorf("expected decision=2, got %d", tags["decision"])
	}
}

func TestExtractTagsCaseAndHyphens(t *testing.T) {
	content := []byte("@my-tag: value\n@CamelTag: value")
	tags := ExtractTags(content)
	if tags["my-tag"] != 1 {
		t.Errorf("expected my-tag=1, got %d", tags["my-tag"])
	}
	if tags["cameltag"] != 1 {
		t.Errorf("expected cameltag=1, got %d", tags["cameltag"])
	}
}

func TestExtractTagsIgnoresEmailsAndMentions(t *testing.T) {
	content := []byte("user@example.com and @mention without colon")
	tags := ExtractTags(content)
	if len(tags) != 0 {
		t.Errorf("expected no tags, got %v", tags)
	}
}
