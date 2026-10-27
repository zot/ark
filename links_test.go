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
	setTestHome(t, home)

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
	want := filepath.Join(s1, "**", "*.md")
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

// A repointed link is followed through the per-path refresh. A link change
// never arrives as a scan, so SyncPath is the only thing that re-derives a
// tracked link's resolution — and the single-hop tier is defined on exactly
// that event. R3390, R3384
func TestLinkSyncPathFollowsRepoint(t *testing.T) {
	root := linkTempRoot(t)
	s1a, s1b, s2 := filepath.Join(root, "s1a"), filepath.Join(root, "s1b"), filepath.Join(root, "s2")
	mkDirs(t, s1a, s1b, s2)
	link := filepath.Join(s2, "link")
	mkSymlink(t, s1a, link)

	lt := NewLinkTable(linkCfg(s1a, s1b, s2))
	if _, status := lt.Observe(link); status != LinkTracked {
		t.Fatalf("initial status = %v, want LinkTracked", status)
	}

	// Repoint: unlink then symlink, which is what `ln -sfn` does and what the
	// watcher sees as a Remove followed by a Create.
	if err := os.Remove(link); err != nil {
		t.Fatalf("Remove(link): %v", err)
	}
	mkSymlink(t, s1b, link)

	entry, status := lt.SyncPath(link)
	if status != LinkTracked {
		t.Fatalf("status after repoint = %v, want LinkTracked", status)
	}
	// Asserting the new target rather than merely that something changed: the
	// old answer is a real path that still exists and still reads as correct.
	if entry.Resolved != s1b {
		t.Errorf("Resolved = %q, want %q", entry.Resolved, s1b)
	}
	if got, want := lt.Resolve(filepath.Join(link, "a.md")), filepath.Join(s1b, "a.md"); got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

// A deleted link is forgotten, returning whatever depended on it to
// unresolved rather than leaving it pointing at a file it can no longer
// reach. R3369, R3390
func TestLinkSyncPathForgetsDeletedLink(t *testing.T) {
	root := linkTempRoot(t)
	s1, s2 := filepath.Join(root, "s1"), filepath.Join(root, "s2")
	mkDirs(t, s1, s2)
	link := filepath.Join(s2, "link")
	mkSymlink(t, s1, link)

	lt := NewLinkTable(linkCfg(s1, s2))
	lt.Observe(link)
	if lt.Len() != 1 {
		t.Fatalf("Len = %d before removal, want 1", lt.Len())
	}

	if err := os.Remove(link); err != nil {
		t.Fatalf("Remove(link): %v", err)
	}
	if _, status := lt.SyncPath(link); status != LinkUnresolvable {
		t.Errorf("status = %v, want LinkUnresolvable", status)
	}
	if lt.Len() != 0 {
		t.Errorf("Len = %d after removal, want 0", lt.Len())
	}
	// A vanished alias is ark's ordinary not-indexed answer, not an error.
	addr := filepath.Join(link, "a.md")
	if got := lt.Resolve(addr); got != addr {
		t.Errorf("Resolve = %q, want the literal %q", got, addr)
	}
}

// A link replaced by a regular file stops being an alias prefix and is handed
// back as not the table's business, so the caller indexes it as the file it
// now is. Lstat-before-following is the whole correction: a routine that stats
// first sees a directory for a repoint and a file here, and in both cases has
// already lost the question it was asked. R3390
func TestLinkSyncPathForgetsLinkReplacedByFile(t *testing.T) {
	root := linkTempRoot(t)
	s1, s2 := filepath.Join(root, "s1"), filepath.Join(root, "s2")
	mkDirs(t, s1, s2)
	link := filepath.Join(s2, "link")
	mkSymlink(t, s1, link)

	lt := NewLinkTable(linkCfg(s1, s2))
	lt.Observe(link)

	if err := os.Remove(link); err != nil {
		t.Fatalf("Remove(link): %v", err)
	}
	mkFile(t, link)

	if _, status := lt.SyncPath(link); status != LinkUnresolvable {
		t.Errorf("status = %v, want LinkUnresolvable", status)
	}
	if _, tracked := lt.Lookup(link); tracked {
		t.Errorf("%q is still tracked after being replaced by a regular file", link)
	}
}

// A proposed link's target is recorded as an unresolved candidate through the
// refresh, exactly as the scan records it. The scan and the refresh must
// dispose of the same link identically: two surfaces that disagree about what
// a link is are worse than one that is consistently wrong. R3355, R3357, R3390
func TestSyncLinkPathRecordsProposedTarget(t *testing.T) {
	root := linkTempRoot(t)
	s2, outside := filepath.Join(root, "s2"), filepath.Join(root, "outside")
	mkDirs(t, s2, outside)
	link := filepath.Join(s2, "link")
	mkSymlink(t, outside, link)

	dbPath := filepath.Join(t.TempDir(), "db")
	mkDirs(t, dbPath)
	fts, err := microfts2.Create(IndexPath(dbPath), microfts2.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fts.Close() })
	store, err := OpenStore(fts.DB())
	if err != nil {
		t.Fatal(err)
	}

	cfg := &Config{Sources: []Source{{Dir: s2}}}
	db := &DB{
		fts:     fts,
		store:   store,
		config:  cfg,
		dbPath:  dbPath,
		scanner: NewScanner(cfg, &Matcher{}, fts, NewEmptyFiles()),
	}

	db.SyncLinkPath(link)

	recs, err := store.ListUnresolved()
	if err != nil {
		t.Fatalf("ListUnresolved: %v", err)
	}
	var found bool
	for _, r := range recs {
		if r.Path == outside {
			found = true
		}
	}
	if !found {
		t.Errorf("unresolved records %+v carry no candidate for the proposed target %q", recs, outside)
	}
	// The link proposes its target; it never admits it.
	if n := db.scanner.Links().Len(); n != 0 {
		t.Errorf("tracked links = %d, want 0 — a proposed link was tracked", n)
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

// Test: test-LinkTable.md — the predicate is re-derived against the new config.
//
// R3381. The predicate is a test over (target, source config) with no index
// lookup, so a config change is the whole trigger. SetConfig before Rebuild is
// not optional: the table's config *is* the source membership the predicate
// tests, so rebuilding against the previous one classifies links correctly for
// a configuration nobody is running — which looks like the reload did nothing.
//
// This covers the DEMOTION direction only. Promotion (proposed -> tracked)
// needs the proposal list, which lives in U records that do not yet carry the
// proposing link — gap O202.
func TestRebuildRederivesAgainstNewConfig(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "notes")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	linkHome := filepath.Join(dir, "src")
	if err := os.Mkdir(linkHome, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(linkHome, "notes-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	// With the target inside a source, the link is tracked.
	withTarget := &Config{Sources: []Source{{Dir: linkHome}, {Dir: target}}}
	lt := NewLinkTable(withTarget)
	if _, status := lt.Observe(link); status != LinkTracked {
		t.Fatalf("target is inside a source but the link was not tracked (status %v)", status)
	}
	if lt.Len() != 1 {
		t.Fatalf("tracked set has %d entries, want 1", lt.Len())
	}

	// Drop the target's source — the same edit a user makes in ark.toml — and
	// re-derive. The link must fall back to proposed.
	without := &Config{Sources: []Source{{Dir: linkHome}}}
	lt.SetConfig(without)
	proposed := lt.Rebuild(lt.Links())

	if lt.Len() != 0 {
		t.Errorf("link still tracked after its target's source was removed (%d entries)", lt.Len())
	}
	if len(proposed) != 1 {
		t.Fatalf("Rebuild returned %d proposals, want 1 — the demoted link must be offered as a candidate", len(proposed))
	}
	if proposed[0].Path != link {
		t.Errorf("proposal names %q, want %q", proposed[0].Path, link)
	}
}

// Test: test-LinkTable.md — every door resolves, and none of them races.
//
// R3359. The doors are `Fetch`, `IsIndexed`, `ReadSourceFile` (which the
// `/content` URL handler reaches through), and the search file filters. Each
// takes a path from outside and must translate it before use, because stored
// paths are canonical: an alias-spelled path that skips resolution produces a
// plausible "not indexed" for a file that is indexed — the original defect.
//
// The single-door assertion is deliberately absent. What matters is that the
// *set* agrees, since a surface that resolves and one that does not disagree
// silently, so this exercises the shared translation every door calls.
func TestResolveIsComponentWiseAndFallsBack(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "src")
	sub := filepath.Join(real, "deep")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "alias")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{Sources: []Source{{Dir: dir}}}
	lt := NewLinkTable(cfg)
	if _, status := lt.Observe(link); status != LinkTracked {
		t.Fatalf("link not tracked (status %v)", status)
	}

	// Component-wise: the link is a mid-path *directory*, not the leaf. This
	// is the shape of the original defect — a file at <farm>/<dir>/<file>
	// where <farm>/<dir> was the link.
	if got, want := lt.Resolve(filepath.Join(link, "deep", "a.md")),
		filepath.Join(real, "deep", "a.md"); got != want {
		t.Errorf("mid-path link not resolved: got %q, want %q", got, want)
	}
	// A path traversing no tracked link is returned unchanged — the cheap
	// test for whether any of this applies at all.
	untouched := filepath.Join(dir, "elsewhere", "b.md")
	if got := lt.Resolve(untouched); got != untouched {
		t.Errorf("unrelated path rewritten: got %q, want %q", got, untouched)
	}
	// A pattern is not a path: only the wildcard-free prefix resolves.
	if got, want := lt.ResolvePattern(filepath.Join(link, "**", "*.md")),
		filepath.Join(real, "**", "*.md"); got != want {
		t.Errorf("pattern prefix not resolved: got %q, want %q", got, want)
	}
	// A wildcard in the first component leaves nothing concrete to resolve.
	if got := lt.ResolvePattern("**/*.md"); got != "**/*.md" {
		t.Errorf("rootless pattern rewritten: got %q", got)
	}
	// Dangling: resolution still happens, and that is the point. The chain
	// is read with readlink, which does not stat the target, so the answer
	// is the target path — which then does not exist, so the door replies
	// with ark's ordinary not-indexed. A resolver that refused here would
	// turn a missing file into a resolution *error*, which is exactly what
	// R3361 forbids.
	if err := os.RemoveAll(real); err != nil {
		t.Fatal(err)
	}
	lt2 := NewLinkTable(cfg)
	if _, status := lt2.Observe(link); status != LinkTracked {
		t.Errorf("a dangling link stopped being tracked (status %v); its target path is still inside a source", status)
	}
	if got, want := lt2.Resolve(filepath.Join(link, "deep", "a.md")),
		filepath.Join(real, "deep", "a.md"); got != want {
		t.Errorf("dangling link did not resolve: got %q, want %q", got, want)
	}
	if _, err := os.Stat(lt2.Resolve(filepath.Join(link, "deep", "a.md"))); !os.IsNotExist(err) {
		t.Error("the resolved path exists, so this case proves nothing about dangling links")
	}
}

// Test: test-LinkTable.md — a cycle falls back to the literal path.
//
// R3361, R3374. This is the case that genuinely cannot be resolved. The
// layout permits it: ark already contains a link pointing at the farm that
// contains it, so the guard is a real limit rather than defensive coding.
func TestResolveCycleFallsBackToLiteral(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	if err := os.Symlink(b, a); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(a, b); err != nil {
		t.Fatal(err)
	}
	lt := NewLinkTable(&Config{Sources: []Source{{Dir: dir}}})
	lt.Observe(a)
	lt.Observe(b)

	literal := filepath.Join(a, "x.md")
	if got := lt.Resolve(literal); got != literal {
		t.Errorf("a cycle did not fall back to the literal path: got %q, want %q", got, literal)
	}
}

// Test: test-LinkTable.md — readers never see a half-built table.
//
// R3359. Every mutator runs on the DB actor and every door is an off-actor
// read, so the contents live in an immutable snapshot behind an atomic
// pointer. This is the guard on that: run readers flat out against a mutator
// and let -race judge. Without publication it is the same latent map race
// O154 was.
func TestLinkTableReadsAreRaceFree(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"t1", "t2"} {
		if err := os.Mkdir(filepath.Join(dir, n), 0755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(dir, "alias")
	if err := os.Symlink(filepath.Join(dir, "t1"), link); err != nil {
		t.Fatal(err)
	}
	lt := NewLinkTable(&Config{Sources: []Source{{Dir: dir}}})
	lt.Observe(link)

	done := make(chan struct{})
	go func() { // the actor side
		defer close(done)
		for i := 0; i < 200; i++ {
			lt.Observe(link)
			lt.Remove(link)
			lt.Rebuild([]string{link})
		}
	}()
	for i := 0; i < 200; i++ { // the doors
		lt.Resolve(filepath.Join(link, "a.md"))
		lt.ResolvePattern(filepath.Join(link, "**", "*.md"))
		lt.Expand(filepath.Join(dir, "t1", "a.md"))
		lt.Links()
		lt.Len()
		lt.Lookup(link)
	}
	<-done
}

// An alias-spelled `-files` filter must find the file its canonical spelling
// finds. The row is resolved where it becomes a *closure*, not where it
// enters a struct: the CLI and both server sites build their filters through
// BuildChunkFilters without ever reaching Searcher.normalizeOpts, so
// resolving at the search entry points alone left this exact case broken on
// both doors while every unit test stayed green (gap O204/O206).
//
// This test therefore goes through BuildChunkFilters and carries the built
// options as extraOpts — the CLI's own shape. Routing the glob through
// opts.ChunkFilters instead would be resolved by normalizeOpts and the test
// would pass with the fix removed.
//
// Three files, not one: the trigram filter (R572, FilterByRatio 0.50)
// discards any trigram present in more than half the chunks, so on a
// one-chunk corpus every trigram is discarded and the search finds nothing
// for reasons that have nothing to do with the filter under test.
// CRC: crc-Searcher.md | Seq: seq-filter-stack.md | R3400
func TestBuildChunkFiltersResolvesAliasSpelledFilesRow(t *testing.T) {
	root := linkTempRoot(t)
	realDir := filepath.Join(root, "real")
	deep := filepath.Join(realDir, "deep")
	src := filepath.Join(root, "src")
	mkDirs(t, deep, src)
	link := filepath.Join(src, "alias")
	mkSymlink(t, realDir, link)

	ftsDir := filepath.Join(t.TempDir(), "fts")
	mkDirs(t, ftsDir)
	fts, err := microfts2.Create(IndexPath(ftsDir), microfts2.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fts.Close() })
	if err := fts.AddChunker("line", microfts2.FuncChunker{Fn: microfts2.LineChunkFunc}); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(fts.DB())
	if err != nil {
		t.Fatal(err)
	}
	idx := &Indexer{fts: fts, store: store}

	// The target file lives under the link's subtree; the fillers do not, so
	// a filter that resolves keeps exactly one result and a filter that does
	// not keeps none.
	note := filepath.Join(deep, "note.md")
	if err := os.WriteFile(note, []byte("the quick brown fox\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"f1.md", "f2.md"} {
		p := filepath.Join(root, f)
		if err := os.WriteFile(p, []byte("unrelated maritime cartography\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := idx.AddFile(p, "line"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := idx.AddFile(note, "line"); err != nil {
		t.Fatal(err)
	}

	lt := NewLinkTable(linkCfg(realDir, src))
	if _, status := lt.Observe(link); status != LinkTracked {
		t.Fatalf("Observe(%q) = %v, want tracked — the fixture is wrong, not the code", link, status)
	}

	paths, err := fts.FileIDPaths()
	if err != nil {
		t.Fatal(err)
	}

	search := func(glob string) []microfts2.SearchResult {
		t.Helper()
		var opts SearchOpts
		rows := []ChunkFilterRow{{Mode: "files", Query: glob, Polarity: "with"}}
		opts.SetExtraOpts(BuildChunkFilters(rows, lt, nil, paths, store)...)
		fr, serr := fts.Search("fox", defaultSearchOpts(nil, "", opts)...)
		if serr != nil {
			t.Fatalf("search %q: %v", glob, serr)
		}
		return fr.Results
	}

	// Control first. Without it every assertion below is vacuous — an earlier
	// measurement of this exact defect proved nothing because BOTH spellings
	// returned nothing and the corpus was simply not matching.
	canonical := search(realDir + "/**")
	if len(canonical) != 1 || canonical[0].Path != note {
		t.Fatalf("canonical spelling found %d results (%v), want exactly %q — control failed, the rest of this test proves nothing",
			len(canonical), canonical, note)
	}

	alias := search(link + "/**")
	if len(alias) != 1 || alias[0].Path != note {
		t.Errorf("alias-spelled -files found %d results (%v), want exactly %q — the glob reached the matcher unresolved",
			len(alias), alias, note)
	}

	// An exact path, no wildcard: ResolvePattern hands the whole string to
	// Resolve rather than resolving a prefix. Both spellings of the same file
	// must agree, which is the disagreement R3359 exists to prevent.
	exact := search(filepath.Join(link, "deep", "note.md"))
	if len(exact) != 1 || exact[0].Path != note {
		t.Errorf("alias-spelled exact path found %d results (%v), want exactly %q",
			len(exact), exact, note)
	}
}

// Both scan paths must persist the links they walked past. ScanAsync is the
// one the *server* takes (doReconcile, runRebuildScan) while Scan is the CLI's,
// and for as long as both existed only Scan recorded them — so every startup
// and every reconcile met every symlink and persisted none, silently. The
// symptom was a cold CLI resolving aliases differently from a running server,
// which is exactly the disagreement R3359 exists to prevent (gap O208).
//
// Asserting the two paths AGREE rather than checking one in isolation: the
// defect was a divergence, and a test pinned to one path is what let it live.
//
// Fire alarm: delete the recordScannedLinks call in ScanAsync and this must go
// red. The property is invisible when absent — nothing fails, the scan reports
// success, and the links are simply not there.
// CRC: crc-DB.md | R3383
func TestBothScanPathsRecordLinks(t *testing.T) {
	build := func(t *testing.T) (*DB, string) {
		t.Helper()
		root := linkTempRoot(t)
		s1, s2 := filepath.Join(root, "s1"), filepath.Join(root, "s2")
		mkDirs(t, s1, s2)
		link := filepath.Join(s2, "link")
		mkSymlink(t, s1, link)

		dbPath := filepath.Join(t.TempDir(), "db")
		mkDirs(t, dbPath)
		fts, err := microfts2.Create(IndexPath(dbPath), microfts2.Options{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { fts.Close() })
		store, err := OpenStore(fts.DB())
		if err != nil {
			t.Fatal(err)
		}
		cfg := &Config{Sources: []Source{{Dir: s1}, {Dir: s2}}}
		return &DB{
			fts:     fts,
			store:   store,
			config:  cfg,
			dbPath:  dbPath,
			scanner: NewScanner(cfg, &Matcher{}, fts, NewEmptyFiles()),
		}, link
	}

	recorded := func(t *testing.T, db *DB, link string) bool {
		t.Helper()
		paths, err := db.store.ListLinks()
		if err != nil {
			t.Fatalf("ListLinks: %v", err)
		}
		for _, p := range paths {
			if p == link {
				return true
			}
		}
		return false
	}

	syncDB, syncLink := build(t)
	if _, err := syncDB.Scan(); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	// Control: if the synchronous path does not record, the fixture never
	// tracked a link and the async assertion below would be vacuous.
	if !recorded(t, syncDB, syncLink) {
		t.Fatalf("control: Scan recorded no L record for %q — fixture problem, not a path problem", syncLink)
	}

	asyncDB, asyncLink := build(t)
	if _, err := asyncDB.ScanAsync(); err != nil {
		t.Fatalf("ScanAsync: %v", err)
	}
	if !recorded(t, asyncDB, asyncLink) {
		t.Errorf("ScanAsync recorded no L record for %q, while Scan recorded its equivalent — "+
			"the server's scan path drops every link it walks past", asyncLink)
	}
}

// linkViewRig builds a table over two sources with one tracked link, and
// returns the table, the canonical file set, and the paths under test.
func linkViewRig(t *testing.T) (lt *LinkTable, canonical []string, s1, s2, link, canonFile string) {
	t.Helper()
	root := linkTempRoot(t)
	s1, s2 = filepath.Join(root, "s1"), filepath.Join(root, "s2")
	mkDirs(t, s1, s2)
	canonFile = filepath.Join(s1, "a.md")
	mkFile(t, canonFile)
	link = filepath.Join(s2, "link")
	mkSymlink(t, s1, link)

	cfg := &Config{Sources: []Source{{Dir: s1}, {Dir: s2}}}
	lt = NewLinkTable(cfg)
	if _, st := lt.Observe(link); st != LinkTracked {
		t.Fatalf("fixture: link status = %v, want tracked — every assertion below would be vacuous", st)
	}
	return lt, []string{canonFile}, s1, s2, link, canonFile
}

// The address space is listable, links included, and --nolinks omits both the
// link and the addresses beneath it. R3380, R3375.
// CRC: crc-LinkTable.md | R3375, R3380
func TestAddressSpaceListableLinksIncluded(t *testing.T) {
	lt, canonical, _, _, link, canonFile := linkViewRig(t)
	alias := filepath.Join(link, "a.md")

	def := lt.AddressSpaceFor(canonical, LinkViewDefault)
	for _, want := range []string{canonFile, alias, link} {
		if !hasAddr(def, want) {
			t.Errorf("default view is missing %q; got %v", want, def)
		}
	}

	// R3375: canonical-only, as if no symlink had been indexed. The link itself
	// must go too — listing a link while omitting what it addresses, or the
	// reverse, would describe an address space whose structure is invisible.
	only := lt.AddressSpaceFor(canonical, LinkViewCanonical)
	if len(only) != 1 || only[0] != canonFile {
		t.Errorf("--nolinks view = %v, want exactly [%q]", only, canonFile)
	}
}

// Creating a symlink does not change the canonical file set — R3375's whole
// reason for existing, since a script counting files needs a stable answer
// while a human enumerating addresses wants the growing one.
// CRC: crc-LinkTable.md | R3375
func TestNolinksIsStableAcrossNewLinks(t *testing.T) {
	lt, canonical, s1, s2, _, _ := linkViewRig(t)

	before := lt.AddressSpaceFor(canonical, LinkViewCanonical)
	defBefore := lt.AddressSpaceFor(canonical, LinkViewDefault)

	second := filepath.Join(s2, "link2")
	mkSymlink(t, s1, second)
	if _, st := lt.Observe(second); st != LinkTracked {
		t.Fatalf("fixture: second link status = %v, want tracked", st)
	}

	after := lt.AddressSpaceFor(canonical, LinkViewCanonical)
	if len(after) != len(before) {
		t.Errorf("--nolinks grew from %d to %d when a link was created: %v", len(before), len(after), after)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("--nolinks entry %d changed: %q -> %q", i, before[i], after[i])
		}
	}

	// Control: the default view MUST grow, or the stability above proves
	// nothing — a table that saw no new link would hold both views still.
	defAfter := lt.AddressSpaceFor(canonical, LinkViewDefault)
	if len(defAfter) <= len(defBefore) {
		t.Fatalf("control: default view did not grow (%d -> %d); the new link was never tracked", len(defBefore), len(defAfter))
	}
}

// --links expands each matched result to every address of that file (R3378).
// The default matches addresses; this is the inverse question, asked after
// matching, which is why naming one address answers with all of them.
// CRC: crc-LinkTable.md | R3378
func TestExpandMatchesGivesEveryAddress(t *testing.T) {
	lt, _, _, _, link, canonFile := linkViewRig(t)
	alias := filepath.Join(link, "a.md")

	got := lt.ExpandMatches([]string{canonFile})
	if !hasAddr(got, canonFile) || !hasAddr(got, alias) {
		t.Errorf("naming the canonical address expanded to %v, want both %q and %q", got, canonFile, alias)
	}
	// Naming the alias must expand to the same set, not a different one.
	viaAlias := lt.ExpandMatches([]string{alias})
	if len(viaAlias) != len(got) {
		t.Errorf("expanding from the alias gave %v, from the canonical %v — one file, one address set", viaAlias, got)
	}
}
