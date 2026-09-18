package ark

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// CRC: crc-Scanner.md | Test: test-Scanner.md | R3437

// leafKey is the classification-relevant projection of a scanLeaf. mtime is
// omitted on purpose — the property under test is which files the walk finds
// and how it classifies them, not their timestamps (and time.Time == is fragile).
type leafKey struct {
	path     string
	dir      string
	class    Classification
	size     int64
	strategy string
}

func keyLeaves(ls []scanLeaf) []leafKey {
	out := make([]leafKey, len(ls))
	for i, l := range ls {
		out[i] = leafKey{l.path, l.dir, l.class, l.size, l.strategy}
	}
	sort.Slice(out, func(i, j int) bool { return dfsLess(out[i].path, out[j].path) })
	return out
}

// sequentialWalkOracle is the sequential filepath.WalkDir reference walkSources
// must match: the same per-entry classification, symlinks recorded in WalkDir's
// DFS order. It is the pre-Item-6 phase-1 logic, so equality proves the parallel
// walk is behaviour-preserving. R3437
func sequentialWalkOracle(cfg *Config, matcher *Matcher) ([]scanLeaf, []linkCand) {
	var leaves []scanLeaf
	var links []linkCand
	for _, src := range cfg.Sources {
		if IsGlob(src.Dir) {
			continue
		}
		includes, excludes := cfg.EffectivePatterns(src)
		s := src
		_ = filepath.WalkDir(s.Dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				links = append(links, linkCand{path: path, srcDir: s.Dir})
				return nil
			}
			rel, rerr := filepath.Rel(s.Dir, path)
			if rerr != nil || rel == "." {
				return nil
			}
			if d.IsDir() {
				if matcher.Classify(includes, excludes, path, s.Dir, true) == Excluded {
					return filepath.SkipDir
				}
				return nil
			}
			cls := matcher.Classify(includes, excludes, path, s.Dir, false)
			if cls != Included && cls != Unresolved {
				return nil
			}
			leaf := scanLeaf{path: path, dir: s.Dir, class: cls}
			if info, ierr := d.Info(); ierr == nil {
				leaf.size = info.Size()
				leaf.mtime = info.ModTime()
			}
			if cls == Included {
				leaf.strategy = cfg.StrategyForFile(rel, s.Strategies)
			}
			leaves = append(leaves, leaf)
			return nil
		})
	}
	return leaves, links
}

func buildWalkFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"a", "a/sub", "a/skip", "b"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"a/keep.md":        "x", // Included
		"a/weird.xyz":      "x", // Unresolved (no include match)
		"a/sub/nested.md":  "x", // Included, nested
		"a/skip/secret.md": "x", // under an excluded dir — must never be visited
		"b/notes.md":       "x", // Included
		"b/empty.md":       "",  // Included, size 0
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Two directory symlinks at different depths (order-distinguishing).
	if err := os.Symlink(filepath.Join(root, "b"), filepath.Join(root, "a", "linkA")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "b"), filepath.Join(root, "linkRoot")); err != nil {
		t.Fatal(err)
	}
	return root
}

func walkTestConfig(root string) (*Config, *Matcher) {
	cfg := &Config{
		Sources:        []Source{{Dir: root}},
		DefaultInclude: []string{"**/*.md"},
		DefaultExclude: []string{"skip/"}, // trailing slash → matches the dir
	}
	return cfg, &Matcher{Dotfiles: true}
}

// TestWalkSourcesMatchesSequentialWalk proves the parallel walk finds and
// classifies exactly what a sequential filepath.WalkDir does, honors an
// excluded directory (never descending it), and returns link candidates in the
// same DFS order — so a parallel walk cannot change link classification or the
// reverse index. Covers test-Scanner.md alarms 1 (excluded dir) and 2 (DFS
// order). R3437
func TestWalkSourcesMatchesSequentialWalk(t *testing.T) {
	root := buildWalkFixture(t)
	cfg, matcher := walkTestConfig(root)

	wantLeaves, wantLinks := sequentialWalkOracle(cfg, matcher)

	// Guard the guard: the fixture must actually exercise an excluded dir and
	// real symlinks, or the test would pass for the wrong reason.
	secret := filepath.Join(root, "a", "skip", "secret.md")
	for _, l := range wantLeaves {
		if l.path == secret {
			t.Fatal("fixture broken: oracle descended the excluded skip/ dir")
		}
	}
	if len(wantLinks) != 2 {
		t.Fatalf("fixture broken: want 2 link candidates, oracle found %d", len(wantLinks))
	}

	for _, workers := range []int{1, 8} {
		gotLeaves, gotLinks, err := walkSources(cfg, matcher, workers)
		if err != nil {
			t.Fatalf("walkSources(%d): %v", workers, err)
		}
		if !reflect.DeepEqual(keyLeaves(gotLeaves), keyLeaves(wantLeaves)) {
			t.Errorf("workers=%d: leaves differ from sequential walk\n got %+v\nwant %+v",
				workers, keyLeaves(gotLeaves), keyLeaves(wantLeaves))
		}
		// Links compared as an ORDERED slice: this is what pins dfsLess to
		// WalkDir's DFS order. A reversed comparator makes this fail.
		if !reflect.DeepEqual(gotLinks, wantLinks) {
			t.Errorf("workers=%d: link order differs from DFS walk order\n got %+v\nwant %+v",
				workers, gotLinks, wantLinks)
		}
	}
}

// TestWalkSourcesConcurrentIsRaceFree drives many workers over a wide tree so
// the shared-result append is exercised concurrently; run under -race it flags a
// missing mutex, and the count guards against lost appends. Covers
// test-Scanner.md alarm 3. R3437
func TestWalkSourcesConcurrentIsRaceFree(t *testing.T) {
	root := t.TempDir()
	const dirs, perDir = 24, 8
	for i := 0; i < dirs; i++ {
		d := filepath.Join(root, fmt.Sprintf("d%02d", i))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < perDir; j++ {
			if err := os.WriteFile(filepath.Join(d, fmt.Sprintf("f%02d.md", j)), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	cfg := &Config{Sources: []Source{{Dir: root}}, DefaultInclude: []string{"**/*.md"}}
	matcher := &Matcher{Dotfiles: true}

	want, _ := sequentialWalkOracle(cfg, matcher)
	got, _, err := walkSources(cfg, matcher, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("parallel walk lost leaves: got %d, want %d", len(got), len(want))
	}
	if len(got) != dirs*perDir {
		t.Fatalf("want %d leaves, got %d", dirs*perDir, len(got))
	}
}
