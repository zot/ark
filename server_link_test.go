package ark

// CRC: crc-Server.md | Test: test-Server.md

import (
	"strings"
	"testing"

	"github.com/zot/microfts2"
)

// linkTestDB indexes one markdown file with @id values declared in the
// preamble and under a heading, then returns a DB sufficient for
// ResolveLink and wrapTagElements. Uses newTestDB so the literal
// stays in one place — adding required DB fields touches only that
// helper, not every callsite.
func linkTestDB(t *testing.T, fileName, content string) (*DB, string) {
	t.Helper()
	idx, dir := testIndexer(t)
	if err := idx.fts.AddChunker("markdown", microfts2.MarkdownChunker{}); err != nil {
		t.Fatal(err)
	}
	fp := writeFile(t, dir, fileName, content)
	if _, err := idx.AddFile(fp, "markdown"); err != nil {
		t.Fatal(err)
	}
	idx.store.LoadTvidMap()
	return newTestDB(idx, dir), fp
}

// newTestDB returns a DB scoped to an existing test indexer + temp
// dir. Centralizing the literal lets new required fields be added
// here once.
func newTestDB(idx *Indexer, dir string) *DB {
	return &DB{
		fts:      idx.fts,
		store:    idx.store,
		indexer:  idx,
		dbPath:   dir,
		tmpPaths: make(map[string]uint64),
	}
}

// TestResolveLinkUUID verifies UUID lookup → (path, location).
// CRC: crc-DB.md | R1976 | R1977
func TestResolveLinkUUID(t *testing.T) {
	const preID = "doc-preamble-7771"
	const secID = "doc-section-8842"
	body := "@id: " + preID + "\n\nPreamble.\n\n" +
		"## Section A\n\n@id: " + secID + "\n\nSection A body.\n"
	db, fp := linkTestDB(t, "doc.md", body)

	prePath, preLoc, ok := db.ResolveLink(preID)
	if !ok {
		t.Fatalf("preamble UUID: not resolved")
	}
	if prePath != fp {
		t.Errorf("preamble path: want %q got %q", fp, prePath)
	}
	if preLoc == "" {
		t.Errorf("preamble location: should be non-empty")
	}

	secPath, secLoc, ok := db.ResolveLink(secID)
	if !ok {
		t.Fatalf("section UUID: not resolved")
	}
	if secPath != fp {
		t.Errorf("section path: want %q got %q", fp, secPath)
	}
	if secLoc == preLoc {
		t.Errorf("section/preamble locations should differ (got %q == %q)", secLoc, preLoc)
	}
}

// TestResolveLinkPath verifies path-branch fallback for unrecognized
// values that name an indexed file. CRC: crc-DB.md | R1978
func TestResolveLinkPath(t *testing.T) {
	db, fp := linkTestDB(t, "plain.md", "no ids here\n")
	path, loc, ok := db.ResolveLink(fp)
	if !ok {
		t.Fatalf("path resolution: not ok")
	}
	if path != fp {
		t.Errorf("path: want %q got %q", fp, path)
	}
	if loc != "" {
		t.Errorf("path-branch location should be empty, got %q", loc)
	}
}

// TestResolveLinkUnknown verifies failure path for non-UUID, non-path
// values. CRC: crc-DB.md | R1976
func TestResolveLinkUnknown(t *testing.T) {
	db, _ := linkTestDB(t, "x.md", "@id: known-id\n")
	if _, _, ok := db.ResolveLink("unknown-uuid-or-path"); ok {
		t.Errorf("unknown value should not resolve")
	}
}

// TestWrapTagElementsLinkResolved confirms a resolved @link emits an
// <a> tag that points at /content/PATH?range=LOC.
// CRC: crc-Server.md | R1980
func TestWrapTagElementsLinkResolved(t *testing.T) {
	const id = "wrap-id-91a2"
	body := "## Heading\n\n@id: " + id + "\n\nbody.\n"
	db, fp := linkTestDB(t, "wrap.md", body)

	out := wrapTagElements("@link: "+id+"\n", db)
	if !strings.Contains(out, `<a class="ark-link" href="/content`+fp) {
		t.Errorf("resolved link missing /content href: %q", out)
	}
	if !strings.Contains(out, "range=") {
		t.Errorf("resolved link should carry ?range= for chunk-scoped target: %q", out)
	}
	if strings.Contains(out, "ark-tag") {
		t.Errorf("resolved link should not fall through to <ark-tag>: %q", out)
	}
}

// TestWrapTagElementsLinkBroken confirms unresolved @link gets the
// broken-link <ark-tag> wrapper.
// CRC: crc-Server.md | R1981
func TestWrapTagElementsLinkBroken(t *testing.T) {
	db, _ := linkTestDB(t, "x.md", "no ids\n")
	out := wrapTagElements("@link: definitely-not-an-id\n", db)
	if !strings.Contains(out, `class="ark-link-broken"`) {
		t.Errorf("broken link should carry ark-link-broken class: %q", out)
	}
	if strings.Contains(out, "<a ") {
		t.Errorf("broken link must not emit <a>: %q", out)
	}
}

// TestWrapTagElementsLinkNilDB confirms a nil DB short-circuits to the
// broken renderer (test paths that bypass the server).
// CRC: crc-Server.md | R1979
func TestWrapTagElementsLinkNilDB(t *testing.T) {
	out := wrapTagElements("@link: anything\n", nil)
	if !strings.Contains(out, "ark-link-broken") {
		t.Errorf("nil db should render broken: %q", out)
	}
}

// TestWrapTagElementsNonLinkUnchanged confirms tag wrapping for
// non-@link tags is unchanged by the new code path.
// CRC: crc-Server.md | R1485-R1489
func TestWrapTagElementsNonLinkUnchanged(t *testing.T) {
	out := wrapTagElements("@status: open\n", nil)
	if !strings.Contains(out, "<ark-tag><name>status</name>") {
		t.Errorf("non-link tag should still render <ark-tag>: %q", out)
	}
	if strings.Contains(out, "ark-link") {
		t.Errorf("non-link tag should not carry ark-link class: %q", out)
	}
}
