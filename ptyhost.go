package ark

// Managed-PTY host: ark serve hosts a Claude Code session inside a pty and fans
// its byte stream out to attached clients. The first hosted session is the
// Luhmann orchestrator (cwd ~/.ark/luhmann); the host itself is not
// Luhmann-specific. See specs/managed-pty.md.
//
// CRC: crc-PtyHost.md | Seq: seq-pty-launch.md, seq-pty-attach.md | R3114, R3115, R3116, R3117, R3118, R3119, R3120, R3121, R3122, R3124, R3125, R3126, R3127, R3128

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
)

const (
	// ptyDefaultBootstrap is the first input sent at launch — the string that
	// loads the orchestrator skill (R3122). Overridable with --bootstrap.
	ptyDefaultBootstrap = "load /luhmann"

	// ptyReadChunk bounds one master read; a fresh slice is allocated per read
	// so each broadcast chunk is independent of the next.
	ptyReadChunk = 32 * 1024

	// ptyLaunchSettle lets Claude Code's TUI initialize and switch the pty to raw
	// mode before we type the bootstrap (R3126). Sending earlier risks the input
	// being flushed by the canonical→raw termios switch; empirically the REPL is
	// ready well within this window.
	ptyLaunchSettle = 3 * time.Second
	// ptyLaunchSeatTimeout caps the wait for the seat claim — the authoritative,
	// content-free confirmation that /luhmann loaded and claimed the seat (R3126).
	// Generous: it covers Claude Code startup plus skill load.
	ptyLaunchSeatTimeout = 150 * time.Second
	// ptyLaunchPoll is the seat-claim poll cadence.
	ptyLaunchPoll = 250 * time.Millisecond

	// ptyInitCols/ptyInitRows seed the pty before any client attaches; the
	// first attach recomputes to the client's size (R3120).
	ptyInitCols = 80
	ptyInitRows = 24

	// ptyStopGrace is how long a teardown waits for the child to exit after
	// SIGTERM before escalating to SIGKILL (R3125): a graceful stop lets claude
	// flush its transcript and run its exit hooks rather than being hard-killed.
	ptyStopGrace = 5 * time.Second

	// ptyTrustTimeout bounds how long a new-project launch waits for the "trust
	// this folder" dialog to be accepted before sending the bootstrap (R3128).
	// Generous: the dialog renders within ~2s; if it never appears (the project
	// was not actually new) the launch proceeds anyway.
	ptyTrustTimeout = 10 * time.Second
	// ptyTrustScanCap bounds the early output accumulated while hunting for the
	// trust dialog, so a session that never shows one cannot grow the buffer.
	ptyTrustScanCap = 64 * 1024
)

// PtyWinsize is a client's reported terminal size, the unit of the smallest-wins
// resize (R3120). Cols/Rows only — pixel dims are not used.
type PtyWinsize struct{ Cols, Rows uint16 }

// PtyClient is one attached consumer of a hosted pty session, addressed through
// this transport-agnostic interface (R3117): the CLI attach client (unix socket)
// implements it now, a browser xterm.js client (websocket) later. The host
// broadcasts output to it and closes it on drop or teardown; input and size flow
// the other way, into the host (Input / Attach / Resize).
type PtyClient interface {
	// Write enqueues output bytes for delivery to this client's transport.
	// Non-blocking: it returns false when the client's bounded buffer has
	// overflowed, which signals the host to drop this client rather than let it
	// stall the fan-out (R3118).
	Write(p []byte) bool
	// Close tears down the client's transport (on drop, detach, or host stop).
	Close()
}

// ptyClientReg is the host's per-client record: the client plus its last
// reported size, both owned by the actor (touched only inside jobs closures).
type ptyClientReg struct {
	client PtyClient
	size   PtyWinsize
}

