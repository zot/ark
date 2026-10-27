package ark

// CRC: crc-RecallWatcher.md | Test: test-BloodhoundWatermarkPool.md

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zot/microfts2"
)

// subscribeFinding registers a consumer on @ark-bloodhound-result=<session>,
// the level-3 opt-in the relay and the not-served report both check.
func subscribeFinding(t *testing.T, db *DB, session string) {
	t.Helper()
	p, err := ParseMatchSyntax("ark-bloodhound-result=" + session)
	if err != nil {
		t.Fatalf("ParseMatchSyntax: %v", err)
	}
	db.pubsub.Subscribe(session, []*TagSub{{Kind: TagSubChunk, Predicate: p}})
}

// newPoolWatcher builds a watcher over a live test DB with a seated fake hub,
// markdown registered so tmp:// docs chunk, and a live PubSub — setupRecall
// leaves db.pubsub nil, and the relay's subscriber check needs a real one
// (a nil pubsub short-circuits the gates to "present", which would hide the
// no-subscriber branch this suite asserts).
func newPoolWatcher(t *testing.T, hub *fakeLuhmannHub) *RecallWatcher {
	t.Helper()
	_, db := setupRecall(t)
	if err := db.fts.AddChunker("markdown", microfts2.MarkdownChunker{}); err != nil {
		t.Fatalf("AddChunker(markdown): %v", err)
	}
	db.SetPubSub(NewPubSub(time.Minute, 32))
	w := NewRecallWatcher(db, nil, nil, nil)
	w.luhmann = hub
	return w
}

// Refs: R3280
func TestPoolCanServe(t *testing.T) {
	cases := []struct {
		name  string
		hub   *fakeLuhmannHub
		setup func(p *secretaryPool)
		want  bool
	}{
		{"no orchestrator seated", &fakeLuhmannHub{owner: ""}, nil, false},
		{"seated, empty roster has room", &fakeLuhmannHub{owner: "L"}, nil, true},
		{
			"seated, a secretary is free", &fakeLuhmannHub{owner: "L"},
			func(p *secretaryPool) { p.secretaries[1] = &poolSec{nonce: 1} }, true,
		},
		{
			"seated, all busy but room to grow", &fakeLuhmannHub{owner: "L"},
			func(p *secretaryPool) { p.secretaries[1] = &poolSec{nonce: 1, busy: true} }, true,
		},
		{
			"seated, all busy at pool_max", &fakeLuhmannHub{owner: "L"},
			func(p *secretaryPool) {
				for i := uint64(1); i <= 3; i++ { // default pool_max is 3
					p.secretaries[i] = &poolSec{nonce: i, busy: true}
				}
			}, false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newFixerWatcher(tc.hub)
			if tc.setup != nil {
				tc.setup(w.pool)
			}
			if got := w.poolCanServe(); got != tc.want {
				t.Errorf("poolCanServe() = %v, want %v", got, tc.want)
			}
		})
	}
	// A nil hub must not panic — the watcher runs without an orchestrator.
	if NewRecallWatcher(nil, nil, nil, nil).poolCanServe() {
		t.Error("poolCanServe() with nil hub = true, want false")
	}
}

// Refs: R3280
func TestWatermarkHuntIDCannotCollideWithCLIID(t *testing.T) {
	id := watermarkHuntID("sess-A", 7)
	if !strings.HasPrefix(id, "wm-") {
		t.Errorf("watermarkHuntID = %q, want a wm- prefix", id)
	}
	if !strings.Contains(id, "sess-A") || !strings.HasSuffix(id, "-7") {
		t.Errorf("watermarkHuntID = %q, want session and bid embedded", id)
	}
	// A CLI id is a bare decimal nonce; the watermark id must never parse as one,
	// or the two origins could land on the same request-doc path.
	if _, err := strconv.ParseUint(id, 10, 64); err == nil {
		t.Errorf("watermarkHuntID = %q parses as a bare integer (CLI id space)", id)
	}
}

