package ark

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/zot/microfts2"
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

// --- Item 1b: per-chunk reuse + preemption (R3428-R3432) ---

// fakeGate is a test scanGate backed by a single in-memory grams map, exactly as
// the DB-actor reuseLookup/reuseKeep back the real one by fs.reuse. supersedeAt
// makes reuse report supersession from a given call index onward (-1 = never), so
// a test can drive preemption deterministically without a DB or a real changeNum.
type fakeGate struct {
	grams       map[[32]byte]*microfts2.PreparedChunk
	supersedeAt int
	calls       int
}

func newFakeGate() *fakeGate {
	return &fakeGate{grams: map[[32]byte]*microfts2.PreparedChunk{}, supersedeAt: -1}
}

func (g *fakeGate) reuse(hash [32]byte) (bool, *microfts2.PreparedChunk) {
	c := g.calls
	g.calls++
	if g.supersedeAt >= 0 && c >= g.supersedeAt {
		return true, nil
	}
	return false, g.grams[hash]
}

func (g *fakeGate) keep(hash [32]byte, pc *microfts2.PreparedChunk) { g.grams[hash] = pc }

// splitChunk builds a microfts2.SplitChunk with a distinct hash and content —
// enough for buildEntries, which only reads Hash and Chunk.Content.
func splitChunk(id byte, content string) microfts2.SplitChunk {
	var h [32]byte
	h[0] = id
	return microfts2.SplitChunk{Chunk: microfts2.Chunk{Content: []byte(content)}, Hash: h}
}

// TestBuildEntriesReuse: buildEntries computes grams once per unique hash and
// reuses cached grams — both intra-scan (a repeated chunk) and cross-scan (a
// prior scan's deposits preloaded into the gate). This is the REUSE fire alarm's
// subject: without the map lookup PrepareChunk runs for every chunk. R3428, R3429
func TestBuildEntriesReuse(t *testing.T) {
	// Intra-scan: [A, B, A] — A computed once, reused on its second occurrence.
	splits := []microfts2.SplitChunk{splitChunk(1, "a"), splitChunk(2, "b"), splitChunk(1, "a")}
	gate := newFakeGate()
	computes := 0
	compute := func([]byte) *microfts2.PreparedChunk { computes++; return &microfts2.PreparedChunk{} }

	entries, err := buildEntries(splits, gate, compute)
	if err != nil {
		t.Fatalf("buildEntries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries: got %d, want 3 (positions come from each occurrence)", len(entries))
	}
	if computes != 2 {
		t.Fatalf("intra-scan dedup: PrepareChunk ran %d times, want 2 (A reused)", computes)
	}
	if entries[0].Grams != entries[2].Grams {
		t.Fatalf("repeated chunk A did not share one *PreparedChunk")
	}

	// Cross-scan: a second scan whose two chunks were both finished by a prior
	// scan (preloaded into the gate) recomputes nothing.
	var hA, hB [32]byte
	hA[0], hB[0] = 1, 2
	warm := newFakeGate()
	warm.grams[hA] = &microfts2.PreparedChunk{}
	warm.grams[hB] = &microfts2.PreparedChunk{}
	computes = 0
	if _, err := buildEntries([]microfts2.SplitChunk{splitChunk(1, "a"), splitChunk(2, "b")}, warm, compute); err != nil {
		t.Fatalf("buildEntries (warm): %v", err)
	}
	if computes != 0 {
		t.Fatalf("cross-scan reuse: PrepareChunk ran %d times on an all-hit scan, want 0", computes)
	}
}

// TestBuildEntriesPreemption: when the gate reports supersession, buildEntries
// abandons between chunks — it returns errScanSuperseded and does NOT compute the
// abandoned chunks. This is the PREEMPTION fire alarm's subject: without the
// early return the loop recomputes work a newer scan owns. R3430
func TestBuildEntriesPreemption(t *testing.T) {
	splits := []microfts2.SplitChunk{splitChunk(1, "a"), splitChunk(2, "b"), splitChunk(3, "c")}
	gate := newFakeGate()
	gate.supersedeAt = 1 // superseded from the second reuse() call onward (before chunk index 1)
	computes := 0
	compute := func([]byte) *microfts2.PreparedChunk { computes++; return &microfts2.PreparedChunk{} }

	entries, err := buildEntries(splits, gate, compute)
	if !errors.Is(err, errScanSuperseded) {
		t.Fatalf("superseded scan: got err=%v entries=%d, want errScanSuperseded", err, len(entries))
	}
	if computes != 1 {
		t.Fatalf("superseded scan recomputed abandoned chunks: PrepareChunk ran %d times, want 1 (only chunk 0)", computes)
	}
}

// TestReuseLookupKeep: the DB-actor reuse map — a deposit is visible to the same
// stamp, survives supersession (so the newer scan reuses it: convergence), and is
// dropped when the entry retires. All on a zero-value *DB. R3429, R3431
func TestReuseLookupKeep(t *testing.T) {
	db := &DB{}
	my := db.stampFileScan("a")
	var hA [32]byte
	hA[0] = 1

	if r := db.reuseLookup("a", my, hA); r.superseded || r.cached != nil {
		t.Fatalf("fresh entry: got %+v, want a clean miss", r)
	}
	pc := &microfts2.PreparedChunk{}
	db.reuseKeep("a", hA, pc)
	if r := db.reuseLookup("a", my, hA); r.superseded || r.cached != pc {
		t.Fatalf("after keep: got %+v, want cached==pc", r)
	}

	// A newer event supersedes my but shares the entry's reuse map.
	my2 := db.stampFileScan("a")
	if r := db.reuseLookup("a", my, hA); !r.superseded {
		t.Fatalf("stale stamp read as current: %+v", r)
	}
	if r := db.reuseLookup("a", my2, hA); r.superseded || r.cached != pc {
		t.Fatalf("convergence: newer scan did not inherit the deposit: %+v", r)
	}

	// Retire drops the entry (and its map).
	db.retireFileScanIfCurrent("a", my2)
	if r := db.reuseLookup("a", my2, hA); !r.superseded {
		t.Fatalf("retired entry still served a lookup: %+v", r)
	}
}

// TestRunScanBoundedCapsConcurrency proves the per-file dispatch runs at most
// cap(scanSem) preparations at once — the semaphore, not the number of stale
// files, sets the ceiling. A cap violation is invisible (the index still ends up
// correct), so this pins it directly. test-FileScanActor.md alarm 8. R3438
func TestRunScanBoundedCapsConcurrency(t *testing.T) {
	const cap, m = 3, 30
	db := &DB{scanSem: make(chan struct{}, cap)}

	var mu sync.Mutex
	live, peak := 0, 0
	entered := make(chan struct{}, m)
	release := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < m; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db.runScanBounded(func() {
				mu.Lock()
				live++
				if live > peak {
					peak = live
				}
				mu.Unlock()
				entered <- struct{}{}
				<-release
				mu.Lock()
				live--
				mu.Unlock()
			})
		}()
	}

	// The first cap preparations acquire slots and block on release; the rest
	// must be stuck on the semaphore. Drain exactly cap, then prove no (cap+1)th
	// slips through within a generous window.
	for i := 0; i < cap; i++ {
		<-entered
	}
	select {
	case <-entered:
		t.Fatalf("more than cap=%d preparations ran concurrently", cap)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	wg.Wait()

	if peak > cap {
		t.Fatalf("peak concurrency %d exceeded cap %d", peak, cap)
	}
	if peak < 1 {
		t.Fatal("nothing ran under runScanBounded")
	}
}
