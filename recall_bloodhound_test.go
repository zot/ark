package ark

// CRC: crc-RecallWatcher.md, crc-RecallAgentBuilder.md | Test: test-Bloodhound.md
//
// Bloodhound (directed search): recognition, cookie routing, the search task
// shape, and the finding round-trip. R2934, R2935, R2937, R2938, R2943,
// R2944, R2945, R2946.

import (
	"strings"
	"testing"

	"github.com/zot/microfts2"
)

// TestScanBloodhounds covers recognition (R2934): watermarks in assistant text
// only, multi-line capture, multiple-per-line, non-assistant ignored, and
// orthogonality to the turn signals (R2935 — the same bytes carry a
// turn_duration that scanBloodhounds ignores and scanNewBytes alone reports).
func TestScanBloodhounds(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"none", `{"type":"assistant","message":{"content":"just chatting"}}`, nil},
		{"single", `{"type":"assistant","message":{"content":[{"type":"text","text":"sure <BLOODHOUND>did we discuss BM25? verdict</BLOODHOUND> ok"}]}}`, []string{"did we discuss BM25? verdict"}},
		{"two-in-one-line", `{"type":"assistant","message":{"content":"<BLOODHOUND>a</BLOODHOUND> and <BLOODHOUND>b</BLOODHOUND>"}}`, []string{"a", "b"}},
		{"non-assistant-ignored", `{"type":"user","message":{"content":"<BLOODHOUND>x</BLOODHOUND>"}}`, nil},
	}
	for _, tc := range cases {
		if got := scanBloodhounds([]byte(tc.in)); !equalStrings(got, tc.want) {
			t.Errorf("%s: scanBloodhounds = %q want %q", tc.name, got, tc.want)
		}
	}

	// Multi-line payload captured whole (DOTALL).
	multi := `{"type":"assistant","message":{"content":"<BLOODHOUND>investigate dedup\nstop when you name the key</BLOODHOUND>"}}`
	if got := scanBloodhounds([]byte(multi)); len(got) != 1 || !strings.Contains(got[0], "stop when you name the key") {
		t.Errorf("multi-line: got %q", got)
	}

	// Orthogonality (R2935): a buffer with BOTH a turn_duration and a watermark.
	mixed := []byte(`{"type":"system","subtype":"turn_duration"}` + "\n" +
		`{"type":"assistant","message":{"content":"<BLOODHOUND>find X</BLOODHOUND>"}}`)
	if bh := scanBloodhounds(mixed); len(bh) != 1 || bh[0] != "find X" {
		t.Errorf("mixed bloodhound: got %q", bh)
	}
	if sigs := scanNewBytes(mixed); len(sigs) != 1 || sigs[0] != signalTurnDuration {
		t.Errorf("mixed signal: got %v", sigs)
	}
}

// TestParseBloodhoundToken covers cookie routing (R2945): a kind-marked
// <session>-b<B> parses as bloodhound; a plain recall fire token does not, so
// the shared `close` routes both kinds correctly.
func TestParseBloodhoundToken(t *testing.T) {
	sess := "5d081d3a-e87c-40e1-a993-ba78ddfa3d75"
	cookie := bloodhoundToken(sess, 7)
	if cookie != sess+"-b7" {
		t.Fatalf("bloodhoundToken = %q", cookie)
	}
	if gotSess, bid, ok := parseBloodhoundToken(cookie); !ok || gotSess != sess || bid != 7 {
		t.Errorf("parseBloodhoundToken(%q) = (%q,%d,%v)", cookie, gotSess, bid, ok)
	}
	if _, _, ok := parseBloodhoundToken(fireToken(sess, 7)); ok {
		t.Errorf("recall fire token must not parse as bloodhound")
	}
	if _, _, ok := parseBloodhoundToken(sess + "-bx"); ok {
		t.Errorf("-bx must not parse as bloodhound")
	}
}

// TestBuildSearchTask covers the task doc shape (R2937, R2938, R3006): curate
// head tag, the ## Search task header with cookie, the payload, the ## Recall
// seed block between the payload and the crank handle, and the crank handle with
// the cookie substituted; stripCurateTagLine drops the head tag.
func TestBuildSearchTask(t *testing.T) {
	cookie := bloodhoundToken("sess-A", 3)
	seed := renderBloodhoundSeed(&RecallResult{Chunks: []RecalledChunk{
		{Path: "knowledge/bm25.md", Range: "12-19", Score: 0.72, Content: "BM25 was considered for recall", Tags: []RecallTag{{Tag: "topic"}}},
	}})
	body := buildSearchTask("sess-A", cookie, "where is the tag-strip logic? pointers", seed)
	for _, want := range []string{
		"@ark-secretary-work: sess-A",
		"## Search task " + cookie,
		"where is the tag-strip logic? pointers",
		"## Recall seed",
		"knowledge/bm25.md:12-19",
		"finding " + cookie,
		"close " + cookie,
		"<your nonce>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("buildSearchTask missing %q", want)
		}
	}
	// The seed rides on path:range, never a chunkid on the wire (R3006).
	if strings.Contains(body, "@chunk-id") {
		t.Errorf("buildSearchTask seed leaked a chunkid")
	}
	// Seed sits between the payload and the crank handle.
	if strings.Index(body, "## Recall seed") >= strings.Index(body, "You are the bloodhound") {
		t.Errorf("## Recall seed must precede the crank handle")
	}
	if strings.Contains(body, "COOKIE") {
		t.Errorf("buildSearchTask left COOKIE unsubstituted")
	}
	stripped := stripCurateTagLine(body)
	if strings.HasPrefix(stripped, "@ark-secretary-work:") || !strings.HasPrefix(stripped, "## Search task") {
		t.Errorf("stripCurateTagLine: got %.40q", stripped)
	}
}

