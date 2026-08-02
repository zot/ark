package ark

// CRC: crc-LinkTable.md | Seq: seq-link-resolve.md
//
// Tests for LinkTable. Design: design/test-LinkTable.md
//
// All of these run against a temporary directory tree and a zero-value
// Config carrying only Sources. No DB, no server, no fixtures — the logic
// under test is the track/propose predicate, component-wise resolution, and
// reverse expansion, none of which need indexed content.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zot/microfts2"
)

// linkTempRoot returns a temp directory with symlinks already resolved, so
// a test asserting on canonical paths is not defeated by an aliased
// tmpdir — on macOS /tmp is itself a link, which is exactly the mid-path
// case under test.
func linkTempRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks(TempDir): %v", err)
	}
	return root
}

func mkDirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", d, err)
		}
	}
}

func mkSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(link), err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink(%s -> %s): %v", link, target, err)
	}
}

func mkFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

// linkCfg builds the only Config field LinkTable reads.
func linkCfg(dirs ...string) *Config {
	cfg := &Config{}
	for _, d := range dirs {
		cfg.Sources = append(cfg.Sources, Source{Dir: d})
	}
	return cfg
}

func hasAddr(addrs []string, want string) bool {
	for _, a := range addrs {
		if a == want {
			return true
		}
	}
	return false
}

// A directory link whose target is in a source is tracked — the track arm
// of the single predicate. R3381
func TestLinkTrackedWhenTargetInsideSource(t *testing.T) {
	root := linkTempRoot(t)
	s1, s2 := filepath.Join(root, "s1"), filepath.Join(root, "s2")
	mkDirs(t, s1, s2)
	link := filepath.Join(s2, "link")
	mkSymlink(t, s1, link)

	lt := NewLinkTable(linkCfg(s1, s2))
	entry, status := lt.Observe(link)
	if status != LinkTracked {
		t.Fatalf("status = %v, want LinkTracked", status)
	}
	if entry.Resolved != s1 {
		t.Errorf("Resolved = %q, want %q", entry.Resolved, s1)
	}
	if got := lt.Resolve(link); got != s1 {
		t.Errorf("Resolve(link) = %q, want %q", got, s1)
	}
	if entry.Tier != TierSingleHop {
		t.Errorf("Tier = %v, want single-hop", entry.Tier)
	}
}

// A directory link whose target is outside every source is proposed, never
// admitted — the same predicate, other branch. R3355, R3381
func TestLinkProposedWhenTargetOutsideEverySource(t *testing.T) {
	root := linkTempRoot(t)
	s2, outside := filepath.Join(root, "s2"), filepath.Join(root, "outside")
	mkDirs(t, s2, outside)
	link := filepath.Join(s2, "link")
	mkSymlink(t, outside, link)

	lt := NewLinkTable(linkCfg(s2))
	entry, status := lt.Observe(link)
	if status != LinkProposed {
		t.Fatalf("status = %v, want LinkProposed", status)
	}
	// The candidate is keyed on the target, and names the proposing link so
	// it can explain itself — a bare directory the user may not recognise
	// is not an actionable candidate. R3357
	if entry.Resolved != outside {
		t.Errorf("proposed target = %q, want %q", entry.Resolved, outside)
	}
	if entry.Path != link {
		t.Errorf("proposing link = %q, want %q", entry.Path, link)
	}
	if lt.Len() != 0 {
		t.Errorf("proposed link was tracked; Len = %d, want 0", lt.Len())
	}
}

// The predicate does not consult the index: a link tracks even with nothing
// indexable beneath its target. The rejected alternative — track only when
// the target has indexed files — would make this untracked, and would flap
// as files come and go. R3381
func TestLinkTracksWithEmptyTarget(t *testing.T) {
	root := linkTempRoot(t)
	s1, s2 := filepath.Join(root, "s1"), filepath.Join(root, "s2")
	mkDirs(t, s1, s2) // s1 deliberately empty
	link := filepath.Join(s2, "link")
	mkSymlink(t, s1, link)

	lt := NewLinkTable(linkCfg(s1, s2))
	if _, status := lt.Observe(link); status != LinkTracked {
		t.Fatalf("status = %v, want LinkTracked", status)
	}
	// The accepted cost of the predicate: an alias namespace over a target
	// with nothing beneath it. It answers nothing, which is the truthful
	// answer, not an error.
	if got := lt.Resolve(filepath.Join(link, "a.md")); got != filepath.Join(s1, "a.md") {
		t.Errorf("Resolve = %q, want %q", got, filepath.Join(s1, "a.md"))
	}
}

