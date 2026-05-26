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
{"type":"user","message":{"role":"user"}}
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
	// silently skips when the JSON is incomplete.
	input := []byte(`{"type":"user"}` + "\n" + `{"type":"sys`)
	got := scanNewBytes(input)
	if len(got) != 1 || got[0] != signalUser {
		t.Errorf("scanNewBytes partial: got %v want [signalUser]", got)
	}
}

// R2702, R2703, R2704, R2737, R2738 — grouped body composition.
func TestComposeRecallBody_GroupedShape(t *testing.T) {
	sections := []recallSection{
		{
			sourceChunkID: 1001,
			inputExcerpt: "the user's question about asparagus",
			recalled: []RecalledChunk{{
				ChunkID:      4711,
				Path:         "notes/foo.md",
				Range:        "12-18",
				Score:        0.84,
				PerSubstrate: ChunkSubstrate{VectorEC: 0.91, TrigramEC: 0.62},
				Tags:         []RecallTag{{Tag: "cooking"}, {Tag: "course", Value: "main"}},
				Content:      "asparagus risotto",
			}},
		},
		{
			sourceChunkID: 1002,
			inputExcerpt: "assistant explanation of risotto technique",
			recalled: []RecalledChunk{{
				ChunkID:      5023,
				Path:         "notes/bar.md",
				Range:        "1-7",
				Score:        0.76,
				PerSubstrate: ChunkSubstrate{VectorEC: 0.81, TrigramEC: 0.55},
				Tags:         []RecallTag{{Tag: "technique"}},
				Content:      "toast the rice in fat",
			}},
		},
	}
	body := composeRecallBody("42", sections)

	if !strings.HasPrefix(body, "@ark-recall-fire: 42\n\n") {
		t.Errorf("body must lead with @ark-recall-fire; got:\n%s", body)
	}
	if !strings.Contains(body, "## What this is") {
		t.Errorf("missing instruction block header")
	}
	if !strings.Contains(body, "## Recalled for paragraph\n@source-chunk: 1001") {
		t.Errorf("missing section 1 header + @source-chunk")
	}
	if !strings.Contains(body, "## Recalled for paragraph\n@source-chunk: 1002") {
		t.Errorf("missing section 2 header + @source-chunk")
	}
	if !strings.Contains(body, "> the user's question about asparagus") {
		t.Errorf("missing section 1 excerpt blockquote")
	}
	if !strings.Contains(body, "> assistant explanation of risotto technique") {
		t.Errorf("missing section 2 excerpt blockquote")
	}
	if !strings.Contains(body, "### Recalled chunks") {
		t.Errorf("missing per-section ### Recalled chunks header")
	}
	if !strings.Contains(body, "@chunk-id: 4711") {
		t.Errorf("missing section 1 recalled chunk stencil")
	}
	if !strings.Contains(body, "@chunk-id: 5023") {
		t.Errorf("missing section 2 recalled chunk stencil")
	}
	// Sections appear in input order.
	if i, j := strings.Index(body, "@source-chunk: 1001"), strings.Index(body, "@source-chunk: 1002"); i >= j {
		t.Errorf("section ordering wrong: 1001 at %d, 1002 at %d", i, j)
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
