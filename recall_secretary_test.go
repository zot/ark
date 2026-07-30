package ark

// CRC: crc-RecallAgentBuilder.md, crc-RecallWatcher.md | Test: test-Secretary.md

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// --- Recall Secretary seam 3a tests ---

// TestUserProse verifies genuine-user extraction. R3009 keys on the positive
// human-origin marker (origin.kind == "human"), not origin-absence: a genuine
// turn is stamped "human" and yields its prose string; an unstamped turn, an
// injected notification, tool-results, and empty content are all rejected. Used
// by the R2891 conversation-injection path.
func TestUserProse(t *testing.T) {
	cases := []struct {
		name    string
		origin  string
		content string
		want    string
		ok      bool
	}{
		{"genuine", "human", `"hello there"`, "hello there", true},
		{"unstamped", "", `"hello there"`, "", false}, // R3009: no human marker → not genuine
		{"notification", "task-notification", `"background done"`, "", false},
		{"tool-result", "human", `[{"type":"tool_result","content":"x"}]`, "", false},
		{"empty", "human", ``, "", false},
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
	w := NewRecallWatcher(db, nil, db.store, nil)

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
	w := NewRecallWatcher(db, nil, db.store, nil)
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
	// R3318: the dictated re-run command names the tube, not a session. The
	// crank handle IS the secretary's instruction, so a flag that no longer
	// exists here would fail its very next loop iteration.
	if !strings.Contains(prompt, "--tube sess-A 2") {
		t.Error("recallDocPrompt must carry the tube+nonce re-run command")
	}
	if strings.Contains(prompt, "--session") {
		t.Error("recallDocPrompt dictates a --session flag the CLI no longer accepts (R3318)")
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

// TestResolveTube pins the address derivation (R3322): a secretary is never
// told its own tube, so the server works it out from the identity and nonce the
// call already carries. An address a model types is a snapshot, and a re-host
// makes every snapshot stale, after which the secretary waits on a tube nobody
// publishes to — silent, and shaped exactly like a corpus with nothing to say.
// Refs: R3322, R3032
func TestResolveTube(t *testing.T) {
	cases := []struct {
		name      string
		requested string
		session   string
		nonce     uint32
		pooled    bool
		want      string
	}{
		// The load-bearing pair. Collapse it and every session's own secretary
		// subscribes to <S>-<nonce>, a tube the watcher never publishes to.
		{"pool secretary gets the composite", "", "S", 7, true, "S-7"},
		{"own-session secretary gets the bare session", "", "S", 7, false, "S"},
		// --tube is an override, not a hint: derivation must not second-guess it.
		{"explicit tube wins over derivation", "OTHER-9", "S", 7, true, "OTHER-9"},
		{"explicit tube wins when unpooled too", "OTHER-9", "S", 7, false, "OTHER-9"},
		// No identity means nothing to derive from. The server must leave the
		// caller unaddressed rather than invent a tube. Unreachable from ark's
		// own CLI, which fails before its Action without an identity (R3320).
		{"no identity derives nothing", "", "", 7, true, ""},
		{"no identity, unpooled, derives nothing", "", "", 7, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveTube(tc.requested, tc.session, tc.nonce, tc.pooled); got != tc.want {
				t.Errorf("resolveTube(%q, %q, %d, %v) = %q, want %q",
					tc.requested, tc.session, tc.nonce, tc.pooled, got, tc.want)
			}
		})
	}
}

// TestIsPoolNonceNilSafe confirms the roster lookup the derivation depends on is
// safe on a server with no watcher — the derivation runs on every `next` call,
// so a nil-panic here would take down the loop verb for every secretary.
// Refs: R3322
func TestIsPoolNonceNilSafe(t *testing.T) {
	var w *RecallWatcher
	if w.IsPoolNonce(7) {
		t.Error("a nil watcher must report no pool membership")
	}
	if (&RecallWatcher{}).IsPoolNonce(7) {
		t.Error("a watcher with no rosters must report no pool membership")
	}
}
