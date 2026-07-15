package ark

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// Luhmann command/response bridge — the synchronous producer half of the drain
// tube (crc-LuhmannSend.md). `ark luhmann send "<instruction>"` enqueues one
// `command` work item, blocks until the orchestrator handles it, and returns
// the orchestrator's turns for that command. Correlation rides an inert nonce
// inside the request content (Watermark), recognized on the session JSONL tap
// the server already owns; the reply window is bracketed from the nonce's
// appearance to the orchestrator's first turn completion.

// sendTailPollInterval is how often LuhmannSend re-reads the orchestrator's
// JSONL while waiting for the reply window to close (R3132).
const sendTailPollInterval = 150 * time.Millisecond

// DefaultLuhmannSendTimeout bounds the wait when the caller passes none (R3133).
const DefaultLuhmannSendTimeout = 120 * time.Second

// Errors LuhmannSend returns; the HTTP handler maps each to a status code so
// the CLI can tell a timeout (enqueue stands) from a missing orchestrator.
var (
	errLuhmannNoOrchestrator = errors.New("no orchestrator hosting the tube (run `ark luhmann launch`)") // R3134
	errLuhmannNoJSONL        = errors.New("could not locate the orchestrator's session log")
	errLuhmannQueueFull      = errors.New("orchestrator work queue is full")
	errLuhmannSendTimeout    = errors.New("command enqueued but no turn completed in time")
)

// commandNonceMarker is the inert, backquoted correlation token embedded in a
// command request and recognized on the JSONL tap (R3131). The backticks make
// it a code literal (inert to the orchestrator) and delimit it so nonce 7 never
// matches nonce 70's marker.
func commandNonceMarker(n uint64) string {
	return fmt.Sprintf("`LSEND:%d`", n)
}

// buildCommandRequest renders one `send` instruction as a markdown command
// request carrying the inert nonce marker (R3131, Baby Food). The instruction
// is verbatim; the marker rides in a trailing HTML comment (invisible in
// rendered markdown, backquoted inside it) so it never nudges the reply, yet
// lands verbatim in the JSONL for the bracket scan.
func buildCommandRequest(instruction string, n uint64) string {
	return fmt.Sprintf("%s\n\n<!-- ark correlation %s — ignore -->\n", instruction, commandNonceMarker(n))
}

// scanSendWindow finds the reply window in accumulated JSONL bytes: from the
// line carrying marker (the open bracket) to the first turn_duration line after
// it (the close bracket), reusing scanNewBytes for the turn signal (R3132).
// Returns the raw window lines and whether the close was seen. A turn_duration
// before the marker is ignored (the window has not opened); an open with no
// following turn_duration yet returns closed=false so the caller keeps tailing.
func scanSendWindow(data []byte, marker string) (window []byte, closed bool) {
	mk := []byte(marker)
	open := -1
	pos := 0
	for pos < len(data) {
		nl := bytes.IndexByte(data[pos:], '\n')
		var line []byte
		lineEnd := len(data)
		if nl >= 0 {
			line = data[pos : pos+nl]
			lineEnd = pos + nl + 1 // include the newline in the window
		} else {
			line = data[pos:]
		}
		if open < 0 {
			if bytes.Contains(line, mk) {
				open = pos
			}
		} else if slices.Contains(scanNewBytes(line), signalTurnDuration) {
			return data[open:lineEnd], true
		}
		if nl < 0 {
			break
		}
		pos += nl + 1
	}
	if open >= 0 {
		return data[open:], false
	}
	return nil, false
}

// locateSessionJSONL finds a Claude Code session's raw JSONL by UUID directly
// on the filesystem — `~/.claude/projects/*/<uuid>.jsonl` (R3132). The lookup is
// index-independent on purpose: the orchestrator runs in `~/.ark/luhmann`, which
// is NOT a corpus source, so its own session log is never indexed and
// db.SessionJSONLs (an index query) would never find it.
func locateSessionJSONL(sessionUUID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	matches, err := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", sessionUUID+".jsonl"))
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", errLuhmannNoJSONL
	}
	return matches[0], nil
}

