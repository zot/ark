package ark

// CRC: crc-LuhmannEvents.md | Test: test-LuhmannEvents.md

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// --- #35 event-tube unification: `ark luhmann events` routing + the /wait gate ---
//
// The routing state machine and the gate are pure decision logic over one
// mutex-guarded pair of fields, so they run against a bare &Server{} — no DB, no
// listener, no live UI. The pump tests mount a fake /wait handler on a throwaway
// mux: the same seam the real pump reads, so no HTTP client or socket is needed.

// newPumpServer returns a Server whose uiMux serves /wait from `handler`, with a
// tube deep enough for the batch under test.
func newPumpServer(queueCap int, handler http.HandlerFunc) *Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/wait", handler)
	return &Server{uiMux: mux, nextQueue: make(chan LuhmannWork, queueCap)}
}

// TestLuhmannEventsSeatGate: routing is a privilege of the `next` seat, not a
// second identity — a non-owner and an unowned seat both get R3013's stand-down
// string, and neither leaves routing behind.
// Refs: R3145
func TestLuhmannEventsSeatGate(t *testing.T) {
	cases := []struct {
		name    string
		owner   string // seat owner before the call
		session string
	}{
		{"foreign-session-refused", "A", "B"},
		{"unowned-seat-refused", "", "A"},
		{"empty-session-refused", "A", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := &Server{luhmannOwner: tc.owner}
			err := srv.LuhmannEvents(tc.session, false)
			if err == nil || !strings.Contains(err.Error(), luhmannErrNoOwnership) {
				t.Fatalf("err = %v, want %q", err, luhmannErrNoOwnership)
			}
			if got := srv.EventOwner(); got != "" {
				t.Errorf("EventOwner() = %q, want \"\" (no routing granted)", got)
			}
			if srv.eventPumpCancel != nil {
				t.Error("a pump was started for a refused opt-in")
			}
		})
	}
}

// TestLuhmannEventsClaimAndRelease: the seat owner claims routing (one pump),
// re-claiming is idempotent (a second pump would split the destructive drain),
// and --off releases and cancels.
// Refs: R3145, R3146
func TestLuhmannEventsClaimAndRelease(t *testing.T) {
	// Each live pump parks in the handler, so one signal per call counts readers.
	calls := make(chan struct{}, 4)
	srv := newPumpServer(1, func(w http.ResponseWriter, r *http.Request) {
		calls <- struct{}{}
		<-r.Context().Done() // park: this test is about ownership, not delivery
	})
	srv.luhmannOwner = "A"

	if err := srv.LuhmannEvents("A", false); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if got := srv.EventOwner(); got != "A" {
		t.Fatalf("EventOwner() = %q, want \"A\"", got)
	}
	if srv.eventPumpCancel == nil {
		t.Fatal("claim started no pump")
	}
	select {
	case <-calls:
	case <-time.After(2 * time.Second):
		t.Fatal("the claimed pump never read /wait")
	}

	// Idempotent: the same owner re-asking must not start a second reader — two
	// readers would split the destructive drain, the exact harm R3146 prevents.
	if err := srv.LuhmannEvents("A", false); err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	if srv.eventOwner != "A" {
		t.Errorf("eventOwner = %q after re-claim, want \"A\"", srv.eventOwner)
	}
	select {
	case <-calls:
		t.Fatal("re-claim started a second reader")
	case <-time.After(200 * time.Millisecond):
	}

	if err := srv.LuhmannEvents("A", true); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := srv.EventOwner(); got != "" {
		t.Errorf("EventOwner() = %q after --off, want \"\"", got)
	}
	if srv.eventPumpCancel != nil {
		t.Error("eventPumpCancel not cleared on --off")
	}
}

// TestEventRoutingNoInheritance: routing belongs to the session that asked, not
// to the seat — a different session taking the seat clears it in the same locked
// step, while the same session re-claiming keeps it.
// Refs: R3148
func TestEventRoutingNoInheritance(t *testing.T) {
	cases := []struct {
		name      string
		mode      luhmannNextMode
		claimant  string
		wantOwner string // event-routing owner after the seat claim
	}{
		{"force-by-other-clears", luhmannModeForce, "B", ""},
		{"first-by-self-keeps", luhmannModeFirst, "A", "A"},
		{"force-by-self-keeps", luhmannModeForce, "A", "A"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newPumpServer(1, func(w http.ResponseWriter, r *http.Request) {
				<-r.Context().Done()
			})
			srv.luhmannOwner = "A"
			if err := srv.LuhmannEvents("A", false); err != nil {
				t.Fatalf("claim: %v", err)
			}

			srv.claimLuhmann(tc.claimant, tc.mode)

			if got := srv.EventOwner(); got != tc.wantOwner {
				t.Errorf("EventOwner() = %q, want %q", got, tc.wantOwner)
			}
			if tc.wantOwner == "" && srv.eventPumpCancel != nil {
				t.Error("pump left running after the seat changed hands")
			}
			if tc.wantOwner != "" && srv.eventPumpCancel == nil {
				t.Error("pump cancelled on a same-session re-claim")
			}
		})
	}
}

