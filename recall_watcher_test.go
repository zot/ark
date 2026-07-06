package ark

// CRC: crc-RecallWatcher.md | Test: test-RecallWatcher.md
//
// Unit coverage for the watcher's pure helpers and the
// SourceQualifies / OnAppend gates. Full pipeline coverage
// (turn_duration arming, fire → per-chunk Recall + grouped DM
// + RD writes) lands alongside the larger integration
// scaffolding — see O117 in design.md.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionFromJSONLPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/home/u/.claude/projects/foo/abc123.jsonl", "abc123"},
		{"abc123.jsonl", "abc123"},
		{"/path/no-ext", "no-ext"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := sessionFromJSONLPath(tc.path); got != tc.want {
			t.Errorf("sessionFromJSONLPath(%q) = %q want %q", tc.path, got, tc.want)
		}
	}
}

// R2705, R2738 — bound enforcement is UTF-8 safe.
func TestTruncateUTF8(t *testing.T) {
	cases := []struct {
		in       string
		maxBytes int
		want     string
	}{
		{"hello", 100, "hello"},
		{"hello world", 5, "hello"},
		{"héllo", 2, "h"},
		{"héllo", 3, "hé"},
		{"héllo", 4, "hél"},
		{"日本語", 5, "日"},
		{"abc", 0, ""},
	}
	for _, tc := range cases {
		got := truncateUTF8(tc.in, tc.maxBytes)
		if got != tc.want {
			t.Errorf("truncateUTF8(%q, %d) = %q want %q", tc.in, tc.maxBytes, got, tc.want)
		}
	}
}

// R2731, R2732, R3009 — per-line JSON scan picks up turn_duration and
// *genuine* user signals; "userType" inside other records doesn't false-trip,
// and a user record without the positive origin.kind=="human" marker (R3009)
// yields no signal (an origin-less line stands in for a tool-result / injected
// wake turn — counting it would re-arm the recall ping-pong).
func TestScanNewBytes_Signals(t *testing.T) {
	input := []byte(`{"type":"assistant","content":"hi"}
{"type":"system","subtype":"turn_duration","durationMs":12,"userType":"external"}
{"type":"user","message":{"content":"injected wake"}}
{"type":"user","message":{"content":"hello"},"origin":{"kind":"human"}}
{"type":"tool_use","name":"Bash"}
`)
	got := scanNewBytes(input)
	want := []jsonlSignal{signalTurnDuration, signalUser}
	if len(got) != len(want) {
		t.Fatalf("scanNewBytes: got %d signals %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("scanNewBytes[%d] = %v want %v", i, got[i], want[i])
		}
	}
}

func TestScanNewBytes_PartialTrailingLine(t *testing.T) {
	// Unterminated trailing line — scanner attempts a parse and
	// silently skips when the JSON is incomplete. The complete line is
	// a genuine user message (string content + the positive
	// origin.kind=="human" marker, R3009) so it yields signalUser.
	input := []byte(`{"type":"user","message":{"content":"hi"},"origin":{"kind":"human"}}` + "\n" + `{"type":"sys`)
	got := scanNewBytes(input)
	if len(got) != 1 || got[0] != signalUser {
		t.Errorf("scanNewBytes partial: got %v want [signalUser]", got)
	}
}

// R2747, R2748, R2749 — curation-doc shape.
func TestRecallCurationBuilder_Shape(t *testing.T) {
	b := &RecallAgentBuilder{
		curations: make(map[string]*RecallCurationBuilder),
		results:   make(map[string]*recallResultDoc),
	}
	cb := b.RecallCurationOpen("sess-abc", 17)
	cb.Section("conv.jsonl", "5-9", "the user's question about asparagus")
	cb.Candidate(
		"notes/foo.md", "12-18", 1834, 0.84,
		"main-meaning", ChunkSubstrate{VectorEC: 0.84, TrigramEC: 0.40},
		[]string{"cooking", "course"},
		[]string{"persona"}, []float64{0.72},
		"asparagus risotto", false,
	)
	cb.Section("conv.jsonl", "20-24", "assistant explanation of risotto technique")
	cb.Candidate(
		"notes/bar.md", "1-7", 480, 0.76,
		"main-tags", ChunkSubstrate{TagVector: 0.76, TagTrigram: 0.30},
		[]string{"technique"},
		nil, nil,
		"toast the rice in fat", false,
	)
	body := cb.buf.String()

	if !strings.HasPrefix(body, "@ark-secretary-work: sess-abc\n@ark-recall-fire: 17\n") {
		t.Errorf("body must lead with header tags; got:\n%s", body)
	}
	if !strings.Contains(body, "\n# Source: conv.jsonl:5-9\n") {
		t.Errorf("missing section 1 H1")
	}
	if !strings.Contains(body, "\n# Source: conv.jsonl:20-24\n") {
		t.Errorf("missing section 2 H1")
	}
	if !strings.Contains(body, "## Candidate: notes/foo.md:12-18 (2K)\n") {
		t.Errorf("missing candidate 1 H2 (path:range + size)")
	}
	if !strings.Contains(body, "## Candidate: notes/bar.md:1-7 (480b)\n") {
		t.Errorf("missing candidate 2 H2 (path:range + size)")
	}
	if !strings.Contains(body, "- score: 0.84\n") {
		t.Errorf("missing candidate 1 score line")
	}
	if !strings.Contains(body, "- cell: main-meaning\n") {
		t.Errorf("missing candidate 1 cell line (R2909)")
	}
	if !strings.Contains(body, "- evidence: text-vec=0.84 text-tri=0.40 tag-vec=0.00 tag-tri=0.00\n") {
		t.Errorf("missing candidate 1 evidence line (R2909)")
	}
	if !strings.Contains(body, "- tags: cooking, course\n") {
		t.Errorf("missing candidate 1 tags line")
	}
	if !strings.Contains(body, "- proposed-tags: persona (0.72)\n") {
		t.Errorf("missing candidate 1 proposed-tags line")
	}
	if !strings.Contains(body, "```\nasparagus risotto\n```") {
		t.Errorf("missing candidate 1 fenced content excerpt")
	}
	if i, j := strings.Index(body, "# Source: conv.jsonl:5-9"), strings.Index(body, "# Source: conv.jsonl:20-24"); i < 0 || i >= j {
		t.Errorf("section ordering wrong: 5-9 at %d, 20-24 at %d", i, j)
	}
	if cb.Sections() != 2 {
		t.Errorf("Sections() = %d, want 2", cb.Sections())
	}
}

