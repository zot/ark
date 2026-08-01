package ark

// Server-side wiring for the managed-PTY host: the Server implements PtyEnv (the
// seat lease + roster + directories the host depends on), the four HTTP handlers
// the CLI verbs proxy to, and the unix-socket attach client that plugs into the
// host's fan-out as one PtyClient.
//
// CRC: crc-PtyHost.md, crc-Server.md | R3117, R3122, R3123, R3124, R3125, R3126

import (
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
)

const (
	// luhmannLaunchDoc is the tmp:// document every launch announces itself on
	// (R3326). One document, **replaced** per launch rather than appended, so
	// PublishTmpDiff fires exactly the pair that changed and the prior outcome
	// drops out of the tag-set cache instead of accumulating.
	luhmannLaunchDoc = "tmp://luhmann/launch.md"
	// tagLuhmannHosted carries the confirmed session id of a launch that came up.
	tagLuhmannHosted = "luhmann-hosted"
	// tagLuhmannLaunchFailed carries the reason a launch was abandoned. It is
	// published rather than swallowed because a user who pressed Launch would
	// otherwise get nothing at all for as long as the seat-claim timeout runs.
	tagLuhmannLaunchFailed = "luhmann-launch-failed"
)

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
// luhmannCwd is the hosted orchestrator session's working directory
// (~/.ark/luhmann) — the single source of truth shared by the pty host
// (LuhmannDir) and the auto-index source (Config.EnsureLuhmannSource, R3135), so
// where Luhmann runs, where its log lands, and what ark indexes can never drift.
func luhmannCwd() string { return filepath.Join(arkHomeDir(), "luhmann") }

func (srv *Server) LuhmannDir() string { return luhmannCwd() }

// LuhmannChildArgv resolves the pty child's command line, delegating to the
// config so the rule lives in exactly one place (R3329, R3330, R3331). PtyEnv —
// this indirection is what keeps the host free of any config dependency.
// CRC: crc-Server.md | R3329
func (srv *Server) LuhmannChildArgv(model string) ([]string, error) {
	return srv.db.Config().Luhmann.EffectiveChildArgv(model)
}

// PoolRosterCount is the pool-secretary roster size for status (R3124). PtyEnv.
func (srv *Server) PoolRosterCount() int { return srv.recallWatcher.PoolSize() }

// RecordHostedExits records an exit for every pool secretary of the hosted
// session and drops it from the roster (R3125): the pty going down takes these
// in-session subagents down with it, so recording their exits leaves the
// monitoring log truthful rather than showing ghosts. Called on either path down
// — an `ark luhmann stop` or the child exiting on its own (R3323).
//
// The kind is "exit" either way (it resets the streak counters): the secretaries
// were healthy, and the session they lived in went away. `reason` is what
// distinguishes the two, and it is load-bearing — these records are frequently
// the only trace, since a child that dies abruptly writes no exit record of its
// own (R3324). PtyEnv.
// CRC: crc-Server.md | R3125, R3323, R3324
func (srv *Server) RecordHostedExits(_, reason string) {
	if srv.recallWatcher == nil {
		return
	}
	// R3298: record and deregister against the member's OWN class. Hardcoding
	// bloodhound here meant a recall secretary was never exit-recorded and never
	// dropped from its roster on a clean stop — a ghost that miscounts pool_max
	// and draws work to a dead tube.
	for _, m := range srv.recallWatcher.PoolNonces() {
		rec := LuhmannRecord{
			Kind:   "exit",
			Class:  m.Class,
			Nonce:  int(m.Nonce),
			Reason: reason,
		}
		_ = AppendLuhmannRecord(srv.db, srv.db.Path(), rec)
		srv.recallWatcher.DeregisterPoolSecretary(m.Class, "exit", m.Nonce)
	}
}

