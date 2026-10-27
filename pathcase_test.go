//go:build !windows

package ark

import (
	"os"
	"testing"
)

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

// Off Windows a drive-shaped prefix is an ordinary relative path, and PATH is
// still a :-separated list with ark's dir before /usr/bin.
// CRC: crc-FilenameCase.md | R3635, R3639
func TestDriveLenAndPathListOffWindows(t *testing.T) {
	if n := DriveLen("C:/x.md"); n != 0 {
		t.Errorf("DriveLen off Windows = %d, want 0", n)
	}
	t.Setenv("PATH", "/opt/bin:/usr/bin")
	injectPath("/home/me/.ark")
	if got := os.Getenv("PATH"); got != "/opt/bin:/home/me/.ark:/usr/bin" {
		t.Errorf("PATH = %q, want ark's dir just before /usr/bin", got)
	}
}

// Off Windows a drive-shaped glob is relative, so anchoring joins it.
// CRC: crc-Searcher.md | R3641
func TestAnchorGlobToDirJoinsADriveShapeOffWindows(t *testing.T) {
	if got := AnchorGlobToDir("C:/notes/**", "/p"); got != "/p/C:/notes/**" {
		t.Errorf("AnchorGlobToDir = %q, want it joined to /p", got)
	}
}
