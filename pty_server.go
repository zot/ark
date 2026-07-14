package ark

// Server-side wiring for the managed-PTY host: the Server implements PtyEnv (the
// seat lease + roster + directories the host depends on), the four HTTP handlers
// the CLI verbs proxy to, and the unix-socket attach client that plugs into the
// host's fan-out as one PtyClient.
//
// CRC: crc-PtyHost.md, crc-Server.md | R3117, R3122, R3123, R3124, R3125, R3126

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"
)

// ptyClientBufChunks bounds a socket client's output buffer (R3118): past this
// many undelivered chunks the client is dropped rather than allowed to stall the
// fan-out.
const ptyClientBufChunks = 256

// --- Server as PtyEnv (R3124, R3125, R3126) ---------------------------------

// ReleaseLuhmann clears the seat lease when the given session holds it (R3125):
// the graceful counterpart to a --first claim, called on stop and on child
// self-exit so status stays truthful. A no-op when a different session owns the
// seat (never clobber a live owner) or the seat is already free.
// CRC: crc-LuhmannCLI.md | R3125
func (srv *Server) ReleaseLuhmann(session string) {
	srv.luhmannMu.Lock()
	defer srv.luhmannMu.Unlock()
	if session != "" && srv.luhmannOwner == session {
		srv.luhmannOwner = ""
	}
}

// SeatOwner reports the current Luhmann seat owner for the host's launch wait
// (R3126). PtyEnv.
func (srv *Server) SeatOwner() string { return srv.LuhmannOwner() }

// ForceReleaseSeat clears any seat claim before a managed launch (R3126): a stale
// owner (a prior session that died without releasing the in-memory lease) would
// otherwise block the new session's --first claim. Unconditional, unlike
// ReleaseSeat — the launch is the authoritative start. PtyEnv.
// CRC: crc-LuhmannCLI.md | R3126
func (srv *Server) ForceReleaseSeat() {
	srv.luhmannMu.Lock()
	srv.luhmannOwner = ""
	srv.luhmannMu.Unlock()
}

// ReleaseSeat releases the seat lease on teardown (R3125). PtyEnv.
func (srv *Server) ReleaseSeat(session string) { srv.ReleaseLuhmann(session) }

// LuhmannDir is the hosted session's cwd, ~/.ark/luhmann (R3126). PtyEnv.
func (srv *Server) LuhmannDir() string { return filepath.Join(arkHomeDir(), "luhmann") }

// PoolRosterCount is the pool-secretary roster size for status (R3124). PtyEnv.
func (srv *Server) PoolRosterCount() int { return srv.recallWatcher.PoolSize() }

// RecordHostedExits records an exit for every pool secretary of the hosted
// session and drops it from the roster (R3125): killing the pty takes these
// in-session subagents down with it, so recording their exits leaves the
// monitoring log truthful rather than showing ghosts. A clean stop is kind
// "exit" (resets the streak counters), not a crash. PtyEnv.
// CRC: crc-Server.md | R3125
func (srv *Server) RecordHostedExits(_ string) {
	if srv.recallWatcher == nil {
		return
	}
	for _, nonce := range srv.recallWatcher.PoolNonces() {
		rec := LuhmannRecord{
			Kind:   "exit",
			Class:  bloodhoundPoolClass,
			Nonce:  int(nonce),
			Reason: "luhmann-stopped",
		}
		_ = AppendLuhmannRecord(srv.db, srv.db.Path(), rec)
		srv.recallWatcher.DeregisterPoolSecretary(bloodhoundPoolClass, "exit", nonce)
	}
}

// --- HTTP handlers (R3122–R3125) --------------------------------------------

