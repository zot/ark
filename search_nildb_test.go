package ark

import (
	"testing"

	"github.com/zot/microfts2"
)

// Sleeping Sentry for the overlay nil-DB crash: an overlay (tmp://) CRecord is
// never attach'd to a DB, so its db field is nil. A -fuzzy chunk filter over
// such chunks reaches resolveChunkLocation -> CRecord.FileRecord ->
// (*DB).readFRecord on a nil *DB, which panics the search actor and crashes the
// server. resolveChunkLocation must treat a DB-less CRecord as unresolved
// (folding into the R1401 "can't verify -> keep" degradation), not deref it.
//
// Before the fix this test panics; after, it returns ok=false.
//
// CRC: crc-Searcher.md | Test: test-Searcher.md "resolveChunkLocation guards a DB-less (overlay) record" | R2959
func TestResolveChunkLocationNilDB(t *testing.T) {
	crec := microfts2.CRecord{
		ChunkID: 1,
		FileIDs: []microfts2.FileIDCount{{FileID: 42}}, // resolves to a path...
		// ...but db is nil (unattached overlay record).
	}
	paths := map[uint64]string{42: "/tmp/overlay-doc.md"}

	if _, _, ok := resolveChunkLocation(crec, paths); ok {
		t.Fatalf("expected unresolved for a DB-less (overlay) CRecord, got ok=true")
	}
}