// readAppended reads path from offset to EOF, returning the new bytes and the
// advanced offset. A transient open/seek/read error returns the offset
// unchanged so the caller waits and retries (Stubborn Plumbing, R3133).
func readAppended(path string, offset int64) ([]byte, int64) {
	f, err := os.Open(path)
	if err != nil {
		return nil, offset
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, offset
	}
	return data, offset + int64(len(data))
}

// LuhmannSend is the synchronous command bridge (R3129): gate on a live
// orchestrator, enqueue the instruction as a `command` work item carrying an
// inert nonce, then tail the orchestrator's JSONL and return the reply window
// bracketed from the nonce to its first turn completion. One blocking call does
// enqueue → wait → collect (Batteries Included).
// CRC: crc-LuhmannSend.md | Seq: seq-luhmann-send.md#1.1 | R3129, R3131, R3132, R3133, R3134
func (srv *Server) LuhmannSend(ctx context.Context, instruction string, timeout time.Duration) ([]byte, error) {
	// R3134: no live orchestrator ⇒ orchestrator-not-running, no enqueue.
	owner := srv.LuhmannOwner()
	if owner == "" {
		return nil, errLuhmannNoOrchestrator
	}
	// Locate the orchestrator's JSONL and anchor the tail start BEFORE the
	// enqueue, so the command's delivery (and its marker) is guaranteed to land
	// after the anchor (R3132).
	path, err := locateSessionJSONL(owner)
	if err != nil {
		return nil, err
	}
	// A not-yet-written session log stat-fails; anchor at 0 (the marker still
	// lands after this) rather than treat it as an error.
	startOffset, _ := fileSize(path)

	// R3131: mint the nonce, build the markdown request with the inert marker.
	n := srv.sendCounter.Add(1)
	if !srv.EnqueueLuhmann(LuhmannWork{Kind: "command", Command: buildCommandRequest(instruction, n), Nonce: n}) {
		return nil, errLuhmannQueueFull
	}

	if timeout <= 0 {
		timeout = DefaultLuhmannSendTimeout
	}
	return srv.tailSendWindow(ctx, path, startOffset, commandNonceMarker(n), timeout)
}

// tailSendWindow polls the orchestrator's JSONL from startOffset until the reply
// window closes or timeout elapses (R3132, R3133). A read error mid-tail is a
// wait condition, not a failure (Stubborn Plumbing) — the poll retries.
// CRC: crc-LuhmannSend.md | Seq: seq-luhmann-send.md#1.10 | R3132, R3133
func (srv *Server) tailSendWindow(ctx context.Context, path string, startOffset int64, marker string, timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	offset := startOffset
	var buf []byte
	for {
		if data, next := readAppended(path, offset); len(data) > 0 {
			buf = append(buf, data...)
			offset = next
			if window, closed := scanSendWindow(buf, marker); closed {
				return window, nil
			}
		}
		if time.Now().After(deadline) {
			return nil, errLuhmannSendTimeout
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(sendTailPollInterval):
		}
	}
}

// handleLuhmannSend is the POST /luhmann/send endpoint (R3129): decode the
// instruction + timeout, run LuhmannSend, and return the window as raw JSONL, or
// map the error to a status code so the CLI distinguishes a timeout (the enqueue
// stands) from a missing orchestrator (R3133, R3134).
// CRC: crc-LuhmannSend.md | Seq: seq-luhmann-send.md#1.11 | R3129, R3133, R3134
func (srv *Server) handleLuhmannSend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Instruction string `json:"instruction"`
		Timeout     int    `json:"timeout"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}
	if req.Instruction == "" {
		http.Error(w, "instruction required", http.StatusBadRequest)
		return
	}
	window, err := srv.LuhmannSend(r.Context(), req.Instruction, time.Duration(req.Timeout)*time.Second)
	switch {
	case errors.Is(err, errLuhmannNoOrchestrator):
		http.Error(w, err.Error(), http.StatusConflict) // 409
	case errors.Is(err, errLuhmannQueueFull):
		http.Error(w, err.Error(), http.StatusServiceUnavailable) // 503
	case errors.Is(err, errLuhmannSendTimeout):
		http.Error(w, err.Error(), http.StatusGatewayTimeout) // 504
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	default:
		w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
		w.Write(window)
	}
}
