package ark

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zot/microfts2"
)

// extLinkTestDB builds a DB whose scanner — and therefore whose LinkTable —
// is the real one, with a file indexed at its canonical path and a tracked
// directory symlink giving that file a second, equally legal address.
//
// newTestDB deliberately omits the scanner, so an @ext-through-a-link test
// written against it would take the nil-scanner branch of resolvePath and
// pass without ever touching a link. That is precisely the shape that let a
// hand-wired withFTS drop extmap and db — killing every @ext routing for
// three months while the suite stayed green — so the collaborator under test
// is wired here on purpose rather than omitted by default.
//
// Returns the DB, the file's canonical path, and its alias through the link.
func extLinkTestDB(t *testing.T) (db *DB, canonical, alias string) {
	t.Helper()
	root := linkTempRoot(t)
	s1, s2 := filepath.Join(root, "s1"), filepath.Join(root, "s2")
	mkDirs(t, s1, s2)
	link := filepath.Join(s2, "link")
	mkSymlink(t, s1, link)

	idx, dir := testIndexer(t)
	if err := idx.fts.AddChunker("markdown", microfts2.MarkdownChunker{}); err != nil {
		t.Fatal(err)
	}
	canonical = writeFile(t, s1, "target.md", "Preamble.\n\n## Heading\n\nbody.\n")
	if _, err := idx.AddFile(canonical, "markdown"); err != nil {
		t.Fatal(err)
	}
	idx.store.LoadTvidMap()

	cfg := &Config{
		Sources:        []Source{{Dir: s1}, {Dir: s2}},
		DefaultInclude: []string{"*.md"},
	}
	sc := NewScanner(cfg, &Matcher{}, idx.fts, NewEmptyFiles())
	if _, err := sc.Scan(); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if sc.Links().Len() == 0 {
		t.Fatalf("fixture: the scan tracked no link, so every assertion below would be vacuous")
	}

	db = newTestDB(idx, dir)
	db.scanner = sc
	return db, canonical, filepath.Join(link, "target.md")
}

// An @ext TARGET naming a path through a tracked link resolves to the same
// chunks as the canonical spelling. R3367's originating defect: the TARGET is
// stored as its author wrote it, so a path spelled through a link names a file
// the index knows only canonically and the routing simply never fires.
//
// Fire alarm (test-ExtAssumptions.md): delete the resolve in
// resolveExtFilePath and this must go red. The property is invisible when
// absent — a routing that never fires looks exactly like one nobody wrote —
// which is why it gets an alarm rather than trust.
// CRC: crc-ExtMap.md | Seq: seq-link-resolve.md#2 | R3367
func TestExtTargetThroughSymlinkResolves(t *testing.T) {
	db, canonical, alias := extLinkTestDB(t)

	// Control first: without it, a fixture that never indexed would answer
	// zero for both spellings and this test would pass having proved nothing.
	want := db.ResolveExtTarget(canonical, "")
	if len(want) == 0 {
		t.Fatalf("control: canonical target %q resolved to no chunks — the fixture never indexed", canonical)
	}

	got := db.ResolveExtTarget(alias, "")
	if len(got) != len(want) {
		t.Fatalf("alias target %q: got %d chunks, want %d — same file, second address", alias, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("alias chunk %d: got %d, want %d", i, got[i], want[i])
		}
	}
}

