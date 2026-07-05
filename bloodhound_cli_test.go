package ark

// CRC: crc-RecallWatcher.md | Test: test-BloodhoundCLIFixer.md

import (
	"strings"
	"testing"
	"time"
)

// fakeLuhmannHub is a test double for the S1 orchestrator-seat bridge: a fixed
// owner and a captured queue, so the Fixer's routing decisions are observable
// without a live Server.
type fakeLuhmannHub struct {
	owner  string
	queued []LuhmannWork
	full   bool
}

func (f *fakeLuhmannHub) LuhmannOwner() string { return f.owner }
func (f *fakeLuhmannHub) EnqueueLuhmann(w LuhmannWork) bool {
	if f.full {
		return false
	}
	f.queued = append(f.queued, w)
	return true
}

// newFixerWatcher builds a RecallWatcher with a fake hub and no DB. Scenarios
// must avoid the pending+free coincidence so enhanceRequestDoc (the only
// DB-touching path) is never reached.
func newFixerWatcher(hub *fakeLuhmannHub) *RecallWatcher {
	return &RecallWatcher{pool: newCLIPool(), luhmann: hub}
}

func intp(n int) *int { return &n }

// Refs: R3017, R3018
func TestPoolConfigDefaultsAndOverrides(t *testing.T) {
	var empty LuhmannConfig
	if got := empty.EffectivePoolMax("bloodhound"); got != 3 {
		t.Errorf("default pool_max = %d, want 3", got)
	}
	if got := empty.EffectiveCooldownSeconds("bloodhound"); got != 120 {
		t.Errorf("default cooldown = %d, want 120", got)
	}
	set := LuhmannConfig{Classes: map[string]LuhmannClass{
		"bloodhound": {PoolMax: intp(5), CooldownSeconds: intp(30)},
	}}
	if got := set.EffectivePoolMax("bloodhound"); got != 5 {
		t.Errorf("pool_max override = %d, want 5", got)
	}
	if got := set.EffectiveCooldownSeconds("bloodhound"); got != 30 {
		t.Errorf("cooldown override = %d, want 30", got)
	}
}

// Refs: R3011, R3024
func TestEnqueueLuhmannNonBlockingWhenFull(t *testing.T) {
	srv := &Server{nextQueue: make(chan LuhmannWork, 1)}
	if !srv.EnqueueLuhmann(LuhmannWork{Kind: "curation", Path: "a"}) {
		t.Fatal("first enqueue should succeed")
	}
	if srv.EnqueueLuhmann(LuhmannWork{Kind: "curation", Path: "b"}) {
		t.Fatal("second enqueue on a full queue must return false, not block")
	}
}

// Refs: R3020
func TestCLIRequestGatedWithoutLuhmann(t *testing.T) {
	hub := &fakeLuhmannHub{owner: ""}
	w := newFixerWatcher(hub)
	w.onBloodhoundCLIRequest("tmp://BLOODHOUND-CLI/x1")
	if len(hub.queued) != 0 {
		t.Errorf("queued %d items, want 0 (no orchestrator)", len(hub.queued))
	}
	if len(w.pool.pending) != 0 {
		t.Errorf("pending %d, want 0 (gated before enqueue)", len(w.pool.pending))
	}
}

// Refs: R3023
func TestCLIRequestStandsUpWhenNoSecretary(t *testing.T) {
	hub := &fakeLuhmannHub{owner: "S"}
	w := newFixerWatcher(hub)
	w.onBloodhoundCLIRequest("tmp://BLOODHOUND-CLI/x2")
	if len(w.pool.pending) != 1 || w.pool.pending[0] != "tmp://BLOODHOUND-CLI/x2" {
		t.Errorf("pending = %v, want [the request path]", w.pool.pending)
	}
	if len(hub.queued) != 1 {
		t.Fatalf("queued %d, want 1 stand-up directive", len(hub.queued))
	}
	got := hub.queued[0]
	if got.Kind != "directive" || got.Directive != "stand-up" || got.Class != "bloodhound" {
		t.Errorf("directive = %+v, want stand-up/bloodhound", got)
	}
}

// Refs: R3024, R3025
func TestCLIReturnFreesAndRoutesToCuration(t *testing.T) {
	hub := &fakeLuhmannHub{owner: "S"}
	w := newFixerWatcher(hub)
	const path = "tmp://BLOODHOUND-CLI/x3"
	// Pre-seed a busy secretary running this hunt.
	w.pool.secretaries[7] = &poolSec{nonce: 7, busy: true}
	w.pool.inflight[path] = 7

	w.onBloodhoundCLIReturn(path)

	if w.pool.secretaries[7].busy {
		t.Error("secretary should be freed on return, before curation")
	}
	if _, still := w.pool.inflight[path]; still {
		t.Error("inflight should no longer hold the returned path")
	}
	if w.pool.secretaries[7].idleSince.IsZero() {
		t.Error("cooldown clock (idleSince) should be stamped on free")
	}
	if len(hub.queued) != 1 || hub.queued[0].Kind != "curation" || hub.queued[0].Path != path {
		t.Errorf("queued = %+v, want one curation task carrying the path", hub.queued)
	}
}

