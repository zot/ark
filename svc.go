package ark

// The closure actor. See specs/db-concurrency.md §Delivery for the contract and
// design/crc-ChanSvc.md for the card.
//
// CRC: crc-ChanSvc.md | R986, R3344, R3345, R3346, R3347, R3348, R3349, R3350

/*
@pattern: closure-actor — this type is ark's implementation of the substrate; three owners hold one (DB, RecallWatcher, PtyHost)
@pattern: actor-queue-ordering — the guarantee ChanSvc exists to provide: a closure posted behind another runs behind it, so "post the follow-up" replaces "synchronize against it" (R3344)
@pattern: sewing — post, don't call: the work goroutine posts `next` back to the loop rather than calling into it, which is what keeps every mutation of queue/running on one goroutine (R3346)
*/

// chanSvcSlots is the depth of every actor's channel, and it is 1 on purpose.
//
// The channel is a **doorway, not a buffer**: bursts are absorbed by the queue
// behind it (R3347), and the loop does only O(1) bookkeeping, so depth buys a
// poster nothing but the chance to skip a rendezvous — microseconds. What depth
// costs is diagnostic. A loop that somehow gets held (someone puts real work
// back on it) would let the first N posts succeed anyway, so the stall surfaces
// later and somewhere else. One slot makes a held loop block the very next
// poster, at the site, which is where it can be understood.
//
// It cannot deadlock: the loop blocks only on its own receive and neither
// accept nor next can block, so it always returns to drain. A depth-1 channel
// can stall a sender briefly; it cannot stall one permanently.
const chanSvcSlots = 1

// ChanSvc serializes access to whatever state its owner protects: exactly one
// closure runs at any instant, and closures posted by one goroutine run in the
// order that goroutine posted them (R986, R3344).
//
// The ordering guarantee rests on two things that hold each other up. A post is
// a **direct send on the calling goroutine** (R3345) — handing the send to a
// spawned goroutine lets two posts race, which is what ark did until 2026-07-31
// and why ordering was silently absent. That is only viable because the loop
// goroutine **never runs a caller's closure** (R3346): it manipulates the queue
// and the in-flight flag and nothing else, so it is never held for longer than a
// slice operation and is effectively always back at the receive. Running caller
// work on the loop instead is exactly what made escaping the send tempting.
//
// `queue` and `running` are touched only on the loop goroutine, which is why
// neither needs a lock and why `running` is a plain bool.
type ChanSvc struct {
	// ch is a doorway, not the buffer. Bursts are absorbed by queue, so this
	// stays small (R3347).
	ch chan func()

	// loop-goroutine only:
	queue   []func()
	running bool
}

// newChanSvc builds an actor without starting it; call start when the owner is
// ready to serve. The two phases are separate because owners construct the actor
// in a struct literal and start it once the rest of their state is built.
func newChanSvc(bufSize int) *ChanSvc {
	return &ChanSvc{ch: make(chan func(), bufSize)}
}

// start launches the loop goroutine. Every closure it receives is bookkeeping —
// accept or next — so the loop does no caller work and cannot be held (R3346).
func (s *ChanSvc) start() {
	go func() {
		for fn := range s.ch {
			if fn == nil {
				return // the stop sentinel
			}
			fn()
		}
	}()
}

// stop shuts the loop down once everything already posted has been handled: the
// sentinel is an ordinary send, so it lands behind the existing backlog and the
// loop drains to it. Closures still waiting in `queue` behind an in-flight one
// are abandoned, which is acceptable for a shutdown verb and is why this is not
// a drain-and-wait.
//
// **Deliberately not close(s.ch).** Every closure posts `next` back on
// completion (R3346), so a work goroutine finishing after a close would panic
// sending on a closed channel — a window that barely existed before this design
// and is wide now. Posts arriving after stop are dropped into the buffer
// instead, which is the safe failure for a shutdown path.
func (s *ChanSvc) stop() { s.ch <- nil }

// post is the single door onto the loop. The send happens here, on the caller's
// goroutine, which is what makes delivery ordered (R3344, R3345).
func (s *ChanSvc) post(fn func()) { s.ch <- func() { s.accept(fn) } }

// accept runs on the loop goroutine: queue the closure if one is in flight,
// otherwise start it (R3346).
func (s *ChanSvc) accept(fn func()) {
	if s.running {
		s.queue = append(s.queue, fn)
		return
	}
	s.launch(fn)
}

// launch runs on the loop goroutine: mark the actor busy and hand the closure to
// its own goroutine, which posts next back when it finishes.
//
// **No recover.** A panicking closure takes the process down (R3348). That is
// deliberate — the state the actor protects is in an unknown condition once a
// closure has panicked, so continuing on it is worse than stopping — and it also
// removes a failure mode with no symptom: a central recover that omitted to
// clear `running` would queue every later closure forever, silently. A closure
// that can meaningfully survive its own panic recovers internally, as the write
// goroutine does (R1059).
func (s *ChanSvc) launch(fn func()) {
	s.running = true
	go func() {
		fn()
		s.ch <- s.next
	}()
}

// next runs on the loop goroutine: start whatever is waiting, or go idle.
func (s *ChanSvc) next() {
	if len(s.queue) == 0 {
		s.running = false
		return
	}
	fn := s.queue[0]
	s.queue = s.queue[1:]
	s.launch(fn)
}

// svc posts a fire-and-forget closure. Safe to call from inside a running
// closure: the closure runs off the loop goroutine, so this merely queues behind
// it (R3349).
func svc(s *ChanSvc, fn func()) { s.post(fn) }

// svcSync posts a closure and blocks until it completes.
//
// **Never call this from inside a running closure** (R3349). The nested post
// queues because a closure is already in flight, the queued closure can start
// only when the active one finishes, and the active one is blocked here waiting
// for it. That `svc` above *is* re-entrant makes the inference tempting and
// wrong. Where an operation object is threaded, the receiver is the audit
// surface for this (R3350).
func svcSync[T any](s *ChanSvc, fn func() (T, error)) (T, error) {
	done := make(chan struct{})
	var val T
	var err error
	s.post(func() {
		val, err = fn()
		close(done)
	})
	<-done
	return val, err
}

// svcSyncVoid posts a closure with no return value and blocks until it
// completes. The R3349 rule on svcSync applies here identically.
func svcSyncVoid(s *ChanSvc, fn func() error) error {
	done := make(chan struct{})
	var err error
	s.post(func() {
		err = fn()
		close(done)
	})
	<-done
	return err
}