// TestGateFrictionlessWait: the single-reader invariant is enforced at the door
// — /wait is refused while routing is owned, passes through when it isn't, and
// no other path is ever touched.
// Refs: R3146
func TestGateFrictionlessWait(t *testing.T) {
	cases := []struct {
		name       string
		owner      string
		path       string
		wantInner  bool
		wantStatus int
	}{
		{"wait-refused-while-routed", "A", "/wait", false, http.StatusConflict},
		{"wait-passes-when-unrouted", "", "/wait", true, http.StatusOK},
		{"state-untouched-while-routed", "A", "/state", true, http.StatusOK},
		{"api-untouched-while-routed", "A", "/api/ui_run", true, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			innerCalled := false
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				innerCalled = true
				w.WriteHeader(http.StatusOK)
			})
			srv := &Server{eventOwner: tc.owner}
			req, err := http.NewRequest(http.MethodGet, "http://ark"+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			rec := &eventRecorder{status: http.StatusOK}
			srv.gateFrictionlessWait(inner).ServeHTTP(rec, req)

			if innerCalled != tc.wantInner {
				t.Errorf("inner called = %v, want %v", innerCalled, tc.wantInner)
			}
			if rec.status != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.status, tc.wantStatus)
			}
			if tc.wantStatus == http.StatusConflict && !strings.Contains(string(rec.body), tc.owner) {
				t.Errorf("409 body %q does not name the owner %q", rec.body, tc.owner)
			}
		})
	}
}

