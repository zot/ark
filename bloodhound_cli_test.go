package ark

// CRC: crc-RecallWatcher.md | Test: test-BloodhoundCLIFixer.md

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

// --- S4: secretary CLI-hunt close + Luhmann's `bloodhound add` stencil ---

// TestCLIHuntFindingRoutesByNamespace confirms the pool secretary's bare-id
// cookie routes `finding` to the CLI-hunt accumulator only when a live request
// doc matches, else it is rejected (R3025).
// Refs: R3025
func TestCLIHuntFindingRoutesByNamespace(t *testing.T) {
	_, db, _ := setupConnections(t)
	b := NewRecallAgentBuilder(db)
	b.monitorPath = filepath.Join(t.TempDir(), "recall.jsonl")
	id, err := b.BloodhoundCLIOpen("clue: x\nscope: all\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.FindingItem(id, "specs/foo.md:1-5", "", "matches"); err != nil {
		t.Fatalf("finding with a live CLI request id should be accepted: %v", err)
	}
	if err := b.FindingItem("999", "specs/foo.md:1-5", "", "x"); err == nil {
		t.Error("finding with a bare id and no request doc must be rejected")
	}
}

// TestCLIHuntCloseRetagsRequestDoc guards the whole secretary-close path: the
// request doc is re-tagged @ark-bloodhound-cli-return: <id> WITH COLON (the S3
// regression), raw findings are appended, and the secretary-facing seed + crank
// handle are cut (R3025, R3031).
// Refs: R3025, R3031
func TestCLIHuntCloseRetagsRequestDoc(t *testing.T) {
	// Neutralize the harness's ambient CLAUDE_CODE_SESSION_ID so close-time
	// subagent-JSONL discovery early-returns (as in a normal `go test` run),
	// rather than probing the test db's unset config.
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	l, db, _ := setupConnections(t)
	b := NewRecallAgentBuilder(db)
	b.monitorPath = filepath.Join(t.TempDir(), "recall.jsonl")
	w := NewRecallWatcher(db, l, db.store, b)
	id, err := b.BloodhoundCLIOpen("clue: needle\nscope: all\n")
	if err != nil {
		t.Fatal(err)
	}
	path := bloodhoundCLIPrefix + id
	if err := w.enhanceRequestDoc(path, "L-1"); err != nil {
		t.Fatal(err)
	}
	if err := b.FindingItem(id, "specs/foo.md:10-20", "", "the needle is here"); err != nil {
		t.Fatal(err)
	}
	if err := b.CloseResult(id, 0, false); err != nil {
		t.Fatalf("CLI-hunt close: %v", err)
	}
	data, err := db.TmpContent(path)
	if err != nil {
		t.Fatalf("request doc should still exist (re-tagged, not removed): %v", err)
	}
	s := string(data)
	if first := strings.SplitN(s, "\n", 2)[0]; first != "@"+bloodhoundCLIReturnTag+": "+id {
		t.Errorf("head tag = %q, want @%s: %s (WITH COLON)", first, bloodhoundCLIReturnTag, id)
	}
	if !strings.Contains(s, "## Raw findings") || !strings.Contains(s, "specs/foo.md:10-20") {
		t.Errorf("body missing raw findings section/loc:\n%s", s)
	}
	if strings.Contains(s, "You are the bloodhound") || strings.Contains(s, "## Recall seed") {
		t.Errorf("secretary seed/crank handle should be cut from the curation view:\n%s", s)
	}
}