// Refs: R3033
func TestRegisterPoolSecretaryIdempotent(t *testing.T) {
	hub := &fakeLuhmannHub{owner: "S"}
	w := newFixerWatcher(hub)
	w.RegisterPoolSecretary(42)
	w.RegisterPoolSecretary(42) // idempotent
	if len(w.pool.secretaries) != 1 {
		t.Fatalf("secretaries = %d, want 1 (idempotent)", len(w.pool.secretaries))
	}
	sec := w.pool.secretaries[42]
	if sec == nil || sec.busy {
		t.Errorf("registered secretary = %+v, want present and free", sec)
	}
}

// Refs: R3019
func TestPruneRetiresIdlePastCooldown(t *testing.T) {
	hub := &fakeLuhmannHub{owner: "S"}
	w := newFixerWatcher(hub)                                                                             // db nil → cooldown default 120s
	w.pool.secretaries[1] = &poolSec{nonce: 1, idleSince: time.Now().Add(-200 * time.Second)}             // past cooldown
	w.pool.secretaries[2] = &poolSec{nonce: 2, idleSince: time.Now()}                                     // within cooldown
	w.pool.secretaries[3] = &poolSec{nonce: 3, busy: true, idleSince: time.Now().Add(-200 * time.Second)} // busy

	w.prune()

	if _, ok := w.pool.secretaries[1]; ok {
		t.Error("idle-past-cooldown secretary should be pruned")
	}
	if _, ok := w.pool.secretaries[2]; !ok {
		t.Error("within-cooldown secretary must not be pruned")
	}
	if _, ok := w.pool.secretaries[3]; !ok {
		t.Error("busy secretary must not be pruned")
	}
	if len(hub.queued) != 1 || hub.queued[0].Directive != "stop" || hub.queued[0].Nonce != 1 || hub.queued[0].Class != "bloodhound" {
		t.Errorf("queued = %+v, want one stop directive for nonce 1", hub.queued)
	}
}

// TestBloodhoundCLIOpenTagsWithColon guards the colon bug: a colon-less head tag
// (`@ark-bloodhound-cli`) is never extracted/published, so the watcher never
// wakes. The tag MUST be `@ark-bloodhound-cli: <id>`. Also checks the result-tag
// subscription lands before the doc (R3021).
// Refs: R3021, R3028
func TestBloodhoundCLIOpenTagsWithColon(t *testing.T) {
	_, db, _ := setupConnections(t)
	b := NewRecallAgentBuilder(db)
	id, err := b.BloodhoundCLIOpen("clue: x\nscope: all\ndepth: lookup\nwant: passages\n")
	if err != nil {
		t.Fatal(err)
	}
	data, err := db.TmpContent(bloodhoundCLIPrefix + id)
	if err != nil {
		t.Fatalf("request doc not created: %v", err)
	}
	first := strings.SplitN(string(data), "\n", 2)[0]
	if !strings.HasPrefix(first, "@"+bloodhoundCLIRequestTag+": ") {
		t.Errorf("head tag = %q, want %q + colon + id (colon-less tags never publish)", first, "@"+bloodhoundCLIRequestTag)
	}
	if db.pubsub.SubscriberCount(bloodhoundCLIResultTag, id) == 0 {
		t.Errorf("result-tag subscription (%s=%s) should exist before the doc lands", bloodhoundCLIResultTag, id)
	}
}

// TestBloodhoundEnhanceReTagsInPlace guards the add-vs-update bug: the enhance
// must UpdateTmpFile the existing request doc, not AddTmpFile it (which fails
// "file already indexed"). The baton flips the head tag to @ark-secretary-work.
// Refs: R3031, R3032
func TestBloodhoundEnhanceReTagsInPlace(t *testing.T) {
	l, db, _ := setupConnections(t)
	b := NewRecallAgentBuilder(db)
	w := NewRecallWatcher(db, l, db.store, b)
	id, err := b.BloodhoundCLIOpen("clue: x\nscope: all\n")
	if err != nil {
		t.Fatal(err)
	}
	path := bloodhoundCLIPrefix + id
	if err := w.enhanceRequestDoc(path, "L-9"); err != nil {
		t.Fatalf("enhanceRequestDoc must overwrite in place, not re-add: %v", err)
	}
	data, _ := db.TmpContent(path)
	first := strings.SplitN(string(data), "\n", 2)[0]
	if first != "@ark-secretary-work: L-9" {
		t.Errorf("re-tagged head = %q, want '@ark-secretary-work: L-9' (baton flip)", first)
	}
}

// Refs: R3022
func TestPoolBusy(t *testing.T) {
	hub := &fakeLuhmannHub{owner: "S"}
	w := newFixerWatcher(hub) // db nil → poolMax default 3
	if w.PoolBusy() {
		t.Error("empty pool has room — not busy")
	}
	for i := uint64(1); i <= 3; i++ {
		w.pool.secretaries[i] = &poolSec{nonce: i, busy: true}
	}
	if !w.PoolBusy() {
		t.Error("pool at max with all busy should be busy")
	}
	w.pool.secretaries[1].busy = false
	if w.PoolBusy() {
		t.Error("a free secretary means not busy")
	}
}

// Refs: R3019
func TestPruneNoOpWithoutLuhmann(t *testing.T) {
	hub := &fakeLuhmannHub{owner: ""}
	w := newFixerWatcher(hub)
	w.pool.secretaries[1] = &poolSec{nonce: 1, idleSince: time.Now().Add(-200 * time.Second)}
	w.prune()
	if _, ok := w.pool.secretaries[1]; !ok {
		t.Error("prune must not retire without a live Luhmann to receive the stop")
	}
	if len(hub.queued) != 0 {
		t.Error("prune must queue nothing without a live Luhmann")
	}
}
