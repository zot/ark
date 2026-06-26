package ark

// CRC: crc-EventScheduler.md

import (
	"strings"
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

// TestParseDateValueMalformed covers the malformed-datetime guards:
// dash-form normalization (R2846), date-with-timezone-but-no-time
// rejection (R2847), and ambiguous mm/dd rejection (R2848).
func TestParseDateValueMalformed(t *testing.T) {
	loc := time.UTC

	// R2846: dash-joined date/time normalizes to the intended time-of-day
	// instead of dateparse's silent midnight (it reads -13:45 as a tz offset).
	dr, err := ParseDateValue("2026-05-28-13:45", "", loc)
	if err != nil {
		t.Fatalf("dash form: unexpected error: %v", err)
	}
	if dr.Start.Hour() != 13 || dr.Start.Minute() != 45 {
		t.Errorf("dash form: got %02d:%02d, want 13:45", dr.Start.Hour(), dr.Start.Minute())
	}

	// R2846: dash form with trailing description.
	dr, err = ParseDateValue("2026-05-28-13:45 standup", "", loc)
	if err != nil {
		t.Fatalf("dash form + desc: unexpected error: %v", err)
	}
	if dr.Start.Hour() != 13 || dr.Description != "standup" {
		t.Errorf("dash form + desc: got %02d:%02d desc=%q, want 13:xx desc=standup",
			dr.Start.Hour(), dr.Start.Minute(), dr.Description)
	}

	// R2846: both sides of a range normalize.
	dr, err = ParseDateValue("2026-05-28-13:45..2026-05-28-14:00", "", loc)
	if err != nil {
		t.Fatalf("dash range: unexpected error: %v", err)
	}
	if dr.Start.Hour() != 13 || dr.End.Hour() != 14 {
		t.Errorf("dash range: got start=%02d end=%02d, want 13..14", dr.Start.Hour(), dr.End.Hour())
	}

	// R2847: a date with a timezone but no time-of-day is an error, not
	// midnight. 2026-05-28+0700 parses (as date + tz offset, no clock) and
	// must be caught by the layout guard — not by ParseIn failing, which is
	// why we use the +offset form rather than a bare "Z".
	_, err = ParseDateValue("2026-05-28+0700", "", loc)
	if err == nil {
		t.Error("date+timezone+no-time: expected error, got none")
	} else if !strings.Contains(err.Error(), "timezone but no time-of-day") {
		t.Errorf("date+timezone+no-time: wrong error path: %v", err)
	}

	// R2848: ambiguous mm/dd vs dd/mm is rejected rather than guessed.
	if _, err := ParseDateValue("3/1/2014", "", loc); err == nil {
		t.Error("ambiguous mm/dd: expected error, got none")
	}

	// Regressions: well-formed values still parse cleanly.
	good := []struct {
		in       string
		wantHour int // -1 = all-day / don't check
	}{
		{"2026-05-28T13:45", 13},
		{"2026-05-28 13:45", 13},
		{"2026-04-15", -1},
		{"April 15 2026", -1},
	}
	for _, g := range good {
		dr, err := ParseDateValue(g.in, "", loc)
		if err != nil {
			t.Errorf("%q: unexpected error: %v", g.in, err)
			continue
		}
		if g.wantHour >= 0 && dr.Start.Hour() != g.wantHour {
			t.Errorf("%q: got hour %d, want %d", g.in, dr.Start.Hour(), g.wantHour)
		}
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
		nb, na, rem := ExtractBounds(tt.input, loc)
		if tt.wantBefore && nb.IsZero() {
			t.Errorf("ExtractBounds(%q): expected notBefore, got zero", tt.input)
		}
		if !tt.wantBefore && !nb.IsZero() {
			t.Errorf("ExtractBounds(%q): unexpected notBefore %v", tt.input, nb)
		}
		if tt.wantAfter && na.IsZero() {
			t.Errorf("ExtractBounds(%q): expected notAfter, got zero", tt.input)
		}
		if !tt.wantAfter && !na.IsZero() {
			t.Errorf("ExtractBounds(%q): unexpected notAfter %v", tt.input, na)
		}
		if rem != tt.wantRemainder {
			t.Errorf("ExtractBounds(%q): remainder = %q, want %q", tt.input, rem, tt.wantRemainder)
		}
	}
}

func TestComputeNextWithNotAfter(t *testing.T) {
	after := time.Date(2026, 3, 1, 0, 0, 0, 0, time.Local)

	// Without bound — should find next Monday.
	next := ComputeNext("every Monday at 09:00", after, time.Time{})
	if next.IsZero() {
		t.Fatal("expected non-zero next")
	}
	if next.Weekday() != time.Monday {
		t.Errorf("expected Monday, got %s", next.Weekday())
	}

	// With bound before the next occurrence — should return zero.
	tightBound := after.Add(1 * time.Hour) // March 1 at 1am — before next Monday
	next2 := ComputeNext("every Monday at 09:00", after, tightBound)
	if !next2.IsZero() {
		t.Errorf("expected zero with tight bound, got %v", next2)
	}

	// With bound after the next occurrence — should return normally.
	looseBound := time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local)
	next3 := ComputeNext("every Monday at 09:00", after, looseBound)
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

	chunks := []LogChunk{
		{
			Event:  "standup",
			Source: "~/notes/schedule.md",
			SpecMarkers: []SpecMarker{
				{Kind: "initial", Time: time.Date(2026, 3, 1, 0, 0, 0, 0, time.Local), Spec: "every Monday at 09:00"},
			},
			NotBefore: start,
			NotAfter:  end,
		},
	}

	if err := WriteLogFile(path, chunks); err != nil {
		t.Fatal(err)
	}

	parsed, err := ReadLogFile(path)
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