// launchOutcomeContent renders the tmp-doc body announcing a launch outcome
// (R3326), or nil when there is nothing to say. Split out of
// PublishLaunchOutcome so the collapsing below is testable without a DB.
//
// The collapsing is not cosmetic: a tag's value runs to end of line, so a
// multi-line error would be cut at the first newline and its tail indexed as
// loose prose rather than as part of the reason. Fields folds every run of
// whitespace, newlines included, into single spaces.
// CRC: crc-Server.md | R3326
func launchOutcomeContent(session, failure string) []byte {
	tag, value := tagLuhmannHosted, session
	if session == "" {
		tag, value = tagLuhmannLaunchFailed, failure
	}
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return nil // never publish an empty-valued tag
	}
	return []byte("@" + tag + ": " + value + "\n")
}

// PublishLaunchOutcome announces a launch's outcome on the pub/sub integration
// plane (R3326): `@luhmann-hosted: <session>` when it came up,
// `@luhmann-launch-failed: <reason>` when it was abandoned. Exactly one argument
// is set. Subscribers are symmetric — the ark app's Luhmann view, a test, a
// monitor — so this is a publication, not a UI call.
//
// The host calls it at its confirmation point, which is what makes a CLI `ark
// luhmann launch` reach a browser that never asked: both ways in pass through
// there. Failures are logged rather than returned, since a launch that worked
// must not be reported as failed because its announcement did not land. PtyEnv.
// CRC: crc-Server.md | R3326
func (srv *Server) PublishLaunchOutcome(session, failure string) {
	if srv.db == nil {
		return
	}
	content := launchOutcomeContent(session, failure)
	if content == nil {
		return // nothing to say
	}
	if err := SyncVoid(srv.db, func(db *DB) error {
		if uerr := db.UpdateTmpFile(luhmannLaunchDoc, "markdown", content); uerr != nil {
			_, aerr := db.AddTmpFile(luhmannLaunchDoc, "markdown", content)
			return aerr
		}
		return nil
	}); err != nil {
		Logv(1, "luhmann: publishing launch outcome: %v", err)
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
		// Model names the model for this launch only (R3329). The CLI omits
		// the field when --model was not given, so absent and empty mean the
		// same thing here: fall back to config.
		Model string `json:"model"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	sid, err := srv.ptyHost.Launch(req.Bootstrap, req.Model)
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
		case ptyFrameRepaint:
			srv.ptyHost.ForceRepaint() // R3136: client asked the child to redraw
		}
	}
	srv.ptyHost.Detach(reg)
}

// --- socketPtyClient: the unix-socket transport as a PtyClient (R3117) -------

// socketPtyClient adapts a hijacked connection to the host's PtyClient interface
// (R3117). It buffers nothing: a bounded channel, a drain goroutine, and an 8ms
// flush ticker used to live here, and all three moved into the host, where the
// bounded buffering and the R3118 drop are written once instead of once per
// transport (R3336, R3340, R3341). What is left is the transport itself.
type socketPtyClient struct {
	conn      net.Conn
	closeOnce sync.Once
}

func newSocketPtyClient(conn net.Conn) *socketPtyClient {
	return &socketPtyClient{conn: conn}
}

// Write writes the host's buffer to the connection in full, blocking until it is
// written (R3341). It runs on the host's per-connection write goroutine, so a
// stalled reader blocks only that goroutine; the host's pending buffer grows and
// the connection is dropped at the cap (R3340). Close is what unblocks a write
// stuck here. No bufio: the host coalesces whatever accumulates during a write
// into the next one, so batching is adaptive and there is nothing left for a
// flush timer to bound.
func (c *socketPtyClient) Write(p []byte) error {
	_, err := c.conn.Write(p)
	return err
}

// Close releases the transport once; the second call (drop + detach race) no-ops.
func (c *socketPtyClient) Close() {
	c.closeOnce.Do(func() { c.conn.Close() })
}

var _ PtyClient = (*socketPtyClient)(nil)

// interface assertion: Server satisfies PtyEnv.
var _ PtyEnv = (*Server)(nil)
