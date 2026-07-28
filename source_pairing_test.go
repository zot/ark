package ark

// CRC: crc-Config.md | Test: test-SourcePairing.md

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	lua "github.com/yuin/gopher-lua"
)

// TestChatOptionsFromLua drives the *production* option parsers the mcp:
// bindings call, through a real LState and real Lua tables. It deliberately
// does not rebuild the mapping: a hand-mirrored copy would keep passing while
// the binding drifted, which is how a dropped `extmap` stayed green for three
// months. R3287, R3291
func TestChatOptionsFromLua(t *testing.T) {
	L := lua.NewState()
	defer L.Close()

	// A missing or non-table argument is the unnarrowed listing, so
	// `mcp:chatSessions()` works with no arguments at all.
	if got := chatSessionOptionsFromLua(L, lua.LNil); got != (ChatSessionOptions{}) {
		t.Errorf("nil opts = %+v, want zero value", got)
	}
	if got := chatRenderOptionsFromLua(L, lua.LString("nonsense")); got != (ChatRenderOptions{}) {
		t.Errorf("non-table opts = %+v, want zero value", got)
	}

	st := L.NewTable()
	L.SetField(st, "source", lua.LString("ark"))
	// `for` is a Lua keyword — app code writes ["for"]=, which lands on the
	// table under the same name. Pinning it here is the point: a binding that
	// read "forSource" would silently never narrow.
	L.SetField(st, "for", lua.LString("proj"))
	L.SetField(st, "limit", lua.LNumber(5))
	got := chatSessionOptionsFromLua(L, st)
	if got.Source != "ark" || got.For != "proj" || got.Limit != 5 {
		t.Errorf("session opts = %+v, want {ark proj 5}", got)
	}

	rt := L.NewTable()
	L.SetField(rt, "withTools", lua.LTrue)
	L.SetField(rt, "lineLength", lua.LNumber(72))
	r := chatRenderOptionsFromLua(L, rt)
	if !r.WithTools || r.WithThinking || r.Sidechain || r.LineLength != 72 {
		t.Errorf("render opts = %+v, want only WithTools and LineLength set", r)
	}
	// An unset lineLength must fall back to the default rather than to a
	// zero width, which would wrap every line to nothing.
	if n := (ChatRenderOptions{}).lineLength(); n != DefaultChatLineLength {
		t.Errorf("default lineLength = %d, want %d", n, DefaultChatLineLength)
	}
}

// TestSourceName pins the naming rule the rest of the pairing rests on: a
// source's name is its directory's last segment, trailing separator or not.
// R3292
func TestSourceName(t *testing.T) {
	for _, tc := range []struct{ dir, want string }{
		{"/home/deck/work/ark", "ark"},
		{"/home/deck/work/ark/", "ark"},
		{"ark", "ark"},
	} {
		if got := SourceName(tc.dir); got != tc.want {
			t.Errorf("SourceName(%q) = %q, want %q", tc.dir, got, tc.want)
		}
	}
}

// TestResolveSourceNamePrefersDir verifies the full dir wins over the last
// segment, so `dir` stays the source's identity (specs/config.md) and the short
// form never becomes a second one. The fixture is deliberately ambiguous by
// last segment: without the dir-first rule this would error. R3292
func TestResolveSourceNamePrefersDir(t *testing.T) {
	c := &Config{Sources: []Source{{Dir: "/w/ark"}, {Dir: "/other/ark"}}}
	got, err := c.ResolveSourceName("/w/ark")
	if err != nil {
		t.Fatalf("exact dir must resolve even when the short name is ambiguous: %v", err)
	}
	if got.Dir != "/w/ark" {
		t.Errorf("resolved %q, want /w/ark", got.Dir)
	}
}

// TestResolveSourceNameAmbiguous verifies a colliding short name errors and
// names every candidate. A silent pick would pair a chat source with the wrong
// project and nothing would report it. R3292
func TestResolveSourceNameAmbiguous(t *testing.T) {
	c := &Config{Sources: []Source{{Dir: "/w/ark"}, {Dir: "/other/ark"}}}
	_, err := c.ResolveSourceName("ark")
	var amb *ErrAmbiguousSource
	if !errors.As(err, &amb) {
		t.Fatalf("got %v, want *ErrAmbiguousSource", err)
	}
	if len(amb.Candidates) != 2 {
		t.Fatalf("candidates = %v, want both dirs", amb.Candidates)
	}
	joined := strings.Join(amb.Candidates, " ")
	if !strings.Contains(joined, "/w/ark") || !strings.Contains(joined, "/other/ark") {
		t.Errorf("candidates %v must name both dirs so the operator can act", amb.Candidates)
	}
}

// TestResolveSourceNameUnknown verifies an unknown name errors rather than
// yielding a zero Source, which would list nothing and read exactly like "this
// project has no conversations." R3292
func TestResolveSourceNameUnknown(t *testing.T) {
	c := &Config{Sources: []Source{{Dir: "/w/ark"}}}
	if _, err := c.ResolveSourceName("nosuch"); err == nil {
		t.Fatal("unknown source name must error, not return a zero Source")
	}
}

