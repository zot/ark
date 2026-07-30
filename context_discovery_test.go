package ark

// CRC: crc-RecallAgentBuilder.md, crc-Monitor.md | Seq: seq-recall-agent.md#7.4 | Test: test-ContextDiscovery.md

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// plantSubagent writes a `.meta.json` / `.jsonl` pair for `nonce` under
// <home>/.claude/projects/<project>/<parent>/subagents/, the layout Claude Code
// uses, and returns the project dir so a caller can register it as a source.
// `ctx` becomes the transcript's context fill (cache_creation + cache_read on
// the last assistant record).
func plantSubagent(t *testing.T, home, project, parent string, nonce int, ctx int) string {
	t.Helper()
	projDir := filepath.Join(home, ".claude", "projects", project)
	dir := filepath.Join(projDir, parent, "subagents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	base := filepath.Join(dir, fmt.Sprintf("agent-%s-%d", parent, nonce))
	meta := map[string]any{
		"agentType":   "ark-recall-agent",
		"description": fmt.Sprintf("ark-recall secretary loop nonce %d", nonce),
	}
	mb, _ := json.Marshal(meta)
	if err := os.WriteFile(base+".meta.json", mb, 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
	rec := map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"usage": map[string]any{
				"cache_creation_input_tokens": 0,
				"cache_read_input_tokens":     ctx,
				"input_tokens":                1,
				"output_tokens":               2,
			},
		},
	}
	rb, _ := json.Marshal(rec)
	if err := os.WriteFile(base+".jsonl", append(rb, '\n'), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}
	return projDir
}

// newDiscoveryBuilder builds a RecallAgentBuilder over a temp HOME with the
// given project dirs registered as chat sources. It deliberately does NOT set
// CLAUDE_CODE_SESSION_ID — that is production's environment, and the old
// implementation only ever passed because a test binary inherits the
// developer's shell, which production does not.
func newDiscoveryBuilder(t *testing.T, home string, projectDirs ...string) *RecallAgentBuilder {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	srcs := make([]Source, 0, len(projectDirs))
	for _, d := range projectDirs {
		srcs = append(srcs, Source{Dir: d})
	}
	db := &DB{config: &Config{Sources: srcs}}
	b := NewRecallAgentBuilder(db)
	b.monitorPath = filepath.Join(home, "recall.jsonl")
	return b
}

// TestDiscoveryWithoutSessionEnvVar is the sentry: production's `ark serve` has
// no CLAUDE_CODE_SESSION_ID, and the previous implementation returned "" for
// every nonce because of it — silently, for weeks. Refs: R3306
func TestDiscoveryWithoutSessionEnvVar(t *testing.T) {
	home := t.TempDir()
	proj := plantSubagent(t, home, "-home-deck-work-ark", "parent-A", 7, 41_000)
	b := newDiscoveryBuilder(t, home, proj)

	b.RegisterParentSession(7, "parent-A")

	got, ok := b.ContextTokens(7)
	if !ok {
		t.Fatal("discovery failed with no CLAUDE_CODE_SESSION_ID — that is production's environment")
	}
	if got != 41_000 {
		t.Errorf("context = %d, want 41000", got)
	}
}

// TestRegisteredParentBeatsInheritedEnv guards the sharper half: an inherited
// env var is worse than a missing one, because it resolves the launching
// session's children and silently misses everyone else's. Refs: R3306
func TestRegisteredParentBeatsInheritedEnv(t *testing.T) {
	home := t.TempDir()
	// Two parents, both with a transcript matching `nonce 7`.
	proj := plantSubagent(t, home, "-proj", "right-session", 7, 41_000)
	plantSubagent(t, home, "-proj", "wrong-session", 7, 99_999)
	b := newDiscoveryBuilder(t, home, proj)

	// The environment names the WRONG session; the registry names the right one.
	t.Setenv("CLAUDE_CODE_SESSION_ID", "wrong-session")
	b.RegisterParentSession(7, "right-session")

	got, ok := b.ContextTokens(7)
	if !ok {
		t.Fatal("registered parent did not resolve")
	}
	if got == 99_999 {
		t.Fatal("resolved the environment's session — the getenv is back")
	}
	if got != 41_000 {
		t.Errorf("context = %d, want 41000", got)
	}
}

