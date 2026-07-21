package ark

// CRC: crc-Matcher.md | Test: test-Matcher.md

import "testing"

func TestMatchNameFormMatchesFileOnly(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	if !m.Match("readme", "readme", "", false) {
		t.Error("should match file named readme")
	}
	if m.Match("readme", "readme", "", true) {
		t.Error("should not match directory named readme")
	}
}

func TestMatchDirFormMatchesDirectoryOnly(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	if !m.Match("vendor/", "vendor", "", true) {
		t.Error("should match directory named vendor")
	}
	if m.Match("vendor/", "vendor", "", false) {
		t.Error("should not match file named vendor")
	}
}

func TestMatchSingleStarChildren(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	if !m.Match("docs/*", "docs/readme.md", "", false) {
		t.Error("should match immediate child docs/readme.md")
	}
	if m.Match("docs/*", "docs/api/spec.md", "", false) {
		t.Error("should not match nested docs/api/spec.md")
	}
}

func TestMatchDescendantsForm(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	if !m.Match("src/**", "src/main.go", "", false) {
		t.Error("should match src/main.go")
	}
	if !m.Match("src/**", "src/pkg/db/store.go", "", false) {
		t.Error("should match src/pkg/db/store.go")
	}
}

func TestMatchDoublestarWithExtension(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	if !m.Match("**/*.md", "readme.md", "", false) {
		t.Error("**/*.md should match readme.md at root")
	}
	if !m.Match("**/*.md", "docs/guide.md", "", false) {
		t.Error("**/*.md should match docs/guide.md")
	}
	if !m.Match("**/*.md", "a/b/c/notes.md", "", false) {
		t.Error("**/*.md should match a/b/c/notes.md")
	}
	if m.Match("**/*.md", "readme.txt", "", false) {
		t.Error("**/*.md should not match readme.txt")
	}
}

func TestMatchDoublestarMidPattern(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	if !m.Match("docs/**/*.txt", "docs/a.txt", "", false) {
		t.Error("should match docs/a.txt")
	}
	if !m.Match("docs/**/*.txt", "docs/sub/b.txt", "", false) {
		t.Error("should match docs/sub/b.txt")
	}
	if m.Match("docs/**/*.txt", "other/c.txt", "", false) {
		t.Error("should not match other/c.txt")
	}
}

func TestMatchAlternationBraces(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	if !m.Match("*.{md,txt}", "readme.md", "", false) {
		t.Error("should match readme.md")
	}
	if !m.Match("*.{md,txt}", "notes.txt", "", false) {
		t.Error("should match notes.txt")
	}
	if m.Match("*.{md,txt}", "data.csv", "", false) {
		t.Error("should not match data.csv")
	}
}

func TestMatchDotfilesEnabled(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	if !m.Match("*", ".gitignore", "", false) {
		t.Error("* should match dotfiles when dotfiles=true")
	}
}

func TestMatchDotfilesDisabled(t *testing.T) {
	m := &Matcher{Dotfiles: false}
	if m.Match("*", ".gitignore", "", false) {
		t.Error("* should not match dotfiles when dotfiles=false")
	}
}

func TestMatchDotfilesDisabledExplicitDot(t *testing.T) {
	m := &Matcher{Dotfiles: false}
	if !m.Match(".gitignore", ".gitignore", "", false) {
		t.Error("explicit .gitignore should match even with dotfiles=false")
	}
}

// R2133: ./X form is source-root-anchored
func TestMatchSourceAnchoredDotSlashForm(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	if !m.Match("./vendor/", "/proj/vendor", "/proj", true) {
		t.Error("./vendor/ should match at source root")
	}
	if m.Match("./vendor/", "/proj/pkg/vendor", "/proj", true) {
		t.Error("./vendor/ should not match nested")
	}
}

// R2133: bare pattern matches at any depth in source
func TestMatchBareMatchesAnyDepth(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	if !m.Match("node_modules/", "/proj/node_modules", "/proj", true) {
		t.Error("bare node_modules/ should match at source root")
	}
	if !m.Match("node_modules/", "/proj/pkg/node_modules", "/proj", true) {
		t.Error("bare node_modules/ should match nested")
	}
}

// R2133: /X form is filesystem-absolute
func TestMatchFilesystemAbsoluteForm(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	// Under /tmp — match
	if !m.Match("/tmp/**", "/tmp/foo", "/home/me/proj", false) {
		t.Error("/tmp/** should match /tmp/foo regardless of source")
	}
	// Same-named tmp/ inside source — should NOT match (pattern is filesystem-rooted)
	if m.Match("/tmp/**", "/home/me/proj/tmp/foo", "/home/me/proj", false) {
		t.Error("/tmp/** should not match a tmp/ path inside another source")
	}
}

// R2133: filesystem-absolute pattern unrelated to source matches nothing in that source
func TestMatchFilesystemAbsoluteUnrelatedNoMatch(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	if m.Match("/var/log/**", "/home/me/proj/var/log/x", "/home/me/proj", false) {
		t.Error("/var/log/** with unrelated source should not match")
	}
}

func TestClassifyIncludeWins(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	c := m.Classify([]string{"*.md"}, []string{"*.md", "*.log"}, "readme.md", "", false)
	if c != Included {
		t.Errorf("expected Included, got %d", c)
	}
}

func TestClassifyUnresolved(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	c := m.Classify([]string{"*.md"}, []string{"*.log"}, "data.csv", "", false)
	if c != Unresolved {
		t.Errorf("expected Unresolved, got %d", c)
	}
}

