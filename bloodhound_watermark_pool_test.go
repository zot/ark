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
	return &RecallWatcher{db: db, pool: newCLIPool(), luhmann: hub}
}

// Refs: R3280
func TestPoolCanServe(t *testing.T) {
	cases := []struct {
		name  string
		hub   *fakeLuhmannHub
		setup func(p *cliPool)
		want  bool
	}{
		{"no orchestrator seated", &fakeLuhmannHub{owner: ""}, nil, false},
		{"seated, empty roster has room", &fakeLuhmannHub{owner: "L"}, nil, true},
		{
			"seated, a secretary is free", &fakeLuhmannHub{owner: "L"},
			func(p *cliPool) { p.secretaries[1] = &poolSec{nonce: 1} }, true,
		},
		{
			"seated, all busy but room to grow", &fakeLuhmannHub{owner: "L"},
			func(p *cliPool) { p.secretaries[1] = &poolSec{nonce: 1, busy: true} }, true,
		},
		{
			"seated, all busy at pool_max", &fakeLuhmannHub{owner: "L"},
			func(p *cliPool) {
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
	if (&RecallWatcher{pool: newCLIPool()}).poolCanServe() {
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
