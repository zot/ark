package ark

// CRC: crc-RecallWatcher.md | Test: test-RecallWatcher.md
//
// Unit coverage for the watcher's pure helpers and the
// SourceQualifies / OnAppend gates. Full pipeline coverage
// (turn_duration arming, fire → per-chunk Recall + grouped DM
// + RD writes) lands alongside the larger integration
// scaffolding — see O117 in design.md.

import (
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

// R2731, R2732 — per-line JSON scan picks up turn_duration and
// user signals; "userType" inside other records doesn't false-trip.
func TestScanNewBytes_Signals(t *testing.T) {
	input := []byte(`{"type":"assistant","content":"hi"}
{"type":"system","subtype":"turn_duration","durationMs":12,"userType":"external"}
{"type":"user","message":{"content":"hello"}}
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
	// a genuine user message (content is a JSON string, no origin —
	// R2732) so it yields signalUser.
	input := []byte(`{"type":"user","message":{"content":"hi"}}` + "\n" + `{"type":"sys`)
	got := scanNewBytes(input)
	if len(got) != 1 || got[0] != signalUser {
		t.Errorf("scanNewBytes partial: got %v want [signalUser]", got)
	}
}

// R2747, R2748, R2749 — curation-doc shape.
func TestRecallCurationBuilder_Shape(t *testing.T) {
	b := &RecallAgentBuilder{
		curations: make(map[uint64]*RecallCurationBuilder),
		results:   make(map[uint64]*recallResultDoc),
	}
	cb := b.RecallCurationOpen("sess-abc", 17)
	cb.Section(1001, "the user's question about asparagus")
	cb.Candidate(
		4711, "notes/foo.md", "12-18", 1834, 0.84,
		[]string{"cooking", "course"},
		[]string{"persona"}, []float64{0.72},
		"asparagus risotto", false,
	)
	cb.Section(1002, "assistant explanation of risotto technique")
	cb.Candidate(
		5023, "notes/bar.md", "1-7", 480, 0.76,
		[]string{"technique"},
		nil, nil,
		"toast the rice in fat", false,
	)
	body := cb.buf.String()

	if !strings.HasPrefix(body, "@ark-recall-curate: sess-abc\n@ark-recall-fire: 17\n") {
		t.Errorf("body must lead with header tags; got:\n%s", body)
	}
	if !strings.Contains(body, "\n# Source Chunk: 1001\n") {
		t.Errorf("missing section 1 H1")
	}
	if !strings.Contains(body, "\n# Source Chunk: 1002\n") {
		t.Errorf("missing section 2 H1")
	}
	if !strings.Contains(body, "## Candidate: 4711 (2K) notes/foo.md:12-18\n") {
		t.Errorf("missing candidate 1 H2 (with size)")
	}
	if !strings.Contains(body, "## Candidate: 5023 (480b) notes/bar.md:1-7\n") {
		t.Errorf("missing candidate 2 H2 (with size)")
	}
	if !strings.Contains(body, "- score: 0.84\n") {
		t.Errorf("missing candidate 1 score line")
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
	if i, j := strings.Index(body, "Source Chunk: 1001"), strings.Index(body, "Source Chunk: 1002"); i < 0 || i >= j {
		t.Errorf("section ordering wrong: 1001 at %d, 1002 at %d", i, j)
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
		curations: make(map[uint64]*RecallCurationBuilder),
		results:   make(map[uint64]*recallResultDoc),
	}
	cb := b.RecallCurationOpen("sess-xyz", 3)
	cb.Section(2001, "user revisiting an earlier point")
	cb.Candidate(7001, "~/.claude/projects/p/sess-xyz.jsonl", "40-44", 300, 0.81,
		[]string{"topic"}, nil, nil, "earlier we discussed this", true)
	cb.Candidate(7002, "notes/ext.md", "1-3", 200, 0.79,
		[]string{"topic"}, nil, nil, "external knowledge", false)
	body := cb.buf.String()

	if strings.Count(body, "- tag-only: true\n") != 1 {
		t.Errorf("expected exactly one tag-only marker; got body:\n%s", body)
	}
	own := strings.Index(body, "## Candidate: 7001")
	ext := strings.Index(body, "## Candidate: 7002")
	marker := strings.Index(body, "- tag-only: true")
	if !(own < marker && marker < ext) {
		t.Errorf("tag-only marker must sit under candidate 7001, not 7002 (own=%d marker=%d ext=%d)", own, marker, ext)
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
