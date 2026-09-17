package main

// CRC: test-TagOutput.md | R3411, R3412

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTagFile drops a minimal, well-formed tag-block file and returns its path.
func writeTagFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "msg.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestTagGetBulletDefaultAndCompact pins R3411: the default is a markdown
// bullet list with values verbatim; --compact restores the legacy TSV.
func TestTagGetBulletDefaultAndCompact(t *testing.T) {
	path := writeTagFile(t, "@from-project: ark\n@issue: a long value: with a colon\n\nbody\n")

	bullet := captureStdout(t, func() { cmdTagGet([]string{path}, false) })
	if !strings.Contains(bullet, "- from-project: ark") {
		t.Errorf("bullet output missing tag bullet\ngot:\n%s", bullet)
	}
	if !strings.Contains(bullet, "- issue: a long value: with a colon") {
		t.Errorf("value not verbatim in bullet output\ngot:\n%s", bullet)
	}

	compact := captureStdout(t, func() { cmdTagGet([]string{path}, true) })
	if !strings.Contains(compact, "from-project\tark") {
		t.Errorf("compact output missing TSV row\ngot:\n%s", compact)
	}
	if strings.Contains(compact, "- from-project") {
		t.Errorf("compact output leaked a bullet\ngot:\n%s", compact)
	}
}

// TestTagCheckSuccessLine pins R3412: success is stated, not silent — an agent
// must be able to tell a passed check from a command that did nothing.
func TestTagCheckSuccessLine(t *testing.T) {
	path := writeTagFile(t, "@from-project: ark\n@to-project: microfts2\n\nplain body, no heading\n")
	out := captureStdout(t, func() { cmdTagCheck([]string{path}) })
	if !strings.Contains(out, "tag block is well-formed") {
		t.Errorf("check success output missing the well-formed line\ngot:\n%s", out)
	}
}
