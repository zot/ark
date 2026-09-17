package ark

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

// CRC: crc-FileScanActor.md | R3419, R3420, R3421, R3422, R3423, R3424, R3425, R3426, R3427

// Tests for the per-file scan actor's registry / changeNum decision logic
// (R3419-R3427). All run on a zero-value *DB — just the maps + counter — so they
// are cheap and deterministic. The properties are all invisible when violated: a
// wrong changeNum comparison, a retire that fires when it shouldn't, or a
// content-staleness escape that skips a real change all pass a naive suite while
// corrupting the index or starving a file. See design/test-FileScanActor.md.

// TestStampMonotonicABASafe: each startFileScan draws a strictly larger stamp
// from the global counter, even across retire-and-recreate, so a stale compute
// can never match a fresh entry. R3421
func TestStampMonotonicABASafe(t *testing.T) {
	db := &DB{}

	s1 := db.stampFileScan("a")
	db.retireFileScanIfCurrent("a", s1)
	s2 := db.stampFileScan("a")

	if s2 <= s1 {
		t.Fatalf("stamp not monotonic across recreate: s1=%d s2=%d", s1, s2)
	}
	// A stale compute holding s1 must not read as current against the recreated
	// entry (which now carries s2).
	if db.fileScanCurrent("a", s1) {
		t.Fatalf("stale stamp s1=%d read as current after recreate (entry=%d)", s1, s2)
	}
	if !db.fileScanCurrent("a", s2) {
		t.Fatalf("fresh stamp s2=%d not current", s2)
	}
}

// TestChangeNumRedoEscape: finishFileScan stores only the current stamp; a scan
// whose stamp was bumped by a later event is dropped. Also the guard that keeps
// ErrAppendTailMoved from firing. R3422, R3427
func TestChangeNumRedoEscape(t *testing.T) {
	db := &DB{}

	my1 := db.stampFileScan("a")
	my2 := db.stampFileScan("a") // a newer event supersedes my1

	if db.fileScanCurrent("a", my1) {
		t.Fatalf("superseded scan my1=%d still current (entry=%d) — stale store would land", my1, my2)
	}
	if !db.fileScanCurrent("a", my2) {
		t.Fatalf("current scan my2=%d not current", my2)
	}
}

// TestRetireOnlyWhenCurrent: retireFileScanIfCurrent removes the entry only if
// its changeNum still matches; a newer event's entry survives. Closes the
// retire-vs-new-event race. R3424
func TestRetireOnlyWhenCurrent(t *testing.T) {
	db := &DB{}

	my1 := db.stampFileScan("a")
	my2 := db.stampFileScan("a")

	// A stale store's retire (my1) must be a no-op — the newer scan owns the entry.
	db.retireFileScanIfCurrent("a", my1)
	if !db.fileScanCurrent("a", my2) {
		t.Fatalf("stale retire(my1=%d) deleted the entry newer scan my2=%d needs", my1, my2)
	}

	// The current store's retire (my2) removes it.
	db.retireFileScanIfCurrent("a", my2)
	if _, ok := db.fileScans["a"]; ok {
		t.Fatalf("current retire(my2=%d) did not remove the entry", my2)
	}
}

// TestContentStalenessEscape: the full-store escape skips only when committed
// length AND hash already match the prepared file; an append is never escaped.
// R3423
func TestContentStalenessEscape(t *testing.T) {
	data := []byte("hello world\n")
	hash := sha256.Sum256(data)
	prepHash := fmt.Sprintf("%x", hash)
	size := int64(len(data))

	// (a) committed == prep, full → skip.
	if !contentUnchangedForStore(false, size, hash, size, prepHash) {
		t.Fatalf("full store with identical committed content was not skipped")
	}
	// (b) same equality but append → must not skip (append-exemption).
	if contentUnchangedForStore(true, size, hash, size, prepHash) {
		t.Fatalf("append was escaped by the content-staleness check — new bytes would be dropped")
	}
	// (c) hash differs → do not skip.
	var other [32]byte
	if contentUnchangedForStore(false, size, other, size, prepHash) {
		t.Fatalf("changed content (hash mismatch) was wrongly skipped")
	}
	// (d) length differs → do not skip.
	if contentUnchangedForStore(false, size+1, hash, size, prepHash) {
		t.Fatalf("changed content (length mismatch) was wrongly skipped")
	}
	// (e) no committed content (fresh) → do not skip.
	if contentUnchangedForStore(false, 0, hash, size, prepHash) {
		t.Fatalf("fresh file with no committed content was wrongly skipped")
	}
}

// TestStoreResultAction: a failed store retires the entry and re-emits one
// change event; a successful store retires without re-emitting. No inline
// reindex. R3425
func TestStoreResultAction(t *testing.T) {
	fail := storeResultAction(fmt.Errorf("disk error"))
	if !fail.retire || !fail.reEmit {
		t.Fatalf("store failure: got %+v, want {retire:true reEmit:true}", fail)
	}
	ok := storeResultAction(nil)
	if !ok.retire || ok.reEmit {
		t.Fatalf("store success: got %+v, want {retire:true reEmit:false}", ok)
	}
}