// Adding link and source in either order reaches the same state — the
// ordering independence the single predicate buys. Adding the source later
// is a config change, and re-deriving at load promotes the candidate with
// no additional trigger. R3381
func TestLinkAndSourceInEitherOrder(t *testing.T) {
	root := linkTempRoot(t)
	s1, s2 := filepath.Join(root, "s1"), filepath.Join(root, "s2")
	mkDirs(t, s1, s2)
	link := filepath.Join(s2, "link")
	mkSymlink(t, s1, link)

	// (a) link first: s1 is not yet a source, so the link is proposed.
	ltA := NewLinkTable(linkCfg(s2))
	if _, status := ltA.Observe(link); status != LinkProposed {
		t.Fatalf("before adding source: status = %v, want LinkProposed", status)
	}
	// Now s1 becomes a source. Re-deriving at config load is the whole
	// promotion mechanism.
	ltA.cfg = linkCfg(s1, s2)
	if proposed := ltA.Rebuild([]string{link}); len(proposed) != 0 {
		t.Errorf("after adding source: %d still proposed, want 0", len(proposed))
	}

	// (b) source first.
	ltB := NewLinkTable(linkCfg(s1, s2))
	if _, status := ltB.Observe(link); status != LinkTracked {
		t.Fatalf("source first: status = %v, want LinkTracked", status)
	}

	entA, okA := ltA.Lookup(link)
	entB, okB := ltB.Lookup(link)
	if !okA || !okB {
		t.Fatalf("tracked in a=%v b=%v, want both", okA, okB)
	}
	if entA.Resolved != entB.Resolved || entA.Tier != entB.Tier {
		t.Errorf("states differ: a=%+v b=%+v", entA, entB)
	}
}

