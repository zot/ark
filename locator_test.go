package ark

// CRC: crc-DB.md | Test: test-Anchor.md | R3077

import (
	"strconv"
	"strings"
	"testing"
)

// TestDB_SuggestAnchor exercises the resolution glue end-to-end: index a
// file, resolve its chunk by chunkID (path="" form) and by path:range, and
// assert both yield the same parseable @ext target. (R3077)
func TestDB_SuggestAnchor(t *testing.T) {
	_, db := setupRecall(t)
	cid, _ := indexLine(t, db, "note.md", "a unique first line here\nsecond line here\nthird line here")

	byID, err := db.SuggestAnchor("", strconv.FormatUint(cid, 10))
	if err != nil {
		t.Fatalf("SuggestAnchor by chunkID: %v", err)
	}
	if byID == "" {
		t.Fatal("empty target from chunkID")
	}
	info, err := db.ChunkInfo(cid)
	if err != nil {
		t.Fatalf("ChunkInfo: %v", err)
	}
	byLoc, err := db.SuggestAnchor(info.Path, info.Range)
	if err != nil {
		t.Fatalf("SuggestAnchor by path:range: %v", err)
	}
	if byLoc != byID {
		t.Errorf("chunkID vs path:range targets differ: %q vs %q", byID, byLoc)
	}
	if parts, ok := ParseExtTargetParts(byID, ""); !ok || parts.Invalid {
		t.Errorf("assembled target %q does not parse", byID)
	}
}

// TestLocatorSuggestion_Target assembles each LocatorKind into its @ext
// TARGET string, escaping the narrower delimiter where needed. (R3077)
func TestLocatorSuggestion_Target(t *testing.T) {
	cases := []struct {
		name string
		sug  LocatorSuggestion
		want string
	}{
		{"bare uuid", LocatorSuggestion{BaseValue: "%abc-123", LocatorKind: "bare"}, "%abc-123"},
		{"bare path", LocatorSuggestion{BaseValue: "/a/b.md", LocatorKind: "bare"}, "/a/b.md"},
		{"absolute range", LocatorSuggestion{BaseValue: "/a/b.md", LocatorKind: "absolute", LocatorText: "3-9"}, "/a/b.md:3-9"},
		{"string", LocatorSuggestion{BaseValue: "/a/b.md", LocatorKind: "string", LocatorText: "unique snippet"}, `/a/b.md:"unique snippet"`},
		{"regex", LocatorSuggestion{BaseValue: "/a/b.md", LocatorKind: "regex", LocatorText: "foo.*bar"}, "/a/b.md:/foo.*bar/"},
		{"string escapes embedded quote", LocatorSuggestion{BaseValue: "/a/b.md", LocatorKind: "string", LocatorText: `say "hi"`}, `/a/b.md:"say \"hi\""`},
		{"regex escapes embedded slash", LocatorSuggestion{BaseValue: "/a/b.md", LocatorKind: "regex", LocatorText: "foo/bar"}, `/a/b.md:/foo\/bar/`},
		{"empty kind falls back to base", LocatorSuggestion{BaseValue: "%u", LocatorKind: ""}, "%u"},
	}
	for _, tc := range cases {
		if got := tc.sug.Target(); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

// TestLocatorSuggestion_Target_Parseable confirms the assembled target
// parses back to the intended base + anchor kind — the escape keeps the
// closing delimiter findable even for a delimiter-bearing snippet. (R3077)
func TestLocatorSuggestion_Target_Parseable(t *testing.T) {
	cases := []struct {
		sug      LocatorSuggestion
		wantBase string
		wantKind string
	}{
		{LocatorSuggestion{BaseValue: "%abc-123", LocatorKind: "bare"}, "uuid", ""},
		{LocatorSuggestion{BaseValue: "/a/b.md", LocatorKind: "bare"}, "path", ""},
		{LocatorSuggestion{BaseValue: "/a/b.md", LocatorKind: "absolute", LocatorText: "3-9"}, "path", "range"},
		{LocatorSuggestion{BaseValue: "/a/b.md", LocatorKind: "string", LocatorText: `say "hi"`}, "path", "string"},
		{LocatorSuggestion{BaseValue: "/a/b.md", LocatorKind: "regex", LocatorText: "foo/bar"}, "path", "regex"},
	}
	for _, tc := range cases {
		target := tc.sug.Target()
		parts, ok := ParseExtTargetParts(target, "")
		if !ok || parts.Invalid {
			t.Errorf("Target %q did not parse (ok=%v invalid=%v)", target, ok, parts.Invalid)
			continue
		}
		if parts.BaseKind != tc.wantBase {
			t.Errorf("Target %q: base %q want %q", target, parts.BaseKind, tc.wantBase)
		}
		if parts.AnchorKind != tc.wantKind {
			t.Errorf("Target %q: anchor kind %q want %q", target, parts.AnchorKind, tc.wantKind)
		}
	}
}

// TestBloodhoundCrankHandle_WrapsChunkReads is a Sleeping Sentry (#33): the
// weak secretary's chunk-read instructions must carry --wrap so it gets
// clean text (Baby Food), not JSON. Guards against a silent revert to a
// bare `ark chunks <path:range>` instruction.
func TestBloodhoundCrankHandle_WrapsChunkReads(t *testing.T) {
	if !strings.Contains(searchCrankHandle, "ark chunks --wrap") {
		t.Error("searchCrankHandle: chunk-read instruction must use `ark chunks --wrap`")
	}
	if strings.Contains(searchCrankHandle, "ark chunks <path:range>") {
		t.Error("searchCrankHandle: found an un-wrapped `ark chunks <path:range>` instruction")
	}
	seed := renderBloodhoundSeed(&RecallResult{Chunks: []RecalledChunk{{Path: "/a.md", Range: "1-2"}}}, false)
	if !strings.Contains(seed, "ark chunks --wrap") {
		t.Errorf("renderBloodhoundSeed: chunk read must use `ark chunks --wrap`; got:\n%s", seed)
	}
}
