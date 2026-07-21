package main

// CRC: crc-CLI.md | Test: test-ConnectionsCLI.md, test-Recall.md

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zot/ark"
)

// Test: test-Config.md — reorderArgs puts flags before positional args
func TestReorderArgs(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "flag after positional",
			in:   []string{"*.md", "--source", "/path"},
			want: []string{"--source", "/path", "*.md"},
		},
		{
			name: "flag already first",
			in:   []string{"--source", "/path", "*.md"},
			want: []string{"--source", "/path", "*.md"},
		},
		{
			name: "positional only",
			in:   []string{"*.md"},
			want: []string{"*.md"},
		},
		{
			name: "flag only",
			in:   []string{"--source", "/path"},
			want: []string{"--source", "/path"},
		},
		{
			name: "empty",
			in:   nil,
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reorderArgs(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("reorderArgs(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// TestRecallCLI validates CLI routing, local fallback, missing-model
// gripe, and proxying behavior by driving runConnectionsRecall directly so failure
// paths stay in-process. Refs: R2627, R2630, R2631, R2632, R2633, R2634,
// R2635, R2637, R2638, R2645, R2646
func TestRecallCLI(t *testing.T) {
	tempDir := t.TempDir()

	oldArkDir := arkDir
	defer func() { arkDir = oldArkDir }()
	arkDir = tempDir

	if err := ark.Init(tempDir, ark.InitOpts{}); err != nil {
		t.Fatal(err)
	}

	var fp string
	withDB(func(db *ark.DB) {
		fp = filepath.Join(tempDir, "sample.txt")
		content := "the quick brown fox jumps over the lazy dog\n"
		if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		if err := db.Add([]string{fp}, "lines"); err != nil {
			t.Fatal(err)
		}
	})

	// Case A: in-process trigram-only fallback (no model, no server).
	// -all keeps the tagless test chunks; this test isn't exercising
	// the tag filter, just the fallback routing.
	var buf bytes.Buffer
	if err := runConnectionsRecall([]string{"-all", "quick brown"}, &buf); err != nil {
		t.Fatalf("runConnectionsRecall fallback: %v", err)
	}
	stdoutText := buf.String()
	if !strings.Contains(stdoutText, "## Chunks") {
		t.Errorf("expected ## Chunks header, got:\n%s", stdoutText)
	}
	if !strings.Contains(stdoutText, "@recall-warning: embedding unavailable") {
		t.Errorf("expected warning header, got:\n%s", stdoutText)
	}
	if !strings.Contains(stdoutText, "the quick brown fox") {
		t.Errorf("expected matched chunk content, got:\n%s", stdoutText)
	}

	// Case B: JSON output.
	buf.Reset()
	if err := runConnectionsRecall([]string{"-all", "quick brown", "--json"}, &buf); err != nil {
		t.Fatalf("runConnectionsRecall json: %v", err)
	}
	var res ark.RecallResult
	if err := json.Unmarshal(buf.Bytes(), &res); err != nil {
		t.Fatalf("decode json: %v, output:\n%s", err, buf.String())
	}
	if res.Warning != "embedding unavailable" {
		t.Errorf("expected warning, got %q", res.Warning)
	}
	if len(res.Chunks) == 0 {
		t.Errorf("expected matches in JSON result")
	}

	// Case C: server down + tag_model configured AND file exists →
	// "server not running" error.
	configPath := filepath.Join(tempDir, "ark.toml")
	if err := os.WriteFile(configPath, []byte("[embedding]\nmodel = \"fake-model.gguf\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(tempDir, "fake-model.gguf")
	if err := os.WriteFile(modelPath, []byte("fake model data"), 0644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	err := runConnectionsRecall([]string{"quick brown"}, &buf)
	if err == nil || !strings.Contains(err.Error(), "server not running; model configured") {
		t.Errorf("expected server-not-running error, got %v", err)
	}

	// Case D: server down + tag_model configured BUT file missing →
	// "configured tag_model not found" error. R2646
	if err := os.Remove(modelPath); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	err = runConnectionsRecall([]string{"quick brown"}, &buf)
	if err == nil || !strings.Contains(err.Error(), "configured embedding model not found at") {
		t.Errorf("expected missing-model error, got %v", err)
	}

	// Case E: server running → proxy path.
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	sockPath := filepath.Join(tempDir, "ark.sock")
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "POST" && r.URL.Path == "/recall" {
				resp := ark.RecallResult{
					Chunks: []ark.RecalledChunk{{
						ChunkID: 42,
						Path:    "proxied.txt",
						Range:   "1-1",
						Score:   0.95,
						Tags: []ark.RecallTag{
							{Tag: "topic"},
							{Tag: "status", Value: "in progress"},
						},
						Content: "proxied chunk content line",
					}},
				}
				data, _ := json.Marshal(resp)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(data)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}),
	}
	go srv.Serve(listener)
	defer srv.Close()

	buf.Reset()
	if err := runConnectionsRecall([]string{"proxied input"}, &buf); err != nil {
		t.Fatalf("runConnectionsRecall proxy: %v", err)
	}
	proxyOut := buf.String()
	if !strings.Contains(proxyOut, "proxied chunk content line") {
		t.Errorf("expected proxied content, got:\n%s", proxyOut)
	}

	// R2645: @chunk-tags carries names only; the value-bearing tag
	// appears as a sub-list item under the chunk.
	if !strings.Contains(proxyOut, "@chunk-tags: topic, status\n") {
		t.Errorf("expected name-only @chunk-tags line, got:\n%s", proxyOut)
	}
	if strings.Contains(proxyOut, "@chunk-tags: topic, status:") {
		t.Errorf("@chunk-tags should not embed values, got:\n%s", proxyOut)
	}
	if !strings.Contains(proxyOut, "- @chunk-tag-value: status: in progress\n") {
		t.Errorf("expected @chunk-tag-value sub-item, got:\n%s", proxyOut)
	}
}

// TestParseDiscussedTagArg covers bare-name, exact-pair, and error
// shapes for the `ark discussed` tag-input grammar. R2654
func TestParseDiscussedTagArg(t *testing.T) {
	cases := []struct {
		in        string
		wantTag   string
		wantVal   string
		wantError bool
	}{
		{"@topic", "topic", "", false},
		{"@topic:messaging", "topic", "messaging", false},
		{"@topic: hello world", "topic", " hello world", false}, // value runs to end
		{"@status:in progress", "status", "in progress", false},
		{"@nested:foo:bar", "nested", "foo:bar", false}, // only first colon splits
		{"", "", "", true},
		{"topic", "", "", true},           // missing @ sigil
		{"@", "", "", true},               // empty name
		{"@:value", "", "", true},         // empty name with colon
		{"@bad\x00name", "", "", true},    // NUL in name
		{"@tag:bad\x00val", "", "", true}, // NUL in value
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			tag, value, err := parseDiscussedTagArg(tc.in)
			if tc.wantError {
				if err == nil {
					t.Errorf("expected error, got tag=%q value=%q", tag, value)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tag != tc.wantTag || value != tc.wantVal {
				t.Errorf("got (tag=%q value=%q), want (%q, %q)", tag, value, tc.wantTag, tc.wantVal)
			}
		})
	}
}

// TestParseDiscussedList exercises the comma-separated --discussed
// parser used by `ark connections recall --discussed`. R2654, R2655
func TestParseDiscussedList(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		got, err := parseDiscussedList("")
		if err != nil || len(got) != 0 {
			t.Errorf("expected empty result, got %+v err=%v", got, err)
		}
	})
	t.Run("mixed", func(t *testing.T) {
		got, err := parseDiscussedList("@topic:messaging, @ext, @status:in progress")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 3 {
			t.Fatalf("expected 3 entries, got %d (%+v)", len(got), got)
		}
		if got[0].Tag != "topic" || got[0].Value != "messaging" {
			t.Errorf("entry 0: %+v", got[0])
		}
		if got[1].Tag != "ext" || got[1].Value != "" {
			t.Errorf("entry 1: %+v", got[1])
		}
		if got[2].Tag != "status" || got[2].Value != "in progress" {
			t.Errorf("entry 2: %+v", got[2])
		}
	})
	t.Run("invalid token surfaces error", func(t *testing.T) {
		if _, err := parseDiscussedList("@topic,badtoken"); err == nil {
			t.Errorf("expected error for missing @ on second token")
		}
	})
}

// TestDiscussedCLI_RoundTrip drives the in-process cold-start path
// for `ark discussed add` and `list` to confirm the wiring lands the
// records in the Store. R2650, R2651, R2652
func TestDiscussedCLI_RoundTrip(t *testing.T) {
	tempDir := t.TempDir()
	oldArkDir := arkDir
	defer func() { arkDir = oldArkDir }()
	arkDir = tempDir

	if err := ark.Init(tempDir, ark.InitOpts{}); err != nil {
		t.Fatal(err)
	}

	// add — directly exercise the cold-start path via withDB so we
	// don't hit fatal() on flag/arg validation.
	withDB(func(db *ark.DB) {
		if err := db.AddDiscussed("S1", "topic", "messaging"); err != nil {
			t.Fatalf("AddDiscussed: %v", err)
		}
		if err := db.AddDiscussed("S1", "ext", ""); err != nil {
			t.Fatalf("AddDiscussed bare: %v", err)
		}
	})

	// list — confirm both entries surface.
	var entries []ark.Discussed
	withDB(func(db *ark.DB) {
		es, err := db.ListDiscussed("S1", 0)
		if err != nil {
			t.Fatal(err)
		}
		entries = es
	})
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %+v", len(entries), entries)
	}

	// clear — wipes the session.
	withDB(func(db *ark.DB) {
		count, err := db.ClearDiscussed("S1")
		if err != nil {
			t.Fatal(err)
		}
		if count != 2 {
			t.Errorf("expected 2 cleared, got %d", count)
		}
	})
	withDB(func(db *ark.DB) {
		es, _ := db.ListDiscussed("S1", 0)
		if len(es) != 0 {
			t.Errorf("expected empty after clear, got %+v", es)
		}
	})
}

// CRC: crc-CLI.md | Seq: seq-message.md | Test: test-MessageDM.md
// Covers the composeDM helper that backs `ark message dm` and the
// in-process emit path used by the simple-recall watcher.
// R2716-R2727.
func TestComposeDM_SessionSingleRecipient(t *testing.T) {
	path, payload, err := ark.ComposeDM(
		ark.DMSender{Session: "sess-A"},
		[]string{"sess-B"},
		"", "", "hello\n")
	if err != nil {
		t.Fatalf("composeDM: %v", err)
	}
	if path != "tmp://sess-A/dm-sess-B" {
		t.Errorf("path = %q", path)
	}
	// R2716 single-recipient @dm shape; R2723 @from for session sender.
	wantLines := []string{
		"",
		"@dm: sess-B",
		"@from: sess-A",
		"hello",
		"",
		"",
	}
	if got := strings.Split(payload, "\n"); !reflect.DeepEqual(got, wantLines) {
		t.Errorf("payload lines mismatch:\n got %q\nwant %q", got, wantLines)
	}
}

func TestComposeDM_ServiceWithSubject(t *testing.T) {
	path, payload, err := ark.ComposeDM(
		ark.DMSender{Service: "ARK-RECALL"},
		[]string{"sess-B"},
		"recall", "", "body\n")
	if err != nil {
		t.Fatalf("composeDM: %v", err)
	}
	// R2724 service identity drives the tmp:// sender segment.
	if path != "tmp://ARK-RECALL/dm-sess-B" {
		t.Errorf("path = %q", path)
	}
	// R2716 subject form; R2723 @from-service in place of @from.
	if !strings.Contains(payload, "@dm: sess-B: recall\n") {
		t.Errorf("missing subject form in payload:\n%s", payload)
	}
	if !strings.Contains(payload, "@from-service: ARK-RECALL\n") {
		t.Errorf("missing @from-service in payload:\n%s", payload)
	}
	if strings.Contains(payload, "@from:") {
		t.Errorf("service form should not emit @from")
	}
}

func TestComposeDM_MultiRecipient(t *testing.T) {
	path, payload, err := ark.ComposeDM(
		ark.DMSender{Session: "sess-A"},
		[]string{"sess-B", "sess-C", "sess-D"},
		"standup-ping", "", "body")
	if err != nil {
		t.Fatalf("composeDM: %v", err)
	}
	// R2725 recipients joined by single space.
	if !strings.Contains(payload, "@dm: sess-B sess-C sess-D: standup-ping\n") {
		t.Errorf("multi-recipient @dm shape wrong:\n%s", payload)
	}
	// R2724 path uses the first recipient.
	if path != "tmp://sess-A/dm-sess-B" {
		t.Errorf("path = %q (expected first recipient)", path)
	}
}

func TestComposeDM_RefAppended(t *testing.T) {
	_, payload, err := ark.ComposeDM(
		ark.DMSender{Session: "sess-A"},
		[]string{"sess-B"},
		"", "msg-1", "body")
	if err != nil {
		t.Fatalf("composeDM: %v", err)
	}
	if !strings.Contains(payload, "@ref: msg-1\n") {
		t.Errorf("missing @ref line:\n%s", payload)
	}
}

// R2722 mutex.
func TestComposeDM_RejectsBothSenderForms(t *testing.T) {
	_, _, err := ark.ComposeDM(
		ark.DMSender{Session: "sess-A", Service: "ARK-RECALL"},
		[]string{"sess-B"},
		"", "", "body")
	if err == nil {
		t.Fatal("expected error when both --from and --from-service set")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutex error, got: %v", err)
	}
}

// R2722 required.
func TestComposeDM_RejectsNoSender(t *testing.T) {
	_, _, err := ark.ComposeDM(
		ark.DMSender{},
		[]string{"sess-B"},
		"", "", "body")
	if err == nil {
		t.Fatal("expected error when neither --from nor --from-service set")
	}
}

func TestComposeDM_RejectsEmptyRecipients(t *testing.T) {
	_, _, err := ark.ComposeDM(
		ark.DMSender{Session: "sess-A"},
		nil,
		"", "", "body")
	if err == nil {
		t.Fatal("expected error for empty recipients")
	}
}

// R2718 single-token recipients.
func TestComposeDM_RejectsWhitespaceRecipient(t *testing.T) {
	_, _, err := ark.ComposeDM(
		ark.DMSender{Session: "sess-A"},
		[]string{"two tokens"},
		"", "", "body")
	if err == nil {
		t.Fatal("expected error when a recipient contains whitespace")
	}
}

// R2719 reject trailing-colon "subject" (empty after the colon).
func TestComposeDM_RejectsEmptySubject(t *testing.T) {
	_, _, err := ark.ComposeDM(
		ark.DMSender{Session: "sess-A"},
		[]string{"sess-B"},
		"   ", "", "body")
	if err == nil {
		t.Fatal("expected error for whitespace-only subject")
	}
}

// Test: test-FilterStack.md — the path-only filter stack.
//
// The stack was a search-only DSL exercised only by hand (gap O80). #51 makes
// it the single path-glob surface for six commands, so its arg walker is now
// load-bearing for files, status, tag files, tag values, and subscribe too.
// It is a pure deterministic string transform, so it gets tested rather than
// deferred. R3204, R3205, R3206, R3197

// stackFixture runs parsePathFilterStack from a known directory so the
// anchoring half is deterministic, and reports the results relative to it.
func stackFixture(t *testing.T, args []string) (include, exclude, remaining []string, dir string) {
	t.Helper()
	dir = t.TempDir()
	// t.TempDir on macOS hands back a /var symlink; the anchor joins the
	// resolved cwd, so resolve here too or the comparisons are noise.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(prev)
	include, exclude, remaining = parsePathFilterStack("test", args)
	return include, exclude, remaining, dir
}

// Polarity is sticky: -without applies to every later row until -with.
func TestPathFilterStackStickyPolarity(t *testing.T) {
	inc, exc, rest, dir := stackFixture(t, []string{
		"-files", "a.md", "-without", "-files", "b.md", "-with", "-files", "c.md",
	})
	wantInc := []string{filepath.Join(dir, "a.md"), filepath.Join(dir, "c.md")}
	wantExc := []string{filepath.Join(dir, "b.md")}
	if !reflect.DeepEqual(inc, wantInc) {
		t.Errorf("include = %v, want %v", inc, wantInc)
	}
	if !reflect.DeepEqual(exc, wantExc) {
		t.Errorf("exclude = %v, want %v", exc, wantExc)
	}
	if len(rest) != 0 {
		t.Errorf("remaining = %v, want empty", rest)
	}
}

// Everything that is not a stack token passes through untouched — flags for
// the caller's own flag.Parse and positional arguments alike. This is why the
// path stack is narrower than the search walker: the search walker coalesces
// bare terms into a -contains group, which would swallow `ark tag files TAG`.
func TestPathFilterStackPassesThroughNonStackArgs(t *testing.T) {
	inc, _, rest, dir := stackFixture(t, []string{
		"--status", "-files", "*.md", "mytag", "--detail", "extra",
	})
	if len(inc) != 1 || inc[0] != filepath.Join(dir, "*.md") {
		t.Errorf("include = %v, want one anchored row", inc)
	}
	want := []string{"--status", "mytag", "--detail", "extra"}
	if !reflect.DeepEqual(rest, want) {
		t.Errorf("remaining = %v, want %v", rest, want)
	}
}

// Double-dash and single-dash spellings are the same token, which is exactly
// why the `tag values` boolean had to be renamed to --show-files: `--files`
// normalizes to `-files` before any flag.Parse runs. R3206
func TestPathFilterStackNormalizesDoubleDash(t *testing.T) {
	inc, _, rest, dir := stackFixture(t, []string{"--files", "status"})
	if len(inc) != 1 || inc[0] != filepath.Join(dir, "status") {
		t.Errorf("--files should be read as a filter row consuming the next arg, got %v", inc)
	}
	if len(rest) != 0 {
		t.Errorf("the following arg was consumed as a glob, so nothing remains; got %v", rest)
	}
}

// No positive row means all paths are candidates — a stack of only negative
// rows narrows rather than empties. R3204
func TestPathFilterStackNegativeOnlyKeepsCandidates(t *testing.T) {
	inc, exc, _, dir := stackFixture(t, []string{"-without", "-files", "vendor/**"})
	if len(inc) != 0 {
		t.Errorf("include = %v, want empty", inc)
	}
	if len(exc) != 1 {
		t.Fatalf("exclude = %v, want one row", exc)
	}
	if !ark.MatchPathFilters(filepath.Join(dir, "src/a.go"), inc, exc) {
		t.Error("a path outside the exclusion must still be a candidate")
	}
	if ark.MatchPathFilters(filepath.Join(dir, "vendor/x/a.go"), inc, exc) {
		t.Error("a path inside the exclusion must be rejected")
	}
}

// Anchoring rewrites relative rows and passes absolute ones through, so a
// glob that has already been anchored survives a second pass unchanged
// (filterPaths relies on that idempotence). R3197
func TestPathFilterStackAnchoringIsIdempotent(t *testing.T) {
	inc, _, _, dir := stackFixture(t, []string{
		"-files", "*.md", "-files", "/**/*.md", "-files", "~/x/**",
	})
	if len(inc) != 3 {
		t.Fatalf("include = %v, want three rows", inc)
	}
	if inc[0] != filepath.Join(dir, "*.md") {
		t.Errorf("relative row should anchor to cwd, got %q", inc[0])
	}
	if inc[1] != "/**/*.md" {
		t.Errorf("absolute row should pass through, got %q", inc[1])
	}
	if inc[2] != "~/x/**" {
		t.Errorf("tilde row should pass through for later expansion, got %q", inc[2])
	}
	again := ark.AnchorGlobsToDir(inc, dir)
	if !reflect.DeepEqual(again, inc) {
		t.Errorf("re-anchoring changed the globs: %v -> %v", inc, again)
	}
}

// The retired flags produce a pointing error, never an alias. The message
// must carry BOTH facts: the new spelling and the semantic change. An alias
// would have taught only the first, leaving the user with a filter that
// silently returns fewer results — exactly the invisible failure this
// unification removed. R3205
func TestRetiredPathFlagError(t *testing.T) {
	err := retiredPathFlagError("ark files", "--filter-files")
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	for _, want := range []string{
		"--filter-files",  // what the user typed
		"-files",          // the new spelling
		"-without -files", // the negative form
		"anchored",        // the semantic change...
		"'/**/*.md'",      // ...and how to get the old behavior back
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("pointing error must mention %q; got:\n%s", want, msg)
		}
	}

	// Every spelling the five commands used to accept is refused.
	for _, f := range []string{"-filter-files", "-exclude-files", "-except-files"} {
		if !retiredPathFlags[f] {
			t.Errorf("%s should be a retired path flag", f)
		}
	}
}

// A SkipFlagParsing node's Action receives --help like any other token, so
// without an explicit check the args reach the handler's own flag.Parse and
// the command grows two different help texts: `ark help X` rendering the
// node's Description and `ark X --help` printing the flag package's bare
// listing. helpRequested is what keeps help single-source. R2917, R2918
func TestHelpRequested(t *testing.T) {
	for _, args := range [][]string{
		{"--help"}, {"-h"}, {"-help"},
		{"-files", "*.md", "--help"}, // help after stack rows still counts
		{"mytag", "-h"},
	} {
		if !helpRequested(args) {
			t.Errorf("helpRequested(%v) = false, want true", args)
		}
	}
	for _, args := range [][]string{
		{}, {"-files", "*.md"}, {"mytag"}, {"--show-files", "status"},
	} {
		if helpRequested(args) {
			t.Errorf("helpRequested(%v) = true, want false", args)
		}
	}
}
