package ark

// CRC: crc-DB.md | Test: test-Sweep.md

import "testing"

// R2139: a path that no longer classifies as Included must be swept
func TestShouldSweepNewlyExcluded(t *testing.T) {
	cfg := &Config{
		Sources:        []Source{{Dir: "/proj"}},
		DefaultInclude: []string{"*.md"},
		DefaultExclude: []string{"**/*.js"},
	}
	matcher := &Matcher{Dotfiles: true}
	if !shouldSweep("/proj/foo.js", matcher, cfg) {
		t.Error("excluded *.js file should be swept")
	}
}

// R2139: a still-included file must not be swept
func TestShouldSweepKeepIncluded(t *testing.T) {
	cfg := &Config{
		Sources:        []Source{{Dir: "/proj"}},
		DefaultInclude: []string{"*.md"},
	}
	matcher := &Matcher{Dotfiles: true}
	if shouldSweep("/proj/notes.md", matcher, cfg) {
		t.Error("still-included file should not be swept")
	}
}

// R2140: a file with no claiming source must be swept
func TestShouldSweepOrphanedSource(t *testing.T) {
	cfg := &Config{
		Sources:        []Source{{Dir: "/proj"}},
		DefaultInclude: []string{"*.md"},
	}
	matcher := &Matcher{Dotfiles: true}
	if !shouldSweep("/old-src/a.md", matcher, cfg) {
		t.Error("file outside any configured source should be swept")
	}
}

// R2139: the Unresolved branch (matches no include/exclude rule) should
// also sweep — those files were indexed before a rule changed
func TestShouldSweepUnresolvedAfterRuleRemoval(t *testing.T) {
	cfg := &Config{
		Sources: []Source{{Dir: "/proj"}},
		// No includes — every file is Unresolved
	}
	matcher := &Matcher{Dotfiles: true}
	if !shouldSweep("/proj/orphan.md", matcher, cfg) {
		t.Error("Unresolved file should be swept (Classify != Included)")
	}
}
