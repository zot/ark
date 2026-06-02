package ark

// CRC: crc-RecallAgentBuilder.md, crc-RecallWatcher.md | Test: test-Secretary.md

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// --- Recall Secretary seam 3a tests ---

// TestUserProse verifies genuine-user extraction: prose string content
// with no harness origin yields the text; tool-results and injected
// notifications are rejected. R2891
func TestUserProse(t *testing.T) {
	cases := []struct {
		name    string
		origin  string
		content string
		want    string
		ok      bool
	}{
		{"genuine", "", `"hello there"`, "hello there", true},
		{"notification", "task-notification", `"background done"`, "", false},
		{"tool-result", "", `[{"type":"tool_result","content":"x"}]`, "", false},
		{"empty", "", ``, "", false},
	}
	for _, tc := range cases {
		got, ok := userProse(tc.origin, json.RawMessage(tc.content))
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s: userProse(%q,%q) = (%q,%v) want (%q,%v)", tc.name, tc.origin, tc.content, got, ok, tc.want, tc.ok)
		}
	}
}

// TestAssistantText verifies assistant-text extraction from both the
// bare-string and content-array shapes; tool_use blocks are skipped. R2891
func TestAssistantText(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"string", `"plain reply"`, "plain reply"},
		{"array-text", `[{"type":"text","text":"a"},{"type":"text","text":"b"}]`, "a b"},
		{"array-mixed", `[{"type":"text","text":"keep"},{"type":"tool_use","name":"Bash"}]`, "keep"},
		{"array-tool-only", `[{"type":"tool_use","name":"Bash"}]`, ""},
		{"empty", ``, ""},
	}
	for _, tc := range cases {
		if got := assistantText(json.RawMessage(tc.content)); got != tc.want {
			t.Errorf("%s: assistantText(%q) = %q want %q", tc.name, tc.content, got, tc.want)
		}
	}
}

// TestDropCooledCandidates verifies the surface-cooldown floor (R2893):
// a candidate whose (session, chunk) was surfaced within the window is
// dropped; other chunks and other sessions are unaffected.
func TestDropCooledCandidates(t *testing.T) {
	_, db := setupRecall(t)
	db.config.Recall.SurfaceCooldown = "24h"
	w := &RecallWatcher{db: db, store: db.store}

	if err := db.store.MarkSurfaced("sess-A", 100); err != nil {
		t.Fatal(err)
	}
	got := w.dropCooledCandidates("sess-A", []RecalledChunk{{ChunkID: 100}, {ChunkID: 200}})
	if len(got) != 1 || got[0].ChunkID != 200 {
		t.Errorf("expected only chunk 200 to survive cooldown; got %+v", got)
	}
	// A different session has no cooldown for chunk 100.
	other := w.dropCooledCandidates("sess-B", []RecalledChunk{{ChunkID: 100}})
	if len(other) != 1 {
		t.Errorf("chunk 100 should not be cooled for sess-B; got %+v", other)
	}
}

// TestDropCooledCandidates_Disabled verifies a zero window disables the
// floor (no candidates dropped). R2893
func TestDropCooledCandidates_Disabled(t *testing.T) {
	_, db := setupRecall(t)
	db.config.Recall.SurfaceCooldown = "0"
	w := &RecallWatcher{db: db, store: db.store}
	if err := db.store.MarkSurfaced("sess-A", 100); err != nil {
		t.Fatal(err)
	}
	got := w.dropCooledCandidates("sess-A", []RecalledChunk{{ChunkID: 100}})
	if len(got) != 1 {
		t.Errorf("zero window must disable the floor; got %+v", got)
	}
}

// TestWriteCurationFile_AndPointer verifies next materializes the doc to a
// file and the crank-handle is a short pointer that does NOT embed the
// (potentially huge) doc content — the core of the R2896 delivery fix.
func TestWriteCurationFile_AndPointer(t *testing.T) {
	b := &RecallAgentBuilder{curationDir: t.TempDir()}
	content := "## Recent conversation\n\n**user:** big\n\n# Source: x.md:1\n\n## Candidate: x.md:1-9 (500b)\n- score: 0.9\n" + strings.Repeat("padding ", 1000)
	path, err := b.writeCurationFile("sess-A", 7, content)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != content {
		t.Fatalf("curation file round-trip failed: err=%v", err)
	}
	if !strings.HasSuffix(path, "curation-sess-A-7.md") {
		t.Errorf("unexpected path: %s", path)
	}
	prompt := recallDocPrompt(7, "sess-A", "sess-A", 2, path)
	if strings.Contains(prompt, "padding padding") {
		t.Error("recallDocPrompt must NOT embed the doc content — it should point to the file")
	}
	if !strings.Contains(prompt, path) {
		t.Error("recallDocPrompt must name the curation file path")
	}
	if !strings.Contains(prompt, "--session sess-A 2") {
		t.Error("recallDocPrompt must carry the session+nonce re-run command")
	}
}

// TestSurfaceItem_MarksSurfaced verifies the surface verb starts the
// cooldown clock for the surfaced (session, chunk). R2894
func TestSurfaceItem_MarksSurfaced(t *testing.T) {
	_, db := setupRecall(t)
	// Index a real chunk so the loc → chunkID resolution (R2900) the
	// surface cooldown depends on has something to find.
	cid, _ := indexLine(t, db, "x.md", "some genuinely relevant content")
	info, err := db.ChunkInfo(cid)
	if err != nil {
		t.Fatalf("ChunkInfo(%d): %v", cid, err)
	}
	loc := info.Path + ":" + info.Range
	b := &RecallAgentBuilder{
		db:        db,
		curations: make(map[string]*RecallCurationBuilder),
		results:   make(map[string]*recallResultDoc),
	}
	b.RecallCurationOpen("sess-A", 5) // registers the fire so openResult resolves the session
	if err := b.SurfaceItem(fireToken("sess-A", 5), loc, "genuinely relevant"); err != nil {
		t.Fatal(err)
	}
	nanos, present, err := db.store.LastSurfaced("sess-A", cid)
	if err != nil || !present || nanos == 0 {
		t.Errorf("SurfaceItem should MarkSurfaced(sess-A,%d); got present=%v nanos=%d err=%v", cid, present, nanos, err)
	}
}