// PtyEnv is the narrow slice of the server the host depends on: the Luhmann seat
// lease (launch waits on a claim, stop releases it), the pool roster (status
// count + stop exit recording), and the two directories the confirmation
// protocol reads. Kept small so the host stays decoupled and testable with a
// fake env. R3124, R3125, R3126
type PtyEnv interface {
	SeatOwner() string                // current Luhmann seat owner ("" = unowned)
	ForceReleaseSeat()                // clear any seat claim before a launch (R3126)
	ReleaseSeat(session string)       // release the lease this session holds (R3125)
	RecordHostedExits(session string) // record + deregister the hosted pool secretaries (R3125)
	LuhmannDir() string               // ~/.ark/luhmann — the hosted session's cwd
	PoolRosterCount() int             // pool-secretary roster size (R3124)
}

// PtyHost owns one hosted Claude Code pty session (R3116). A Closure Actor: the
// master, child, and client set are touched only inside jobs closures, so no
// lock is held across pty I/O. Two dedicated goroutines keep the streaming reads
// and writes off the actor: readLoop drains the master and broadcasts; inputLoop
// serializes client input onto the master (R3119). masterPtr mirrors the current
// master for the lock-free input path.
type PtyHost struct {
	env  PtyEnv
	jobs chan func() // actor: client set + lifecycle state
	inCh chan []byte // serialized input funnel → inputLoop → master (R3119)

	masterPtr atomic.Pointer[os.File] // current master, for inputLoop's lock-free read

	// actor-owned — touch ONLY inside a jobs closure:
	master    *os.File
	child     *exec.Cmd
	clients   map[*ptyClientReg]struct{}
	sessionID string
	launching bool

	// resizeSink applies a recomputed pty size. nil in production → pty.Setsize
	// on the master (the real SIGWINCH). A seam so the deterministic smallest-
	// wins logic (R3120) is unit-testable without a real pty (test-PtyHost.md).
	resizeSink func(PtyWinsize)

	// trust-dialog acceptance (R3128), armed only when launching into a project
	// Claude Code has not seen. trustBuf is touched by readLoop alone; trustArmed
	// and trustDone are established by Launch before readLoop starts and are the
	// synchronization with it.
	trustArmed atomic.Bool
	trustBuf   []byte        // readLoop-only: early output accumulated while hunting
	trustDone  chan struct{} // closed once the dialog is accepted
}

// NewPtyHost constructs the host and starts its actor + input goroutines. The
// host idles with no session until Launch (R3114: never proactive — construction
// starts no `claude`).
func NewPtyHost(env PtyEnv) *PtyHost {
	h := &PtyHost{
		env:     env,
		jobs:    make(chan func(), 16),
		inCh:    make(chan []byte, 64),
		clients: make(map[*ptyClientReg]struct{}),
	}
	runSvc(h.jobs)
	go h.inputLoop()
	return h
}

// --- launch (R3114, R3116, R3122, R3126) -----------------------------------

