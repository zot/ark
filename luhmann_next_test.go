package ark

// CRC: crc-LuhmannCLI.md | Test: test-LuhmannCLI.md

import (
	"context"
	"strings"
	"testing"
	"time"
)

// --- Bloodhound-CLI S1: `ark luhmann next` drain tube ---
//
// The ownership lease (claimLuhmann) and the blocking drain (LuhmannNext) both
// run against a bare &Server{} — the lease touches only luhmannMu/luhmannOwner
// and the drain only those plus nextQueue, so no DB/pubsub/socket is needed.

// TestClaimLuhmannMatrix drives the full 3-modes × 3-owner-states lease matrix:
// disposition, error string, and the owner mutation (claim only when it should).
// Refs: R3012, R3013, R3014
func TestClaimLuhmannMatrix(t *testing.T) {
	cases := []struct {
		name      string
		mode      luhmannNextMode
		owner     string // owner before the call ("" = unowned)
		session   string
		wantDisp  luhmannNextDisposition
		wantMsg   string
		wantOwner string // owner after the call
	}{
		// --force always claims (idempotent on self, reclaims a foreign seat).
		{"force/unowned", luhmannModeForce, "", "A", luhmannDispOK, "", "A"},
		{"force/self", luhmannModeForce, "A", "A", luhmannDispOK, "", "A"},
		{"force/foreign-reclaims", luhmannModeForce, "A", "B", luhmannDispOK, "", "B"},
		// --first claims when unowned or self, else stands the caller down.
		{"first/unowned-claims", luhmannModeFirst, "", "A", luhmannDispOK, "", "A"},
		{"first/self-idempotent", luhmannModeFirst, "A", "A", luhmannDispOK, "", "A"},
		{"first/foreign-standdown", luhmannModeFirst, "A", "B", luhmannDispExit, luhmannErrNoOwnership, "A"},
		// plain validates only — never claims.
		{"plain/unowned-reclaim", luhmannModePlain, "", "A", luhmannDispReclaim, luhmannErrNoSessions, ""},
		{"plain/self-ok", luhmannModePlain, "A", "A", luhmannDispOK, "", "A"},
		{"plain/foreign-standdown", luhmannModePlain, "A", "B", luhmannDispExit, luhmannErrNoOwnership, "A"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := &Server{luhmannOwner: tc.owner}
			disp, msg := srv.claimLuhmann(tc.session, tc.mode)
			if disp != tc.wantDisp {
				t.Errorf("disposition = %d, want %d", disp, tc.wantDisp)
			}
			if msg != tc.wantMsg {
				t.Errorf("msg = %q, want %q", msg, tc.wantMsg)
			}
			if srv.luhmannOwner != tc.wantOwner {
				t.Errorf("owner after = %q, want %q", srv.luhmannOwner, tc.wantOwner)
			}
		})
	}
}

// TestLuhmannNextKeepalive blocks with an owned seat and no work, expecting the
// keepalive crank-handle once the (short) window elapses.
// Refs: R3011, R3016
func TestLuhmannNextKeepalive(t *testing.T) {
	srv := &Server{luhmannOwner: "A", nextQueue: make(chan LuhmannWork, 1)}
	body, disp, err := srv.LuhmannNext(context.Background(), "A", luhmannModePlain, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if disp != luhmannDispOK {
		t.Fatalf("disposition = %d, want OK", disp)
	}
	if !strings.Contains(body, "keep the seat warm") {
		t.Errorf("body = %q, want keepalive crank-handle", body)
	}
}

// TestLuhmannNextCurationWork drains a queued curation task ahead of the
// keepalive and expects the request-doc pointer + the bloodhound-add instruction.
// Refs: R3011
func TestLuhmannNextCurationWork(t *testing.T) {
	srv := &Server{luhmannOwner: "A", nextQueue: make(chan LuhmannWork, 1)}
	srv.nextQueue <- LuhmannWork{Kind: "curation", Path: "tmp://BLOODHOUND-CLI/xyz"}
	body, disp, err := srv.LuhmannNext(context.Background(), "A", luhmannModePlain, time.Second)
	if err != nil || disp != luhmannDispOK {
		t.Fatalf("err=%v disp=%d, want nil/OK", err, disp)
	}
	if !strings.Contains(body, "tmp://BLOODHOUND-CLI/xyz") || !strings.Contains(body, "bloodhound add") {
		t.Errorf("body = %q, want curation crank-handle", body)
	}
}

// TestLuhmannNextDirectiveWork drains a supervisor directive and expects the
// spawn/stop crank-handle naming the directive and managed class.
// Refs: R3011
func TestLuhmannNextDirectiveWork(t *testing.T) {
	srv := &Server{luhmannOwner: "A", nextQueue: make(chan LuhmannWork, 1)}
	srv.nextQueue <- LuhmannWork{Kind: "directive", Directive: "stand-up", Class: "bloodhound"}
	body, _, err := srv.LuhmannNext(context.Background(), "A", luhmannModePlain, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "stand-up") || !strings.Contains(body, "bloodhound") {
		t.Errorf("body = %q, want directive crank-handle", body)
	}
}

// TestLuhmannNextStandDownImmediate confirms a non-owner short-circuits on the
// lease and never enters the blocking select, even with an hour-long keepalive.
// Refs: R3013, R3014
func TestLuhmannNextStandDownImmediate(t *testing.T) {
	srv := &Server{luhmannOwner: "A", nextQueue: make(chan LuhmannWork)}
	start := time.Now()
	body, disp, err := srv.LuhmannNext(context.Background(), "B", luhmannModePlain, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if disp != luhmannDispExit {
		t.Fatalf("disposition = %d, want Exit", disp)
	}
	if !strings.Contains(body, "Stand down") {
		t.Errorf("body = %q, want stand-down crank-handle", body)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("stand-down took %v — it must not block", elapsed)
	}
}

// TestLuhmannNextReclaim confirms a plain call on an unowned (post-bounce) server
// returns the reclaim crank-handle without claiming or blocking.
// Refs: R3013, R3014
func TestLuhmannNextReclaim(t *testing.T) {
	srv := &Server{nextQueue: make(chan LuhmannWork)} // unowned
	body, disp, err := srv.LuhmannNext(context.Background(), "A", luhmannModePlain, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if disp != luhmannDispReclaim {
		t.Fatalf("disposition = %d, want Reclaim", disp)
	}
	if !strings.Contains(body, "--first") {
		t.Errorf("body = %q, want reclaim crank-handle", body)
	}
	if srv.luhmannOwner != "" {
		t.Errorf("owner = %q, want unchanged (plain never claims)", srv.luhmannOwner)
	}
}

// TestLuhmannNextContextCancel confirms a cancelled request context unblocks the
// drain with the context error, not a spurious work/keepalive return.
// Refs: R3010
func TestLuhmannNextContextCancel(t *testing.T) {
	srv := &Server{luhmannOwner: "A", nextQueue: make(chan LuhmannWork)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := srv.LuhmannNext(ctx, "A", luhmannModePlain, time.Hour)
	if err == nil {
		t.Fatal("want context error, got nil")
	}
}