// Refs: R3280, R3281
func TestEnqueueWatermarkHuntWritesFixerShapedDoc(t *testing.T) {
	w := newPoolWatcher(t, &fakeLuhmannHub{owner: "L"})
	const sess, bid = "sess-A", uint64(2)
	req := bloodhoundReq{
		payload:      "where did we settle the chunker interface? pointers",
		filterFiles:  []string{"/home/x/work/ark/**"},
		excludeFiles: []string{"/home/x/.claude/projects/**"},
	}
	if err := w.enqueueWatermarkHunt(sess, bid, req); err != nil {
		t.Fatalf("enqueueWatermarkHunt: %v", err)
	}

	path := bloodhoundCLIPrefix + watermarkHuntID(sess, bid)
	data, err := w.db.TmpContent(path)
	if err != nil {
		t.Fatalf("request doc not written: %v", err)
	}
	body := string(data)
	for _, want := range []string{
		"@" + bloodhoundCLIRequestTag + ": " + watermarkHuntID(sess, bid),
		"curate: false",
		"filter-files: /home/x/work/ark/**",
		"exclude-files: /home/x/.claude/projects/**",
		"chunker interface",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("request doc missing %q; got:\n%s", want, body)
		}
	}

	// The Fixer's own readers must recover the hunt from what we wrote — this is
	// the contract that lets the pool path reuse the CLI pipeline unchanged.
	payload := stripCLIRequestTag(body)
	filter, exclude := scopeOf(payload)
	if len(filter) != 1 || filter[0] != "/home/x/work/ark/**" {
		t.Errorf("scopeOf filter = %v, want the one include glob", filter)
	}
	if len(exclude) != 1 || exclude[0] != "/home/x/.claude/projects/**" {
		t.Errorf("scopeOf exclude = %v, want the one exclude glob", exclude)
	}
	if clue := clueOf(payload); clue != req.payload {
		t.Errorf("clueOf = %q, want the clue with metadata stripped (%q)", clue, req.payload)
	}

	w.pool.mu.Lock()
	rec := w.pool.requests[path]
	pending := len(w.pool.pending)
	w.pool.mu.Unlock()
	if rec == nil {
		t.Fatal("no cliRequest recorded for the watermark hunt")
	}
	if !rec.raw || rec.origin != sess || rec.bid != bid || rec.clue != req.payload {
		t.Errorf("cliRequest = %+v, want raw with origin=%s bid=%d and the clue", rec, sess, bid)
	}
	if pending != 1 {
		t.Errorf("pending = %d, want the hunt queued", pending)
	}
}

