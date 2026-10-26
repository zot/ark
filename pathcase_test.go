//go:build !windows

package ark

import "testing"

// Off Windows the filesystem is case-sensitive, so two spellings are two
// files: DiskSpelling must not touch a path and nothing is ever respelled.
// CRC: crc-FilenameCase.md | R3626, R3629, R3630
func TestDiskSpellingIsTheIdentityOffWindows(t *testing.T) {
	for _, p := range []string{"/tmp/Casey.md", "/TMP/CASEY.MD", "relative/Path.md", "tmp://X"} {
		if got := DiskSpelling(p); got != p {
			t.Errorf("DiskSpelling(%q) = %q, want it unchanged", p, got)
		}
		if respelled(p) {
			t.Errorf("respelled(%q) = true off Windows", p)
		}
	}
	if foldFilenames {
		t.Error("foldFilenames is true off Windows; microfts2 would merge files that differ only in case")
	}
}