// TestUnregisteredNonceIsUnmeasured — a failed lookup reports "unknown", not
// "zero". R2760 explicitly mandated logging zeros here, which is the whole
// reason a dead gauge was indistinguishable from an idle system. Refs: R3307
func TestUnregisteredNonceIsUnmeasured(t *testing.T) {
	home := t.TempDir()
	proj := plantSubagent(t, home, "-proj", "parent-A", 7, 41_000)
	b := newDiscoveryBuilder(t, home, proj)
	// nonce 9 never registered.
	if got, ok := b.ContextTokens(9); ok {
		t.Errorf("ContextTokens(9) = (%d, true), want ok=false for an unregistered nonce", got)
	}
}

// TestReserveOutsideAgentShellRegistersNothing — a human running the CLI by
// hand has no session; that must be a clean "unmeasured", not a crash or a
// bogus registration. Refs: R3305
func TestReserveOutsideAgentShellRegistersNothing(t *testing.T) {
	home := t.TempDir()
	b := newDiscoveryBuilder(t, home)
	b.RegisterParentSession(4, "") // empty session, as an unset env yields
	if _, ok := b.parentSession(4); ok {
		t.Error("an empty session must register nothing")
	}
	if _, ok := b.ContextTokens(4); ok {
		t.Error("want unmeasured for a nonce with no parent")
	}
}

// TestAppendMonitorMarksUnmeasured — the flag reaches the log, and is omitted
// on the normal path so existing records and tooling are unaffected.
// Refs: R3307
func TestAppendMonitorMarksUnmeasured(t *testing.T) {
	home := t.TempDir()
	b := newDiscoveryBuilder(t, home)

	b.appendMonitorMeasured(1, "s", 7, 10, 20, 30, 0, 0, 0, "result-emitted", true)
	b.appendMonitorMeasured(2, "s", 8, 0, 0, 0, 0, 0, 0, "result-emitted", false)

	data, err := os.ReadFile(b.monitorPath)
	if err != nil {
		t.Fatalf("read monitor log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 records, got %d", len(lines))
	}
	if strings.Contains(lines[0], "unmeasured") {
		t.Errorf("measured record must omit the flag: %s", lines[0])
	}
	if !strings.Contains(lines[1], `"unmeasured":true`) {
		t.Errorf("unmeasured record must carry the flag: %s", lines[1])
	}
}

// TestMonitorAveragesExcludeUnmeasured reproduces the real numbers: 20 records
// of which 17 were gaps counted as zeros produced avg_context_tokens 6495
// against a measured mean of 43304. Refs: R3308
func TestMonitorAveragesExcludeUnmeasured(t *testing.T) {
	var recs []map[string]any
	for i := 0; i < 17; i++ {
		recs = append(recs, map[string]any{
			"outcome": "result-emitted", "unmeasured": true,
			"in_tokens": float64(0), "out_tokens": float64(0), "context_tokens": float64(0),
			"timestamp": "2026-07-29T12:00:00Z",
		})
	}
	for _, ctx := range []float64{40_000, 44_000, 46_000} {
		recs = append(recs, map[string]any{
			"outcome":   "result-emitted",
			"in_tokens": float64(100), "out_tokens": float64(200), "context_tokens": ctx,
			"timestamp": "2026-07-29T12:00:00Z",
		})
	}

	sum := deriveClassSummary("recall", recs)

	if got := sum.Counters["measured_fires"]; got != 3 {
		t.Errorf("measured_fires = %d, want 3", got)
	}
	if got := sum.Counters["recent_fires"]; got != 20 {
		t.Errorf("recent_fires = %d, want 20 (the window is still 20)", got)
	}
	if got := sum.Counters["avg_context_tokens"]; got != 43_333 {
		t.Errorf("avg_context_tokens = %d, want 43333 (mean of the 3 measured, not 6500 over 20)", got)
	}
	if got := sum.Counters["avg_in_tokens"]; got != 100 {
		t.Errorf("avg_in_tokens = %d, want 100", got)
	}
}