// Refs: R3281
func TestRelayRawResultRoutesByOrigin(t *testing.T) {
	const sess, bid, id = "sess-A", uint64(3), "wm-sess-A-3"
	reqDoc := bloodhoundCLIPrefix + id
	const findings = "- notes/a.md:1-4 (2K) — the chunker interface decision"

	write := func(w *RecallWatcher) {
		t.Helper()
		body := "@" + bloodhoundCLIReturnTag + ": " + id + "\n\n## Raw findings\n\n" + findings + "\n"
		if err := SyncVoid(w.db, func(db *DB) error {
			_, e := db.AddTmpFile(reqDoc, "markdown", []byte(body))
			return e
		}); err != nil {
			t.Fatalf("seed request doc: %v", err)
		}
	}

	t.Run("watermark origin lands on the session finding channel", func(t *testing.T) {
		w := newPoolWatcher(t, &fakeLuhmannHub{owner: "L"})
		subscribeFinding(t, w.db, sess)
		write(w)
		req := &cliRequest{raw: true, origin: sess, bid: bid, clue: "where is the chunker interface?"}
		if err := w.relayRawResult(reqDoc, req); err != nil {
			t.Fatalf("relayRawResult: %v", err)
		}
		data, err := w.db.TmpContent(bloodhoundFindingPath(sess, bid))
		if err != nil {
			t.Fatalf("finding doc not written: %v", err)
		}
		got := string(data)
		for _, want := range []string{
			"@ark-bloodhound-result: " + sess,
			"## Finding: where is the chunker interface?",
			findings,
		} {
			if !strings.Contains(got, want) {
				t.Errorf("finding doc missing %q; got:\n%s", want, got)
			}
		}
		// A watermark hunt must not also wake a CLI client.
		if _, err := w.db.TmpContent(bloodhoundCLIResultPath(id)); err == nil {
			t.Error("CLI result doc written for a watermark-origin hunt")
		}
		if _, err := w.db.TmpContent(reqDoc); err == nil {
			t.Error("request doc not dropped")
		}
	})

	t.Run("empty origin keeps the CLI destination", func(t *testing.T) {
		w := newPoolWatcher(t, &fakeLuhmannHub{owner: "L"})
		write(w)
		if err := w.relayRawResult(reqDoc, &cliRequest{raw: true}); err != nil {
			t.Fatalf("relayRawResult: %v", err)
		}
		data, err := w.db.TmpContent(bloodhoundCLIResultPath(id))
		if err != nil {
			t.Fatalf("CLI result doc not written: %v", err)
		}
		if got := string(data); !strings.Contains(got, "@"+bloodhoundCLIResultTag+": "+id) ||
			!strings.Contains(got, findings) {
			t.Errorf("CLI result doc wrong; got:\n%s", got)
		}
	})

	t.Run("no subscriber writes nothing but still cleans up", func(t *testing.T) {
		w := newPoolWatcher(t, &fakeLuhmannHub{owner: "L"})
		write(w)
		req := &cliRequest{raw: true, origin: sess, bid: bid, clue: "dropped consumer"}
		if err := w.relayRawResult(reqDoc, req); err != nil {
			t.Fatalf("relayRawResult: %v", err)
		}
		if _, err := w.db.TmpContent(bloodhoundFindingPath(sess, bid)); err == nil {
			t.Error("finding doc written with no subscriber")
		}
		if _, err := w.db.TmpContent(reqDoc); err == nil {
			t.Error("request doc leaked when the consumer had dropped")
		}
	})
}

