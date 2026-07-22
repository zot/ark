package ark

import "testing"

// TestArkLabelsCoverRecordClasses is a Sleeping Sentry (R3078): the arkLabels
// allowlist must label every ark-bucket record class in specs/record-formats.md
// except any class a spec marks "no status display" (currently only S). Because
// buildRecordCounts iterates arkLabels, a class missing here silently vanishes
// from `ark status -db`. When you add a record class, add it to
// record-formats.md, arkLabels, and this list together.
// CRC: crc-DB.md | R3078
func TestArkLabelsCoverRecordClasses(t *testing.T) {
	// The ark-bucket prefixes from specs/record-formats.md, minus S
	// (vector-freshness.md: "No status display").
	want := []string{
		"B", "D", "E:", "EC", "ED", "EF", "EV", "F", "HC", "I", "M",
		"PC", "RC", "RD", "RF", "RJ", "RM", "T", "U", "V", "X",
	}
	for _, prefix := range want {
		if _, ok := arkLabels[prefix]; !ok {
			t.Errorf("arkLabels missing record class %q — status -db will silently omit it; add its label (see specs/record-formats.md)", prefix)
		}
	}
	if len(arkLabels) != len(want) {
		t.Errorf("arkLabels has %d entries, want %d — a label drifted from specs/record-formats.md; reconcile the two", len(arkLabels), len(want))
	}
}
