//go:build windows

package ark

import (
	"os"
	"strings"
	"testing"
)

// Windows only: drive letters mean nothing elsewhere. Run on the VM.

// TestDriveLen: the one definition of a drive prefix.
// CRC: crc-FilenameCase.md | R3635
func TestDriveLen(t *testing.T) {
	for s, want := range map[string]int{
		`C:\x.md`: 3, `c:/x.md`: 3, `Z:\`: 3,
		`C:x.md`: 0, `1:\x`: 0, `/x/y.md`: 0, `x.md`: 0, `C:`: 0, ``: 0,
		`~/x.md`: 0, `%uuid:"a"`: 0,
	} {
		if got := DriveLen(s); got != want {
			t.Errorf("DriveLen(%q) = %d, want %d", s, got, want)
		}
	}
}

// TestExtTargetKeepsItsDrive: a drive-absolute @ext target keeps its drive,
// and the narrower is the next colon.
// CRC: crc-FilenameCase.md | R3636
func TestExtTargetKeepsItsDrive(t *testing.T) {
	cases := []struct{ in, base, anchor string }{
		{`C:\notes\a.md`, `C:\notes\a.md`, ``},
		{`C:\notes\a.md:"hello"`, `C:\notes\a.md`, `"hello"`},
		{`C:/notes/a.md:3-5`, `C:/notes/a.md`, `3-5`},
	}
	for _, c := range cases {
		parts, ok := ParseExtTargetParts(c.in, "")
		if !ok || parts.Invalid || parts.BaseKind != "path" || parts.BaseValue != c.base {
			t.Errorf("ParseExtTargetParts(%q) = %+v, want path base %q", c.in, parts, c.base)
		}
		if i := indexNarrowerColon(c.in); c.anchor == "" && i >= 0 || c.anchor != "" && (i < 0 || c.in[i+1:] != c.anchor) {
			t.Errorf("indexNarrowerColon(%q) = %d, want the colon before %q", c.in, i, c.anchor)
		}
	}
	if n := anchorSkip(`C:\notes\a.md:"x @tag: y"`); n != len(`C:\notes\a.md:"x @tag: y"`) {
		t.Errorf("anchorSkip should skip the quoted anchor after the drive, got %d", n)
	}
}

// TestSplitLocKeepsItsDrive: a drive-absolute locator with no range is a path.
// CRC: crc-FilenameCase.md | R3637
func TestSplitLocKeepsItsDrive(t *testing.T) {
	if p, r := splitLoc(`C:\x.md`); p != `C:\x.md` || r != "" {
		t.Errorf("splitLoc(no range) = (%q, %q), want the whole path and no range", p, r)
	}
	if p, r := splitLoc(`C:\x.md:3-5`); p != `C:\x.md` || r != "3-5" {
		t.Errorf("splitLoc(with range) = (%q, %q)", p, r)
	}
}

// TestInjectPathKeepsWindowsList: PATH is a ;-separated list on Windows, and a
// split at : would break it at every drive.
// CRC: crc-FilenameCase.md | R3639
func TestInjectPathKeepsWindowsList(t *testing.T) {
	t.Setenv("PATH", `C:\Windows\system32;C:\Program Files\Git\cmd`)
	injectPath(`C:\Users\me\.ark`)
	got := os.Getenv("PATH")
	parts := strings.Split(got, ";")
	if len(parts) != 3 || parts[0] != `C:\Windows\system32` || parts[2] != `C:\Users\me\.ark` {
		t.Errorf("PATH = %q, want the two entries intact and ark's dir added", got)
	}
}

// TestMirrorSlugDropsTheDriveColon: a Windows filename cannot hold ":".
// CRC: crc-FilenameCase.md | R3640
func TestMirrorSlugDropsTheDriveColon(t *testing.T) {
	got, err := extMirrorPath(`C:\Users\me\notes`, `C:\Users\me\notes\a.md`, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `external\C-Users-me-notes\a.md.md`) || strings.Count(got, ":") != 1 {
		t.Errorf("mirror path %q, want the slug C-Users-me-notes and no colon past the drive", got)
	}
}