// Refs: R3283
func TestProposesTagsIsOriginDependent(t *testing.T) {
	cases := []struct {
		name string
		req  *cliRequest
		want bool
	}{
		{"nil request", nil, false},
		{"CLI origin never proposes", &cliRequest{origin: ""}, false},
		{"session-delegated proposes", &cliRequest{origin: "sess-A"}, true},
		{"watermark notags overrides", &cliRequest{origin: "sess-A", notags: true}, false},
		{"CLI origin with notags is still false", &cliRequest{origin: "", notags: true}, false},
	}
	for _, tc := range cases {
		if got := tc.req.proposesTags(); got != tc.want {
			t.Errorf("%s: proposesTags() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Refs: R3283
func TestRequestDocRecommendSurvivesClose(t *testing.T) {
	w := newPoolWatcher(t, &fakeLuhmannHub{owner: "L"})
	b := &RecallAgentBuilder{
		db:              w.db,
		bloodhounds:     make(map[string]*recallResultDoc),
		bloodhoundClues: make(map[string]string),
		results:         make(map[string]*recallResultDoc),
		curations:       make(map[string]*RecallCurationBuilder),
		cliHunts:        make(map[string]*recallResultDoc),
	}
	const id = "wm-sess-A-9"
	body := "@" + bloodhoundCLIRequestTag + ": " + id + "\n\n## Search task " + id + "\n\nfind the thing\n"
	if err := SyncVoid(w.db, func(db *DB) error {
		_, e := db.AddTmpFile(bloodhoundCLIPrefix+id, "markdown", []byte(body))
		return e
	}); err != nil {
		t.Fatalf("seed request doc: %v", err)
	}

	if err := b.FindingItem(id, "", "the thing is in store.go", ""); err != nil {
		t.Fatalf("FindingItem: %v", err)
	}
	if err := b.RecommendItem(id, "notes/a.md:1-4", "closure-actor", "same lockless pattern"); err != nil {
		t.Fatalf("RecommendItem: %v", err)
	}
	if err := b.closeCLIHunt(id, 7); err != nil {
		t.Fatalf("closeCLIHunt: %v", err)
	}

	data, err := w.db.TmpContent(bloodhoundCLIPrefix + id)
	if err != nil {
		t.Fatalf("request doc missing after close: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "the thing is in store.go") {
		t.Errorf("finding lost at close; got:\n%s", got)
	}
	// The regression this guards: without RecommendItem's isCLIHunt branch the
	// bare request id falls through to openResult, which has no in-flight
	// curation doc for it, so the verb fails and the proposal never lands.
	// Verified by reverting the branch — this assertion is what catches it.
	if !strings.Contains(got, "closure-actor") {
		t.Errorf("recommend did not reach the request doc; got:\n%s", got)
	}
}

// Refs: R3282
func TestEmitNotServedReportsRatherThanDropping(t *testing.T) {
	const sess, bid = "sess-B", uint64(1)
	req := bloodhoundReq{payload: "did we ever discuss BM25 for recall? verdict"}

	cases := []struct {
		name       string
		hub        *fakeLuhmannHub
		wantReason string
	}{
		{"no orchestrator seated", &fakeLuhmannHub{owner: ""}, "no orchestrator seated"},
		{"pool full", &fakeLuhmannHub{owner: "L"}, "full with no room to grow"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newPoolWatcher(t, tc.hub)
			subscribeFinding(t, w.db, sess)
			w.emitNotServed(sess, bid, req)

			data, err := w.db.TmpContent(bloodhoundFindingPath(sess, bid))
			if err != nil {
				t.Fatalf("not-served report missing — a dropped watermark is silent: %v", err)
			}
			got := string(data)
			for _, want := range []string{
				"@ark-bloodhound-result: " + sess,
				"## Finding: " + req.payload,
				"Not served",
				tc.wantReason,
				"ark bloodhound search --raw",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("report missing %q; got:\n%s", want, got)
				}
			}
		})
	}
}

// TestReapDeadlineIsPerOrigin pins the discriminator R3293 turns on: who is
// still waiting. A CLI request's client has exited, so it gets the generous
// configured TTL; a watermark's emitter is blocked on `listen`, so it learns
// sooner. Pure logic — no DB, no pool, no clock.
// Refs: R3041, R3293
func TestReapDeadlineIsPerOrigin(t *testing.T) {
	const cliTTL = 900 * time.Second
	cli := &cliRequest{}                  // origin "" — a CLI hunt
	mark := &cliRequest{origin: "sess-A"} // a watermark hunt

	if got := reapDeadline(cli, cliTTL); got != cliTTL {
		t.Errorf("CLI deadline = %v, want the configured TTL %v", got, cliTTL)
	}
	if got := reapDeadline(mark, cliTTL); got != bloodhoundStrandTTL {
		t.Errorf("watermark deadline = %v, want the shorter strand TTL %v", got, bloodhoundStrandTTL)
	}

	// The strand deadline must clear the re-issue threshold, or the sweep would
	// reap before the recovered stand-up it just pushed could possibly land —
	// turning a slow orchestrator into an absent one.
	if bloodhoundStrandTTL <= bloodhoundReissueThreshold {
		t.Errorf("strand TTL %v must exceed the re-issue threshold %v",
			bloodhoundStrandTTL, bloodhoundReissueThreshold)
	}

	// A configured TTL shorter than the strand deadline wins: the operator asked
	// for prompt reaping, and a watermark must never outlive a CLI request
	// submitted at the same moment.
	short := 30 * time.Second
	if got := reapDeadline(mark, short); got != short {
		t.Errorf("with a short configured TTL, watermark deadline = %v, want %v", got, short)
	}
}

// TestReapedWatermarkReports is the #84 defect itself: a hunt accepted into the
// pool and then never served must not vanish quietly. R3282 covered only the
// dispatch-time refusal, so the silence it exists to kill had merely moved to
// the reaper — harder to spot, because the hunt appeared to be accepted.
// Refs: R3293
func TestReapedWatermarkReports(t *testing.T) {
	const sess, bid = "sess-strand", uint64(7)
	w := newPoolWatcher(t, &fakeLuhmannHub{owner: "L"})
	subscribeFinding(t, w.db, sess)

	// A watermark request that was accepted long enough ago to be reaped.
	path := bloodhoundCLIPrefix + watermarkHuntID(sess, bid)
	w.pool.pending = append(w.pool.pending, path)
	w.pool.requests[path] = &cliRequest{
		submitted: time.Now().Add(-2 * bloodhoundStrandTTL),
		raw:       true,
		origin:    sess,
		bid:       bid,
		clue:      "did we ever discuss BM25 for recall?",
	}

	w.sweepRequests()

	if _, ok := w.pool.requests[path]; ok {
		t.Error("request should have been reaped")
	}
	data, err := w.db.TmpContent(bloodhoundFindingPath(sess, bid))
	if err != nil {
		t.Fatalf("no report for a reaped watermark — the session waits forever: %v", err)
	}
	got := string(data)
	for _, want := range []string{
		"@ark-bloodhound-result: " + sess,
		"## Finding: did we ever discuss BM25 for recall?",
		"Not served",
		"no secretary ever became available",
		"ark bloodhound search --raw",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q; got:\n%s", want, got)
		}
	}
	// The strand reason must not claim a dispatch-time condition: all of them
	// held when this hunt was accepted, so naming one would misdescribe it.
	for _, wrong := range []string{"no orchestrator seated", "full with no room to grow"} {
		if strings.Contains(got, wrong) {
			t.Errorf("strand report names a dispatch-time reason %q; got:\n%s", wrong, got)
		}
	}
}

// TestReapedCLIRequestReportsToNobody is the other half of R3293's
// discriminator. A CLI client hit --timeout and exited, so writing it a finding
// would leave an unread tmp doc behind for every abandoned hunt.
// Refs: R3293
func TestReapedCLIRequestReportsToNobody(t *testing.T) {
	const sess, bid = "sess-cli", uint64(3)
	w := newPoolWatcher(t, &fakeLuhmannHub{owner: "L"})
	subscribeFinding(t, w.db, sess)

	path := bloodhoundCLIPrefix + "cli-request"
	w.pool.pending = append(w.pool.pending, path)
	w.pool.requests[path] = &cliRequest{
		submitted: time.Now().Add(-2 * w.requestTTL()),
		clue:      "a CLI hunt whose caller has gone",
		bid:       bid,
	}

	w.sweepRequests()

	if _, ok := w.pool.requests[path]; ok {
		t.Error("request should have been reaped")
	}
	if _, err := w.db.TmpContent(bloodhoundFindingPath(sess, bid)); err == nil {
		t.Error("a reaped CLI request must not write a finding — nobody is listening")
	}
}

// subscribeRecall registers a consumer on @ark-recall-result=<session> — the
// ambient opt-in, and after R3294 the *only* thing ambient arming gates on.
func subscribeRecall(t *testing.T, db *DB, session string) {
	t.Helper()
	p, err := ParseMatchSyntax("ark-recall-result=" + session)
	if err != nil {
		t.Fatalf("ParseMatchSyntax: %v", err)
	}
	db.pubsub.Subscribe(session+"-recall", []*TagSub{{Kind: TagSubChunk, Predicate: p}})
}

// TestGatesAreIndependentOfTheSecretary is the assertion O133 was opened for and
// could not write until a pubsub harness existed. It pins R3279 and R3294
// together: **neither** capability consults the session secretary, so each is on
// exactly while its own result tag is subscribed.
//
// The nil-pubsub short-circuit in both helpers returns true, which is why this
// needs newPoolWatcher's live PubSub — with a nil one the test would pass
// vacuously and prove nothing.
// Refs: R3279, R3294
func TestGatesAreIndependentOfTheSecretary(t *testing.T) {
	const sess = "sess-gates"
	cases := []struct {
		name                   string
		bloodhound, recall     bool
		wantDirected, wantAmbi bool
	}{
		{"neither subscribed", false, false, false, false},
		{"bloodhound only", true, false, true, false},
		{"recall only", false, true, false, true},
		{"both", true, true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newPoolWatcher(t, &fakeLuhmannHub{owner: "L"})
			if tc.bloodhound {
				subscribeFinding(t, w.db, sess)
			}
			if tc.recall {
				subscribeRecall(t, w.db, sess)
			}
			// No secretary is ever registered in this test — that is the point.
			if w.secretaryPresent(sess) {
				t.Fatal("fixture error: no secretary should be present")
			}
			if got := w.bloodhoundEnabled(sess); got != tc.wantDirected {
				t.Errorf("bloodhoundEnabled = %v, want %v (must not consult the secretary)", got, tc.wantDirected)
			}
			if got := w.ambientEnabled(sess); got != tc.wantAmbi {
				t.Errorf("ambientEnabled = %v, want %v — after R3294 ambient arms on its own subscription alone", got, tc.wantAmbi)
			}
		})
	}
}

// The watermark doc's write fires the Fixer's request trigger like any CLI
// request. Earlier tests called the handlers by hand, so none saw the trigger
// re-record the hunt — origin dropped, path queued twice (measured live
// 2026-10-22). This one delivers the real event through the real dispatch.
// Refs: R3552, R3281
func TestFixerTriggerLeavesWatermarkHuntAsRecorded(t *testing.T) {
	hub := &fakeLuhmannHub{owner: "L"}
	w := newPoolWatcher(t, hub)
	if !w.subscribeFixer() {
		t.Fatal("subscribeFixer failed")
	}
	const sess, bid = "sess-A", uint64(5)
	req := bloodhoundReq{payload: "where does a pool-served finding land? pointers"}
	if err := w.enqueueWatermarkHunt(sess, bid, req); err != nil {
		t.Fatalf("enqueueWatermarkHunt: %v", err)
	}

	path := bloodhoundCLIPrefix + watermarkHuntID(sess, bid)
	delivered := 0
	for _, ev := range w.db.pubsub.Listen(bloodhoundCLIFixerSession, 2*time.Second) {
		if ev.Tag == bloodhoundCLIRequestTag && ev.Path == path {
			delivered++
		}
		w.dispatchFixerEvent(ev)
	}
	if delivered == 0 {
		t.Fatal("no request event reached the Fixer; the test would prove nothing")
	}

	w.pool.mu.Lock()
	rec := w.pool.requests[path]
	queued := 0
	for _, p := range w.pool.pending {
		if p == path {
			queued++
		}
	}
	w.pool.mu.Unlock()
	if rec == nil || rec.origin != sess || rec.bid != bid || rec.clue != req.payload || !rec.raw {
		t.Errorf("after the Fixer trigger, record = %+v, want raw with origin=%s bid=%d and the clue", rec, sess, bid)
	}
	if queued != 1 {
		t.Errorf("path pending %d times, want exactly once", queued)
	}
	standUps := 0
	for _, q := range hub.queued {
		if q.Kind == "directive" && q.Directive == "stand-up" {
			standUps++
		}
	}
	if standUps != 1 {
		t.Errorf("%d stand-up directives pushed, want exactly one", standUps)
	}
}

// In production the Fixer drains on its own goroutine, so its request trigger
// can run before enqueueWatermarkHunt has recorded the hunt. The guard must hold
// then too: an unrecorded watermark path is still not the Fixer's to record.
// Refs: R3552
func TestFixerTriggerIgnoresWatermarkBeforeItIsRecorded(t *testing.T) {
	hub := &fakeLuhmannHub{owner: "L"}
	w := newPoolWatcher(t, hub)
	path := bloodhoundCLIPrefix + watermarkHuntID("sess-A", 6)
	w.dispatchFixerEvent(Event{Tag: bloodhoundCLIRequestTag, Path: path})

	w.pool.mu.Lock()
	rec, pending := w.pool.requests[path], len(w.pool.pending)
	w.pool.mu.Unlock()
	if rec != nil || pending != 0 || len(hub.queued) != 0 {
		t.Errorf("Fixer trigger acted on an unrecorded watermark path: record=%+v pending=%d directives=%d",
			rec, pending, len(hub.queued))
	}
}

// subscribeSecretaryWork stands in for a session secretary's `next` loop: a
// subscriber on @ark-secretary-work=<session>, which secretaryPresent counts.
func subscribeSecretaryWork(t *testing.T, db *DB, session string) {
	t.Helper()
	p, err := ParseMatchSyntax("ark-secretary-work=" + session)
	if err != nil {
		t.Fatalf("ParseMatchSyntax: %v", err)
	}
	db.pubsub.Subscribe("secretary-"+session, []*TagSub{{Kind: TagSubChunk, Predicate: p}})
}

// Every recognized watermark leaves one default-verbosity log line naming its
// route, so a lost hunt and a slow one stop looking alike. Drives the real
// dispatchBloodhound for each route.
// Refs: R3623
func TestDispatchLogsEveryWatermarkRoute(t *testing.T) {
	const sess, bid = "sess-route", uint64(7)
	req := bloodhoundReq{payload: "which chunker owns the voice tags? pointers"}
	cases := []struct {
		name      string
		hub       *fakeLuhmannHub
		listen    bool
		secretary bool
		wantRoute string
	}{
		{"own secretary", &fakeLuhmannHub{owner: ""}, true, true, routeSecretary},
		{"pool", &fakeLuhmannHub{owner: "L"}, true, false, routePool},
		{"not served", &fakeLuhmannHub{owner: ""}, true, false, routeNotServed + "no secretary"},
		{"listener gone", &fakeLuhmannHub{owner: "L"}, false, false, routeListenerGone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setTestHome(t, t.TempDir()) // the builder writes under ~/.ark/monitoring
			lib, db := setupRecall(t)   // the secretary route seeds through the librarian
			if err := db.fts.AddChunker("markdown", microfts2.MarkdownChunker{}); err != nil {
				t.Fatalf("AddChunker(markdown): %v", err)
			}
			db.SetPubSub(NewPubSub(time.Minute, 32))
			w := NewRecallWatcher(db, lib, nil, NewRecallAgentBuilder(db))
			w.luhmann = tc.hub
			if tc.listen {
				subscribeFinding(t, w.db, sess)
			}
			if tc.secretary {
				subscribeSecretaryWork(t, w.db, sess)
			}
			logs := captureCurationLog(t)
			w.dispatchBloodhound(sess, bid, req)

			want := "watermark session=" + sess + " B=7 clue=\"which chunker owns the voice tags? pointers\" route=" + tc.wantRoute
			if n := strings.Count(logs.String(), "recall-watcher: watermark session="); n != 1 || !strings.Contains(logs.String(), want) {
				t.Errorf("want exactly one route line containing %q; log:\n%s", want, logs.String())
			}
		})
	}
}

