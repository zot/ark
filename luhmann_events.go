package ark

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Frictionless event-routing bridge — the third producer onto the orchestrator
// drain tube (crc-LuhmannEvents.md). Frictionless events (a Lua app's
// mcp.pushState() payloads) are drained today by `ark ui event`, the first
// lotto tube. On a session's opt-in this file reroutes them onto that session's
// `next` tube, so a pty-hosted orchestrator handles UI work in the same
// serialized thread it already uses for curation, directives, and commands.
// R3145-R3148

const (
	// eventPumpWaitTimeout is the long-poll window the pump asks /wait for.
	// flib caps it at 120s; the poll returns early the moment an event arrives,
	// so this only paces the idle case. R3147
	eventPumpWaitTimeout = 120 * time.Second
	// eventPumpNoSessionPause backs off when no UI session is up yet. The UI is
	// optional and may arrive later, so a missing session is a wait condition,
	// not an error (Stubborn Plumbing). R3147
	eventPumpNoSessionPause = 2 * time.Second
	// eventPumpWaitPath is flib's long-poll endpoint, registered on ark's mux.
	eventPumpWaitPath = "/wait"
	// luhmannErrEventsRouted is what a gated `ark ui event` caller is told:
	// an orchestrator owns event routing, so this reader would be the second.
	// R3146
	luhmannErrEventsRouted = "orchestrator %q owns event routing (ark luhmann events); this session cannot read UI events"
)

// EventOwner returns the session owning Frictionless event routing, or "" when
// unowned (R3145). The gate's predicate.
// CRC: crc-LuhmannEvents.md | R3145, R3146
func (srv *Server) EventOwner() string {
	srv.luhmannMu.Lock()
	defer srv.luhmannMu.Unlock()
	return srv.eventOwner
}

// clearEventRoutingLocked releases routing and stops the pump. Caller holds
// luhmannMu — which is the point: seat change and routing clear are one atomic
// step (R3148), so an orchestrator can never observe itself owning a seat whose
// routing still belongs to its predecessor.
// CRC: crc-LuhmannEvents.md | Seq: seq-luhmann-events.md#3.3 | R3148
func (srv *Server) clearEventRoutingLocked() {
	srv.eventOwner = ""
	if srv.eventPumpCancel != nil {
		srv.eventPumpCancel()
		srv.eventPumpCancel = nil
	}
}

// LuhmannEvents applies the event-routing opt-in (R3145). Routing is a
// privilege of the `next` seat rather than a second identity, so the caller
// must already own the seat; anyone else gets R3013's stand-down string. `off`
// releases routing and restores `ark ui event`. Claiming is idempotent for the
// owner — a second request must not start a second reader, which would split
// the stream (R3146).
// CRC: crc-LuhmannEvents.md | Seq: seq-luhmann-events.md#1.2 | R3145, R3146
func (srv *Server) LuhmannEvents(session string, off bool) error {
	srv.luhmannMu.Lock()
	defer srv.luhmannMu.Unlock()
	if session == "" || srv.luhmannOwner != session {
		return fmt.Errorf("%s", luhmannErrNoOwnership)
	}
	if off {
		srv.clearEventRoutingLocked()
		return nil
	}
	if srv.eventOwner == session {
		return nil // already routing for this session — no second pump
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv.eventOwner = session
	srv.eventPumpCancel = cancel
	go srv.eventPump(ctx)
	return nil
}

// eventPump is the single reader of the Frictionless event stream (R3146). It
// long-polls flib's /wait in-process against ark's own mux — the same poll `ark
// ui event` makes, without a socket round trip — and fans each drained event
// out onto the tube as its own work item (R3147). It runs until its context is
// cancelled, which happens on release or a seat change (R3148).
// CRC: crc-LuhmannEvents.md | Seq: seq-luhmann-events.md#1.4 | R3146, R3147
func (srv *Server) eventPump(ctx context.Context) {
	for ctx.Err() == nil {
		status, body := srv.pollUIEvents(ctx)
		// Enqueue whatever was drained even if the pump was cancelled mid-poll:
		// the drain is destructive, so discarding the batch would silently eat a
		// user's event, while delivering one final batch to a tube that still
		// exists is merely late. Loss is the worse failure.
		if status == http.StatusOK {
			srv.enqueueUIEvents(body)
		}
		if status == http.StatusNotFound {
			// No UI session up yet — wait for one rather than spinning.
			select {
			case <-ctx.Done():
			case <-time.After(eventPumpNoSessionPause):
			}
		}
	}
}

// pollUIEvents makes one in-process /wait call against the mux flib registered
// its handler on, returning the status and body. External callers reach that
// mux only through gateFrictionlessWait, which wraps it from outside — so the
// pump bypasses its own gate by construction and needs no exemption marker.
// CRC: crc-LuhmannEvents.md | Seq: seq-luhmann-events.md#1.4 | R3146
func (srv *Server) pollUIEvents(ctx context.Context) (int, []byte) {
	if srv.uiMux == nil {
		return http.StatusNotFound, nil
	}
	url := fmt.Sprintf("%s?timeout=%d", eventPumpWaitPath, int(eventPumpWaitTimeout.Seconds()))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return http.StatusInternalServerError, nil
	}
	rec := &eventRecorder{status: http.StatusOK}
	srv.uiMux.ServeHTTP(rec, req)
	return rec.status, rec.body
}