// The reverse direction, and the half with no natural forcing function: when
// the *file* changes, a routing keyed under the author's alias spelling must
// still be found. Asking only the canonical name misses it entirely, and the
// miss is silent — the routing keeps whatever it last resolved to.
//
// Fire alarm (test-ExtAssumptions.md): drop the alias-address arm in
// candidatesForFileChange — reverting it to extByAnchor[path] — and this must
// go red.
// CRC: crc-ExtMap.md | Seq: seq-link-resolve.md#2 | R3367
func TestFileChangeFindsRoutingKeyedUnderAlias(t *testing.T) {
	db, canonical, alias := extLinkTestDB(t)

	status, err := db.fts.CheckFile(canonical)
	if err != nil || status.FileID == 0 {
		t.Fatalf("fixture: canonical %q has no fileid (err=%v) — nothing below would mean anything", canonical, err)
	}

	m := NewExtMap()
	const aliasTvid, canonicalTvid = 4242, 4243
	m.extByAnchor[alias] = []uint64{aliasTvid}
	m.extByAnchor[canonical] = []uint64{canonicalTvid}

	got := m.candidatesForFileChange(db, status.FileID, nil, nil)

	// Control: the canonical arm has always worked, so if it is missing here
	// the failure is in the fixture rather than in the alias expansion.
	if !got[canonicalTvid] {
		t.Fatalf("control: routing keyed under the canonical path was not found — fixture problem, not an alias problem")
	}
	if !got[aliasTvid] {
		t.Errorf("routing keyed under alias %q was not found when its file changed; candidates=%v", alias, got)
	}
}

// A premise is recorded only for a routing whose TARGET crosses a tracked
// link, and it carries the tier of the *chain* rather than of any one entry.
// R3372 is what bounds how much gets tracked at all.
// CRC: crc-ExtMap.md | R3382, R3384
func TestPremiseRecordedOnlyForLinkedTargets(t *testing.T) {
	db, canonical, alias := extLinkTestDB(t)
	lt := db.LinkTable()
	m := NewExtMap()

	_, aliasHops := db.resolvePathVia(alias)
	if len(aliasHops) == 0 {
		t.Fatalf("fixture: alias %q crossed no tracked link — every assertion below would be vacuous", alias)
	}
	_, canonHops := db.resolvePathVia(canonical)
	if len(canonHops) != 0 {
		t.Fatalf("fixture: canonical %q reported a chain (%v); it should cross nothing", canonical, canonHops)
	}

	m.recordLinkAssumption(lt, 1, alias, 77, aliasHops)
	m.recordLinkAssumption(lt, 2, canonical, 88, canonHops)

	a, ok := m.LinkAssumptionFor(1)
	if !ok {
		t.Fatalf("linked routing got no premise")
	}
	if a.FileID != 77 || a.Target != alias {
		t.Errorf("premise = {%q, %d}, want {%q, 77}", a.Target, a.FileID, alias)
	}
	if a.Tier != TierSingleHop {
		t.Errorf("tier = %v, want %v — one link, its own node inside a source", a.Tier, TierSingleHop)
	}
	if _, ok := m.LinkAssumptionFor(2); ok {
		t.Errorf("unlinked routing got a premise; nothing needs verifying and it must not carry a schedule")
	}
}

// A routing that stops crossing a link loses its premise. Keeping the record
// would keep a verification promise for a shape that no longer exists —
// R3385's silent-wrong-direction failure by way of a stale record.
// CRC: crc-ExtMap.md | R3382, R3385
func TestPremiseClearedWhenChainDisappears(t *testing.T) {
	db, _, alias := extLinkTestDB(t)
	lt := db.LinkTable()
	m := NewExtMap()

	_, hops := db.resolvePathVia(alias)
	m.recordLinkAssumption(lt, 1, alias, 77, hops)
	if _, ok := m.LinkAssumptionFor(1); !ok {
		t.Fatalf("control: premise was never recorded, so its removal proves nothing")
	}

	m.recordLinkAssumption(lt, 1, alias, 77, nil)
	if a, ok := m.LinkAssumptionFor(1); ok {
		t.Errorf("premise survived the chain disappearing: %+v", a)
	}
}