// TestRenderBloodhoundSeed covers the seed renderer (R3006): compact locator
// lines with size/score/tags and no chunkid, and the empty-seed note.
func TestRenderBloodhoundSeed(t *testing.T) {
	got := renderBloodhoundSeed(&RecallResult{Chunks: []RecalledChunk{
		{ChunkID: 84213, Path: "knowledge/bm25.md", Range: "12-19", Score: 0.72, Content: "BM25 was considered\nsecond line", Tags: []RecallTag{{Tag: "topic"}, {Tag: "method"}}},
	}})
	for _, want := range []string{
		"## Recall seed",
		"knowledge/bm25.md:12-19",
		"0.72",
		"[topic, method]",
		"> BM25 was considered",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("renderBloodhoundSeed missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "84213") || strings.Contains(got, "@chunk-id") {
		t.Errorf("renderBloodhoundSeed leaked a chunkid:\n%s", got)
	}
	// Empty / nil result → empty-seed note, still a ## Recall seed block.
	for _, empty := range []*RecallResult{nil, {}} {
		note := renderBloodhoundSeed(empty)
		if !strings.Contains(note, "## Recall seed") || !strings.Contains(note, "no corpus matches") {
			t.Errorf("empty seed note wrong: %q", note)
		}
	}
}

// TestBloodhoundRoundTrip covers the finding return channel (R2943, R2944,
// R2945, R2946): open a task, add an -answer and a -loc finding (no own-session
// gate), close, and verify the finding doc carries the clue header + items, the
// task doc is gone, and a silent close writes no finding doc.
func TestBloodhoundRoundTrip(t *testing.T) {
	_, db := setupRecall(t)
	// Bloodhound docs are written with the "markdown" strategy (as curation
	// docs are); register it for the in-memory test fts.
	if err := db.fts.AddChunker("markdown", microfts2.MarkdownChunker{}); err != nil {
		t.Fatalf("AddChunker(markdown): %v", err)
	}
	b := &RecallAgentBuilder{
		db:              db,
		bloodhounds:     make(map[string]*recallResultDoc),
		bloodhoundClues: make(map[string]string),
	}
	cid, _ := indexLine(t, db, "x.md", "the tag-strip-at-embed logic lives here")
	info, err := db.ChunkInfo(cid)
	if err != nil {
		t.Fatal(err)
	}
	loc := info.Path + ":" + info.Range

	const sess = "sess-A"
	if err := b.RecallBloodhoundOpen(sess, 1, "where is the tag-strip logic? pointers", renderBloodhoundSeed(nil)); err != nil {
		t.Fatal(err)
	}
	cookie := bloodhoundToken(sess, 1)
	if data, err := db.TmpContent(bloodhoundTaskPath(sess, 1)); err != nil || !strings.Contains(string(data), "## Search task "+cookie) || !strings.Contains(string(data), "## Recall seed") {
		t.Fatalf("task doc missing/short: err=%v", err)
	}
	if err := b.FindingItem(cookie, "", "It's in stripArkTags at embed time.", ""); err != nil {
		t.Fatal(err)
	}
	// -loc finding — accepted with no own-session gate (R2944).
	if err := b.FindingItem(cookie, loc, "", "the embed-time strip"); err != nil {
		t.Fatal(err)
	}
	if err := b.closeBloodhound(cookie, sess, 1, 9); err != nil {
		t.Fatal(err)
	}
	data, err := db.TmpContent(bloodhoundFindingPath(sess, 1))
	if err != nil {
		t.Fatalf("finding doc not written: %v", err)
	}
	out := string(data)
	for _, want := range []string{
		"@ark-bloodhound-result: " + sess,
		"## Finding: where is the tag-strip logic? pointers",
		"It's in stripArkTags at embed time.",
		info.Path + ":" + info.Range,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("finding doc missing %q in:\n%s", want, out)
		}
	}
	if _, err := db.TmpContent(bloodhoundTaskPath(sess, 1)); err == nil {
		t.Errorf("task doc should be removed after close")
	}

	// Silent close: a fresh task with no findings writes no finding doc.
	if err := b.RecallBloodhoundOpen(sess, 2, "anything?", ""); err != nil {
		t.Fatal(err)
	}
	if err := b.closeBloodhound(bloodhoundToken(sess, 2), sess, 2, 9); err != nil {
		t.Fatal(err)
	}
	if _, err := db.TmpContent(bloodhoundFindingPath(sess, 2)); err == nil {
		t.Errorf("silent close should write no finding doc")
	}
}
