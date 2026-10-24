package ark

// CRC: crc-Librarian.md | Test: test-EmbedPriority.md | R1611, R3577

import (
	"os"
	"path/filepath"
	"testing"
)

// R3577: a file is a conversation log by the strategy it was indexed under,
// not by its suffix; a tag-bearing file is first whatever its strategy.
func TestEmbedPriority_ByIndexedStrategy(t *testing.T) {
	l, db := setupRecall(t)
	if err := db.fts.AddChunker(ChatStrategy, JSONLChunker{}); err != nil {
		t.Fatal(err)
	}
	index := func(name, content, strategy string) string {
		t.Helper()
		fp := filepath.Join(db.dbPath, name)
		if err := os.WriteFile(fp, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := db.indexer.AddFile(fp, strategy); err != nil {
			t.Fatalf("AddFile %s: %v", name, err)
		}
		if got := db.FileStrategy(fp); got != strategy {
			t.Fatalf("fixture invalid: %s indexed as %q, want %q", name, got, strategy)
		}
		return fp
	}
	chat := index("a.jsonl", `{"type":"user","message":{"role":"user","content":"hello"}}`+"\n", ChatStrategy)
	lines := index("b.jsonl", "not a chat log\n", "line")
	authored := index("c.md", "a note\n", "line")

	cases := []struct {
		name   string
		path   string
		tagged bool
		want   int
	}{
		{"chat-jsonl file", chat, false, embedPriorityChat},
		{".jsonl under another strategy", lines, false, embedPriorityAuthored},
		{"tag-bearing", authored, true, embedPriorityTagged},
		{"tag-bearing chat", chat, true, embedPriorityTagged},
	}
	for _, tc := range cases {
		if got := l.embedPriority(tc.path, tc.tagged); got != tc.want {
			t.Errorf("%s: embedPriority = %d, want %d", tc.name, got, tc.want)
		}
	}
}
