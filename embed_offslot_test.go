package ark

import (
	"sync"
	"testing"

	"github.com/zot/microfts2"
)

// TestEnqueueStoreWrite_SerializesBehindWriteSlot proves the embed goroutine's
// store write goes through the write actor (R3415), not directly. It occupies
// the single write slot with a blocking closure, then submits a store write via
// enqueueStoreWrite and asserts the write does not run until the slot frees.
//
// Fire alarm: make enqueueStoreWrite call write() directly (off the actor) and
// this fails — ran flips true while B still holds the slot.
// CRC: crc-Librarian.md | R3415 (test-EmbedOffSlot.md)
func TestEnqueueStoreWrite_SerializesBehindWriteSlot(t *testing.T) {
	l, db := setupRecall(t)

	release := make(chan struct{})
	blocking := make(chan struct{})
	// Occupy the single write slot with a closure that blocks until released.
	SyncVoid(db, func(d *DB) error {
		d.enqueueWrite(func(_ *microfts2.DB) {
			close(blocking)
			<-release
		})
		return nil
	})
	<-blocking // the slot is now held

	var mu sync.Mutex
	ran := false
	done := make(chan struct{})
	go func() {
		l.enqueueStoreWrite(func() {
			mu.Lock()
			ran = true
			mu.Unlock()
		})
		close(done)
	}()
	<-done // enqueueStoreWrite has returned (write is queued, not yet run)

	mu.Lock()
	early := ran
	mu.Unlock()
	if early {
		t.Fatal("store write ran while the write slot was held — it bypassed the actor")
	}

	close(release) // let the blocking write finish; queued store write follows
	db.WaitWritesIdle()

	mu.Lock()
	defer mu.Unlock()
	if !ran {
		t.Fatal("store write never ran after the slot freed")
	}
}

// TestBeginEmbedPass_OneAtATime proves the re-entrancy guard (R3416): a second
// reconcile cannot launch a concurrent embed pass while one runs.
//
// Fire alarm: drop the srv.embedding = true latch in beginEmbedPass and this
// fails — the second call returns true.
// CRC: crc-Server.md | R3416 (test-EmbedOffSlot.md)
func TestBeginEmbedPass_OneAtATime(t *testing.T) {
	srv := &Server{}
	if !srv.beginEmbedPass() {
		t.Fatal("first beginEmbedPass should claim the slot")
	}
	if srv.beginEmbedPass() {
		t.Fatal("second beginEmbedPass should be refused while a pass runs")
	}
	srv.endEmbedPass()
	if !srv.beginEmbedPass() {
		t.Fatal("beginEmbedPass should succeed again after endEmbedPass")
	}
}
