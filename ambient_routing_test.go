package ark

// CRC: crc-RecallWatcher.md | Seq: seq-recall-watcher.md#6 | Test: test-AmbientRouting.md

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zot/microfts2"
)

// newRoutingWatcher builds a watcher over a live test DB with a seated fake hub
// and a real PubSub. A real PubSub matters: a nil one short-circuits every
// subscriber gate to "present", which would hide exactly the branches this
// suite exists to assert. Built through NewRecallWatcher so the per-class
// rosters come from production's constructor rather than being hand-wired here
// — a fixture that assembles what production assembles differently is how a
// dead code path stays green.
func newRoutingWatcher(t *testing.T, hub *fakeLuhmannHub) *RecallWatcher {
	t.Helper()
	_, db := setupRecall(t)
	if err := db.fts.AddChunker("markdown", microfts2.MarkdownChunker{}); err != nil {
		t.Fatalf("AddChunker(markdown): %v", err)
	}
	db.SetPubSub(NewPubSub(time.Minute, 32))
	w := NewRecallWatcher(db, nil, db.store, nil)
	w.luhmann = hub
	return w
}

// subscribeTag registers a consumer on <tag>=<value>.
func subscribeTag(t *testing.T, db *DB, tag, value string) {
	t.Helper()
	p, err := ParseMatchSyntax(tag + "=" + value)
	if err != nil {
		t.Fatalf("ParseMatchSyntax(%s=%s): %v", tag, value, err)
	}
	db.pubsub.Subscribe(tag+":"+value, []*TagSub{{Kind: TagSubChunk, Predicate: p}})
}

// writeCuration lays down a curation doc addressed to `address` with origin
// `origin`, the way fire() leaves it before routing.
func writeCuration(t *testing.T, db *DB, origin string, fire uint64, address string) string {
	t.Helper()
	path := curationDocPath(origin, fire)
	body := curationHeadTag + address + "\n@ark-recall-fire: " +
		strconv.FormatUint(fire, 10) + "\n\n## Section: x\n\n- a:1 (10) — note\n"
	if err := SyncVoid(db, func(db *DB) error {
		_, e := db.AddTmpFile(path, "markdown", []byte(body))
		return e
	}); err != nil {
		t.Fatalf("write curation doc: %v", err)
	}
	return path
}

// recallPool is the class roster under test.
func recallPool(w *RecallWatcher) *secretaryPool { return w.pools[recallPoolClass] }

// Refs: R3303 rule 1
func TestRouteCurationPrefersOwnSecretary(t *testing.T) {
	hub := &fakeLuhmannHub{owner: "L"}
	w := newRoutingWatcher(t, hub)
	const sess = "sess-A"
	subscribeTag(t, w.db, "ark-secretary-work", sess)
	subscribeTag(t, w.db, "ark-recall-result", sess)
	writeCuration(t, w.db, sess, 1, sess)

	pool := recallPool(w)
	pool.secretaries[7] = &poolSec{nonce: 7, idleSince: time.Now()}

	w.routeCuration(sess, 1)

	if pool.secretaries[7].busy {
		t.Error("a session with its own secretary must not consume a pool slot")
	}
	if len(pool.inflight) != 0 || len(pool.pending) != 0 {
		t.Errorf("pool touched: inflight=%v pending=%v", pool.inflight, pool.pending)
	}
	if len(hub.queued) != 0 {
		t.Errorf("no directive expected, got %v", hub.queued)
	}
}

