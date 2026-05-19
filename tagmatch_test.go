package ark

// CRC: crc-TagMatcher.md | R2442, R2443, R2444, R2445, R2446, R2447, R2448, R2449, R2450, R2451

import "testing"

func TestParseMatchSyntax(t *testing.T) {
	cases := []struct {
		arg            string
		wantNameMode   TagNameMode
		wantNameStr    string
		wantValueMode  TagValueMode
		wantValueStr   string
		wantErr        bool
	}{
		// R2447 — bare name
		{"status", NameExact, "status", ValueAny, "", false},
		{"~stat", NameRegex, "stat", ValueAny, "", false},
		{":stat us", NameContains, "stat us", ValueAny, "", false},

		// R2444 — exact value
		{"status=done", NameExact, "status", ValueExact, "done", false},

		// R2445 — contains value
		{"status:open", NameExact, "status", ValueContains, "open", false},
		{"status:open issue", NameExact, "status", ValueContains, "open issue", false},

		// R2446 — regex value
		{"status~^open$", NameExact, "status", ValueRegex, "^open$", false},

		// Regex name combinations
		{"~stat=open", NameRegex, "stat", ValueExact, "open", false},
		{"~stat:open", NameRegex, "stat", ValueContains, "open", false},
		{"~stat~^open$", NameRegex, "stat", ValueRegex, "^open$", false},

		// Contains name combinations
		{":stat=open", NameContains, "stat", ValueExact, "open", false},
		{":stat us:open", NameContains, "stat us", ValueContains, "open", false},
		{":stat~^open$", NameContains, "stat", ValueRegex, "^open$", false},

		// R2448 — empty value after separator
		{"status=", NameExact, "status", ValueExact, "", false},  // matches only empty values
		{"status:", NameExact, "status", ValueAny, "", false},     // degenerate, any
		{"status~", NameExact, "status", ValueAny, "", false},     // degenerate, any

		// R2449 — @ normalization across name-mode sigils
		{"@status", NameExact, "status", ValueAny, "", false},
		{"@~stat", NameRegex, "stat", ValueAny, "", false},
		{"~@stat", NameRegex, "stat", ValueAny, "", false},
		{"@:stat", NameContains, "stat", ValueAny, "", false},
		{":@stat", NameContains, "stat", ValueAny, "", false},
		{"@status:open", NameExact, "status", ValueContains, "open", false},
		{"@~stat=open", NameRegex, "stat", ValueExact, "open", false},

		// R2450 — trailing colon on bare name (legacy compatibility through split logic)
		{"@status:", NameExact, "status", ValueAny, "", false},

		// Errors
		{"", 0, "", 0, "", true},
		{"=value", 0, "", 0, "", true},
	}
	for _, c := range cases {
		p, err := ParseMatchSyntax(c.arg)
		if (err != nil) != c.wantErr {
			t.Errorf("%q: err = %v, wantErr = %v", c.arg, err, c.wantErr)
			continue
		}
		if c.wantErr {
			continue
		}
		if p.NameMode != c.wantNameMode || p.NameStr != c.wantNameStr {
			t.Errorf("%q: name = (%d, %q), want (%d, %q)", c.arg, p.NameMode, p.NameStr, c.wantNameMode, c.wantNameStr)
		}
		if p.ValueMode != c.wantValueMode || p.ValueStr != c.wantValueStr {
			t.Errorf("%q: value = (%d, %q), want (%d, %q)", c.arg, p.ValueMode, p.ValueStr, c.wantValueMode, c.wantValueStr)
		}
	}
}

func TestMatchPredicateMatching(t *testing.T) {
	mustParse := func(s string) MatchPredicate {
		t.Helper()
		p, err := ParseMatchSyntax(s)
		if err != nil {
			t.Fatalf("ParseMatchSyntax(%q): %v", s, err)
		}
		return p
	}

	cases := []struct {
		arg, name, value string
		want             bool
	}{
		// Bare name — matches any value
		{"status", "status", "done", true},
		{"status", "status", "", true},
		{"status", "active", "done", false},
		{"status", "STATUS", "done", true}, // case-insensitive name compare

		// Exact value
		{"status=done", "status", "done", true},
		{"status=done", "status", "well-done", false},
		{"status=", "status", "", true},
		{"status=", "status", "x", false},

		// Contains value — substring-AND, order-independent, case-insensitive
		{"status:open", "status", "open", true},
		{"status:open", "status", "OPEN", true},
		{"status:open", "status", "openness", true}, // substring match
		{"status:open issue", "status", "issue open foo", true},
		{"status:open issue", "status", "issue closed", false},

		// Regex value (no anchoring)
		{"status~op", "status", "open", true},
		{"status~^open$", "status", "open ticket", false},
		{"status~^open$", "status", "open", true},

		// Regex name (case-insensitive)
		{"~^stat", "status", "anything", true},
		{"~^STAT", "status", "anything", true},
		{"~^stat", "different", "anything", false},

		// Contains name — substring-AND on the tag name
		{":stat", "status", "x", true},
		{":stat us", "status", "x", true},  // both substrings present in "status"
		{":stat foo", "status", "x", false}, // "foo" missing
	}
	for _, c := range cases {
		p := mustParse(c.arg)
		got := p.Match(TagValue{Tag: c.name, Value: c.value})
		if got != c.want {
			t.Errorf("%q vs (%q,%q): got %v, want %v", c.arg, c.name, c.value, got, c.want)
		}
	}
}

func TestMatchPredicateRoundTrip(t *testing.T) {
	// Canonical() output should re-parse to an equivalent predicate.
	args := []string{
		"status",
		"~stat",
		":stat us",
		"status=done",
		"status:open issue",
		"status~^open$",
		"~stat:open",
		"~stat=done",
		"~stat~^open$",
		":stat:open",
		":stat=done",
	}
	for _, s := range args {
		p1, err := ParseMatchSyntax(s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		c := p1.Canonical()
		p2, err := ParseMatchSyntax(c)
		if err != nil {
			t.Fatalf("re-parse %q: %v", c, err)
		}
		if p1.NameMode != p2.NameMode || p1.NameStr != p2.NameStr ||
			p1.ValueMode != p2.ValueMode || p1.ValueStr != p2.ValueStr {
			t.Errorf("%q canonical %q parsed to different predicate", s, c)
		}
	}
}