// enqueueUIEvents fans one /wait batch out onto the tube, one work item per
// event (R3147). A full queue drops the event rather than blocking the pump:
// EnqueueLuhmann is non-blocking by contract (R3024), so a stalled orchestrator
// can never wedge the reader.
// CRC: crc-LuhmannEvents.md | Seq: seq-luhmann-events.md#1.7 | R3147
func (srv *Server) enqueueUIEvents(body []byte) {
	var events []json.RawMessage
	if err := json.Unmarshal(body, &events); err != nil {
		Logv(1, "luhmann events: undecodable /wait batch: %v", err)
		return
	}
	for _, e := range events {
		srv.EnqueueLuhmann(LuhmannWork{Kind: "frictionless-event", Event: string(e)})
	}
}

// gateFrictionlessWait wraps ark's whole mux so `ark ui event` is refused while
// an orchestrator owns event routing (R3146). Exactly one reader is served
// because the drain is destructive — each event is delivered once and cleared —
// so two readers would split the stream, each seeing an arbitrary half and
// neither the whole; the second is refused rather than served badly. This is a
// wrapper rather than a route because flib already registered /wait on the mux
// and a second registration would collide; wrapping also keeps flib's route
// list out of ark, so a new flib endpoint needs no change here.
// CRC: crc-LuhmannEvents.md | Seq: seq-luhmann-events.md#2.2 | R3146
func (srv *Server) gateFrictionlessWait(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == eventPumpWaitPath {
			if owner := srv.EventOwner(); owner != "" {
				http.Error(w, fmt.Sprintf(luhmannErrEventsRouted, owner), http.StatusConflict)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// handleLuhmannEvents is the `ark luhmann events` server verb (R3145).
// CRC: crc-LuhmannEvents.md | Seq: seq-luhmann-events.md#1.1 | R3145
func (srv *Server) handleLuhmannEvents(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Session string `json:"session"`
		Off     bool   `json:"off"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := srv.LuhmannEvents(req.Session, req.Off); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	// Fall through to Go's default 200: proxyRaw treats any other status —
	// including a 204 — as an error, and the sibling luhmann handlers signal
	// success the same way.
}

// eventRecorder is the minimal ResponseWriter the pump's in-process /wait call
// writes into — enough for flib's handler (headers, status, body) without
// pulling in net/http/httptest. R3146
type eventRecorder struct {
	header http.Header
	body   []byte
	status int
}

func (e *eventRecorder) Header() http.Header {
	if e.header == nil {
		e.header = make(http.Header)
	}
	return e.header
}

func (e *eventRecorder) Write(b []byte) (int, error) {
	e.body = append(e.body, b...)
	return len(b), nil
}

func (e *eventRecorder) WriteHeader(status int) { e.status = status }
