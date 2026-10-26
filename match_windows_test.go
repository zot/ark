//go:build windows

package ark

import "testing"

// TestMatchSeparatorsOnWindows: the shapes measured failing on 2026-10-26. A
// pattern holding a separator, written either way, matches a backslash path;
// a nested directory exclude reaches its depth; a drive-prefixed pattern is
// absolute.
// CRC: crc-Matcher.md | R3633, R3634
func TestMatchSeparatorsOnWindows(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	src := `C:\t172\src`
	cases := []struct {
		pat, path string
		dir, want bool
	}{
		{"*.md", `C:\t172\src\Casey.md`, false, true},
		{"docs/*.md", `C:\t172\src\docs\Casey.md`, false, true},
		{`docs\*.md`, `C:\t172\src\docs\Casey.md`, false, true},
		{"./docs/*.md", `C:\t172\src\docs\Casey.md`, false, true},
		{"./docs/*.md", `C:\t172\src\a\docs\Casey.md`, false, false},
		{"node_modules/", `C:\t172\src\node_modules`, true, true},
		{"node_modules/", `C:\t172\src\a\node_modules`, true, true},
		{`C:\t172\src\docs\Casey.md`, `C:\t172\src\docs\Casey.md`, false, true},
		{`C:/t172/src/docs/*.md`, `C:\t172\src\docs\Casey.md`, false, true},
		{`C:/t172/other/*.md`, `C:\t172\src\docs\Casey.md`, false, false},
	}
	for _, c := range cases {
		if got := m.Match(c.pat, c.path, src, c.dir); got != c.want {
			t.Errorf("Match(%q, %q, dir=%v) = %v, want %v", c.pat, c.path, c.dir, got, c.want)
		}
	}
}

// TestEmbedPriorityGlobsOnWindows: the librarian's own glob matcher takes the
// same slash form.
// CRC: crc-Matcher.md | R3633
func TestEmbedPriorityGlobsOnWindows(t *testing.T) {
	if !matchesAnyGlob(`C:\t172\src\docs\Casey.md`, []string{"**/docs/*.md"}) {
		t.Error(`**/docs/*.md should match C:\t172\src\docs\Casey.md`)
	}
}
