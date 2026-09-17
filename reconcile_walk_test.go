package ark

// Fire-alarm tests for the off-actor reconcile walk (test-ReconcileWalk.md). The
// two properties are invisible when violated: an enumeration wrongly run on the
// actor still produces the same records (only the UI stalls), and a second walk
// launched during the first still indexes the same files (only work is
// duplicated). Both are exercised on launchReconcileWalk over a zero-value DB
// with just its actor — no server, no sources, no filesystem — with a fake walk
// that blocks on a channel.
//
// CRC: crc-DB.md | Test: test-ReconcileWalk.md | R3433, R3434

import (
	"sync/atomic"
	"testing"
	"time"
)

// newWalkDB returns a DB with only its actor wired. The actor goroutine is
// intentionally left running (never stopped) — a walk goroutine posts its apply
// back through SyncVoid, and stopping the actor while one is in flight would park
// that goroutine on the closed channel. This mirrors setupConnections's
// deliberate actor leak; the test process exits clean.
func newWalkDB(t *testing.T) *DB {
	t.Helper()
	db := &DB{svc: newChanSvc(chanSvcSlots)}
	db.svc.start()
	return db
}

// TestReconcileWalkRunsOffActor: while the walk runs, the DB actor is free — the
// actor closure that launched it returns before the walk finishes. If the
// enumeration ran on the actor (launchReconcileWalk calling walk() inline instead
// of in a goroutine), that closure would block for the whole walk and every
// interleaving write would queue behind it. R3433
//
// Fire alarm: in launchReconcileWalk, replace `go func(){ walk(); ... }()` with a
// synchronous `walk(); apply()` on the actor. Then launchDone never fires while
// the walk blocks and this rings (timeout).
func TestReconcileWalkRunsOffActor(t *testing.T) {
	db := newWalkDB(t)

	walkStarted := make(chan struct{})
	walkRelease := make(chan struct{})
	launchDone := make(chan struct{})
	applyDone := make(chan struct{})

	go func() {
		SyncVoid(db, func(db *DB) error {
			db.launchReconcileWalk(
				func() { close(walkStarted); <-walkRelease }, // walk: block off the actor
				func() { close(applyDone) },                  // apply
				func() {},                                    // relaunch
			)
			return nil
		})
		close(launchDone) // the launching actor closure returned
	}()

	<-walkStarted // the walk goroutine is running and holding walkRelease

	select {
	case <-launchDone:
		// Correct: the actor closure returned while the walk is still blocking, so
		// the walk is on another goroutine and the actor is free.
	case <-time.After(2 * time.Second):
		close(walkRelease)
		t.Fatal("the actor stayed blocked while the walk ran — enumeration ran ON the actor, not off it")
	}

	close(walkRelease) // let the walk finish
	<-applyDone        // and its apply run on the actor, so no goroutine is left in flight
	if walking, _ := Sync(db, func(db *DB) (bool, error) { return db.reconcileWalking, nil }); walking {
		t.Fatal("reconcileWalking not cleared after the walk completed")
	}
}

// TestReconcileWalkCoalesces: a reconcile arriving while a walk is in flight does
// NOT start a second walk — it sets reconcileWalkPending, and the first walk's
// completion re-triggers exactly one reconcile. If the in-flight guard were
// missing, the second launch would run a concurrent walk (duplicate enumeration)
// and the whole suite would stay green. R3434
//
// Fire alarm: drop the `if db.reconcileWalking { reconcileWalkPending = true;
// return }` guard in launchReconcileWalk. Then walk2Called becomes 1 and the
// pending mark is not durably set, so this rings.
func TestReconcileWalkCoalesces(t *testing.T) {
	db := newWalkDB(t)

	walk1Started := make(chan struct{})
	walk1Release := make(chan struct{})
	relaunched := make(chan struct{})
	var walk2Called int32
	var relaunchCalled int32

	// Launch 1 — blocks until released. Its relaunch is the one that should fire.
	go func() {
		SyncVoid(db, func(db *DB) error {
			db.launchReconcileWalk(
				func() { close(walk1Started); <-walk1Release },
				func() {},
				func() { atomic.AddInt32(&relaunchCalled, 1); close(relaunched) },
			)
			return nil
		})
	}()
	<-walk1Started

	// Launch 2 while walk 1 is in flight — must coalesce: walk2 is never called,
	// and reconcileWalkPending is set.
	SyncVoid(db, func(db *DB) error {
		db.launchReconcileWalk(
			func() { atomic.AddInt32(&walk2Called, 1) },
			func() {},
			func() {},
		)
		return nil
	})

	pending, _ := Sync(db, func(db *DB) (bool, error) { return db.reconcileWalkPending, nil })
	if !pending {
		t.Fatal("a reconcile during an in-flight walk did not set reconcileWalkPending — not coalesced")
	}
	if got := atomic.LoadInt32(&walk2Called); got != 0 {
		t.Fatalf("a second walk ran during an in-flight walk (walk2Called=%d) — coalescing failed, concurrent enumeration", got)
	}

	// Release walk 1: its apply runs, the flag clears, and the pending mark
	// re-triggers exactly one reconcile (relaunch).
	close(walk1Release)
	select {
	case <-relaunched:
	case <-time.After(2 * time.Second):
		t.Fatal("the walk completed without re-triggering the reconcile requested during it (relaunch never fired)")
	}
	if got := atomic.LoadInt32(&relaunchCalled); got != 1 {
		t.Fatalf("relaunch fired %d times, want exactly 1 (a reconcile requested during the walk is delayed, not lost)", got)
	}
	// The pending mark is consumed, and no second walk was ever launched.
	pending, _ = Sync(db, func(db *DB) (bool, error) { return db.reconcileWalkPending, nil })
	if pending {
		t.Fatal("reconcileWalkPending not cleared after the relaunch")
	}
	if got := atomic.LoadInt32(&walk2Called); got != 0 {
		t.Fatalf("second walk ran after all (walk2Called=%d)", got)
	}
}