// Launch forks the pty (cwd ~/.ark/luhmann), starts `claude`, and runs the
// content-free confirmation protocol (R3126): await the new session's second
// JSONL record, send the bootstrap, await the seat claim (which teaches ark the
// session id). It is the sole spend-consent gate (R3114) and rejects a second
// concurrent session (R3122). Returns the confirmed session id or a timeout
// error. Blocks for the length of the protocol, so it runs on the caller's
// goroutine, not the actor.
// CRC: crc-PtyHost.md | Seq: seq-pty-launch.md#1.1 | R3114, R3122, R3126, R3128
func (h *PtyHost) Launch(bootstrap string) (sessionID string, err error) {
	if strings.TrimSpace(bootstrap) == "" {
		bootstrap = ptyDefaultBootstrap
	}
	// Reserve: reject if a session is already hosted or a launch is mid-flight
	// (R3122 — one at a time).
	if rerr := svcSyncVoid(h.jobs, func() error {
		if h.master != nil || h.launching {
			return errors.New("a Luhmann session is already hosted")
		}
		h.launching = true
		return nil
	}); rerr != nil {
		return "", rerr
	}
	defer func() {
		if err != nil {
			_ = svcSyncVoid(h.jobs, func() error { h.launching = false; return nil })
		}
	}()

	// Clear any prior seat claim so the new session's --first is the one we
	// observe (R3126). A stale owner — a prior session that died without
	// releasing the in-memory lease — would otherwise block the new claim; a
	// live rival is resolved by the lease protocol's --first race. The managed
	// launch is the authoritative start, so it takes the seat.
	h.env.ForceReleaseSeat()

	// Fork the pty and start `claude` as a child of the server (R3116).
	cmd := exec.Command("claude", "--model", "opus")
	cmd.Dir = h.env.LuhmannDir()
	cmd.Env = ptyChildEnv()
	master, ferr := pty.StartWithSize(cmd, &pty.Winsize{Rows: ptyInitRows, Cols: ptyInitCols})
	if ferr != nil {
		return "", fmt.Errorf("start claude: %w", ferr)
	}
	// Install the master and start the reader immediately: the child's output
	// must be drained continuously or the pty buffer fills and `claude` blocks —
	// even with zero clients attached (R3121). We never read it for session
	// *state*; that comes from the JSONL tap, not this stream (R3115).
	_ = svcSyncVoid(h.jobs, func() error {
		h.master = master
		h.child = cmd
		return nil
	})
	h.masterPtr.Store(master)

	// R3128: a directory Claude Code has not seen shows a "trust this folder"
	// dialog that would swallow the bootstrap. If this project is new, arm the
	// trust accepter before the reader starts, so readLoop clears the dialog
	// first. Armed only for new projects, so it cannot misfire on a normal
	// session's later output.
	newProject := isNewClaudeProject(cmd.Dir)
	if newProject {
		h.trustBuf = nil
		h.trustDone = make(chan struct{})
		h.trustArmed.Store(true)
	}
	go h.readLoop(master)

	// For a new project, wait for the trust dialog to be accepted before the
	// bootstrap so it lands in the message box, not the menu (R3128). Best
	// effort: if the dialog never appears (the heuristic was wrong), proceed
	// after the timeout rather than block the launch.
	if newProject {
		select {
		case <-h.trustDone:
		case <-time.After(ptyTrustTimeout):
			h.trustArmed.Store(false) // close the scan window; no dialog came
		}
	}

	// Let the TUI initialize and switch the pty to raw mode, then send the
	// bootstrap (R3126). Claude Code writes no session JSONL until a turn
	// boundary, so there is no pre-input log to gate on — the bootstrap is what
	// starts the session.
	time.Sleep(ptyLaunchSettle)
	if !h.hosting(master) {
		return "", fmt.Errorf("launch: claude exited during startup")
	}
	h.Input([]byte(bootstrap + "\r"))

	// Await the seat claim — the authoritative, content-free confirmation that
	// /luhmann loaded and claimed the seat via `ark luhmann next --first`, and the
	// event that teaches ark the session id (R3126). Fail fast if the child exits.
	sid, serr := h.waitSeatClaim(master, ptyLaunchSeatTimeout)
	if serr != nil {
		h.finalize(master)
		return "", fmt.Errorf("launch: %w", serr)
	}
	_ = svcSyncVoid(h.jobs, func() error {
		h.sessionID = sid
		h.launching = false
		return nil
	})
	return sid, nil
}

// hosting reports whether m is still the installed master — false once the child
// has exited and finalize has run. The launch fail-fast check.
func (h *PtyHost) hosting(m *os.File) bool {
	var ok bool
	_ = svcSyncVoid(h.jobs, func() error { ok = h.master == m; return nil })
	return ok
}

// --- fan-out: broadcast, input merge, resize (R3117–R3121) ------------------

// readLoop drains the master and broadcasts each chunk to every client until the
// master closes — child self-exit or Stop (R3116). It then finalizes: a self-exit
// leaves the session down, never auto-relaunched (R3114). Output is never parsed
// for session state (R3115); it is opaque bytes to fan out.
// CRC: crc-PtyHost.md | Seq: seq-pty-attach.md#1.3 | R3115, R3118
func (h *PtyHost) readLoop(m *os.File) {
	for {
		buf := make([]byte, ptyReadChunk)
		n, rerr := m.Read(buf)
		if n > 0 {
			if h.trustArmed.Load() {
				h.maybeAcceptTrust(buf[:n]) // R3128: clear a fresh-project trust dialog
			}
			h.broadcast(m, buf[:n])
		}
		if rerr != nil {
			break
		}
	}
	h.finalize(m)
}

