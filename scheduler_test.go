package ark

import (
	"testing"
	"time"
)

func TestStripDateKeyword(t *testing.T) {
	loc := time.Local

	tests := []struct {
		input   string
		wantStr string
		wantKW  string
	}{
		{"on April 15 2026", "April 15 2026", "on"},
		{"from 2026-03-01", "2026-03-01", "from"},
		{"starting March 1 2026", "March 1 2026", "starting"},
		{"until May 30 2026", "May 30 2026", "until"},
		{"through 2026-12-31", "2026-12-31", "through"},
		{"by June 15 2026", "June 15 2026", "by"},
		{"before 2026-04-01", "2026-04-01", "before"},
		{"after 2026-01-01", "2026-01-01", "after"},
		{"ending 2026-06-30", "2026-06-30", "ending"},
		{"beginning 2026-01-15", "2026-01-15", "beginning"},
		// No keyword — unchanged.
		{"2026-04-15", "2026-04-15", ""},
		// Keyword not followed by date — unchanged.
		{"on time", "on time", ""},
		{"to infinity", "to infinity", ""},
	}

	for _, tt := range tests {
		got, kw := stripDateKeyword(tt.input, loc)
		if got != tt.wantStr || kw != tt.wantKW {
			t.Errorf("stripDateKeyword(%q) = (%q, %q), want (%q, %q)",
				tt.input, got, kw, tt.wantStr, tt.wantKW)
		}
	}
}

func TestParseDateTrimmingWithKeywords(t *testing.T) {
	loc := time.Local

	// "on April 15 2026 cleaning" should parse the date and return "cleaning"
	tm, desc, err := parseDateTrimming("on April 15 2026 cleaning", loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tm.Month() != time.April || tm.Day() != 15 || tm.Year() != 2026 {
		t.Errorf("got %v, want April 15 2026", tm)
	}
	if desc != "cleaning" {
		t.Errorf("desc = %q, want %q", desc, "cleaning")
	}

	// Without keyword should still work.
	tm2, desc2, err := parseDateTrimming("2026-04-15 09:00 meeting", loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tm2.IsZero() {
		t.Error("expected non-zero time")
	}
	if desc2 != "meeting" {
		t.Errorf("desc = %q, want %q", desc2, "meeting")
	}
}

func TestExtractBounds(t *testing.T) {
	loc := time.Local

	tests := []struct {
		input         string
		wantBefore    bool // expect notBefore to be set
		wantAfter     bool // expect notAfter to be set
		wantRemainder string
	}{
		{
			"from 2026-03-01 to 2026-05-30 every Monday at 5pm",
			true, true, "every Monday at 5pm",
		},
		{
			"every Monday at 5pm from 2026-03-01 to 2026-05-30",
			true, true, "every Monday at 5pm",
		},
		{
			"every Sat at 9:30am starting 2026-03-02",
			true, false, "every Sat at 9:30am",
		},
		{
			"every Monday at 5pm until 2026-05-30",
			false, true, "every Monday at 5pm",
		},
		{
			"2026-03-01..2026-05-30 every Monday at 09:00",
			true, true, "every Monday at 09:00",
		},
		{
			"every Monday at 09:00 2026-03-01..2026-05-30",
			true, true, "every Monday at 09:00",
		},
		{
			// No bounds.
			"every Monday at 09:00",
			false, false, "every Monday at 09:00",
		},
	}

	for _, tt := range tests {
		nb, na, rem := extractBounds(tt.input, loc)
		if tt.wantBefore && nb.IsZero() {
			t.Errorf("extractBounds(%q): expected notBefore, got zero", tt.input)
		}
		if !tt.wantBefore && !nb.IsZero() {
			t.Errorf("extractBounds(%q): unexpected notBefore %v", tt.input, nb)
		}
		if tt.wantAfter && na.IsZero() {
			t.Errorf("extractBounds(%q): expected notAfter, got zero", tt.input)
		}
		if !tt.wantAfter && !na.IsZero() {
			t.Errorf("extractBounds(%q): unexpected notAfter %v", tt.input, na)
		}
		if rem != tt.wantRemainder {
			t.Errorf("extractBounds(%q): remainder = %q, want %q", tt.input, rem, tt.wantRemainder)
		}
	}
}

func TestComputeNextWithNotAfter(t *testing.T) {
	after := time.Date(2026, 3, 1, 0, 0, 0, 0, time.Local)

	// Without bound — should find next Monday.
	next := computeNext("every Monday at 09:00", after, time.Time{})
	if next.IsZero() {
		t.Fatal("expected non-zero next")
	}
	if next.Weekday() != time.Monday {
		t.Errorf("expected Monday, got %s", next.Weekday())
	}

	// With bound before the next occurrence — should return zero.
	tightBound := after.Add(1 * time.Hour) // March 1 at 1am — before next Monday
	next2 := computeNext("every Monday at 09:00", after, tightBound)
	if !next2.IsZero() {
		t.Errorf("expected zero with tight bound, got %v", next2)
	}

	// With bound after the next occurrence — should return normally.
	looseBound := time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local)
	next3 := computeNext("every Monday at 09:00", after, looseBound)
	if next3.IsZero() {
		t.Fatal("expected non-zero with loose bound")
	}
	if next3.After(looseBound) {
		t.Errorf("next %v exceeds bound %v", next3, looseBound)
	}
}

func TestLogChunkBoundsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/test-log.md"

	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.Local)
	end := time.Date(2026, 5, 30, 0, 0, 0, 0, time.Local)

	chunks := []logChunk{
		{
			Event:     "standup",
			Source:    "~/notes/schedule.md",
			Spec:      "every Monday at 09:00",
			NotBefore: start,
			NotAfter:  end,
			Upcoming:  []string{"2026-03-02 09:00"},
		},
	}

	if err := writeLogFile(path, chunks); err != nil {
		t.Fatal(err)
	}

	parsed, err := readLogFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(parsed))
	}

	c := parsed[0]
	if c.NotBefore.IsZero() {
		t.Error("expected NotBefore to be set")
	}
	if c.NotAfter.IsZero() {
		t.Error("expected NotAfter to be set")
	}
	if c.NotBefore.Year() != 2026 || c.NotBefore.Month() != 3 || c.NotBefore.Day() != 1 {
		t.Errorf("NotBefore = %v, want 2026-03-01", c.NotBefore)
	}
	if c.NotAfter.Year() != 2026 || c.NotAfter.Month() != 5 || c.NotAfter.Day() != 30 {
		t.Errorf("NotAfter = %v, want 2026-05-30", c.NotAfter)
	}
}
