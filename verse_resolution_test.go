package ark

// CRC: crc-DB.md, crc-BibleChunker.md | Test: test-VerseResolution.md | R3179, R3180, R3216, R3220

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParseChapterVerse — test-VerseResolution.md "CHAPTER.VERSE parses, other
// anchor shapes don't". R3179.
func TestParseChapterVerse(t *testing.T) {
	if c, v, ok := parseChapterVerse("12.1"); !ok || c != 12 || v != 1 {
		t.Errorf(`parseChapterVerse("12.1") = (%d, %d, %v), want (12, 1, true)`, c, v, ok)
	}
	for _, s := range []string{"3-6", "12", "12.", ".1", "0.1", "12.0", "a.b", "12.1.3", ""} {
		if _, _, ok := parseChapterVerse(s); ok {
			t.Errorf("parseChapterVerse(%q) parsed as a verse reference; want not", s)
		}
	}
}

// TestVerseSpanContains — test-VerseResolution.md "verse span containment".
// R3176.
func TestVerseSpanContains(t *testing.T) {
	cases := []struct {
		span string
		v    int
		want bool
	}{
		{"1-2", 1, true}, {"1-2", 2, true}, {"1-2", 3, false},
		{"3", 3, true}, {"3", 4, false},
		{"", 1, false}, {"x", 1, false}, // malformed covers nothing
	}
	for _, c := range cases {
		if got := verseSpanContains(c.span, c.v); got != c.want {
			t.Errorf("verseSpanContains(%q, %d) = %v, want %v", c.span, c.v, got, c.want)
		}
	}
}

// TestBibleVirtualTarget — R3216: `<source>/BIBLE/<Book>` is recognized as an
// address rather than looked for on disk, and one reserved segment names
// exactly one book.
func TestBibleVirtualTarget(t *testing.T) {
	source, book, ok := bibleVirtualTarget("/work/esv/BIBLE/1 Samuel")
	if !ok || source != "/work/esv" || book != "1 Samuel" {
		t.Errorf(`got (%q, %q, %v), want ("/work/esv", "1 Samuel", true)`, source, book, ok)
	}
	for _, p := range []string{
		"/work/esv/OEBPS/Text/b43.00.John.text.xhtml", // a real file
		"/work/esv/BIBLE/",                            // no book named
		"/work/esv/BIBLE/John/3",                      // BIBLE names a book, not a subtree
		"BIBLE/John",                                  // no source
	} {
		if _, _, ok := bibleVirtualTarget(p); ok {
			t.Errorf("%q was read as a virtual book address; want not", p)
		}
	}
}