func TestMatchGlobWildcards(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	if !m.Match("file?.txt", "file1.txt", "", false) {
		t.Error("? should match single character")
	}
	if m.Match("file?.txt", "file12.txt", "", false) {
		t.Error("? should not match two characters")
	}
}

func TestMatchBackslashEscapes(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	if !m.Match("file\\*name", "file*name", "", false) {
		t.Error("\\* should match literal asterisk")
	}
}

func TestMatchCharacterClass(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	if !m.Match("[abc].txt", "a.txt", "", false) {
		t.Error("[abc] should match 'a'")
	}
	if m.Match("[abc].txt", "d.txt", "", false) {
		t.Error("[abc] should not match 'd'")
	}
}

// Test: test-Matcher.md — rootless context: bare pattern reaches any depth.
// With no contextual root a bare pattern means **/X — the ark.toml reading,
// where there is nowhere to stand. R3199
func TestMatchRootlessBareAnyDepth(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	for _, p := range []string{"/a/x.md", "/a/b/c/y.md"} {
		if !m.Match("*.md", p, "", false) {
			t.Errorf("rootless bare *.md should match %q at any depth", p)
		}
	}
}

// Test: test-Matcher.md — rootless slash-bearing relative pattern matches.
//
// This is the O160 regression, and it pins the retirement of two hand-rolled
// matchers that both prefixed **/ only when the pattern had no slash at all:
// search.go's basename-first pathMatchesGlob and pubsub's anchorGlob. Under
// either, `specs/**` was matched raw against an absolute indexed path and hit
// nothing — a subscription or a search_exclude that looked scoped and was in
// fact dead. R3195, R3199, R3207
func TestMatchRootlessRelativeWithSlash(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	const path = "/home/me/proj/specs/x.md"
	if !m.Match("specs/**", path, "", false) {
		t.Errorf("rootless specs/** should match %q — matching nothing is the O160 bug", path)
	}
	if !MatchPathFilters(path, []string{"specs/**"}, nil) {
		t.Error("MatchPathFilters should agree with Match on the same pattern")
	}
	if MatchPathFilters(path, nil, []string{"specs/**"}) {
		t.Error("specs/** as an exclude should reject the path")
	}
}

// Test: test-Matcher.md — rootless ./ falls back to the absolute path.
// Documented as a degradation rather than a fix: it is why naming a directory
// inside one source from a [schedule] key requires an absolute path. R3199
func TestMatchRootlessDotSlashFallsBackToAbsolute(t *testing.T) {
	m := &Matcher{Dotfiles: true}
	if m.Match("./specs/**", "/home/me/proj/specs/x.md", "", false) {
		t.Error("rootless ./X has no root to anchor to; it should test the absolute path and miss")
	}
	if !m.Match("./home/**", "home/me/x.md", "", false) {
		t.Error("rootless ./X should match when the absolute path really does start that way")
	}
}

// Test: test-Matcher.md — CLI context: a bare glob is top-level-only after
// anchoring.
//
// THE one intentional behavior change of #51, pinned so it reads as decided
// rather than accidental. `-files '*.go'` anchors to $PWD/*.go and stops
// matching nested files; `/**/*` is the explicit any-depth form. The
// positional shorthand (`ark files '*.go'`) goes through the same anchoring,
// so a command never carries two glob rules. R3197, R3196, R3208
func TestCLIAnchoredBareGlobIsTopLevelOnly(t *testing.T) {
	const dir = "/proj"
	m := &Matcher{Dotfiles: true}

	bare := AnchorGlobToDir("*.go", dir)
	if bare != "/proj/*.go" {
		t.Fatalf("AnchorGlobToDir(*.go) = %q, want /proj/*.go", bare)
	}
	if !m.Match(bare, "/proj/main.go", "", false) {
		t.Error("anchored bare glob should match a top-level file")
	}
	if m.Match(bare, "/proj/pkg/db.go", "", false) {
		t.Error("anchored bare glob must NOT match a nested file — this is the decided change")
	}

	anyDepth := AnchorGlobToDir("/**/*.go", dir)
	if anyDepth != "/**/*.go" {
		t.Fatalf("an already-absolute glob must pass through, got %q", anyDepth)
	}
	for _, p := range []string{"/proj/main.go", "/proj/pkg/db.go"} {
		if !m.Match(anyDepth, p, "", false) {
			t.Errorf("/**/*.go should match %q at any depth", p)
		}
	}
}

// Test: test-Matcher.md — MatchPathFilters composes include and exclude the
// way every filter surface needs: empty lists pass everything, a non-empty
// include list requires a hit, any exclude hit rejects. R3195
func TestMatchPathFiltersComposition(t *testing.T) {
	const path = "/proj/specs/x.md"
	if !MatchPathFilters(path, nil, nil) {
		t.Error("no filters should pass everything")
	}
	if MatchPathFilters(path, []string{"*.go"}, nil) {
		t.Error("a non-empty include list should require a hit")
	}
	if MatchPathFilters(path, []string{"*.md"}, []string{"specs/**"}) {
		t.Error("an exclude hit should reject even when an include matched")
	}
	if got := FilterPaths([]string{path, "/proj/a.go"}, []string{"*.md"}, nil); len(got) != 1 || got[0] != path {
		t.Errorf("FilterPaths = %v, want [%s]", got, path)
	}
}
