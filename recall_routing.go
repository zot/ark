package ark

// CRC: crc-RecallWatcher.md | Seq: seq-recall-watcher.md#6 | R3298, R3300-R3304

// Ambient routing: choosing which secretary serves a fired curation doc.
//
// A curation doc carries two identities. Its **origin** is the session that
// produced it, held in its path (curation-<session>-<fire>); every result verb
// keys on that, so a curated result returns home no matter who curated it. Its
// **address** is the @ark-secretary-work head tag — the tube of the secretary
// that will serve it. The two were necessarily equal while ambient had exactly
// one possible server, which is why the distinction had never needed stating.
// Routing is this file setting an address that differs from the origin.
//
// The watcher decides; nothing is left to whoever asks first. That is what
// makes the preference order real rather than a race, and it is also what
// stamps pool occupancy — a secretary is marked busy by the act of being sent
// work, so anything a secretary found for itself would be invisible to the
// pruner. R3300, R3302, R3303, R3304

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

// The `session` argument to curationDocPath (recall_agent_builder.go) is the
// doc's ORIGIN, never its address — the path is what `recall close` and the
// result verbs key on, and routing must not disturb it. R3302

// curationHeadTag is the head-of-chunk line carrying a curation doc's address.
const curationHeadTag = "@ark-secretary-work: "

// recallRoster returns the recall class's roster, or nil on a watcher that was
// assembled without one. Nil rather than lazily created: a missing roster means
// the watcher was not built by NewRecallWatcher, and silently inventing one
// would let a test fixture diverge from production while still passing.
func (w *RecallWatcher) recallRoster() *secretaryPool {
	if w == nil {
		return nil
	}
	return w.pools[recallPoolClass]
}

// routeCuration picks the address for a freshly written curation doc, by the
// fixed preference order of R3303:
//
//  1. the origin session's own secretary, when it has one — dedicated, so its
//     recollections never queue behind another session's work. The doc is
//     already addressed there, so this costs nothing and consumes no pool slot.
//  2. otherwise a `recall`-class pool secretary, via pumpCuration.
//  3. otherwise the unservable report — never silence.
//
// Called from fire() once the doc is on disk. R3303
func (w *RecallWatcher) routeCuration(origin string, fire uint64) {
	if w == nil {
		return
	}
	// Rule 1. The builder already wrote the origin as the address, so a session
	// with its own secretary needs no re-tag at all.
	if w.secretaryPresent(origin) {
		Logv(2, "recall-routing: session=%s fire=%d → own secretary", origin, fire)
		return
	}
	// Rule 3, first form: no seat means no one can ever stand a secretary up,
	// so this is unservable now and later. Distinct from a busy pool, which is
	// merely deferral and is not reported.
	if w.luhmann == nil || w.luhmann.LuhmannOwner() == "" {
		w.emitUnservableRecollection(origin, fire, "no secretary of its own and no Luhmann seated to staff the recall pool")
		return
	}
	// Rule 2.
	pool := w.recallRoster()
	if pool == nil {
		return
	}
	path := curationDocPath(origin, fire)
	pool.mu.Lock()
	pool.pending = append(pool.pending, path)
	pool.mu.Unlock()
	w.pumpCuration()
}

// pumpCuration routes queued curation docs to free `recall`-class secretaries —
// the same "route when ready" engine pump() is for hunts, called on any state
// change (a new doc, a secretary freed, a secretary registered). Each iteration
// pops one pending path and one free secretary atomically under the lock, so
// concurrent pumps never double-route. R3301, R3303
func (w *RecallWatcher) pumpCuration() {
	if w == nil || w.luhmann == nil {
		return
	}
	owner := w.luhmann.LuhmannOwner()
	if owner == "" {
		return
	}
	pool := w.recallRoster()
	if pool == nil {
		return
	}
	for {
		pool.mu.Lock()
		if len(pool.pending) == 0 {
			pool.mu.Unlock()
			return
		}
		sec := pool.pickFreeLocked()
		if sec == nil {
			// R3301: grow the class if it has room; otherwise the doc simply
			// waits. A full pool is DEFERRAL, not unservability — nobody is
			// blocked on a recollection, so it is not reported (R3304).
			room := len(pool.secretaries) < w.poolMax(recallPoolClass)
			pool.mu.Unlock()
			if room {
				Logv(1, "recall-routing: no free recall secretary, standing one up")
				w.luhmann.EnqueueLuhmann(LuhmannWork{Kind: "directive", Directive: "stand-up", Class: recallPoolClass})
			}
			return
		}
		path := pool.pending[0]
		pool.pending = pool.pending[1:]
		sec.busy = true // R3300: stamped by the dispatch, so prune sees it working
		sec.idleSince = time.Time{}
		nonce := sec.nonce
		pool.inflight[path] = nonce
		pool.mu.Unlock()

		composite := owner + "-" + strconv.FormatUint(nonce, 10)
		if err := w.retagCuration(path, composite); err != nil {
			log.Printf("recall-routing: re-address %s failed: %v", path, err)
			pool.mu.Lock()
			delete(pool.inflight, path)
			sec.busy = false
			sec.idleSince = time.Now()
			// Front of the queue, not the back: this doc is older than anything
			// behind it, and R2939's lowest-first ordering should survive a
			// transient write failure.
			pool.pending = append([]string{path}, pool.pending...)
			pool.mu.Unlock()
			return
		}
		Logv(1, "recall-routing: routed %s → @ark-secretary-work=%s", path, composite)
	}
}

