package ark

// CRC: crc-RecallAgentBuilder.md, crc-RecallWatcher.md | Test: test-Secretary.md

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zot/microfts2"
)

// --- Recall Secretary seam 3a tests ---

// TestUserProse verifies genuine-user extraction, now through the transcript
// reader's Prose (R3553). R3009 keys on the positive human-origin marker
// (origin.kind == "human"), not origin-absence: a genuine turn is stamped
// "human" and yields its prose; an unstamped turn, an injected notification,
// tool-results, and empty content are all rejected. Used by the R2891
// conversation-injection path and by recall's arming.
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
		line := `{"type":"user"`
		if tc.origin != "" {
			line += `,"origin":{"kind":"` + tc.origin + `"}`
		}
		if tc.content != "" {
			line += `,"message":{"role":"user","content":` + tc.content + `}`
		}
		line += `}`
		rec, used := ParseTranscriptLine([]byte(line), 1, nil)
		got, ok := rec.Prose()
		if !used || ok != tc.ok || got != tc.want {
			t.Errorf("%s: Prose(%s) = (%q,%v) used=%v want (%q,%v)", tc.name, line, got, ok, used, tc.want, tc.ok)
		}
	}
}

// TestAssistantText verifies assistant-text extraction from both the
// bare-string and content-array shapes, now through the transcript reader's
// Text (R3553); tool_use blocks are skipped. R2891
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
		line := `{"type":"assistant"`
		if tc.content != "" {
			line += `,"message":{"role":"assistant","content":` + tc.content + `}`
		}
		line += `}`
		rec, _ := ParseTranscriptLine([]byte(line), 1, nil)
		if got := rec.Text(); got != tc.want {
			t.Errorf("%s: Text(%s) = %q want %q", tc.name, line, got, tc.want)
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

// R3322: a secretary is never told to type its routing address. RecallNext
// routes on the resolved tube but its "run next again" line echoes only what
// the caller typed. Measured 2026-10-22: every prompt echoed the resolved
// composite as `--tube`, the template forbids passing one, and a pool
// secretary alternated between the two forms and quit.
func TestRecallNextEchoesOnlyTheRequestedTube(t *testing.T) {
	_, db := setupRecall(t)
	if err := db.fts.AddChunker("markdown", microfts2.MarkdownChunker{}); err != nil {
		t.Fatalf("AddChunker(markdown): %v", err)
	}
	db.SetPubSub(NewPubSub(time.Minute, 32))
	b := NewRecallAgentBuilder(db)
	const sess = "sess-A"
	subscribeFinding(t, db, sess) // R2947: a task waits for its listen
	for i, tc := range []struct {
		requested string
		want      string
	}{
		{"", "~/.ark/ark connections recall next 3 again"},
		{"override-X", "~/.ark/ark connections recall next --tube override-X 3 again"},
	} {
		bid := uint64(i + 1)
		if err := b.RecallBloodhoundOpen(sess, bid, bloodhoundReq{payload: "where is it? pointers"}, ""); err != nil {
			t.Fatal(err)
		}
		body, exit, err := b.RecallNext(context.Background(), 3, sess, tc.requested, 0)
		if err != nil || exit {
			t.Fatalf("RecallNext: exit=%v err=%v", exit, err)
		}
		if !strings.Contains(body, "Search task") {
			t.Fatalf("expected the pending search task, got:\n%s", body)
		}
		if !strings.Contains(body, tc.want) {
			t.Errorf("requested=%q: prompt lacks %q; got tail:\n%s", tc.requested, tc.want, body[max(0, len(body)-200):])
		}
		if tc.requested == "" && strings.Contains(body, "--tube") {
			t.Errorf("prompt tells the secretary to pass --tube although it typed none")
		}
		if err := SyncVoid(db, func(db *DB) error { return db.RemoveTmpFile(bloodhoundTaskPath(sess, bid)) }); err != nil {
			t.Fatal(err)
		}
	}
}

// R2865: listen's crank handle describes what arrived. Measured 2026-10-22: a
// directed finding came back calling itself ambient recall, with Surface and
// Recommend guidance for items it did not have.
func TestListenFooterDescribesWhatArrived(t *testing.T) {
	_, db := setupRecall(t)
	if err := db.fts.AddChunker("markdown", microfts2.MarkdownChunker{}); err != nil {
		t.Fatalf("AddChunker(markdown): %v", err)
	}
	b := NewRecallAgentBuilder(db)
	write := func(path, body string) {
		t.Helper()
		if err := SyncVoid(db, func(db *DB) error {
			_, e := db.AddTmpFile(path, "markdown", []byte(body))
			return e
		}); err != nil {
			t.Fatal(err)
		}
	}
	write("tmp://T/finding", "## Finding: where is it?\n- a.md:1-2\n")
	write("tmp://T/finding-rec", "## Finding: where is it?\n- a.md:1-2\n\n## Recommend: a.md:1-2 @t\n")
	write("tmp://T/ambient", "## Surface: a.md:1-2\n")

	cases := []struct {
		name           string
		ev             Event
		want, dontWant []string
	}{
		{"finding", Event{Tag: bloodhoundResultTag, Path: "tmp://T/finding"},
			[]string{"answer to a `<BLOODHOUND>`", "recall listen` again"},
			[]string{"ambient recall", "reject-derived"}},
		{"finding with a proposal", Event{Tag: bloodhoundResultTag, Path: "tmp://T/finding-rec"},
			[]string{"answer to a `<BLOODHOUND>`", "reject-derived"},
			[]string{"ambient recall"}},
		{"ambient", Event{Tag: recallResultTag, Path: "tmp://T/ambient"},
			[]string{"ambient recall", "`## Surface:` items", "reject-derived"},
			[]string{"answer to a `<BLOODHOUND>`"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := b.recallListenPrompt("sess-A", []Event{tc.ev})
			footer := out[strings.LastIndex(out, "\n---\n"):]
			for _, w := range tc.want {
				if !strings.Contains(footer, w) {
					t.Errorf("footer lacks %q:\n%s", w, footer)
				}
			}
			for _, w := range tc.dontWant {
				if strings.Contains(footer, w) {
					t.Errorf("footer has %q, which this batch does not warrant:\n%s", w, footer)
				}
			}
		})
	}
}

// R2777, R3560: a subagent's context fill is the *latest* assistant turn that
// reports one, skipping turns with no usage, and its token totals sum every
// assistant turn. Neither was tested before the transcript reader moved them
// (found 2026-10-22 by injecting "first instead of latest" and "drop output",
// which turned nothing red).
func TestSubagentTokenReaders(t *testing.T) {
	turn := func(in, out, create, read int) string {
		return `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"x"}],"usage":{` +
			`"input_tokens":` + itoa(in) + `,"output_tokens":` + itoa(out) +
			`,"cache_creation_input_tokens":` + itoa(create) + `,"cache_read_input_tokens":` + itoa(read) + `}}}`
	}
	body := strings.Join([]string{
		turn(1, 10, 500, 500), // context 1000
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"ok"}]}}`,
		turn(2, 20, 0, 25000), // context 25000: the latest nonzero
		turn(3, 30, 0, 0),     // no context: skipped, but its tokens still count
	}, "\n") + "\n"
	path := filepath.Join(t.TempDir(), "agent.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := SubagentContextTokens(path); !ok || got != 25000 {
		t.Errorf("SubagentContextTokens = (%d, %v), want (25000, true): the latest nonzero fill", got, ok)
	}
	if in, out := sumSubagentTokens(path); in != 6 || out != 60 {
		t.Errorf("sumSubagentTokens = (%d, %d), want (6, 60)", in, out)
	}
	if _, ok := SubagentContextTokens(filepath.Join(t.TempDir(), "missing.jsonl")); ok {
		t.Error("a missing transcript reported a context fill")
	}
}