// handleLuhmannLaunch runs the pty launch + content-free confirmation protocol
// (R3122, R3126) and returns the confirmed session id, or an error if a session
// is already hosted or the confirmation times out.
// CRC: crc-Server.md | Seq: seq-pty-launch.md#1.1 | R3122, R3126
func (srv *Server) handleLuhmannLaunch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Bootstrap string `json:"bootstrap"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	sid, err := srv.ptyHost.Launch(req.Bootstrap)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, map[string]string{"session": sid})
}

// handleLuhmannStatus reports whether a session is hosted, its id, and the
// pool-secretary roster count (R3124).
// CRC: crc-Server.md | R3124
func (srv *Server) handleLuhmannStatus(w http.ResponseWriter, _ *http.Request) {
	hosted, sid := srv.ptyHost.Status()
	writeJSON(w, map[string]any{
		"hosted":      hosted,
		"session":     sid,
		"secretaries": srv.PoolRosterCount(),
	})
}

// handleLuhmannStop grace-tears-down the hosted session (R3125).
// CRC: crc-Server.md | R3125
func (srv *Server) handleLuhmannStop(w http.ResponseWriter, _ *http.Request) {
	if err := srv.ptyHost.Stop(); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, map[string]string{"status": "stopped"})
}

// handleLuhmannAttach hijacks the connection into a raw bidirectional stream and
// plugs it into the host's fan-out as one client (R3117, R3123). The client's
// first frame carries its initial size; thereafter data frames are input and
// resize frames drive the smallest-wins minimum (R3119, R3120). Host output
// streams back unframed. The connection closing (detach or drop) deregisters the
// client, undisturbing the others (R3121).
// CRC: crc-Server.md | Seq: seq-pty-attach.md#1.1 | R3117, R3119, R3120, R3121, R3123
func (srv *Server) handleLuhmannAttach(w http.ResponseWriter, r *http.Request) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "connection does not support hijacking", http.StatusInternalServerError)
		return
	}
	if hosted, _ := srv.ptyHost.Status(); !hosted {
		http.Error(w, "no Luhmann session is hosted", http.StatusConflict)
		return
	}
	conn, bufrw, err := hj.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Acknowledge the hijack with a bare 200 so the client can tell a successful
	// attach (raw stream follows) from a pre-hijack HTTP error.
	if _, err = bufrw.WriteString("HTTP/1.1 200 OK\r\nConnection: close\r\n\r\n"); err != nil {
		conn.Close()
		return
	}
	if err = bufrw.Flush(); err != nil {
		conn.Close()
		return
	}
	// First frame must be the initial resize (R3120). Fail closed if the client
	// opens with anything else.
	kind, _, ws, ferr := ReadPtyFrame(bufrw)
	if ferr != nil || kind != ptyFrameResize {
		conn.Close()
		return
	}
	client := newSocketPtyClient(conn)
	go client.writeLoop()
	reg, aerr := srv.ptyHost.Attach(client, ws)
	if aerr != nil {
		client.Close()
		return
	}
	// Drain client → host frames until the connection closes, then detach.
	for {
		k, data, ws, rerr := ReadPtyFrame(bufrw)
		if rerr != nil {
			break
		}
		switch k {
		case ptyFrameData:
			srv.ptyHost.Input(data)
		case ptyFrameResize:
			srv.ptyHost.Resize(reg, ws)
		}
	}
	srv.ptyHost.Detach(reg)
}

// --- socketPtyClient: the unix-socket transport as a PtyClient (R3117) -------

// socketPtyClient adapts a hijacked connection to the host's PtyClient interface
// (R3117): a bounded output channel drained by writeLoop, so a stalled reader
// backs up into the buffer and is dropped (Write returns false) rather than
// blocking the fan-out (R3118).
type socketPtyClient struct {
	conn      net.Conn
	out       chan []byte
	done      chan struct{}
	closeOnce sync.Once
}

func newSocketPtyClient(conn net.Conn) *socketPtyClient {
	return &socketPtyClient{
		conn: conn,
		out:  make(chan []byte, ptyClientBufChunks),
		done: make(chan struct{}),
	}
}

// Write enqueues an output chunk non-blockingly; false means the bounded buffer
// overflowed and the host should drop this client (R3118).
func (c *socketPtyClient) Write(p []byte) bool {
	select {
	case <-c.done:
		return false
	case c.out <- p:
		return true
	default:
		return false // R3118: buffer full — signal drop
	}
}

// Close releases the transport once; the second call (drop + detach race) no-ops.
func (c *socketPtyClient) Close() {
	c.closeOnce.Do(func() {
		close(c.done)
		c.conn.Close()
	})
}

// writeLoop streams enqueued output to the connection until Close. A write error
// closes the client, which the host observes as a drop on the next broadcast.
func (c *socketPtyClient) writeLoop() {
	w := bufio.NewWriter(c.conn)
	flush := time.NewTicker(8 * time.Millisecond)
	defer flush.Stop()
	for {
		select {
		case <-c.done:
			return
		case chunk := <-c.out:
			if _, err := w.Write(chunk); err != nil {
				c.Close()
				return
			}
			// Coalesce any immediately-available chunks, then flush.
			if len(c.out) == 0 {
				if err := w.Flush(); err != nil {
					c.Close()
					return
				}
			}
		case <-flush.C:
			if w.Buffered() > 0 {
				if err := w.Flush(); err != nil {
					c.Close()
					return
				}
			}
		}
	}
}

var _ PtyClient = (*socketPtyClient)(nil)

// interface assertion: Server satisfies PtyEnv.
var _ PtyEnv = (*Server)(nil)
