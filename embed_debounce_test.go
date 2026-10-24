package ark

// CRC: crc-Server.md | Test: test-EmbedDebounce.md | R3579, R3580

import (
	"slices"
	"testing"
	"time"
)

func newEmbedDebounce() *embedDebounce {
	return &embedDebounce{quiet: embedQuietWindow, maxWait: embedMaxWait}
}

// R3579: the set is embedded once quiet for 15 seconds, and one timer is
// enough however many paths join while it is armed.
func TestEmbedDebounce_Quiet(t *testing.T) {
	d := newEmbedDebounce()
	t0 := time.Unix(1_000_000, 0)
	if arm, delay := d.note("a", t0); !arm || delay != embedQuietWindow {
		t.Fatalf("first note: arm=%v delay=%v, want true %v", arm, delay, embedQuietWindow)
	}
	if arm, _ := d.note("b", t0.Add(10*time.Second)); arm {
		t.Fatal("a note while armed must not arm a second timer")
	}
	paths, again := d.fire(t0.Add(15 * time.Second))
	if paths != nil || again != 10*time.Second {
		t.Fatalf("early fire: paths=%v again=%v, want none and 10s (quiet after b)", paths, again)
	}
	paths, again = d.fire(t0.Add(25 * time.Second))
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"a", "b"}) || again != 0 {
		t.Fatalf("due fire: paths=%v again=%v, want [a b] and no re-arm", paths, again)
	}
	if arm, delay := d.note("c", t0.Add(30*time.Second)); !arm || delay != embedQuietWindow {
		t.Fatalf("note after a flush: arm=%v delay=%v, want a fresh timer", arm, delay)
	}
}

// R3579: a path joining every ten seconds never lets the set go quiet, so the
// one-minute ceiling is what embeds it.
func TestEmbedDebounce_MaxWait(t *testing.T) {
	d := newEmbedDebounce()
	t0 := time.Unix(1_000_000, 0)
	_, delay := d.note("a", t0)
	timerAt := t0.Add(delay)
	for k := 1; k <= 12; k++ {
		nextChange := t0.Add(time.Duration(k) * 10 * time.Second)
		// Fire every timer that comes due before the next change.
		for !timerAt.After(nextChange) {
			paths, again := d.fire(timerAt)
			if paths != nil {
				if got := timerAt.Sub(t0); got != embedMaxWait {
					t.Fatalf("flushed %v after the first change, want %v", got, embedMaxWait)
				}
				return
			}
			timerAt = timerAt.Add(again)
		}
		d.note("a", nextChange)
	}
	t.Fatal("a file changing every ten seconds was never embedded")
}

// R3580: a full pass clears the set; the timer still outstanding then finds
// nothing, and a path noted after the clear is timed from its own arrival.
func TestEmbedDebounce_Clear(t *testing.T) {
	d := newEmbedDebounce()
	t0 := time.Unix(1_000_000, 0)
	d.note("a", t0)
	d.clear()
	if arm, _ := d.note("b", t0.Add(5*time.Second)); arm {
		t.Fatal("the timer armed before the clear is still outstanding; no second one")
	}
	paths, again := d.fire(t0.Add(15 * time.Second))
	if paths != nil || again != 5*time.Second {
		t.Fatalf("fire: paths=%v again=%v, want none and 5s (b's quiet window)", paths, again)
	}
	if paths, _ = d.fire(t0.Add(20 * time.Second)); !slices.Equal(paths, []string{"b"}) {
		t.Fatalf("paths=%v, want [b] only (a was covered by the full pass)", paths)
	}
}

// R3580: while a pass runs, a due set is held and a full pass is deferred;
// when the pass ends the full pass goes first and drops the held files, which
// it covers; held files alone start a limited pass.
func TestEmbedPassWaiters(t *testing.T) {
	l, db := setupRecall(t) // no tiers and nothing to embed, so a pass ends at once
	srv := &Server{db: db, librarian: l, embedWait: *newEmbedDebounce()}
	onActor := func(fn func()) {
		t.Helper()
		if err := svcSyncVoid(db.svc, func() error { fn(); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	waitIdle := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			var busy bool
			onActor(func() { busy = srv.embedding })
			if !busy {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("embed pass never ended")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	// Assertions run off the actor: a t.Fatal inside an actor closure would
	// kill the actor goroutine and hang every later post.
	type state struct {
		running, fullPending bool
		held                 map[string]struct{}
	}
	snap := func() state {
		return state{srv.embedding, srv.embedFullPending, srv.embedHeld}
	}
	var busy, ended state
	onActor(func() {
		srv.embedding = true // a pass is running
		srv.launchLimitedEmbed(db, []string{"a"})
		srv.launchFullEmbed(db)
		busy = snap()
		srv.endEmbedPass()
		ended = snap()
	})
	if _, held := busy.held["a"]; !held || !busy.fullPending {
		t.Fatalf("while busy: held=%v fullPending=%v, want both", busy.held, busy.fullPending)
	}
	if !ended.running || ended.fullPending || ended.held != nil {
		t.Fatalf("after end: running=%v fullPending=%v held=%v, want the full pass running and nothing waiting",
			ended.running, ended.fullPending, ended.held)
	}
	waitIdle()

	onActor(func() {
		srv.embedding = true
		srv.launchLimitedEmbed(db, []string{"b"})
		srv.endEmbedPass()
		ended = snap()
	})
	if !ended.running || ended.held != nil {
		t.Fatalf("after end: running=%v held=%v, want the limited pass running", ended.running, ended.held)
	}
	waitIdle()
}