// R2869 — a tag-only candidate (own-session) renders the `- tag-only: true`
// marker so the agent recommends but never surfaces it; non-tag-only
// candidates omit the line.
func TestRecallCurationBuilder_TagOnly(t *testing.T) {
	b := &RecallAgentBuilder{
		curations: make(map[string]*RecallCurationBuilder),
		results:   make(map[string]*recallResultDoc),
	}
	cb := b.RecallCurationOpen("sess-xyz", 3)
	cb.Section("conv.jsonl", "30-34", "user revisiting an earlier point")
	cb.Candidate("~/.claude/projects/p/sess-xyz.jsonl", "40-44", 300, 0.81,
		"conversation-tags", ChunkSubstrate{TagVector: 0.81},
		[]string{"topic"}, nil, nil, "earlier we discussed this", true)
	cb.Candidate("notes/ext.md", "1-3", 200, 0.79,
		"main-tags", ChunkSubstrate{TagVector: 0.79},
		[]string{"topic"}, nil, nil, "external knowledge", false)
	body := cb.buf.String()

	if strings.Count(body, "- tag-only: true\n") != 1 {
		t.Errorf("expected exactly one tag-only marker; got body:\n%s", body)
	}
	own := strings.Index(body, "## Candidate: ~/.claude/projects/p/sess-xyz.jsonl:40-44")
	ext := strings.Index(body, "## Candidate: notes/ext.md:1-3")
	marker := strings.Index(body, "- tag-only: true")
	if !(own < marker && marker < ext) {
		t.Errorf("tag-only marker must sit under candidate 7001, not 7002 (own=%d marker=%d ext=%d)", own, marker, ext)
	}
}

// R2901 — the per-session fire counter seeds from the surviving curation
// files at max+2 (skipping a possibly-unmaterialized in-flight doc), then
// increments in memory; a session with no survivors starts at 1.
func TestRecallWatcher_FireSeed(t *testing.T) {
	tmp := t.TempDir()
	// Survivors for sess-A up to fire 7 (out of order on disk); none for sess-B.
	for _, f := range []string{"curation-sess-A-3.md", "curation-sess-A-7.md", "curation-sess-A-5.md"} {
		if err := os.WriteFile(filepath.Join(tmp, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w := &RecallWatcher{
		builder:      &RecallAgentBuilder{curationDir: tmp},
		fireCounters: make(map[string]uint64),
	}
	w.mu.Lock()
	first := w.nextFireLocked("sess-A")
	second := w.nextFireLocked("sess-A")
	bFirst := w.nextFireLocked("sess-B")
	w.mu.Unlock()

	if first != 9 { // max 7 + 2
		t.Errorf("sess-A first fire = %d, want 9 (max 7 + 2)", first)
	}
	if second != 10 { // in-memory increment, no re-scan
		t.Errorf("sess-A second fire = %d, want 10", second)
	}
	if bFirst != 1 { // no survivors → start at 1
		t.Errorf("sess-B first fire = %d, want 1", bFirst)
	}
}

// R2688 — Enabled() reads live config; nil watcher is safe.
func TestRecallWatcher_NilWatcher_NoOps(t *testing.T) {
	var w *RecallWatcher
	if w.Enabled() {
		t.Errorf("nil watcher should not report enabled")
	}
	w.OnAppend("x.jsonl", "chat-jsonl", []byte(`{"type":"user"}`+"\n"), nil) // must not panic
}

// R2696, R2741 — SourceQualifies enforces strategy + (optional)
// source-dir whitelist. Uses a minimal in-memory DB shim.
func TestRecallWatcher_SourceQualifies(t *testing.T) {
	cfgEnabledAll := RecallConfig{Enabled: true}
	cfgDisabled := RecallConfig{Enabled: false}

	cases := []struct {
		name  string
		cfg   RecallConfig
		path  string
		strat string
		want  bool
	}{
		{"disabled rejects", cfgDisabled, "/home/u/.claude/projects/foo/abc.jsonl", "chat-jsonl", false},
		{"wrong strategy", cfgEnabledAll, "/p/foo.md", "markdown", false},
		{"chat-jsonl, no whitelist", cfgEnabledAll, "/p/foo.jsonl", "chat-jsonl", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := &RecallWatcher{db: newWatcherTestDB(tc.cfg)}
			if got := w.SourceQualifies(tc.path, tc.strat); got != tc.want {
				t.Errorf("SourceQualifies(%q, %q) = %v want %v", tc.path, tc.strat, got, tc.want)
			}
		})
	}
}

// newWatcherTestDB builds a minimal *DB whose Config() returns the
// supplied RecallConfig. Only SourceQualifies / config reads touch
// it in these unit tests; other DB facilities remain nil.
func newWatcherTestDB(rc RecallConfig) *DB {
	cfg := &Config{Recall: rc}
	return &DB{config: cfg}
}