// maybeAcceptTrust accumulates early child output and, on detecting the "trust
// this folder" dialog, selects the "Yes, I trust" option (by the number read from
// the stream) and disarms — so the bootstrap that follows lands in the message
// box, not the menu (R3128). Runs on the readLoop goroutine (sole owner of
// trustBuf) and sends the accept through Input, keeping the single serialized
// writer (R3119). The CompareAndSwap makes the accept fire at most once and lose
// cleanly to a launch that has already timed out and disarmed.
// CRC: crc-PtyHost.md | Seq: seq-pty-launch.md#1.3.1 | R3128
func (h *PtyHost) maybeAcceptTrust(chunk []byte) {
	h.trustBuf = append(h.trustBuf, chunk...)
	if len(h.trustBuf) > ptyTrustScanCap {
		h.trustBuf = h.trustBuf[len(h.trustBuf)-ptyTrustScanCap:]
	}
	keys, ok := scanTrustAccept(h.trustBuf)
	if !ok {
		return
	}
	if !h.trustArmed.CompareAndSwap(true, false) {
		return // launch already disarmed on timeout
	}
	h.trustBuf = nil
	h.Input(keys)
	close(h.trustDone)
}

// broadcast writes one output chunk to every attached client, dropping any whose
// bounded buffer overflowed rather than letting it stall the others or the
// session (R3118). Runs in the actor (client-set access) but does only
// non-blocking Writes, so the streaming read never stalls on a slow client.
// CRC: crc-PtyHost.md | Seq: seq-pty-attach.md#1.3 | R3118
func (h *PtyHost) broadcast(m *os.File, chunk []byte) {
	_ = svcSyncVoid(h.jobs, func() error {
		if h.master != m {
			return nil // stale reader after finalize
		}
		var dropped []*ptyClientReg
		for reg := range h.clients {
			if !reg.client.Write(chunk) {
				dropped = append(dropped, reg)
			}
		}
		for _, reg := range dropped {
			delete(h.clients, reg)
			reg.client.Close()
		}
		if len(dropped) > 0 {
			h.recomputeSizeLocked() // R3120: a drop is a detach — recompute the min
		}
		return nil
	})
}

// inputLoop is the single serialized writer to the master (R3119): it drains
// inCh and writes each client's chunk contiguously, so two clients' bytes cannot
// interleave mid-sequence. Lock-free — it reads the current master via masterPtr,
// which Launch sets and finalize clears.
// CRC: crc-PtyHost.md | Seq: seq-pty-attach.md#1.4 | R3119
func (h *PtyHost) inputLoop() {
	for cp := range h.inCh {
		if m := h.masterPtr.Load(); m != nil {
			_, _ = m.Write(cp)
		}
	}
}

// Input forwards one client's input toward the child, serialized through inCh
// (R3119). Copies the slice so the caller may reuse its buffer.
// CRC: crc-PtyHost.md | Seq: seq-pty-attach.md#1.4 | R3119
func (h *PtyHost) Input(p []byte) {
	cp := append([]byte(nil), p...)
	h.inCh <- cp
}

// Attach registers a client and recomputes the pty size to the new minimum,
// SIGWINCH-ing the child (R3117, R3120). Errors when no session is hosted.
// CRC: crc-PtyHost.md | Seq: seq-pty-attach.md#1.2 | R3117, R3120, R3121
func (h *PtyHost) Attach(c PtyClient, size PtyWinsize) (*ptyClientReg, error) {
	var reg *ptyClientReg
	err := svcSyncVoid(h.jobs, func() error {
		if h.master == nil {
			return errors.New("no Luhmann session is hosted")
		}
		reg = &ptyClientReg{client: c, size: size}
		h.clients[reg] = struct{}{}
		h.recomputeSizeLocked()
		return nil
	})
	return reg, err
}