// Refs: R3300, R3303 rule 2
func TestRouteCurationToFreePoolSecretary(t *testing.T) {
	hub := &fakeLuhmannHub{owner: "L"}
	w := newRoutingWatcher(t, hub)
	const sess = "sess-B"
	subscribeTag(t, w.db, "ark-recall-result", sess) // no secretary-work sub
	path := writeCuration(t, w.db, sess, 3, sess)

	pool := recallPool(w)
	pool.secretaries[7] = &poolSec{nonce: 7, idleSince: time.Now()}

	w.routeCuration(sess, 3)

	if !pool.secretaries[7].busy {
		t.Error("dispatch must stamp occupancy (R3300) or prune will retire a working secretary")
	}
	if got := pool.inflight[path]; got != 7 {
		t.Errorf("inflight[%s] = %d, want 7", path, got)
	}
	data, err := w.db.TmpContent(path)
	if err != nil {
		t.Fatalf("read routed doc: %v", err)
	}
	if !addressedTo(string(data), "L-7") {
		first, _, _ := strings.Cut(string(data), "\n")
		t.Errorf("doc address = %q, want @ark-secretary-work: L-7", first)
	}
}

// Refs: R3300 — the defect itself.
func TestPruneSparesCuratingSecretary(t *testing.T) {
	hub := &fakeLuhmannHub{owner: "L"}
	w := newRoutingWatcher(t, hub)
	pool := recallPool(w)
	stale := time.Now().Add(-700 * time.Second) // default cooldown is 600s

	// nonce 7 is serving an ambient doc; nonce 8 has been idle the same span.
	pool.secretaries[7] = &poolSec{nonce: 7, busy: true, idleSince: stale}
	pool.secretaries[8] = &poolSec{nonce: 8, idleSince: stale}

	w.prune()

	if _, alive := pool.secretaries[7]; !alive {
		t.Error("prune retired a secretary that was curating — the #90 defect")
	}
	if _, alive := pool.secretaries[8]; alive {
		t.Error("prune must still retire a genuinely idle secretary")
	}
	if len(hub.queued) != 1 {
		t.Fatalf("want exactly one stop directive, got %v", hub.queued)
	}
	if d := hub.queued[0]; d.Directive != "stop" || d.Class != recallPoolClass || d.Nonce != 8 {
		t.Errorf("directive = %+v, want stop/recall/8", d)
	}
}

// Refs: R3300
func TestFreeCurationReleasesHolder(t *testing.T) {
	hub := &fakeLuhmannHub{owner: "L"}
	w := newRoutingWatcher(t, hub)
	const sess = "sess-C"
	path := curationDocPath(sess, 5)
	pool := recallPool(w)
	pool.secretaries[7] = &poolSec{nonce: 7, busy: true}
	pool.inflight[path] = 7

	w.FreeCuration(sess, 5)

	if pool.secretaries[7].busy {
		t.Error("close must free the holder, or the pool wedges one slot per fire")
	}
	if pool.secretaries[7].idleSince.IsZero() {
		t.Error("freed secretary must enter its cooldown")
	}
	if _, still := pool.inflight[path]; still {
		t.Error("inflight entry survived the close")
	}
}

// Refs: R3301
func TestNoFreeSecretaryStandsOneUp(t *testing.T) {
	hub := &fakeLuhmannHub{owner: "L"}
	w := newRoutingWatcher(t, hub)
	const sess = "sess-D"
	subscribeTag(t, w.db, "ark-recall-result", sess)
	writeCuration(t, w.db, sess, 2, sess)

	pool := recallPool(w)
	pool.secretaries[7] = &poolSec{nonce: 7, busy: true} // one, busy; pool_max 3

	w.routeCuration(sess, 2)

	if len(hub.queued) != 1 {
		t.Fatalf("want one stand-up directive, got %v", hub.queued)
	}
	if d := hub.queued[0]; d.Directive != "stand-up" || d.Class != recallPoolClass {
		t.Errorf("directive = %+v, want stand-up for the recall class", d)
	}
	if len(pool.pending) != 1 {
		t.Errorf("doc must wait in pending, got %v", pool.pending)
	}
	if _, err := w.db.TmpContent(resultDocPath(sess, 2)); err == nil {
		t.Error("a doc awaiting a stand-up is deferral, not unservability — must not report")
	}
}

