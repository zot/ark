package ark

// Unit tests for reconcile coalescing (test-DBReconcile.md). The gate is a
// deterministic state machine over one actor-owned bool, so it needs no server,
// no sources and no filesystem — a zero-value DB with a running actor and a fake
// scan reaches all of it.
//
// CRC: crc-DB.md | Test: test-DBReconcile.md | R346, R3351

import (
	"sync"
	"testing"
	"time"
)

// coalesceHarness is a DB with only its actor wired, plus a scan that records
// each run and can be held open so requests pile up behind it.
type coalesceHarness struct {
	db *DB

	mu      sync.Mutex
	scans   int
	started chan struct{} // closed by the first scan
	release chan struct{} // closes to let a held scan finish
}

func newCoalesceHarness(t *testing.T) *coalesceHarness {
	t.Helper()
	h := &coalesceHarness{
		db:      &DB{svc: newChanSvc(chanSvcSlots)},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	h.db.svc.start()
	t.Cleanup(h.db.svc.stop)
	return h
}

// hold is a scan that signals it began and then waits for release. Only the
// first run waits; later ones return at once, so a test can hold exactly one.
func (h *coalesceHarness) hold(*DB) {
	h.mu.Lock()
	h.scans++
	first := h.scans == 1
	h.mu.Unlock()
	if first {
		close(h.started)
		<-h.release
	}
}

func (h *coalesceHarness) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.scans
}

func (h *coalesceHarness) waitScans(t *testing.T, n int) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if h.count() >= n {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d scan(s); saw %d", n, h.count())
}

// settle waits for the actor to drain, by posting a synchronous closure behind
// everything already queued — which is only a valid barrier because delivery is
// ordered (R3344).
func (h *coalesceHarness) settle() { _ = SyncVoid(h.db, func(*DB) error { return nil }) }

// Test: many requests during one run collapse into a single further scan
// (R346, R3351). Five requests, one already running: the first waiter runs, the
// rest merge into it, so two scans happen rather than six.
func TestReconcileCoalescesIntoOnePending(t *testing.T) {
	h := newCoalesceHarness(t)

	h.db.postCoalescedReconcile(h.hold) // scan 1 — blocks until released
	<-h.started

	for i := 0; i < 5; i++ {
		h.db.postCoalescedReconcile(h.hold)
	}
	close(h.release)

	h.waitScans(t, 2)
	h.settle()
	time.Sleep(50 * time.Millisecond) // give a wrongly-queued sixth scan room to run
	if got := h.count(); got != 2 {
		t.Errorf("scans = %d, want 2 — one active plus one merged pending (R3351)", got)
	}
}

// Test: the pending mark clears when the scan begins, not when it ends, so a
// later request still gets its own scan (R3351). This is the half that is easy
// to get backwards, and getting it backwards silently drops changes.
func TestReconcilePendingClearsSoLaterRequestScansAgain(t *testing.T) {
	h := newCoalesceHarness(t)

	h.db.postCoalescedReconcile(h.hold)
	<-h.started
	h.db.postCoalescedReconcile(h.hold) // merges-or-queues behind the running one
	close(h.release)
	h.waitScans(t, 2)
	h.settle()

	// Everything has quiesced. A fresh request must scan again — if the mark
	// were cleared at the end of a scan (or never), this would be swallowed.
	h.db.postCoalescedReconcile(h.hold)
	h.waitScans(t, 3)
}

// Test: with nothing in flight, a request scans immediately (R346). The gate
// must not turn the ordinary case into a no-op.
func TestReconcileIdleRequestRunsImmediately(t *testing.T) {
	h := newCoalesceHarness(t)
	close(h.release) // nothing holds

	h.db.postCoalescedReconcile(h.hold)
	h.waitScans(t, 1)
}