// Resize records a client's new size and recomputes the minimum (R3120).
// CRC: crc-PtyHost.md | Seq: seq-pty-attach.md#1.5 | R3120
func (h *PtyHost) Resize(reg *ptyClientReg, size PtyWinsize) {
	_ = svcSyncVoid(h.jobs, func() error {
		if _, ok := h.clients[reg]; ok {
			reg.size = size
			h.recomputeSizeLocked()
		}
		return nil
	})
}

// Detach deregisters a client and recomputes the minimum, so the pty grows when
// the smallest client leaves (R3120); the session survives with the remaining
// clients or with none (R3121). A detach never disturbs another client.
// CRC: crc-PtyHost.md | Seq: seq-pty-attach.md#1.6 | R3120, R3121
func (h *PtyHost) Detach(reg *ptyClientReg) {
	_ = svcSyncVoid(h.jobs, func() error {
		if _, ok := h.clients[reg]; ok {
			delete(h.clients, reg)
			reg.client.Close()
			h.recomputeSizeLocked()
		}
		return nil
	})
}

// minSizeLocked returns the smallest-wins pty size across attached clients and
// whether one is available (a hosted master plus at least one sized client). R3120
func (h *PtyHost) minSizeLocked() (PtyWinsize, bool) {
	if h.master == nil || len(h.clients) == 0 {
		return PtyWinsize{}, false
	}
	var cols, rows uint16
	for reg := range h.clients {
		if reg.size.Cols == 0 || reg.size.Rows == 0 {
			continue
		}
		if cols == 0 || reg.size.Cols < cols {
			cols = reg.size.Cols
		}
		if rows == 0 || reg.size.Rows < rows {
			rows = reg.size.Rows
		}
	}
	if cols == 0 || rows == 0 {
		return PtyWinsize{}, false
	}
	return PtyWinsize{Cols: cols, Rows: rows}, true
}

// setSizeLocked applies a pty size to the child — through the resizeSink seam
// when set (tests), else a real TIOCSWINSZ on the master.
func (h *PtyHost) setSizeLocked(size PtyWinsize) {
	if h.resizeSink != nil {
		h.resizeSink(size)
		return
	}
	if h.master != nil {
		_ = pty.Setsize(h.master, &pty.Winsize{Rows: size.Rows, Cols: size.Cols})
	}
}

// recomputeSizeLocked sets the pty size to the smallest-wins minimum and
// SIGWINCHes the child (R3120). With no clients it leaves the size as is — zero
// attached clients leaves the session running untouched (R3121). Must run inside
// a jobs closure (touches the client set + master).
// CRC: crc-PtyHost.md | R3120, R3121
func (h *PtyHost) recomputeSizeLocked() {
	if size, ok := h.minSizeLocked(); ok {
		h.setSizeLocked(size)
	}
}

// ptyRepaintNudge is how long ForceRepaint holds the shrunk size before restoring
// it. The child's SIGWINCH handler must observe the *intermediate* size to
// repaint: a synchronous shrink-then-restore coalesces (signals are not queued),
// so the handler runs once, reads the restored — unchanged — size, and skips the
// redraw. Holding the shrink a beat makes the change observable. R3136
const ptyRepaintNudge = 100 * time.Millisecond

// ForceRepaint forces the child to redraw the whole screen (R3136): shrink the pty
// one row now, then restore it after ptyRepaintNudge. ark holds no virtual screen
// (R3115), so the only lever is a real SIGWINCH the child repaints on — and the
// delay is what makes the shrink observable (a rapid toggle back to the same size
// is invisible to a coalescing handler; only an *observed* size change repaints).
// The host's answer to a client's repaint frame, on attach (R3137) and on a detach
// cancel (R3138).
// CRC: crc-PtyHost.md | R3136
func (h *PtyHost) ForceRepaint() {
	var shrank bool
	_ = svcSyncVoid(h.jobs, func() error {
		if sz, ok := h.minSizeLocked(); ok && sz.Rows >= 2 {
			h.setSizeLocked(PtyWinsize{Cols: sz.Cols, Rows: sz.Rows - 1})
			Logv(1, "pty force-repaint: shrink to %dx%d, restore in %v", sz.Cols, sz.Rows-1, ptyRepaintNudge)
			shrank = true
		}
		return nil
	})
	if !shrank {
		return
	}
	time.AfterFunc(ptyRepaintNudge, func() {
		_ = svcSyncVoid(h.jobs, func() error {
			if sz, ok := h.minSizeLocked(); ok {
				h.setSizeLocked(sz)
			}
			return nil
		})
	})
}

