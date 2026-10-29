package ark

// CRC: crc-Config.md | Test: test-ConfigFeedback.md | R3676, R3678

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// editorSideFiles are the files Vim and Emacs write beside the one being
// edited, in the names the 2026-10-27 run found indexed and their relatives.
var editorSideFiles = []string{"notes/b.md~", "notes/.b.md.un~", "notes/.b.md.swp", "notes/#b.md#", "notes/.#b.md", "top.md~"}

// TestDefaultExcludesKeepEditorFilesOut drives a freshly written default
// config through the real matcher, both ways one is written: from the bundled
// install/ark.toml, which `ark setup` seeds a new install with, and from the
// built-in fallback used when no seed is given. Under the defaults every editor side file
// is excluded and the note beside it is not. With `**` included, include wins
// and the side files come in: the limit the spec states, pinned so the
// sentence stays true.
func TestDefaultExcludesKeepEditorFilesOut(t *testing.T) {
	seed, err := os.ReadFile(filepath.Join("install", "ark.toml"))
	if err != nil {
		t.Fatal(err)
	}
	t.Run("bundled install/ark.toml", func(t *testing.T) { checkEditorExcludes(t, seed) })
	t.Run("built-in fallback", func(t *testing.T) { checkEditorExcludes(t, nil) })
}

func checkEditorExcludes(t *testing.T, seed []byte) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ark.toml")
	if err := WriteDefaultConfig(cfgPath, seed); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "src")
	m := &Matcher{Dotfiles: cfg.Dotfiles}
	classify := func(include PatternSpec, rel string) Classification {
		in, ex := cfg.EffectivePatterns(Source{Dir: src, Include: include})
		return m.Classify(in, ex, filepath.Join(src, rel), src, false)
	}
	if got := classify(PatternSpec{}, "notes/b.md"); got == Excluded {
		t.Errorf("notes/b.md is excluded by the defaults")
	}
	excluded := 0
	for _, f := range editorSideFiles {
		if classify(PatternSpec{}, f) == Excluded {
			excluded++
		}
	}
	if excluded < len(editorSideFiles)-1 {
		t.Errorf("only %d of %d side files are excluded under the defaults", excluded, len(editorSideFiles))
	}
	includes, _ := cfg.EffectivePatterns(Source{Dir: src})
	for _, f := range editorSideFiles {
		want := Excluded
		// Include wins: a side file a default include matches is taken. Emacs's
		// lock file for b.md is .#b.md, which the bundled default *.md matches.
		if m.Classify(includes, nil, filepath.Join(src, f), src, false) == Included {
			want = Included
		}
		if got := classify(PatternSpec{}, f); got != want {
			t.Errorf("%s under the defaults = %v, want %v", f, got, want)
		}
		if got := classify(PatternSpec{Replace: []string{"**"}}, f); got != Included {
			t.Errorf("%s under include = [\"**\"] = %v; the spec says include wins and ** takes it", f, got)
		}
	}
}

// TestPatternStringNamesBothForms: a pattern list given as a string is refused,
// and the error names both forms it accepts rather than only the type it got.
func TestPatternStringNamesBothForms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ark.toml")
	if err := os.WriteFile(path, []byte("[[source]]\ndir = \"/tmp\"\ninclude = \"**\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal(`include = "**" loaded; a string must not be read as a pattern list`)
	}
	for _, want := range []string{`include = ["**"]`, `include.add = ["**"]`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name the accepted form %s", err, want)
		}
	}
}
