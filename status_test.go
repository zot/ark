package ark

// Fire-alarm tests for the off-actor, no-walk `ark status` (test-Status.md). Both
// properties are invisible when violated: a status read wrongly run on the actor
// still returns the same numbers (only an interleaving write stalls), and a
// status that walks the corpus still returns a plausible count (only wrong, and
// only when the disk diverged from the last reconcile). Each runs on a real
// (temp) index with one indexed file — no server.
//
// CRC: crc-DB.md | Test: test-Status.md | R3435, R3436

import (
	"os"
	"testing"
	"time"
)

// TestStatusReadRunsOffActor: while a status read runs, the DB actor is free —
// the actor closure that binds the view returns before the read finishes. If the
// read ran inside the bind Sync (on the actor), the concurrent hop below would
// queue behind it and time out. R3436
//
// Fire alarm: in readStatusOffActor, run `read` INSIDE the bind Sync closure.
// Then the blocking read holds the single-slot actor, hopDone never fires, and
// this rings (timeout).
func TestStatusReadRunsOffActor(t *testing.T) {
	idx, dir := testIndexer(t)
	db := newTestDB(idx, dir)
	db.svc = newChanSvc(chanSvcSlots)
	db.svc.start()

	readStarted := make(chan struct{})
	readRelease := make(chan struct{})
	readDone := make(chan struct{})

	go func() {
		readStatusOffActor(db, func(b statusBinding) (int, error) {
			close(readStarted)
			<-readRelease // block OFF the actor
			return 0, nil
		})
		close(readDone)
	}()
	<-readStarted // the read goroutine is running and holding readRelease

	// A concurrent actor hop must still complete while the read blocks.
	hopDone := make(chan struct{})
	go func() {
		Sync(db, func(db *DB) (int, error) { return 0, nil })
		close(hopDone)
	}()

	select {
	case <-hopDone:
		// Correct: the bind Sync returned, so the read is off the actor and the
		// actor is free to serve the hop.
	case <-time.After(2 * time.Second):
		close(readRelease)
		t.Fatal("a concurrent actor hop blocked while a status read ran — the read ran ON the actor, not off it")
	}

	close(readRelease)
	<-readDone
}

// TestStatusUsesStoredStaleCount: Status reports the stale/missing/unresolved
// counts from the passed-in reconcileCounts, never a live file walk. A file that
// is stale on disk does not change what status reports. R3435
//
// Fire alarm: in Status, replace rc.stale with a live db.fts.StaleFiles() count.
// Then status.Stale becomes the disk truth (1), the assertion expecting the
// stored 7 fails, and this rings.
func TestStatusUsesStoredStaleCount(t *testing.T) {
	idx, dir := testIndexer(t)
	fp := writeFile(t, dir, "note.txt", "hello\nworld\n")
	if _, err := idx.AddFile(fp, "line"); err != nil {
		t.Fatal(err)
	}
	db := newTestDB(idx, dir)
	db.config = &Config{}

	// Make the file genuinely stale on disk: different content AND a distinctly
	// different mtime, so a live StaleFiles walk classifies it "stale".
	if err := os.WriteFile(fp, []byte("changed\ncontent\nhere\n"), 0644); err != nil {
		t.Fatal(err)
	}
	past := time.Unix(1_000_000_000, 0) // 2001 — clearly != the just-indexed mtime
	if err := os.Chtimes(fp, past, past); err != nil {
		t.Fatal(err)
	}

	// Control: a live walk would report exactly one stale file. If it does not,
	// the test can no longer distinguish stored from live and must be fixed.
	live, err := db.fts.StaleFiles()
	if err != nil {
		t.Fatal(err)
	}
	liveStale := 0
	for _, s := range live {
		if s.Status == "stale" {
			liveStale++
		}
	}
	if liveStale != 1 {
		t.Fatalf("control: expected the mutated file to be live-stale=1, got %d — test cannot distinguish stored from live", liveStale)
	}

	const wantStale, wantMissing, wantUnresolved = 7, 3, 5
	at := time.Unix(1_700_000_000, 0)
	rc := reconcileCounts{stale: wantStale, missing: wantMissing, unresolved: wantUnresolved, at: at}

	status, err := db.Status(rc, 0)
	if err != nil {
		t.Fatal(err)
	}
	if status.Stale != wantStale {
		t.Fatalf("status.Stale = %d, want the stored %d (a live walk would give %d) — Status walked the corpus", status.Stale, wantStale, liveStale)
	}
	if status.Missing != wantMissing {
		t.Fatalf("status.Missing = %d, want the stored %d", status.Missing, wantMissing)
	}
	if status.Unresolved != wantUnresolved {
		t.Fatalf("status.Unresolved = %d, want the stored %d", status.Unresolved, wantUnresolved)
	}
	if !status.ReconcileAt.Equal(at) {
		t.Fatalf("status.ReconcileAt = %v, want %v", status.ReconcileAt, at)
	}
	if status.Files != 1 {
		t.Fatalf("status.Files = %d, want 1 (the one indexed file)", status.Files)
	}
}