// --- stop + status (R3124, R3125) -------------------------------------------

// Stop is a graceful teardown, not a bare kill (R3125): because tearing the pty
// down takes the hosted session's pool secretaries with it, Stop first records
// their exits (so the monitoring log shows no ghosts), then finalizes — which
// SIGTERMs the child (SIGKILL only if it hangs), letting claude flush its
// transcript, and releases the seat lease. Errors when no session is hosted.
// CRC: crc-PtyHost.md | R3125
func (h *PtyHost) Stop() error {
	var session string
	var m *os.File
	_ = svcSyncVoid(h.jobs, func() error {
		session, m = h.sessionID, h.master
		return nil
	})
	if m == nil {
		return errors.New("no Luhmann session is hosted")
	}
	// R3125: record the hosted pool secretaries' exits before the teardown that
	// takes them down, leaving the monitoring log truthful.
	if session != "" {
		h.env.RecordHostedExits(session)
	}
	// finalize kills the child, closes the clients, and releases the seat; it is
	// idempotent with the reader's finalize on the resulting EOF. Synchronous, so
	// status reflects the teardown the moment Stop returns.
	h.finalize(m)
	return nil
}

// Status reports whether a session is hosted (master installed), the hosted
// session id, and the pool-secretary roster count (R3124).
// CRC: crc-PtyHost.md | R3124
func (h *PtyHost) Status() (hosted bool, sessionID string) {
	_ = svcSyncVoid(h.jobs, func() error {
		hosted = h.master != nil
		sessionID = h.sessionID
		return nil
	})
	return hosted, sessionID
}

// finalize tears down the hosted session exactly once (idempotent on the master
// identity): closes every client, clears the actor state, releases the seat lease
// so status stays truthful, and hands the child + master to an off-actor graceful
// shutdown. Called on child self-exit (readLoop) and by Stop; the second caller
// is a no-op. The actor clears state immediately; the SIGTERM grace wait and the
// master close happen off the actor so it never stalls.
// CRC: crc-PtyHost.md | R3114, R3116, R3125
func (h *PtyHost) finalize(m *os.File) {
	_ = svcSyncVoid(h.jobs, func() error {
		if h.master != m {
			return nil // already finalized (self-exit + Stop race)
		}
		session := h.sessionID
		for reg := range h.clients {
			reg.client.Close()
		}
		h.clients = make(map[*ptyClientReg]struct{})
		child := h.child
		h.masterPtr.Store(nil)
		h.master, h.child, h.sessionID, h.launching = nil, nil, "", false
		if session != "" {
			h.env.ReleaseSeat(session)
		}
		// Off the actor: leave the master open through the grace window so
		// claude's flush-on-exit output is still drained by readLoop, then close.
		go gracefulShutdown(child, m)
		return nil
	})
}

// gracefulShutdown asks the child to exit with SIGTERM, waits up to ptyStopGrace,
// escalates to SIGKILL if it is still alive, reaps it, then closes the master
// (R3125). Safe for both a Stop (child alive → graceful, then hard-kill on hang)
// and a child self-exit (child already dead → the signal is a harmless no-op).
// Without this, a launch-confirmation failure or a Stop would leak `claude`.
func gracefulShutdown(child *exec.Cmd, m *os.File) {
	if child != nil && child.Process != nil {
		_ = child.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _, _ = child.Process.Wait(); close(done) }() // reap; avoid a zombie
		select {
		case <-done:
		case <-time.After(ptyStopGrace):
			_ = child.Process.Kill()
			<-done
		}
	}
	if m != nil {
		_ = m.Close()
	}
}