// setupBibleFile indexes the two-chapter fixture with the bible strategy and
// returns the db plus the file's path and its chunk IDs in order.
func setupBibleFile(t *testing.T) (*DB, string, []uint64) {
	t.Helper()
	db := setupBibleDB(t)

	path := filepath.Join(db.dbPath, "b38.00.Zechariah.text.xhtml")
	if err := os.WriteFile(path, []byte(bibleFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	fileid, err := db.indexer.AddFile(path, bibleStrategy)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	info, err := db.fts.FileInfoByID(fileid)
	if err != nil {
		t.Fatalf("FileInfoByID: %v", err)
	}
	ids := make([]uint64, 0, len(info.Chunks))
	for _, c := range info.Chunks {
		ids = append(ids, c.ChunkID)
	}
	return db, path, ids
}

// resolveTarget parses an @ext TARGET and resolves it, the way an indexed
// `@ext:` line does.
func resolveTarget(t *testing.T, db *DB, target string) []uint64 {
	t.Helper()
	parts, ok := ParseExtTargetParts(target, "")
	if !ok {
		t.Fatalf("ParseExtTargetParts(%q) failed", target)
	}
	return db.resolveExtPathBase(parts)
}

// TestVerseResolvesToItsParagraph — test-VerseResolution.md "a verse resolves
// to its paragraph". R3179.
func TestVerseResolvesToItsParagraph(t *testing.T) {
	db, path, ids := setupBibleFile(t)
	if len(ids) != 4 {
		t.Fatalf("fixture indexed %d chunks, want 4", len(ids))
	}

	cases := []struct {
		ref  string
		want uint64
		why  string
	}{
		{"2.1", ids[1], "chapter 2 verse 1 — the block spanning verses 1-2"},
		{"2.2", ids[1], "verse 2 shares its paragraph with verse 1"},
		{"2.3", ids[2], "verse 3 is the following paragraph"},
		{"3.1", ids[3], "chapter 3 opens a new block"},
	}
	for _, c := range cases {
		got := resolveTarget(t, db, path+":"+c.ref)
		if len(got) != 1 {
			t.Errorf("%s → %d chunks, want exactly 1 (%s)", c.ref, len(got), c.why)
			continue
		}
		if got[0] != c.want {
			t.Errorf("%s → chunk %d, want %d (%s)", c.ref, got[0], c.want, c.why)
		}
	}
}

// TestVirtualBookAddressResolves — test-VerseResolution.md "the virtual
// BIBLE/<Book> address resolves through the book index", and "the address book
// name is the normalized form". R3214, R3215, R3216, R3220.
func TestVirtualBookAddressResolves(t *testing.T) {
	db := setupBibleDB(t)
	// The hook is what classifies the virtual namespace as bible; without it
	// the address would never reach the bible resolver.
	if err := db.activateSourceChunkers(db.config); err != nil {
		t.Fatalf("activateSourceChunkers: %v", err)
	}

	path := filepath.Join(db.dbPath, "b09.00.1-Samuel.text.xhtml")
	if err := os.WriteFile(path, []byte(bibleFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.indexer.AddFile(path, bibleStrategy); err != nil {
		t.Fatalf("index: %v", err)
	}

	virtual := filepath.Join(db.dbPath, "BIBLE", "1 Samuel")
	got := resolveTarget(t, db, virtual+":2.2")
	real := resolveTarget(t, db, path+":2.2")
	if len(real) != 1 {
		t.Fatalf("the real path resolved to %v; the virtual comparison needs it", real)
	}
	if len(got) != 1 || got[0] != real[0] {
		t.Errorf("%s:2.2 → %v, want the same chunk the real path gives (%v)", virtual, got, real)
	}

	// The address carries the spaced name the chunker wrote, not the epub's
	// hyphenated filename token (R3215).
	raw := filepath.Join(db.dbPath, "BIBLE", "1-Samuel")
	if got := resolveTarget(t, db, raw+":2.2"); len(got) != 0 {
		t.Errorf("the hyphenated token resolved to %v; the address uses the spaced name", got)
	}
	// A book with no records resolves to nothing rather than erroring (R3180).
	missing := filepath.Join(db.dbPath, "BIBLE", "Obadiah")
	if got := resolveTarget(t, db, missing+":1.1"); len(got) != 0 {
		t.Errorf("an unknown book resolved to %v, want nothing", got)
	}
}

// TestVerseNotFoundResolvesToNothing — test-VerseResolution.md "a nonexistent
// chapter or verse resolves to nothing". R3180.
func TestVerseNotFoundResolvesToNothing(t *testing.T) {
	db, path, ids := setupBibleFile(t)

	for _, ref := range []string{"9.1", "2.99"} {
		got := resolveTarget(t, db, path+":"+ref)
		if len(got) != 0 {
			t.Errorf("%s → %v, want nothing", ref, got)
		}
		if len(got) == 1 && got[0] == ids[0] {
			t.Errorf("%s fell back to the file's first chunk; a missed verse must annotate nothing", ref)
		}
	}
}

// TestBareBibleTargetStillResolves — a bible target that names no verse is
// ordinary path resolution: the bible dispatch owns the path, so it must not
// swallow the cases it does not decode. R2376, R3220.
func TestBareBibleTargetStillResolves(t *testing.T) {
	db, path, ids := setupBibleFile(t)

	got := resolveTarget(t, db, path)
	if len(got) != 1 || got[0] != ids[0] {
		t.Errorf("bare bible path → %v, want the first chunk [%d] (the preamble convention)", got, ids[0])
	}
	if got := resolveTarget(t, db, path+`:"Third verse"`); len(got) != 1 || got[0] != ids[2] {
		t.Errorf("quoted-text anchor on a bible file → %v, want [%d]", got, ids[2])
	}
}

// TestRangeAnchorOnNonBibleFileUnaffected — test-VerseResolution.md "a range
// anchor on a non-bible file is unaffected": ordinary @ext range routings still
// resolve by exact chunk location after the strategy dispatch landed.
//
// This does NOT guard the dispatch — deleting it leaves this passing, because a
// non-bible file has no `chapter` attribute either way. Measured, not assumed;
// see the test design for what the dispatch actually buys. R2377, R3179, R3220.
func TestRangeAnchorOnNonBibleFileUnaffected(t *testing.T) {
	_, db := setupRecall(t)
	db.config.Sources = []Source{{Dir: db.dbPath}}

	path := filepath.Join(db.dbPath, "notes.md")
	if err := os.WriteFile(path, []byte("alpha\nbravo\ncharlie\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fileid, err := db.indexer.AddFile(path, "line")
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	info, err := db.fts.FileInfoByID(fileid)
	if err != nil || len(info.Chunks) == 0 {
		t.Fatalf("FileInfoByID: %v", err)
	}

	// A real location still resolves by exact match.
	loc := info.Chunks[0].Location
	got := resolveTarget(t, db, path+":"+loc)
	if len(got) != 1 || got[0] != info.Chunks[0].ChunkID {
		t.Errorf("location anchor %q → %v, want [%d]", loc, got, info.Chunks[0].ChunkID)
	}

	// A dotted anchor is not a verse here — the file is not bible-strategy, so
	// it is just a location that matches nothing.
	if got := resolveTarget(t, db, path+":1.1"); len(got) != 0 {
		t.Errorf("dotted anchor on a non-bible file → %v, want nothing", got)
	}
}