// Refs: R3304 — the boundary between deferral and unservability.
func TestFullPoolDefersWithoutReporting(t *testing.T) {
	hub := &fakeLuhmannHub{owner: "L"}
	w := newRoutingWatcher(t, hub)
	const sess = "sess-E"
	subscribeTag(t, w.db, "ark-recall-result", sess)
	writeCuration(t, w.db, sess, 4, sess)

	pool := recallPool(w)
	for i := uint64(1); i <= 3; i++ { // default pool_max
		pool.secretaries[i] = &poolSec{nonce: i, busy: true}
	}

	w.routeCuration(sess, 4)

	if len(hub.queued) != 0 {
		t.Errorf("pool is full — no stand-up expected, got %v", hub.queued)
	}
	if len(pool.pending) != 1 {
		t.Errorf("doc must be queued, got %v", pool.pending)
	}
	if _, err := w.db.TmpContent(resultDocPath(sess, 4)); err == nil {
		t.Error("a busy pool must not report unservable — it will arrive")
	}
}

// Refs: R3304
func TestNoLuhmannReportsUnservable(t *testing.T) {
	hub := &fakeLuhmannHub{owner: ""} // no seat
	w := newRoutingWatcher(t, hub)
	const sess = "sess-F"
	subscribeTag(t, w.db, "ark-recall-result", sess)
	curation := writeCuration(t, w.db, sess, 6, sess)

	w.routeCuration(sess, 6)

	data, err := w.db.TmpContent(resultDocPath(sess, 6))
	if err != nil {
		t.Fatalf("no unservable report written: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "@ark-recall-result: "+sess) {
		t.Errorf("report not addressed to the origin session:\n%s", body)
	}
	if !strings.Contains(body, "Luhmann") {
		t.Errorf("report must name the failed condition:\n%s", body)
	}
	if _, err := w.db.TmpContent(curation); err == nil {
		t.Error("an unservable curation doc must be dropped, not left pending forever")
	}
}

// Refs: R3298 — the structural claim. If classes shared slots, separate classes
// bought nothing.
func TestClassesDoNotShareSlots(t *testing.T) {
	hub := &fakeLuhmannHub{owner: "L"}
	w := newRoutingWatcher(t, hub)
	const sess = "sess-G"
	subscribeTag(t, w.db, "ark-recall-result", sess)
	writeCuration(t, w.db, sess, 8, sess)

	// The bloodhound class is saturated; the recall class is empty.
	for i := uint64(1); i <= 3; i++ {
		w.pool.secretaries[i] = &poolSec{nonce: i, busy: true}
	}

	w.routeCuration(sess, 8)

	if len(hub.queued) != 1 || hub.queued[0].Class != recallPoolClass {
		t.Fatalf("a saturated bloodhound pool must not block the recall class; got %v", hub.queued)
	}
	if len(w.pool.secretaries) != 3 {
		t.Errorf("bloodhound roster was touched: %v", w.pool.secretaries)
	}
	for _, s := range w.pool.secretaries {
		if !s.busy {
			t.Error("an ambient doc must never be given a bloodhound-class secretary")
		}
	}
}

// Refs: R3302 — dispatch matches the address, not the origin in the path.
func TestDispatchMatchesAddressNotPath(t *testing.T) {
	hub := &fakeLuhmannHub{owner: "L"}
	w := newRoutingWatcher(t, hub)
	const origin = "sess-H"
	subscribeTag(t, w.db, "ark-recall-result", origin)
	writeCuration(t, w.db, origin, 9, "L-7") // routed away from its own session

	b := NewRecallAgentBuilder(w.db)

	fire, docSession, body, ok := b.lowestPendingCuration("L-7")
	if !ok {
		t.Fatal("a pool secretary must receive the doc addressed to it")
	}
	if fire != 9 {
		t.Errorf("fire = %d, want 9", fire)
	}
	// The origin travels with the work, which is what sends the result home and
	// what selects whose conversation is injected ahead of it (R2891).
	if docSession != origin {
		t.Errorf("docSession = %q, want the origin %q", docSession, origin)
	}
	if !strings.Contains(body, "## Section") {
		t.Errorf("body not returned:\n%s", body)
	}

	// Negative: the origin's own secretary must not take back work the watcher
	// routed elsewhere.
	if _, _, _, taken := b.lowestPendingCuration(origin); taken {
		t.Error("a session's secretary took a doc addressed to a pool secretary")
	}
}

// TestStandUpDirectiveCarriesClass guards the crank handle itself. The class is
// what files a reserved nonce in the right roster, so a dictated command that
// omits it puts every secretary in the bloodhound pool and the other class is
// never served — the symptom appearing nowhere near the cause. This is the
// exact shape of the defect #90 repaired (a flag that existed, plumbed
// correctly, that no caller ever passed), so it is worth a test that fails when
// the dictation and the flag drift apart.
// Refs: R3299, R3301
func TestStandUpDirectiveCarriesClass(t *testing.T) {
	for _, class := range []string{bloodhoundPoolClass, recallPoolClass} {
		t.Run(class, func(t *testing.T) {
			body := luhmannWorkPrompt("L", LuhmannWork{
				Kind: "directive", Directive: "stand-up", Class: class,
			}, "")
			want := "reserve-nonce --luhmann --class " + class
			if !strings.Contains(body, want) {
				t.Errorf("stand-up directive must dictate %q; got:\n%s", want, body)
			}
			if !strings.Contains(body, "spawn-record --class "+class) {
				t.Errorf("spawn-record must name the class; got:\n%s", body)
			}
		})
	}
}

// TestPoolAccessorsCoverEveryClass guards the two accessors that #90 left
// reading only the bloodhound roster. Found live: `ark luhmann status` reported
// "0 pool secretary(ies)" while a recall secretary was actively curating, and a
// graceful stop wrote no exit record for it — leaving a spawn with no exit in
// the monitoring log and a ghost in the roster that miscounts pool_max and
// draws work to a dead tube (the failure R3125 exists to prevent).
//
// The class must be carried, not assumed: a nonce alone cannot say which roster
// to deregister from, and `RecordHostedExits` previously hardcoded bloodhound
// for both the record and the deregistration.
// Refs: R3298, R3125
func TestPoolAccessorsCoverEveryClass(t *testing.T) {
	w := NewRecallWatcher(nil, nil, nil, nil)
	w.pools[bloodhoundPoolClass].secretaries[3] = &poolSec{nonce: 3}
	w.pools[recallPoolClass].secretaries[7] = &poolSec{nonce: 7}

	if got := w.PoolSize(); got != 2 {
		t.Errorf("PoolSize() = %d, want 2 — a bloodhound-only read reports 1", got)
	}

	members := w.PoolNonces()
	if len(members) != 2 {
		t.Fatalf("PoolNonces() returned %d members, want 2: %+v", len(members), members)
	}
	byNonce := map[uint64]string{}
	for _, m := range members {
		byNonce[m.Nonce] = m.Class
	}
	if byNonce[3] != bloodhoundPoolClass {
		t.Errorf("nonce 3 class = %q, want %q", byNonce[3], bloodhoundPoolClass)
	}
	if byNonce[7] != recallPoolClass {
		t.Errorf("nonce 7 class = %q, want %q — the recall secretary was invisible", byNonce[7], recallPoolClass)
	}

	// Replay RecordHostedExits' loop: deregister each member against its OWN
	// class. Both rosters must end empty; hardcoding one class leaves a ghost.
	for _, m := range members {
		w.DeregisterPoolSecretary(m.Class, "exit", m.Nonce)
	}
	for class, pool := range w.pools {
		if n := len(pool.secretaries); n != 0 {
			t.Errorf("%s roster still holds %d secretary(ies) after a graceful stop", class, n)
		}
	}
}
