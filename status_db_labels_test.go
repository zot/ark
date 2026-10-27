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
		"B", "D", "E:", "EC", "ED", "EF", "EV", "F", "HC", "I", "L", "M",
		"N", "PC", "RC", "RD", "RF", "RJ", "RM", "T", "U", "V", "X",
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

// TestRecordPrefixOfRecognizesEveryLabel is the half of the sentry that was
// asleep. TestArkLabelsCoverRecordClasses proved every class had a *label* and
// passed the whole time RC/RD/RF/RJ/HC were reporting 0, because the question
// it never asked was whether recordPrefixOf *recognizes* the label: those keys
// fell through to a bare "R"/"H" with no label and buildRecordCounts dropped
// them. A guard that checks one of two necessary conditions reads exactly like
// a guard that checks both, right up until the day it doesn't. (R3263)
// CRC: crc-Store.md | R3078, R3263
func TestRecordPrefixOfRecognizesEveryLabel(t *testing.T) {
	for prefix := range arkLabels {
		// A realistic key: the prefix followed by payload bytes.
		key := append([]byte(prefix), 0x01, 0x02, 0x03)
		if got := recordPrefixOf(key); got != prefix {
			t.Errorf("recordPrefixOf(%q…) = %q, want %q — this class counts as 0 in `ark status -db` no matter how many records exist", prefix, got, prefix)
		}
	}
}

// TestNoSingleByteLabelShadowsMultiByte asserts the invariant the derived
// recognizer rests on: no single-byte class may share its byte with the start
// of a multi-byte one. Were there ever a bare "R" class alongside "RC", a key
// of one would classify as the other and the counts would be silently wrong in
// a way no individual label check would notice. (R3263)
// CRC: crc-Store.md | R3263
func TestNoSingleByteLabelShadowsMultiByte(t *testing.T) {
	for prefix := range arkLabels {
		if len(prefix) != 1 {
			continue
		}
		for other := range arkLabels {
			if len(other) > 1 && other[0] == prefix[0] {
				t.Errorf("single-byte class %q shadows multi-byte %q — keys of one classify as the other; give the shorter class a distinct byte", prefix, other)
			}
		}
	}
}
