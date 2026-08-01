package ark

// Server-side browser transport for the managed-PTY host: ark's own
// gorilla/websocket endpoint at GET /luhmann/pty and the websocket client
// adapter that plugs into PtyHost's fan-out as one PtyClient (R3117, R3144) —
// the browser counterpart to pty_server.go's unix-socket transport.
//
// CRC: crc-PtyBrowser.md | Seq: seq-pty-attach.md | R3141, R3142, R3143, R3144

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ptyWSWriteWait bounds a single websocket write so a wedged browser socket
// surfaces as a write error (and thus a drop) instead of blocking the write
// goroutine indefinitely.
const ptyWSWriteWait = 10 * time.Second

// ptyWSUpgrader is ark's own websocket upgrader for the raw pty stream, separate
// from ui-engine's structured view-diff socket (R3141). The default same-origin
// CheckOrigin is deliberate: the browser loads the UI from this same origin, and
// rejecting cross-origin keeps another site from driving the hosted session.
var ptyWSUpgrader = websocket.Upgrader{}

// ptyCtrlKind enumerates the control frames a browser client sends as text
// messages; binary messages are raw pty bytes and never carry a control (R3142).
type ptyCtrlKind int

const (
	ptyCtrlNone ptyCtrlKind = iota
	ptyCtrlResize
	ptyCtrlRepaint
)

// ptyControl is a parsed text control frame (R3143): a resize carrying the
// client's terminal size, or a repaint request.
type ptyControl struct {
	Kind ptyCtrlKind
	Size PtyWinsize
}

// ptyControlWire is the on-the-wire JSON shape of a control frame (R3143):
// {"t":"resize","cols":C,"rows":R} or {"t":"repaint"}.
type ptyControlWire struct {
	T    string `json:"t"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// parsePtyControl decodes a text control frame (R3143). Pure — the deterministic
// core of the browser transport, unit-tested (test-PtyBrowser.md). It returns an
// error for malformed JSON or an unknown type so the caller closes the
// connection rather than guess at intent.
// CRC: crc-PtyBrowser.md | R3143
func parsePtyControl(msg []byte) (ptyControl, error) {
	var w ptyControlWire
	if err := json.Unmarshal(msg, &w); err != nil {
		return ptyControl{}, fmt.Errorf("pty control: bad JSON: %w", err)
	}
	switch w.T {
	case "resize":
		return ptyControl{Kind: ptyCtrlResize, Size: PtyWinsize{Cols: w.Cols, Rows: w.Rows}}, nil
	case "repaint":
		return ptyControl{Kind: ptyCtrlRepaint}, nil
	default:
		return ptyControl{}, fmt.Errorf("pty control: unknown type %q", w.T)
	}
}

// handleLuhmannPty bridges a browser websocket to the hosted pty (R3141): reject
// when no session is hosted, upgrade with ark's own gorilla upgrader, require the
// first control frame to be a resize (so Attach has the client's size, R3143),
// then plug the connection into PtyHost as one client (R3144) — binary messages
// are input (R3142), text messages are resize/repaint controls (R3143), and the
// connection closing detaches the client (R3121).
// CRC: crc-PtyBrowser.md | Seq: seq-pty-attach.md#1.1 | R3141, R3142, R3143, R3144
func (srv *Server) handleLuhmannPty(w http.ResponseWriter, r *http.Request) {
	if hosted, _ := srv.ptyHost.Status(); !hosted {
		http.Error(w, "no Luhmann session is hosted", http.StatusConflict)
		return
	}
	conn, err := ptyWSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already wrote the error response
	}
	// First frame must be a resize (R3143): the host needs the client's size
	// before it attaches. Anything else — binary, a non-resize control, or a read
	// error — closes the connection.
	mt, msg, rerr := conn.ReadMessage()
	if rerr != nil || mt != websocket.TextMessage {
		conn.Close()
		return
	}
	ctrl, cerr := parsePtyControl(msg)
	if cerr != nil || ctrl.Kind != ptyCtrlResize {
		conn.Close()
		return
	}
	client := newWSPtyClient(conn)
	reg, aerr := srv.ptyHost.Attach(client, ctrl.Size)
	if aerr != nil {
		client.Close()
		return
	}
	// Drain browser → host messages until the connection closes, then detach.
	for {
		mt, msg, rerr := conn.ReadMessage()
		if rerr != nil {
			break
		}
		switch mt {
		case websocket.BinaryMessage:
			srv.ptyHost.Input(msg) // R3142: raw pty bytes → serialized child input (R3119)
		case websocket.TextMessage:
			ctrl, cerr := parsePtyControl(msg)
			if cerr != nil {
				continue // ignore one malformed control frame; don't drop the session
			}
			switch ctrl.Kind {
			case ptyCtrlResize:
				srv.ptyHost.Resize(reg, ctrl.Size) // R3120: smallest-wins minimum
			case ptyCtrlRepaint:
				srv.ptyHost.ForceRepaint() // R3136: client asked the child to redraw
			}
		}
	}
	srv.ptyHost.Detach(reg) // R3121: disconnect deregisters, session survives
}

// --- wsPtyClient: the websocket transport as a PtyClient (R3117, R3144) ------

// wsPtyClient adapts an upgraded websocket connection to the host's PtyClient
// interface (R3144). It buffers nothing: the bounded channel and drain goroutine
// that used to live here moved into the host, where the buffering and the R3118
// drop are written once rather than once per transport (R3336, R3340, R3341).
// Each buffer the host hands over is sent as one binary message (R3142).
type wsPtyClient struct {
	conn      *websocket.Conn
	closeOnce sync.Once
}

func newWSPtyClient(conn *websocket.Conn) *wsPtyClient {
	return &wsPtyClient{conn: conn}
}

// Write sends the host's buffer as one binary message, blocking until it is
// written and returning the error if it is not (R3142, R3341). It runs on the
// host's per-connection write goroutine, which is also what satisfies gorilla's
// one-concurrent-writer rule: the host guarantees at most one writer per
// connection (R3338), while handleLuhmannPty is the sole reader. The deadline
// makes a wedged socket surface as an error — and so as a drop — rather than
// pinning the goroutine indefinitely. A stalled tab that is merely slow is
// dropped the other way, at the host's pending cap (R3340).
func (c *wsPtyClient) Write(p []byte) error {
	_ = c.conn.SetWriteDeadline(time.Now().Add(ptyWSWriteWait))
	return c.conn.WriteMessage(websocket.BinaryMessage, p)
}

// Close tears down the websocket once; the second call (drop + detach race)
// no-ops. gorilla permits Close concurrently with a write in flight, which is
// what lets a drop unblock a stuck writer.
func (c *wsPtyClient) Close() {
	c.closeOnce.Do(func() { c.conn.Close() })
}

var _ PtyClient = (*wsPtyClient)(nil)