// TestSourcesLoggingForResolvesBothForms verifies logs_for is compared by
// resolved identity, so the short name and the full dir are the same
// declaration. A string comparison would make the two forms behave differently.
// R3288, R3292
func TestSourcesLoggingForResolvesBothForms(t *testing.T) {
	c := &Config{Sources: []Source{
		{Dir: "/w/ark"},
		{Dir: "/p/-w-ark", LogsFor: "ark"},
		{Dir: "/p/-w-ark-2", LogsFor: "/w/ark"},
	}}
	got, err := c.SourcesLoggingFor("ark")
	if err != nil {
		t.Fatalf("SourcesLoggingFor: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d sources, want both declaration forms matched", len(got))
	}
}

// TestDanglingLogsForSkippedButReported verifies a broken pairing does not take
// the whole lookup down, yet is still surfaced — R3290's "a logs_for pointing at
// nothing is worse than an absent one" only holds if something reports it.
func TestDanglingLogsForSkippedButReported(t *testing.T) {
	c := &Config{Sources: []Source{
		{Dir: "/w/ark"},
		{Dir: "/p/-w-ark", LogsFor: "ark"},
		{Dir: "/p/ghosted", LogsFor: "ghost"},
	}}
	got, err := c.SourcesLoggingFor("ark")
	if err != nil {
		t.Fatalf("one broken pairing must not fail the whole lookup: %v", err)
	}
	if len(got) != 1 || got[0].Dir != "/p/-w-ark" {
		t.Fatalf("got %v, want only the valid pairing", got)
	}

	report := c.ProposeLogsFor()
	if len(report.Dangling) != 1 || report.Dangling[0].LogsFor != "ghost" {
		t.Fatalf("dangling = %v, want the ghost pairing reported", report.Dangling)
	}
}

// TestProposeLogsForOnlyMatches verifies proposals cover unpaired *chat*
// sources only. An ordinary code source never gets a logs_for, so listing it as
// an unmatched proposal would put a permanent complaint about every project
// directory into a verb meant to be re-run. R3290
func TestProposeLogsForOnlyMatches(t *testing.T) {
	c := &Config{Sources: []Source{
		{Dir: "/w/proj"},
		{Dir: "/p/" + claudeProjectSlug("/w/proj")},
	}}
	report := c.ProposeLogsFor()
	if len(report.Proposals) != 1 {
		t.Fatalf("proposals = %v, want exactly the chat source", report.Proposals)
	}
	p := report.Proposals[0]
	if p.SourceDir != "/p/"+claudeProjectSlug("/w/proj") || p.LogsFor != "proj" {
		t.Errorf("proposal = %+v, want the chat source paired to proj", p)
	}
	for _, q := range report.Proposals {
		if q.SourceDir == "/w/proj" {
			t.Error("the code source must not appear as a proposal")
		}
	}
}

// TestProposeLogsForSkipsPaired verifies re-running is safe and never
// overwrites a hand-corrected pairing. R3290
func TestProposeLogsForSkipsPaired(t *testing.T) {
	c := &Config{Sources: []Source{
		{Dir: "/w/proj"},
		{Dir: "/p/" + claudeProjectSlug("/w/proj"), LogsFor: "proj"},
	}}
	if report := c.ProposeLogsFor(); len(report.Proposals) != 0 {
		t.Fatalf("proposals = %v, want none — the source is already paired", report.Proposals)
	}
}

// TestSetLogsForRefusesDangling verifies the write path is where "never
// written" is enforced, so a pairing naming nothing cannot be introduced
// through the supported route. R3290
func TestSetLogsForRefusesDangling(t *testing.T) {
	c := &Config{Sources: []Source{{Dir: "/w/ark"}, {Dir: "/p/-w-ark"}}}
	if err := c.SetLogsFor("/p/-w-ark", "ghost"); err == nil {
		t.Fatal("SetLogsFor must refuse a name that resolves to nothing")
	}
	if c.Sources[1].LogsFor != "" {
		t.Errorf("LogsFor = %q, want unchanged after a refused write", c.Sources[1].LogsFor)
	}
}

// TestReadChatLabel verifies the label is the first *non-sidechain* user turn —
// a session's opening line is the human's, not a subagent's — and that the turn
// count matches the default transcript view. R3284, R3286
func TestReadChatLabel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	body := `{"type":"user","isSidechain":true,"message":{"role":"user","content":"SUBAGENT"}}
{"type":"user","message":{"role":"user","content":"the real opening line"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"reply"}]}}
{"type":"user","message":{"role":"user","content":"second"}}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	label, turns := readChatLabel(path)
	if label != "the real opening line" {
		t.Errorf("label = %q, want the first non-sidechain user turn", label)
	}
	if turns != 3 {
		t.Errorf("turns = %d, want 3 (sidechain excluded)", turns)
	}
}

// TestTruncateLabel verifies a long, multi-paragraph, multi-byte opening still
// yields one readable row and never splits a rune. R3284
func TestTruncateLabel(t *testing.T) {
	long := strings.Repeat("naïve ", 40) + "\n\nsecond paragraph"
	got := truncateLabel(long)

	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("label must be one line; got %q", got)
	}
	if !utf8.ValidString(got) {
		t.Errorf("label must not split a rune; got %q", got)
	}
	if n := utf8.RuneCountInString(got); n > chatLabelMax+1 {
		t.Errorf("label is %d runes, want <= %d plus the ellipsis", n, chatLabelMax)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a clipped label should show it was clipped; got %q", got)
	}

	// A short label passes through untouched, ellipsis and all.
	if got := truncateLabel("  hello   there  "); got != "hello there" {
		t.Errorf("short label = %q, want whitespace collapsed and no ellipsis", got)
	}
}