// TestBloodhoundCLIAddStencilFlipsResultTag confirms the add stencil accumulates
// one JSON line per call and the terminal --done writes the result doc tagged
// @ark-bloodhound-cli-result: <id> WITH COLON, then removes the request doc
// (R3027, R3028).
// Refs: R3027, R3028
func TestBloodhoundCLIAddStencilFlipsResultTag(t *testing.T) {
	_, db, _ := setupConnections(t)
	b := NewRecallAgentBuilder(db)
	id, err := b.BloodhoundCLIOpen("clue: x\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.BloodhoundCLIAdd(id, "specs/a.md:1-2", "first", "excerpt one"); err != nil {
		t.Fatal(err)
	}
	if err := b.BloodhoundCLIAdd(id, "specs/b.md:3-4", "second", ""); err != nil {
		t.Fatal(err)
	}
	if err := b.BloodhoundCLIAddDone(id); err != nil {
		t.Fatal(err)
	}
	data, err := db.TmpContent(bloodhoundCLIResultPath(id))
	if err != nil {
		t.Fatalf("result doc not created: %v", err)
	}
	s := string(data)
	if first := strings.SplitN(s, "\n", 2)[0]; first != "@"+bloodhoundCLIResultTag+": "+id {
		t.Errorf("result head tag = %q, want @%s: %s (WITH COLON)", first, bloodhoundCLIResultTag, id)
	}
	lines := strings.Split(strings.TrimRight(stripLeadingTag(s), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 JSONL lines, got %d:\n%s", len(lines), s)
	}
	var f cliFinding
	if err := json.Unmarshal([]byte(lines[0]), &f); err != nil {
		t.Fatalf("line 0 not JSON: %q (%v)", lines[0], err)
	}
	if f.Path != "specs/a.md" || f.Range != "1-2" || f.Note != "first" || f.Chunk != "excerpt one" {
		t.Errorf("line 0 = %+v, want specs/a.md:1-2/first/excerpt one", f)
	}
	if _, err := db.TmpContent(bloodhoundCLIPrefix + id); err == nil {
		t.Error("request doc should be removed after --done")
	}
}

// TestBloodhoundCLIResultStripsHeadTag drives the real subscribe→publish→listen
// path and confirms BloodhoundCLIResult returns pure JSONL — the head tag is doc
// metadata, not output (R3029).
// Refs: R3029
func TestBloodhoundCLIResultStripsHeadTag(t *testing.T) {
	_, db, _ := setupConnections(t)
	b := NewRecallAgentBuilder(db)
	id, err := b.BloodhoundCLIOpen("clue: x\n") // subscribes to the result tag
	if err != nil {
		t.Fatal(err)
	}
	if err := b.BloodhoundCLIAdd(id, "specs/a.md:1-2", "first", ""); err != nil {
		t.Fatal(err)
	}
	if err := b.BloodhoundCLIAddDone(id); err != nil { // publishes the result tag
		t.Fatal(err)
	}
	jsonl, ok, err := b.BloodhoundCLIResult(context.Background(), id, 2*time.Second)
	if err != nil || !ok {
		t.Fatalf("result: ok=%v err=%v", ok, err)
	}
	if strings.HasPrefix(jsonl, "@") {
		t.Errorf("result should be pure JSONL, got a head tag:\n%s", jsonl)
	}
	var f cliFinding
	if err := json.Unmarshal([]byte(strings.TrimRight(jsonl, "\n")), &f); err != nil || f.Path != "specs/a.md" {
		t.Errorf("jsonl = %q parsed %+v err %v", jsonl, f, err)
	}
}

// TestDeregisterPoolSecretaryOnExit confirms a terminal exit for the bloodhound
// class drops the secretary from the roster (and its inflight entry), while a
// wrong class or a non-terminal kind is a no-op — the symmetric counterpart to
// RegisterPoolSecretary (R3034).
// Refs: R3034
func TestDeregisterPoolSecretaryOnExit(t *testing.T) {
	hub := &fakeLuhmannHub{owner: "S"}
	w := newFixerWatcher(hub)
	w.pool.secretaries[7] = &poolSec{nonce: 7}
	w.pool.secretaries[8] = &poolSec{nonce: 8, busy: true}
	w.pool.inflight["tmp://BLOODHOUND-CLI/x"] = 7

	// Wrong class → no-op.
	w.DeregisterPoolSecretary("recall", "exit", 7)
	if _, ok := w.pool.secretaries[7]; !ok {
		t.Error("wrong class must not deregister")
	}
	// Non-terminal kind → no-op.
	w.DeregisterPoolSecretary("bloodhound", "spawn", 7)
	if _, ok := w.pool.secretaries[7]; !ok {
		t.Error("a spawn record must not deregister")
	}
	// Terminal context-limit exit → removes the secretary and its inflight entry.
	w.DeregisterPoolSecretary("bloodhound", "exit", 7)
	if _, ok := w.pool.secretaries[7]; ok {
		t.Error("a context-limit exit should deregister the secretary")
	}
	if _, ok := w.pool.inflight["tmp://BLOODHOUND-CLI/x"]; ok {
		t.Error("the exited secretary's inflight entry should be dropped")
	}
	if _, ok := w.pool.secretaries[8]; !ok {
		t.Error("a different secretary must be untouched")
	}
	// Idempotent (already removed) and the crash kind also deregisters.
	w.DeregisterPoolSecretary("bloodhound", "crash", 7) // no-op, no panic
	w.DeregisterPoolSecretary("bloodhound", "crash", 8)
	if _, ok := w.pool.secretaries[8]; ok {
		t.Error("a crash exit should deregister too")
	}
}

// TestExitRecordDeregistersPoolSecretary is the wiring Sentry: a bloodhound
// `exit-record` POST to /luhmann/record must reach DeregisterPoolSecretary with
// the right (class, kind, nonce) — the S3 lesson that a mis-passed field slips
// past a mechanism-only test (R3033/R3034).
// Refs: R3034
func TestExitRecordDeregistersPoolSecretary(t *testing.T) {
	l, db, _ := setupConnections(t)
	b := NewRecallAgentBuilder(db)
	w := NewRecallWatcher(db, l, db.store, b)
	srv := &Server{db: db, recallWatcher: w}
	w.RegisterPoolSecretary(5)
	if _, ok := w.pool.secretaries[5]; !ok {
		t.Fatal("secretary 5 should be registered")
	}

	body := strings.NewReader(`{"kind":"exit","class":"bloodhound","nonce":5,"reason":"context-limit"}`)
	rec := httptest.NewRecorder()
	srv.handleLuhmannRecord(rec, httptest.NewRequest(http.MethodPost, "/luhmann/record", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("record POST = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if _, ok := w.pool.secretaries[5]; ok {
		t.Error("a bloodhound exit-record must deregister the pool secretary")
	}
}

// TestBloodhoundCLIEmptyHunt confirms a --done with no adds yields an empty
// result body: the CLI prints no lines and exits 0 (R3029).
// Refs: R3029
func TestBloodhoundCLIEmptyHunt(t *testing.T) {
	_, db, _ := setupConnections(t)
	b := NewRecallAgentBuilder(db)
	id, err := b.BloodhoundCLIOpen("clue: x\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.BloodhoundCLIAddDone(id); err != nil {
		t.Fatal(err)
	}
	jsonl, ok, err := b.BloodhoundCLIResult(context.Background(), id, 2*time.Second)
	if err != nil || !ok {
		t.Fatalf("result: ok=%v err=%v", ok, err)
	}
	if strings.TrimSpace(jsonl) != "" {
		t.Errorf("empty hunt should yield no JSONL lines, got:\n%q", jsonl)
	}
}
