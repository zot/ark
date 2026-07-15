package ark

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// CRC: crc-LuhmannSend.md | Test: test-LuhmannSend.md

// Test: request builder embeds an inert backquoted nonce (R3131).
func TestBuildCommandRequestEmbedsInertNonce(t *testing.T) {
	const instruction = "summarize this listing and add a job item"
	req := buildCommandRequest(instruction, 7)

	if !strings.Contains(req, instruction) {
		t.Errorf("request must carry the instruction verbatim; got:\n%s", req)
	}
	marker := commandNonceMarker(7)
	if marker != "`LSEND:7`" {
		t.Errorf("commandNonceMarker(7) = %q, want `LSEND:7` (backquoted)", marker)
	}
	if !strings.Contains(req, marker) {
		t.Errorf("request must contain the backquoted marker %q; got:\n%s", marker, req)
	}
}

// Test: the marker for nonce 7 must not be found inside nonce 70's marker — the
// closing backtick delimits it (R3131).
func TestCommandNonceMarkerNoPrefixCollision(t *testing.T) {
	if strings.Contains(commandNonceMarker(70), commandNonceMarker(7)) {
		t.Errorf("marker for 7 (%q) collides with marker for 70 (%q)", commandNonceMarker(7), commandNonceMarker(70))
	}
}

const (
	tdLine        = `{"type":"system","subtype":"turn_duration"}`
	asstLine      = `{"type":"assistant","message":{"content":[{"type":"text","text":"working"}]}}`
	laterLine     = `{"type":"assistant","message":{"content":[{"type":"text","text":"AFTERWARD"}]}}`
	markerContent = "correlation `LSEND:7` ignore"
)

func markerLine() string {
	// A user tool_result carrying the inert marker in its content bytes.
	return `{"type":"user","message":{"content":[{"type":"tool_result","content":"` + markerContent + `"}]}}`
}

// Test: window scanner brackets open→first-turn-completion (R3132).
func TestScanSendWindowBracketsOpenToFirstTurn(t *testing.T) {
	data := []byte(strings.Join([]string{
		asstLine,     // before the marker — must be excluded
		markerLine(), // OPEN
		asstLine,
		tdLine,    // CLOSE
		laterLine, // after the close — must be excluded
	}, "\n") + "\n")

	window, closed := scanSendWindow(data, commandNonceMarker(7))
	if !closed {
		t.Fatalf("expected the window to close on the turn_duration after the marker")
	}
	w := string(window)
	if !strings.Contains(w, "LSEND:7") {
		t.Errorf("window must start at the marker line; got:\n%s", w)
	}
	if !strings.Contains(w, "turn_duration") {
		t.Errorf("window must include the closing turn_duration line; got:\n%s", w)
	}
	if strings.Contains(w, "AFTERWARD") {
		t.Errorf("window must exclude the line after the close; got:\n%s", w)
	}
}

// Test: window scanner waits when open seen but no close yet (R3132).
func TestScanSendWindowWaitsWithoutClose(t *testing.T) {
	data := []byte(markerLine() + "\n" + asstLine + "\n")
	if _, closed := scanSendWindow(data, commandNonceMarker(7)); closed {
		t.Errorf("a turn still in progress (no turn_duration) must not close the window")
	}
}

// Test: window scanner ignores a turn_duration before the marker (R3132).
func TestScanSendWindowIgnoresTurnDurationBeforeMarker(t *testing.T) {
	data := []byte(tdLine + "\n" + markerLine() + "\n" + asstLine + "\n")
	if _, closed := scanSendWindow(data, commandNonceMarker(7)); closed {
		t.Errorf("a turn_duration before the marker must not close a window that has not opened")
	}
}

// Test: orchestrator gate short-circuits with no owner, before any enqueue or
// JSONL lookup (R3134). A zero-value Server has nil db/nextQueue, so reaching
// them would panic — the clean error is the proof the gate ran first.
func TestLuhmannSendGateNoOrchestrator(t *testing.T) {
	srv := &Server{}
	_, err := srv.LuhmannSend(context.Background(), "do a thing", time.Second)
	if !errors.Is(err, errLuhmannNoOrchestrator) {
		t.Fatalf("LuhmannSend with no owner = %v, want errLuhmannNoOrchestrator", err)
	}
}

// Test: locateSessionJSONL finds a session log by UUID on the filesystem,
// index-independent (R3132) — the regression the live end-to-end caught.
func TestLocateSessionJSONL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	uuid := "d64a137b-5933-46cf-b8d4-5efcb2c53344"
	projDir := filepath.Join(home, ".claude", "projects", "-home-deck--ark-luhmann")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(projDir, uuid+".jsonl")
	if err := os.WriteFile(want, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := locateSessionJSONL(uuid)
	if err != nil {
		t.Fatalf("locateSessionJSONL: %v", err)
	}
	if got != want {
		t.Errorf("locateSessionJSONL = %q, want %q", got, want)
	}
	if _, err := locateSessionJSONL("no-such-session"); !errors.Is(err, errLuhmannNoJSONL) {
		t.Errorf("unknown session = %v, want errLuhmannNoJSONL", err)
	}
}

// Test: the tail loop returns the bracketed window once the JSONL contains a
// closed window past the anchor offset (R3132). Content before the anchor is
// not read; content past the close is not returned.
func TestTailSendWindowReturnsClosedWindow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	pre := asstLine + "\n" // before the anchor — must not be read
	body := strings.Join([]string{markerLine(), asstLine, tdLine, laterLine}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(pre+body), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := &Server{}
	window, err := srv.tailSendWindow(context.Background(), path, int64(len(pre)), commandNonceMarker(7), 2*time.Second)
	if err != nil {
		t.Fatalf("tailSendWindow: %v", err)
	}
	w := string(window)
	if !strings.Contains(w, "LSEND:7") || !strings.Contains(w, "turn_duration") {
		t.Errorf("window must span marker→turn_duration; got:\n%s", w)
	}
	if strings.Contains(w, "AFTERWARD") {
		t.Errorf("window must exclude content past the close; got:\n%s", w)
	}
}

// Test: the tail loop returns errLuhmannSendTimeout when no window closes within
// the deadline (R3133) — the enqueue is not undone by the caller.
func TestTailSendWindowTimesOut(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	if err := os.WriteFile(path, []byte(asstLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := &Server{}
	_, err := srv.tailSendWindow(context.Background(), path, 0, commandNonceMarker(7), 60*time.Millisecond)
	if !errors.Is(err, errLuhmannSendTimeout) {
		t.Fatalf("tailSendWindow with no closing window = %v, want errLuhmannSendTimeout", err)
	}
}