// Only single-hop premises spelled through the changed link are flagged, and
// the prefix test respects component boundaries — /a/bc is not under /a/b.
// CRC: crc-ExtMap.md | R3384
func TestMarkLinkChangedIsScopedAndComponentWise(t *testing.T) {
	// Native paths, as resolution records them: PathUnder splits components on
	// the platform separator.
	p := filepath.FromSlash
	m := NewExtMap()
	m.linkAssumptions[1] = LinkAssumption{Target: p("/s/link/target.md"), Tier: TierSingleHop}
	m.linkAssumptions[2] = LinkAssumption{Target: p("/s/linkage/target.md"), Tier: TierSingleHop}
	m.linkAssumptions[3] = LinkAssumption{Target: p("/s/other/target.md"), Tier: TierSingleHop}
	m.linkAssumptions[4] = LinkAssumption{Target: p("/s/link/deep.md"), Tier: TierInternalMultiHop}

	flagged := m.MarkLinkChanged(p("/s/link"))

	got := map[uint64]bool{}
	for _, tv := range flagged {
		got[tv] = true
	}
	if !got[1] {
		t.Errorf("the single-hop routing under the changed link was not flagged")
	}
	if got[2] {
		t.Errorf("/s/linkage was flagged by a change to /s/link — the prefix test is string-wise, not component-wise")
	}
	if got[3] {
		t.Errorf("an unrelated routing was flagged")
	}
	if got[4] {
		t.Errorf("a multi-hop routing was flagged; this door is single-hop only, its later hops never appear in the written target")
	}
}

// Any file change dirties every internal-multi-hop premise and nothing else.
// The imprecision is the tier's bargain; the other tiers must not be touched.
// CRC: crc-ExtMap.md | R3384
func TestInternalMultiHopDirtiedByAnyFileChange(t *testing.T) {
	m := NewExtMap()
	m.linkAssumptions[1] = LinkAssumption{Target: "/a", Tier: TierSingleHop}
	m.linkAssumptions[2] = LinkAssumption{Target: "/b", Tier: TierInternalMultiHop}
	m.linkAssumptions[3] = LinkAssumption{Target: "/c", Tier: TierExternalMultiHop}

	m.MarkInternalMultiHopDirty()

	if a, _ := m.LinkAssumptionFor(2); !a.Dirty {
		t.Errorf("internal multi-hop was not dirtied by a file change")
	}
	if a, _ := m.LinkAssumptionFor(1); a.Dirty {
		t.Errorf("single-hop was dirtied by a file change; its schedule is its own event, not any change")
	}
	if a, _ := m.LinkAssumptionFor(3); a.Dirty {
		t.Errorf("external multi-hop was dirtied; it caches nothing, so it has nothing to invalidate")
	}
}

// tierTestRig lays out three sources so a link's *shape* can change under a
// recorded premise: s2/link starts as one hop to s1 and can be repointed
// through s3/mid, which makes the same address a two-hop chain.
func tierTestRig(t *testing.T) (db *DB, m *ExtMap, link, s1, s3, target string) {
	t.Helper()
	root := linkTempRoot(t)
	s1, s2, s3 := filepath.Join(root, "s1"), filepath.Join(root, "s2"), filepath.Join(root, "s3")
	mkDirs(t, s1, s2, s3)
	link = filepath.Join(s2, "link")
	mkSymlink(t, s1, link)

	idx, dir := testIndexer(t)
	if err := idx.fts.AddChunker("markdown", microfts2.MarkdownChunker{}); err != nil {
		t.Fatal(err)
	}
	canonical := writeFile(t, s1, "target.md", "Preamble.\n\n## Heading\n\nbody.\n")
	if _, err := idx.AddFile(canonical, "markdown"); err != nil {
		t.Fatal(err)
	}
	idx.store.LoadTvidMap()

	cfg := &Config{
		Sources:        []Source{{Dir: s1}, {Dir: s2}, {Dir: s3}},
		DefaultInclude: []string{"*.md"},
	}
	sc := NewScanner(cfg, &Matcher{}, idx.fts, NewEmptyFiles())
	if _, err := sc.Scan(); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	db = newTestDB(idx, dir)
	db.scanner = sc
	return db, NewExtMap(), link, s1, s3, filepath.Join(link, "target.md")
}

