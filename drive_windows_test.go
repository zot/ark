//go:build windows

package ark

import (
	"os"
	"path/filepath"
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

// TestAnchorGlobToDirKeepsADriveGlob: a drive-absolute or `\`-rooted glob is
// absolute and passes through; joining it to the working directory would give
// `C:\proj\C:\notes\**`, which no file has.
// CRC: crc-Searcher.md | R3641
func TestAnchorGlobToDirKeepsADriveGlob(t *testing.T) {
	for _, g := range []string{`C:\notes\**`, `c:/notes/**`, `\notes\**`} {
		if got := AnchorGlobToDir(g, `C:\proj`); got != g {
			t.Errorf("AnchorGlobToDir(%q) = %q, want it unchanged", g, got)
		}
	}
	if got := AnchorGlobToDir(`specs\**`, `C:\proj`); got != `C:\proj\specs\**` {
		t.Errorf("AnchorGlobToDir(specs) = %q, want it joined to the working directory", got)
	}
}

// TestResolvePatternCutsAtEitherSeparator: a glob written with `/` after a
// native link path still resolves the link, because the wildcard-free prefix
// ends at either separator.
// CRC: crc-LinkTable.md | R3642
func TestResolvePatternCutsAtEitherSeparator(t *testing.T) {
	root := linkTempRoot(t)
	s1, s2 := filepath.Join(root, "s1"), filepath.Join(root, "s2")
	mkDirs(t, s1, s2)
	link := filepath.Join(s2, "link")
	mkSymlink(t, s1, link)
	lt := NewLinkTable(linkCfg(s1, s2))
	lt.Observe(link)

	if got, want := lt.ResolvePattern(link+"/**/*.md"), s1+"/**/*.md"; got != want {
		t.Errorf("ResolvePattern = %q, want %q", got, want)
	}
}

// TestBibleVirtualTargetTakesEitherSeparator: the BIBLE segment is found in a
// native address, the source keeps its native spelling, and a nested book is
// still refused.
// CRC: crc-BibleChunker.md | R3643
func TestBibleVirtualTargetTakesEitherSeparator(t *testing.T) {
	src, book, ok := bibleVirtualTarget(`C:\bibles\esv\BIBLE\1 Samuel`)
	if !ok || src != `C:\bibles\esv` || book != "1 Samuel" {
		t.Errorf("native address → (%q, %q, %v), want (C:\\bibles\\esv, 1 Samuel, true)", src, book, ok)
	}
	if _, _, ok := bibleVirtualTarget(`C:\bibles\esv\BIBLE\a\b`); ok {
		t.Error("a nested book was accepted; BIBLE names exactly one book")
	}
}