// TestEventPumpFansOutBatch: one 200 batch becomes one tube item per event, in
// order, each carrying that event's raw JSON.
// Refs: R3147
func TestEventPumpFansOutBatch(t *testing.T) {
	calls := make(chan struct{}, 4)
	srv := newPumpServer(4, func(w http.ResponseWriter, r *http.Request) {
		select {
		case calls <- struct{}{}:
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"a":1},{"b":2}]`))
		default:
			<-r.Context().Done() // one batch only, then park
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.eventPump(ctx)

	for _, want := range []string{`{"a":1}`, `{"b":2}`} {
		select {
		case w := <-srv.nextQueue:
			if w.Kind != "frictionless-event" {
				t.Errorf("Kind = %q, want \"frictionless-event\"", w.Kind)
			}
			if w.Event != want {
				t.Errorf("Event = %q, want %q", w.Event, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for %s", want)
		}
	}
}

// TestEventPumpWaitConditions: 204 (idle) and 404 (no UI session yet) are both
// wait conditions — the pump enqueues nothing and keeps reading rather than
// exiting, so an event arriving later still lands.
// Refs: R3147
func TestEventPumpWaitConditions(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"idle-timeout", http.StatusNoContent, ""},
		{"no-ui-session", http.StatusNotFound, "No active session\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var n int
			srv := newPumpServer(2, func(w http.ResponseWriter, r *http.Request) {
				n++
				switch n {
				case 1:
					w.WriteHeader(tc.status)
					w.Write([]byte(tc.body))
				case 2:
					w.Write([]byte(`[{"a":1}]`))
				default:
					<-r.Context().Done()
				}
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go srv.eventPump(ctx)

			select {
			case w := <-srv.nextQueue:
				if w.Event != `{"a":1}` {
					t.Errorf("Event = %q, want the event after the wait condition", w.Event)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("pump stopped reading after a wait condition")
			}
		})
	}
}

// TestEventPumpExitsOnCancel: pump lifetime is bounded by routing ownership.
// Refs: R3145, R3148
func TestEventPumpExitsOnCancel(t *testing.T) {
	srv := newPumpServer(1, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		w.WriteHeader(http.StatusNoContent)
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); srv.eventPump(ctx) }()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pump did not exit on cancel")
	}
}

// TestEventPumpFullQueueDoesNotBlock: EnqueueLuhmann is non-blocking by contract
// (R3024), so a stalled orchestrator with a full tube cannot wedge the reader.
// Refs: R3147
func TestEventPumpFullQueueDoesNotBlock(t *testing.T) {
	calls := make(chan struct{}, 8)
	srv := newPumpServer(1, func(w http.ResponseWriter, r *http.Request) {
		select {
		case calls <- struct{}{}:
			w.Write([]byte(`[{"a":1},{"b":2},{"c":3}]`))
		default:
			<-r.Context().Done()
		}
	})
	srv.nextQueue <- LuhmannWork{Kind: "curation"} // tube already full (cap 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); srv.eventPump(ctx) }()

	// The pump must come back for another /wait rather than block on the tube.
	select {
	case <-calls:
	case <-time.After(2 * time.Second):
		t.Fatal("pump wedged on a full tube")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pump did not exit")
	}
}

// TestEventPumpUndecodableBatch: a malformed /wait body is dropped, not fatal —
// the pump keeps reading.
// Refs: R3147
func TestEventPumpUndecodableBatch(t *testing.T) {
	var n int
	srv := newPumpServer(2, func(w http.ResponseWriter, r *http.Request) {
		n++
		switch n {
		case 1:
			w.Write([]byte(`not json`))
		case 2:
			w.Write([]byte(`[{"a":1}]`))
		default:
			<-r.Context().Done()
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.eventPump(ctx)

	select {
	case w := <-srv.nextQueue:
		if w.Event != `{"a":1}` {
			t.Errorf("Event = %q, want the event after the bad batch", w.Event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pump stopped reading after an undecodable batch")
	}
}

// TestFrictionlessEventCrankHandle: the new kind joins the loop-continuity
// contract every work kind honors — its handle leads with re-launch-first and
// carries the payload, rather than falling through to the unrecognized-kind case.
// Refs: R3036, R3147
func TestFrictionlessEventCrankHandle(t *testing.T) {
	payload := `{"task":"summarize this listing"}`
	body := luhmannWorkPrompt("S1", LuhmannWork{Kind: "frictionless-event", Event: payload}, "")

	if !strings.HasPrefix(body, "**First, before anything else") {
		t.Errorf("handle does not lead with re-launch-first (R3036):\n%s", body)
	}
	if !strings.Contains(body, "~/.ark/ark luhmann next --session S1") {
		t.Error("handle does not name the successor next command")
	}
	if !strings.Contains(body, payload) {
		t.Errorf("handle does not carry the event payload:\n%s", body)
	}
	if strings.Contains(body, "unrecognized work kind") {
		t.Error("frictionless-event fell through to the unrecognized-kind fallback")
	}
}

// TestHandleLuhmannEventsStatuses: the HTTP round trip the CLI actually makes.
// proxyRaw treats any status but 200 as an error, so a "successful" 204 would
// surface to the user as `server error (204)` — the unit tests above call
// LuhmannEvents directly and cannot see that. Refs: R3145
func TestHandleLuhmannEventsStatuses(t *testing.T) {
	cases := []struct {
		name       string
		seatOwner  string
		body       string
		wantStatus int
	}{
		{"owner-opt-in-succeeds", "A", `{"session":"A","off":false}`, http.StatusOK},
		{"owner-release-succeeds", "A", `{"session":"A","off":true}`, http.StatusOK},
		{"foreign-session-conflicts", "A", `{"session":"B","off":false}`, http.StatusConflict},
		{"malformed-body-rejected", "A", `not json`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newPumpServer(1, func(w http.ResponseWriter, r *http.Request) {
				<-r.Context().Done()
			})
			srv.luhmannOwner = tc.seatOwner
			req, err := http.NewRequest(http.MethodPost, "http://ark/luhmann/events", strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			rec := &eventRecorder{status: http.StatusOK}
			srv.handleLuhmannEvents(rec, req)
			if rec.status != tc.wantStatus {
				t.Errorf("status = %d, want %d (body %q)", rec.status, tc.wantStatus, rec.body)
			}
			if srv.eventPumpCancel != nil {
				srv.eventPumpCancel() // don't leak a pump between subtests
			}
		})
	}
}

// TestEnqueueUIEventsPreservesRawJSON: the payload reaches the tube byte-for-byte,
// so the orchestrator reads what the Lua app pushed.
// Refs: R3147
func TestEnqueueUIEventsPreservesRawJSON(t *testing.T) {
	srv := &Server{nextQueue: make(chan LuhmannWork, 2)}
	srv.enqueueUIEvents([]byte(`[{"nested":{"x":[1,2]},"s":"a b"}]`))

	select {
	case w := <-srv.nextQueue:
		var got map[string]any
		if err := json.Unmarshal([]byte(w.Event), &got); err != nil {
			t.Fatalf("event is not valid JSON: %v (%q)", err, w.Event)
		}
		if got["s"] != "a b" {
			t.Errorf("payload mangled: %q", w.Event)
		}
	default:
		t.Fatal("nothing enqueued")
	}
}