// retagCuration rewrites a curation doc's address in place, leaving its body and
// its path — the origin — untouched. One write-actor flush, so a secretary never
// wakes on a doc whose head tag is half-written. R3302
func (w *RecallWatcher) retagCuration(path, address string) error {
	data, err := w.db.TmpContent(path)
	if err != nil {
		return err
	}
	body := string(data)
	first, rest, found := strings.Cut(body, "\n")
	if !found || !strings.HasPrefix(strings.TrimSpace(first), curationHeadTag) {
		return fmt.Errorf("curation doc %s has no %s head tag", path, curationHeadTag)
	}
	body = curationHeadTag + address + "\n" + rest
	return SyncVoid(w.db, func(db *DB) error {
		return db.UpdateTmpFile(path, "markdown", []byte(body))
	})
}

// FreeCuration releases the `recall`-class secretary holding the curation doc
// for a fire token, on that doc's close. Occupancy ends when the secretary stops
// working, so the freed secretary enters its cooldown immediately and is
// preferred for the next dispatch. Idempotent: a doc served by a session's own
// secretary was never in the roster, so this is a no-op for it. R3300
func (w *RecallWatcher) FreeCuration(session string, fire uint64) {
	if w == nil {
		return
	}
	pool := w.recallRoster()
	if pool == nil {
		return
	}
	path := curationDocPath(session, fire)
	pool.mu.Lock()
	nonce, held := pool.inflight[path]
	if held {
		delete(pool.inflight, path)
		if s := pool.secretaries[nonce]; s != nil {
			s.busy = false
			s.idleSince = time.Now()
		}
	}
	pool.mu.Unlock()
	if held {
		w.pumpCuration() // a slot opened; take the next waiting doc
	}
}

// emitUnservableRecollection tells the requesting session that its recollection
// will not be served, on the channel a result would have arrived on, and drops
// the curation doc. R3304
//
// Silence is the unacceptable outcome even though nobody is blocked: a session
// that opted into ambient recall and receives nothing cannot tell "the corpus
// had nothing to say" from "nothing was ever going to run", and the remedy for
// the second belongs to it — spawn a secretary of its own, or tell the user no
// Luhmann is seated.
func (w *RecallWatcher) emitUnservableRecollection(origin string, fire uint64, reason string) {
	curation := curationDocPath(origin, fire)
	defer func() {
		if err := SyncVoid(w.db, func(db *DB) error { return db.RemoveTmpFile(curation) }); err != nil {
			log.Printf("recall-routing: drop unservable curation %s: %v", curation, err)
		}
	}()
	if w.db == nil || w.db.pubsub == nil ||
		w.db.pubsub.SubscriberCount("ark-recall-result", origin) == 0 {
		// The consumer dropped between the fire and here; there is nobody to
		// tell, and no obligation to leave a doc behind for a session that
		// stopped listening.
		return
	}
	body := fmt.Sprintf("@ark-recall-result: %s\n\n## Unservable: recollection not served\n\n%s.\n\n"+
		"Nothing is queued for this one — it was dropped rather than left waiting. "+
		"To get recollections again, either spawn a session-local secretary (/bloodhound step 2) "+
		"or have a Luhmann orchestrator seated so the recall pool can serve you.\n",
		origin, reason)
	path := resultDocPath(origin, fire)
	if err := SyncVoid(w.db, func(db *DB) error {
		_, e := db.AddTmpFile(path, "markdown", []byte(body))
		return e
	}); err != nil {
		log.Printf("recall-routing: write unservable result %s: %v", path, err)
		return
	}
	Logv(1, "recall-routing: session=%s fire=%d unservable (%s)", origin, fire, reason)
}