// A watermark in a session nobody is listening to is logged as seen and not
// recognized: no hunt id, no dispatch. An append with no watermark logs nothing.
// Through OnAppend, so the gate's early return is what is under test.
// Refs: R3624
func TestUnheardWatermarkIsLoggedNotRecognized(t *testing.T) {
	const sess = "sess-unheard"
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"<BLOODHOUND>did we discuss BM25? verdict</BLOODHOUND>"}]}}` + "\n"
	db := newWatcherTestDB(RecallConfig{Enabled: true})
	db.SetPubSub(NewPubSub(time.Minute, 32)) // real and empty: nobody is listening
	w := NewRecallWatcher(db, nil, nil, nil)
	logs := captureCurationLog(t)
	path := "/p/" + sess + ".jsonl"

	w.OnAppend(path, "chat-jsonl", []byte(`{"type":"assistant","message":{"content":"just chatting"}}`+"\n"), nil)
	if logs.Len() != 0 {
		t.Fatalf("an append without a watermark logged:\n%s", logs.String())
	}
	w.OnAppend(path, "chat-jsonl", []byte(line), nil)
	got := logs.String()
	if !strings.Contains(got, "session="+sess) || !strings.Contains(got, `clue="did we discuss BM25? verdict"`) || !strings.Contains(got, "no listener") {
		t.Errorf("unheard watermark not logged with session and clue:\n%s", got)
	}
	if strings.Contains(got, "B=") {
		t.Errorf("an unheard watermark must not get a hunt id:\n%s", got)
	}
	if n := len(w.bloodhoundCounters); n != 0 {
		t.Errorf("logging allocated %d hunt counters; logging is not recognition", n)
	}
}
