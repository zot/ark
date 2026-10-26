package ark

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/fsnotify/fsnotify"
)

// TestIsWatchableDirMatchesScannerDescent is the Sleeping-Sentry guard for
// R2952: (F1) the live watcher descends exactly the directories the Scanner
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

// A link event survives the indexability filter and is never cached. This is
// the half of R3390 that is invisible when it fails: a directory symlink
// matches no *file* pattern, so the filter guarding the refresh drops the very
// event that repoints a tracked link and then writes the path into the
// negative cache, after which no later event on it is classified at all. The
// single-hop tier is defined on that event (R3384), so the association simply
// keeps returning its old, once-correct answer.
// CRC: crc-Server.md | Seq: seq-file-change.md#1.3.5 | R3390
func TestClassifyEventDivertsLinkPaths(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(root, "src")
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	indexable := filepath.Join(src, "a.md")
	plain := filepath.Join(src, "notes.txt")
	link := filepath.Join(src, "link") // no extension: matches nothing
	gone := filepath.Join(src, "vanished.md")
	for _, p := range []string{indexable, plain} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{
		Sources:        []Source{{Dir: src}},
		DefaultInclude: []string{"*.md"},
	}
	srv := &Server{
		db:           &DB{config: cfg, matcher: &Matcher{}},
		ignoredPaths: make(map[string]struct{}),
	}

	cases := []struct {
		path string
		want eventDisposition
		why  string
	}{
		{indexable, eventIndex, "an indexable file takes the ordinary refresh path"},
		{link, eventLink, "a directory symlink matches no file pattern — the filter dropped it"},
		{plain, eventIgnore, "a plain non-indexable file is still uninteresting"},
		{gone, eventIndex, "a deleted indexable file must still reach the removal path"},
	}
	for _, c := range cases {
		if got := srv.classifyEvent(c.path); got != c.want {
			t.Errorf("classifyEvent(%s) = %v, want %v — %s", filepath.Base(c.path), got, c.want, c.why)
		}
	}

	// A path that vanished without matching any pattern could still have been
	// a tracked link; only the actor's table knows, so it goes to the table.
	goneUnknown := filepath.Join(src, "vanished")
	if got := srv.classifyEvent(goneUnknown); got != eventLink {
		t.Errorf("classifyEvent(vanished) = %v, want eventLink — a deleted link must reach the table", got)
	}

	// The load-bearing assertion. Caching the link path is what makes the
	// second and every subsequent event on it disappear without being seen.
	if _, cached := srv.ignoredPaths[link]; cached {
		t.Errorf("%q was written to the negative cache; every later link event is now invisible", link)
	}
	if _, cached := srv.ignoredPaths[plain]; !cached {
		t.Errorf("%q was not cached — the negative cache stopped doing its job", plain)
	}
}

// TestWatchOverflowReconciles: an overflow means the OS dropped change events,
// so the watcher must re-scan rather than only log; any other watcher error
// must not trigger a scan. The wrapped case pins errors.Is, so a backend that
// wraps the sentinel still reconciles.
// CRC: crc-Server.md | Seq: seq-file-change.md#2.2 | R3625
func TestWatchOverflowReconciles(t *testing.T) {
	captureCurationLog(t) // keep the watcher's log lines out of test output
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"overflow", fsnotify.ErrEventOverflow, 1},
		{"wrapped overflow", fmt.Errorf("read changes: %w", fsnotify.ErrEventOverflow), 1},
		{"other error", errors.New("access denied"), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			handleWatchError(tc.err, func() { calls++ })
			if calls != tc.want {
				t.Errorf("reconcile called %d times for %v, want %d", calls, tc.err, tc.want)
			}
		})
	}
}
