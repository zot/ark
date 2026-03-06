package ark

// CRC: crc-Matcher.md | Test: test-Matcher.md

import "testing"

func TestMatchNameFormMatchesFileOnly(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	if !m.Match("readme", "readme", false) {
		t.Error("should match file named readme")
	}
	if m.Match("readme", "readme", true) {
		t.Error("should not match directory named readme")
	}
}

func TestMatchDirFormMatchesDirectoryOnly(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	if !m.Match("vendor/", "vendor", true) {
		t.Error("should match directory named vendor")
	}
	if m.Match("vendor/", "vendor", false) {
		t.Error("should not match file named vendor")
	}
}

func TestMatchChildrenForm(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	if !m.Match("docs/*", "docs/readme.md", false) {
		t.Error("should match immediate child docs/readme.md")
	}
	if m.Match("docs/*", "docs/api/spec.md", false) {
		t.Error("should not match nested docs/api/spec.md")
	}
}

func TestMatchDescendantsForm(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	if !m.Match("src/**", "src/main.go", false) {
		t.Error("should match src/main.go")
	}
	if !m.Match("src/**", "src/pkg/db/store.go", false) {
		t.Error("should match src/pkg/db/store.go")
	}
}

func TestMatchDotfilesEnabled(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	if !m.Match("*", ".gitignore", false) {
		t.Error("* should match dotfiles when dotfiles=true")
	}
}

func TestMatchDotfilesDisabled(t *testing.T) {
	m := &Matcher{Dotfiles: false}
	if m.Match("*", ".gitignore", false) {
		t.Error("* should not match dotfiles when dotfiles=false")
	}
}

func TestMatchAnchoredPattern(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	if !m.Match("/vendor/", "vendor", true) {
		t.Error("anchored /vendor/ should match at root")
	}
	if m.Match("/vendor/", "pkg/vendor", true) {
		t.Error("anchored /vendor/ should not match nested")
	}
}

func TestMatchUnanchoredPattern(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	if !m.Match("node_modules/", "node_modules", true) {
		t.Error("should match at root")
	}
	if !m.Match("node_modules/", "pkg/node_modules", true) {
		t.Error("should match nested")
	}
}

func TestClassifyIncludeWins(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	c := m.Classify([]string{"*.md"}, []string{"*.md", "*.log"}, "readme.md", false)
	if c != Included {
		t.Errorf("expected Included, got %d", c)
	}
}

func TestClassifyUnresolved(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	c := m.Classify([]string{"*.md"}, []string{"*.log"}, "data.csv", false)
	if c != Unresolved {
		t.Errorf("expected Unresolved, got %d", c)
	}
}

func TestMatchGlobWildcards(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	if !m.Match("file?.txt", "file1.txt", false) {
		t.Error("? should match single character")
	}
	if m.Match("file?.txt", "file12.txt", false) {
		t.Error("? should not match two characters")
	}
}

func TestMatchBackslashEscapes(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	if !m.Match("file\\*name", "file*name", false) {
		t.Error("\\* should match literal asterisk")
	}
}