// --- launch-confirmation helpers (R3126) ------------------------------------

// waitSeatClaim blocks until the Luhmann seat is claimed (owner becomes
// non-empty) or the timeout elapses (R3126), returning the claiming session id.
// This is the authoritative, content-free launch confirmation: it fires only
// once the launched session has loaded /luhmann and run `ark luhmann next
// --first`. Fails fast if the child exits before claiming (master uninstalled).
func (h *PtyHost) waitSeatClaim(m *os.File, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		if owner := h.env.SeatOwner(); owner != "" {
			return owner, nil
		}
		if !h.hosting(m) {
			return "", errors.New("claude exited before claiming the Luhmann seat")
		}
		if time.Now().After(deadline) {
			return "", errors.New("timed out waiting for the session to claim the Luhmann seat")
		}
		time.Sleep(ptyLaunchPoll)
	}
}

// ptyChildEnv is the hosted child's environment: the server's, filtered so the
// child launches as a fresh top-level session with TERM ensured (R3127).
//
// CRC: crc-PtyHost.md | R3127
func ptyChildEnv() []string {
	return filterChildEnv(os.Environ())
}

// filterChildEnv strips the Claude Code session-identity markers from src so the
// hosted child does not read itself as a nested sub-session (which would make it
// refuse to complete an interactive turn, so the bootstrap never submits and no
// JSONL is ever written), and ensures TERM so the TUI renders. Credentials
// (ANTHROPIC*) and other config pass through untouched.
//
// CRC: crc-PtyHost.md | R3127
func filterChildEnv(src []string) []string {
	env := make([]string, 0, len(src)+1)
	hasTerm := false
	for _, e := range src {
		// R3127: these markers tell a child claude "you are a nested session".
		if strings.HasPrefix(e, "CLAUDECODE=") ||
			strings.HasPrefix(e, "CLAUDE_CODE_") ||
			strings.HasPrefix(e, "AI_AGENT=") {
			continue
		}
		if strings.HasPrefix(e, "TERM=") {
			hasTerm = true
		}
		env = append(env, e)
	}
	if !hasTerm {
		env = append(env, "TERM=xterm-256color")
	}
	// R3139: mark the child as an ark-managed pty session so the /luhmann skill can
	// tell it from a bare `/luhmann` invocation and surface the detach hint.
	env = append(env, "ARK_MANAGED_PTY=1")
	return env
}

// --- trust-dialog acceptance (R3128) ----------------------------------------