// A tier is derived state and is re-derived on every recomputation, never
// carried forward (R3385). A single-hop link becomes multi-hop the moment an
// intermediate appears; keeping the old tier leaves the association on a cheap
// schedule for a shape it no longer has, so it stops being verified for the
// hop that now exists beyond it — trustworthy-looking while its premise goes
// unchecked. The one failure in this scheme that is silent in the wrong
// direction.
//
// Fire alarm (test-ExtAssumptions.md): carry p.a.Tier forward in
// verifyLinkAssumptions instead of re-deriving it, and this must go red.
// CRC: crc-ExtMap.md | Seq: seq-ext-tag-retrieval.md#1.3 | R3385
func TestTierIsRederivedNotCarriedForward(t *testing.T) {
	db, m, link, s1, s3, target := tierTestRig(t)

	_, hops := db.resolvePathVia(target)
	if len(hops) == 0 {
		t.Fatalf("fixture: %q crossed no tracked link", target)
	}
	status, _ := db.fts.CheckFile(db.resolvePath(target))
	m.recordLinkAssumption(db.LinkTable(), 1, target, status.FileID, hops)
	if a, _ := m.LinkAssumptionFor(1); a.Tier != TierSingleHop {
		t.Fatalf("control: premise must start single-hop for the change to mean anything, got %v", a.Tier)
	}

	// Grow the chain: link -> s3/mid -> s1. The address is unchanged; only its
	// shape is.
	mid := filepath.Join(s3, "mid")
	mkSymlink(t, s1, mid)
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	mkSymlink(t, mid, link)
	db.scanner.Links().SyncPath(mid)
	db.scanner.Links().SyncPath(link)

	if _, grown := db.resolvePathVia(target); len(grown) < 2 {
		t.Fatalf("control: the chain did not actually grow (hops=%v); the assertion below would be vacuous", grown)
	}

	m.MarkLinkChanged(link)
	m.verifyLinkAssumptions(db, []uint64{1})

	a, ok := m.LinkAssumptionFor(1)
	if !ok {
		t.Fatalf("premise vanished across recomputation")
	}
	if a.Tier == TierSingleHop {
		t.Errorf("tier is still %v after the chain gained a hop — carried forward rather than re-derived", a.Tier)
	}
}

// An association that cannot be computed is unresolved, never
// last-known-good (R3386). Slowness is bounded and visible; wrongness is
// neither, so an absence someone can see beats a tag routed to whatever it
// used to mean.
//
// Fire alarm (test-ExtAssumptions.md): keep the recorded fileid when CheckFile
// fails, instead of recording zero, and this must go red. Without the alarm
// the test passes whether the code fails closed or open, because the recorded
// value is the correct answer right up until the moment it isn't.
// CRC: crc-ExtMap.md | Seq: seq-ext-tag-retrieval.md#1.4.3 | R3386
func TestUnresolvableTargetIsUnresolvedNotLastKnownGood(t *testing.T) {
	db, m, link, _, s3, target := tierTestRig(t)

	status, _ := db.fts.CheckFile(db.resolvePath(target))
	if status.FileID == 0 {
		t.Fatalf("control: the target has no fileid to begin with, so losing it would prove nothing")
	}
	// External tier so verification always runs — this test is about the
	// fail-closed branch, not about scheduling.
	m.linkAssumptions[1] = LinkAssumption{Target: target, FileID: status.FileID, Tier: TierExternalMultiHop}

	// Repoint at a real, indexed-source directory holding no such file. The
	// chain survives; only the resolution dies.
	empty := filepath.Join(s3, "empty")
	mkDirs(t, empty)
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	mkSymlink(t, empty, link)
	db.scanner.Links().SyncPath(link)

	changed := m.verifyLinkAssumptions(db, []uint64{1})

	a, ok := m.LinkAssumptionFor(1)
	if !ok {
		t.Fatalf("premise vanished; it should survive as unresolved, since the chain is intact")
	}
	if a.FileID == status.FileID {
		t.Errorf("premise kept its last-known-good fileid %d after becoming unresolvable", a.FileID)
	}
	if a.FileID != 0 {
		t.Errorf("unresolvable premise recorded fileid %d, want 0", a.FileID)
	}
	if len(changed) != 1 || changed[0].Now != 0 || changed[0].Was != status.FileID {
		t.Errorf("change report = %+v, want one entry moving %d -> 0", changed, status.FileID)
	}
}