// Resolution is component-wise, with the link mid-path. A leaf-only
// resolver passes every other test here and fails this one — it is the
// originating defect. R3360, R3372
func TestLinkResolveComponentWise(t *testing.T) {
	root := linkTempRoot(t)
	s1, s2 := filepath.Join(root, "s1"), filepath.Join(root, "s2")
	mkDirs(t, s1, s2)
	mkFile(t, filepath.Join(s1, "a.md"))
	link := filepath.Join(s2, "link")
	mkSymlink(t, s1, link)

	lt := NewLinkTable(linkCfg(s1, s2))
	lt.Observe(link)

	// The leaf is a real file and resolves to itself; the link is two
	// components up.
	got := lt.Resolve(filepath.Join(link, "a.md"))
	want := filepath.Join(s1, "a.md")
	if got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

// Tilde expansion alone does not resolve: it yields a lexically absolute
// path that may still traverse links. This is the exact shape of the @ext
// defect, where absolutizeExtPath stopped at IsAbs. R3372
func TestLinkTildeExpansionIsNotResolution(t *testing.T) {
	root := linkTempRoot(t)
	home := filepath.Join(root, "home")
	s1 := filepath.Join(root, "s1")
	mkDirs(t, home, s1)
	mkFile(t, filepath.Join(s1, "a.md"))
	link := filepath.Join(home, "proj")
	mkSymlink(t, s1, link)
	t.Setenv("HOME", home)

	lt := NewLinkTable(linkCfg(s1, home))
	lt.Observe(link)

	got := lt.Resolve("~/proj/a.md")
	want := filepath.Join(s1, "a.md")
	if got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
	// Assert specifically that the expanded-but-unresolved form is not what
	// came back — that is the form a routine stopping at IsAbs produces.
	if got == filepath.Join(link, "a.md") {
		t.Errorf("Resolve returned the expanded-but-unresolved path %q", got)
	}
}

// A dangling link falls back to the literal path. A resolution failure must
// not become an error: ark has already had broken symlinks crash rebuild
// and reconcile once. R3361
func TestLinkDanglingFallsBackToLiteral(t *testing.T) {
	root := linkTempRoot(t)
	s2 := filepath.Join(root, "s2")
	mkDirs(t, s2)
	link := filepath.Join(s2, "link")
	mkSymlink(t, filepath.Join(root, "gone"), link)

	lt := NewLinkTable(linkCfg(s2))
	if _, status := lt.Observe(link); status != LinkProposed {
		t.Fatalf("status = %v, want LinkProposed", status)
	}
	addr := filepath.Join(link, "a.md")
	if got := lt.Resolve(addr); got != addr {
		t.Errorf("Resolve = %q, want the literal %q", got, addr)
	}
}

// Only the wildcard-free prefix of a pattern resolves; the remainder
// re-attaches unchanged. A pattern is not a path. R3362
func TestLinkResolvePatternPrefixOnly(t *testing.T) {
	root := linkTempRoot(t)
	s1, s2 := filepath.Join(root, "s1"), filepath.Join(root, "s2")
	mkDirs(t, s1, s2)
	link := filepath.Join(s2, "link")
	mkSymlink(t, s1, link)

	lt := NewLinkTable(linkCfg(s1, s2))
	lt.Observe(link)

	got := lt.ResolvePattern(filepath.Join(link, "**", "*.md"))
	want := s1 + "/**/*.md"
	if got != want {
		t.Errorf("ResolvePattern = %q, want %q", got, want)
	}
	// A pattern whose first component is a wildcard has nothing concrete to
	// resolve, and must come back untouched — this is the /-anchored form
	// that asks the index-wide question.
	if got := lt.ResolvePattern("/**/a.md"); got != "/**/a.md" {
		t.Errorf("ResolvePattern(/**/a.md) = %q, want it unchanged", got)
	}
}

// Reverse expansion iterates to a fixed point: a link may point into a tree
// another link also targets. Iteration is not defensive — ark contains
// .claude/skills/ark-usage/ark -> .claude/skills/ark, a sibling inside a
// tree other links target. R3374, R3378
func TestLinkExpandIteratesToFixedPoint(t *testing.T) {
	root := linkTempRoot(t)
	sa := filepath.Join(root, "sa")
	sb := filepath.Join(root, "sb")
	sc := filepath.Join(root, "sc")
	mkDirs(t, sa, sb, sc)
	mkFile(t, filepath.Join(sc, "f.md"))
	// sb/x -> sc, and sa/link -> sb, so an address under sa/link/x reaches
	// sc through two substitutions.
	mkSymlink(t, sc, filepath.Join(sb, "x"))
	mkSymlink(t, sb, filepath.Join(sa, "link"))

	lt := NewLinkTable(linkCfg(sa, sb, sc))
	lt.Observe(filepath.Join(sb, "x"))
	lt.Observe(filepath.Join(sa, "link"))

	addrs := lt.Expand(filepath.Join(sc, "f.md"))
	oneStep := filepath.Join(sb, "x", "f.md")
	twoStep := filepath.Join(sa, "link", "x", "f.md")
	if !hasAddr(addrs, oneStep) {
		t.Errorf("addresses %v missing the one-step form %q", addrs, oneStep)
	}
	if !hasAddr(addrs, twoStep) {
		t.Errorf("addresses %v missing the two-step form %q", addrs, twoStep)
	}
	if !hasAddr(addrs, filepath.Join(sc, "f.md")) {
		t.Errorf("addresses %v missing the canonical path", addrs)
	}
}

// A cycle terminates. The layout permits one and ark already has a link
// pointing at the farm containing it, so this is a property of the walk
// rather than of the data. R3353, R3374
func TestLinkCycleTerminates(t *testing.T) {
	root := linkTempRoot(t)
	sa, sb := filepath.Join(root, "sa"), filepath.Join(root, "sb")
	mkDirs(t, sa, sb)
	mkSymlink(t, sb, filepath.Join(sa, "link"))
	mkSymlink(t, sa, filepath.Join(sb, "link"))

	lt := NewLinkTable(linkCfg(sa, sb))
	lt.Observe(filepath.Join(sa, "link"))
	lt.Observe(filepath.Join(sb, "link"))

	// Both must return; the assertion is termination, not a value.
	done := make(chan struct{})
	go func() {
		defer close(done)
		lt.Resolve(filepath.Join(sa, "link", "link", "link", "f.md"))
		lt.Expand(filepath.Join(sa, "f.md"))
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("resolution or expansion did not terminate on a cyclic layout")
	}
}

// A scan records a directory symlink as an entity and stops. WalkDir does
// not follow links, so d.IsDir() is false for one and it would otherwise
// fall to the file branch, be matched against file patterns, and be
// silently dropped — the originating defect, at the site that caused it.
// R3352, R3373
func TestScanRecordsDirectorySymlinkAndStops(t *testing.T) {
	root := linkTempRoot(t)
	s1, s2 := filepath.Join(root, "s1"), filepath.Join(root, "s2")
	mkDirs(t, s1, s2)
	mkFile(t, filepath.Join(s1, "a.txt"))
	link := filepath.Join(s2, "link")
	mkSymlink(t, s1, link)

	cfg := &Config{
		Sources:        []Source{{Dir: s1}, {Dir: s2}},
		DefaultInclude: []string{"*.txt"},
	}
	dbPath := filepath.Join(t.TempDir(), "db")
	mkDirs(t, dbPath)
	fts, err := microfts2.Create(IndexPath(dbPath), microfts2.Options{})
	if err != nil {
		t.Fatal(err)
	}
	fts.AddChunker("line", microfts2.FuncChunker{Fn: microfts2.LineChunkFunc})
	t.Cleanup(func() { fts.Close() })

	sc := NewScanner(cfg, &Matcher{}, fts, NewEmptyFiles())
	res, err := sc.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if len(res.TrackedLinks) != 1 || res.TrackedLinks[0].Path != link {
		t.Fatalf("TrackedLinks = %+v, want exactly %q", res.TrackedLinks, link)
	}
	var paths []string
	for _, f := range res.NewFiles {
		paths = append(paths, f.Path)
	}
	// The file keeps one index entry while gaining a legal address: the
	// alias lives on the link, not on each file beneath it.
	if len(paths) != 1 || paths[0] != filepath.Join(s1, "a.txt") {
		t.Errorf("NewFiles = %v, want exactly %q", paths, filepath.Join(s1, "a.txt"))
	}
	for _, bad := range []string{link, filepath.Join(link, "a.txt")} {
		if hasAddr(paths, bad) {
			t.Errorf("NewFiles contains %q — the scan descended, or indexed the link as a file", bad)
		}
	}
}

// A tier is derived state, re-derived on every recomputation. A recompute
// that refreshes the fileid but keeps the tier leaves an association on a
// verification schedule for a shape it no longer has — the one failure in
// the scheme that is silent in the wrong direction. R3385
func TestLinkTierRederivedWhenShapeChanges(t *testing.T) {
	root := linkTempRoot(t)
	s1, s2, s3 := filepath.Join(root, "s1"), filepath.Join(root, "s2"), filepath.Join(root, "s3")
	mkDirs(t, s1, s2, s3)
	link := filepath.Join(s2, "link")
	mkSymlink(t, s1, link)

	lt := NewLinkTable(linkCfg(s1, s2, s3))
	entry, _ := lt.Observe(link)
	if entry.Tier != TierSingleHop {
		t.Fatalf("initial tier = %v, want single-hop", entry.Tier)
	}

	// Replace the target with a symlink, so the chain becomes two hops. It
	// still ends inside a source, so the link stays tracked and only its
	// shape has changed — which is precisely the case a stale tier hides.
	if err := os.Remove(s1); err != nil {
		t.Fatalf("Remove(s1): %v", err)
	}
	mkSymlink(t, s3, s1)

	entry, status := lt.Observe(link)
	if status != LinkTracked {
		t.Fatalf("status after repoint = %v, want LinkTracked", status)
	}
	// Asserting the tier directly, because every observable behavior still
	// looks correct at the moment of the change: the divergence appears
	// later, when the now-unverified hop moves.
	if entry.Tier == TierSingleHop {
		t.Errorf("tier stayed single-hop after the chain grew a hop: %+v", entry)
	}
	if len(entry.Hops) != 2 {
		t.Errorf("Hops = %v, want two", entry.Hops)
	}
}
