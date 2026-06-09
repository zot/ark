package ark

import (
	"path/filepath"
	"testing"
)

// TestIsWatchableDirMatchesScannerDescent is the Sleeping-Sentry guard for
// R2952 (F1): the live watcher descends exactly the directories the Scanner
// walks. The bug was watchDirRecursive unconditionally skipping every
// dot-prefixed subdirectory, so with dotfiles=true an edit under .scratch/
// never auto-indexed (watch coverage ⊊ scan coverage). This marches the worst
// input — a non-excluded dot-directory under a source — past the predicate and
// asserts it is watchable, and that the predicate equals the Scanner's descent
// rule (Classify isDir=true != Excluded) for every case.
// CRC: crc-DB.md | Seq: seq-file-change.md#1.1.1 | R2952
func TestIsWatchableDirMatchesScannerDescent(t *testing.T) {
	const dir = "/repo"
	cfg := &Config{
		Dotfiles:       true,
		DefaultExclude: []string{".git/", "node_modules/"},
		Sources:        []Source{{Dir: dir}},
	}
	db := &DB{config: cfg, matcher: &Matcher{Dotfiles: cfg.Dotfiles}}

	// The Scanner descends a directory iff Classify(isDir=true) is not Excluded
	// (scanner.go). The watcher must make the identical decision.
	includes, excludes := cfg.EffectivePatterns(cfg.Sources[0])
	scannerDescends := func(path string) bool {
		return db.matcher.Classify(includes, excludes, path, dir, true) != Excluded
	}

	cases := []struct {
		sub  string
		want bool
		why  string
	}{
		{".scratch", true, "dot-dir, not excluded, dotfiles=true — the F1 bug: edits here went stale"},
		{".scratch/nested", true, "descendant of a watched dot-dir"},
		{"src", true, "ordinary subdir — Unresolved, the Scanner still descends"},
		{".git", false, "excluded as a directory (.git/)"},
		{"node_modules", false, "excluded as a directory (node_modules/)"},
	}
	for _, c := range cases {
		path := filepath.Join(dir, c.sub)
		got := db.IsWatchableDir(path)
		if got != c.want {
			t.Errorf("IsWatchableDir(%q) = %v, want %v — %s", c.sub, got, c.want, c.why)
		}
		// Watch coverage == scan coverage (R2952): the watcher's descent
		// decision must equal the Scanner's for every directory.
		if got != scannerDescends(path) {
			t.Errorf("R2952 parity broken: IsWatchableDir(%q)=%v but scanner-descends=%v",
				c.sub, got, scannerDescends(path))
		}
	}

	// A path outside every source is never watchable.
	if db.IsWatchableDir("/elsewhere/.scratch") {
		t.Error("IsWatchableDir returned true for a path under no source")
	}
}