// ansiEscapeRe matches the escape sequences Claude Code's TUI emits — CSI (colors,
// cursor moves by row or column), OSC (window title), and charset designation.
// Stripping them before scanning is essential: cursor-move parameters carry digits
// (e.g. "\x1b[14G" lands immediately before "trust"), which a naive backward scan
// for the option number would otherwise mistake for the number itself (R3128).
var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][A-Za-z0-9]`)

// trustOptionRe pulls the accept option out of escape-stripped output: the digits
// of the "N. Yes … trust" line. Requiring "Yes" between the number and "trust"
// excludes the question ("Do you trust the files …") and reads whichever number the
// "Yes, I trust" option carries, so a reordered or re-defaulted menu still yields
// the correct key (R3128).
var trustOptionRe = regexp.MustCompile(`(?i)([0-9]+)\.[^0-9]{0,60}?Yes[^0-9]{0,60}?trust`)

// stripANSI removes terminal escape sequences from b, leaving printable content in
// stream order (R3128).
func stripANSI(b []byte) []byte {
	return ansiEscapeRe.ReplaceAll(b, nil)
}

// scanTrustAccept detects Claude Code's "trust this folder" dialog in out and, if
// present, returns the keystrokes that select the "Yes, I trust" option — the
// option number read from the escape-stripped stream, then Enter — with ok=true.
// Reading the number (rather than assuming a default or a fixed "1") answers a
// reordered or re-defaulted menu correctly (R3128).
func scanTrustAccept(out []byte) (keys []byte, ok bool) {
	m := trustOptionRe.FindSubmatch(stripANSI(out))
	if m == nil {
		return nil, false
	}
	return append(append([]byte{}, m[1]...), '\r'), true
}

// claudeProjectDir is Claude Code's per-project log directory for cwd:
// ~/.claude/projects/<cwd with '/' and '.' replaced by '-'> (R3128).
func claudeProjectDir(cwd string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	enc := strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
	return filepath.Join(home, ".claude", "projects", enc)
}

// isNewClaudeProject reports whether Claude Code has never run in cwd — its project
// directory does not exist yet — which is exactly when the trust dialog appears
// (R3128). Any stat/home error is treated as "not new" so a false arm never blocks
// a launch.
func isNewClaudeProject(cwd string) bool {
	dir := claudeProjectDir(cwd)
	if dir == "" {
		return false
	}
	_, err := os.Stat(dir)
	return errors.Is(err, os.ErrNotExist)
}

// --- attach wire framing (client → host) ------------------------------------
//
// The host → client direction is raw pty output (the client streams it to
// stdout). The client → host direction multiplexes two frame kinds over the one
// hijacked connection: input data and resize. Each frame is a 1-byte kind then a
// kind-specific payload.

const (
	ptyFrameData    byte = 'd' // input: kind, uint32 length (BE), then length bytes
	ptyFrameResize  byte = 'r' // resize: kind, uint16 cols (BE), uint16 rows (BE)
	ptyFrameRepaint byte = 'p' // repaint: kind only — force the child to redraw (R3136)
)

// WritePtyInput writes a client→host input frame (R3119: the host serializes it
// onto the master).
func WritePtyInput(w io.Writer, p []byte) error {
	var hdr [5]byte
	hdr[0] = ptyFrameData
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(p)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(p)
	return err
}

// WritePtyResize writes a client→host resize frame (R3120: the host recomputes
// the smallest-wins minimum).
func WritePtyResize(w io.Writer, size PtyWinsize) error {
	var frame [5]byte
	frame[0] = ptyFrameResize
	binary.BigEndian.PutUint16(frame[1:], size.Cols)
	binary.BigEndian.PutUint16(frame[3:], size.Rows)
	_, err := w.Write(frame[:])
	return err
}

// WritePtyRepaint writes a client→host repaint frame (R3136): a single kind byte
// asking the host to force the child to redraw the whole screen. No payload.
func WritePtyRepaint(w io.Writer) error {
	_, err := w.Write([]byte{ptyFrameRepaint})
	return err
}

// ReadPtyFrame reads one client→host frame. On kind ptyFrameData it returns the
// input bytes in data; on ptyFrameResize it returns the size in ws; ptyFrameRepaint
// carries only the kind.
func ReadPtyFrame(r io.Reader) (kind byte, data []byte, ws PtyWinsize, err error) {
	var k [1]byte
	if _, err = io.ReadFull(r, k[:]); err != nil {
		return 0, nil, ws, err
	}
	switch k[0] {
	case ptyFrameData:
		var lenb [4]byte
		if _, err = io.ReadFull(r, lenb[:]); err != nil {
			return 0, nil, ws, err
		}
		n := binary.BigEndian.Uint32(lenb[:])
		data = make([]byte, n)
		if _, err = io.ReadFull(r, data); err != nil {
			return 0, nil, ws, err
		}
		return ptyFrameData, data, ws, nil
	case ptyFrameResize:
		var b [4]byte
		if _, err = io.ReadFull(r, b[:]); err != nil {
			return 0, nil, ws, err
		}
		ws = PtyWinsize{Cols: binary.BigEndian.Uint16(b[:2]), Rows: binary.BigEndian.Uint16(b[2:])}
		return ptyFrameResize, nil, ws, nil
	case ptyFrameRepaint:
		return ptyFrameRepaint, nil, ws, nil
	default:
		return 0, nil, ws, fmt.Errorf("unknown pty frame kind %q", k[0])
	}
}
