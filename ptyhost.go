package ark

// Managed-PTY host: ark serve hosts a Claude Code session inside a pty and fans
// its byte stream out to attached clients. The first hosted session is the
// Luhmann orchestrator (cwd ~/.ark/luhmann); the host itself is not
// Luhmann-specific. See specs/managed-pty.md.
//
// CRC: crc-PtyHost.md | Seq: seq-pty-launch.md, seq-pty-attach.md | R3114, R3115, R3116, R3117, R3118, R3119, R3120, R3121, R3122, R3124, R3125, R3126

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
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

	// ptyLaunchJSONLTimeout caps the wait for the new session's second JSONL
	// record — the cheap early liveness gate (R3126 step 1).
	ptyLaunchJSONLTimeout = 30 * time.Second
	// ptyLaunchSeatTimeout caps the wait for the seat claim — the authoritative
	// confirmation that /luhmann loaded and attached (R3126 step 3). Generous:
	// it covers Claude Code startup plus skill load.
	ptyLaunchSeatTimeout = 150 * time.Second
	// ptyLaunchPoll is the poll cadence for both launch waits.
	ptyLaunchPoll = 250 * time.Millisecond

	// ptyInitCols/ptyInitRows seed the pty before any client attaches; the
	// first attach recomputes to the client's size (R3120).
	ptyInitCols = 80
	ptyInitRows = 24
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
	ReleaseSeat(session string)       // release the lease this session holds (R3125)
	RecordHostedExits(session string) // record + deregister the hosted pool secretaries (R3125)
	LuhmannDir() string               // ~/.ark/luhmann — the hosted session's cwd
	ProjectsDir() string              // ~/.claude/projects — where session JSONLs live
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
// CRC: crc-PtyHost.md | Seq: seq-pty-launch.md#1.1 | R3114, R3122, R3126
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

	// The seat must be unowned so the new session's --first claim is the one we
	// observe (R3126 step 3). A live owner means another orchestrator holds the
	// seat — refuse rather than launch a rival.
	if owner := h.env.SeatOwner(); owner != "" {
		return "", fmt.Errorf("the Luhmann seat is already owned by session %s; stop it first", owner)
	}

	dir := h.env.LuhmannDir()
	projDir := filepath.Join(h.env.ProjectsDir(), claudeProjectSlug(dir))
	before := listSessionJSONL(projDir) // R3126 step 1: snapshot pre-launch JSONLs

	// Fork the pty and start `claude` as a child of the server (R3116).
	cmd := exec.Command("claude")
	cmd.Dir = dir
	cmd.Env = ptyChildEnv()
	master, ferr := pty.StartWithSize(cmd, &pty.Winsize{Rows: ptyInitRows, Cols: ptyInitCols})
	if ferr != nil {
		return "", fmt.Errorf("start claude: %w", ferr)
	}
	// Install the master and start the reader immediately: the child's output
	// must be drained continuously or the pty buffer fills and `claude` blocks —
	// even with zero clients attached (R3121). We never read it for session
	// *state*; that comes from the JSONL (R3115).
	_ = svcSyncVoid(h.jobs, func() error {
		h.master = master
		h.child = cmd
		return nil
	})
	h.masterPtr.Store(master)
	go h.readLoop(master)

	// Step 1 (R3126): wait for the new session's second JSONL record — a cheap
	// early liveness gate, and a fail-fast if `claude` never starts.
	if werr := waitSecondRecord(projDir, before, ptyLaunchJSONLTimeout); werr != nil {
		h.finalize(master)
		return "", fmt.Errorf("launch: %w", werr)
	}
	// Step 2 (R3126): send the bootstrap. Claude Code buffers input typed at any
	// time, so ordering against startup is safe.
	h.Input([]byte(bootstrap + "\r"))
	// Step 3 (R3126): wait for the launched session to claim the seat via
	// `ark luhmann next --first`. The claim is authoritative and teaches us the
	// session id.
	sid, serr := waitSeatClaim(h.env, ptyLaunchSeatTimeout)
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
			h.broadcast(m, buf[:n])
		}
		if rerr != nil {
			break
		}
	}
	h.finalize(m)
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

// recomputeSizeLocked sets the pty size to the minimum of all attached clients'
// sizes and SIGWINCHes the child (R3120). With no clients it leaves the size as
// is — zero attached clients leaves the session running untouched (R3121). Must
// run inside a jobs closure (touches the client set + master).
// CRC: crc-PtyHost.md | R3120, R3121
func (h *PtyHost) recomputeSizeLocked() {
	if h.master == nil || len(h.clients) == 0 {
		return
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
		return
	}
	size := PtyWinsize{Cols: cols, Rows: rows}
	if h.resizeSink != nil {
		h.resizeSink(size)
		return
	}
	_ = pty.Setsize(h.master, &pty.Winsize{Rows: rows, Cols: cols})
}

// --- stop + status (R3124, R3125) -------------------------------------------

// Stop is a graceful teardown, not a bare kill (R3125): because killing the pty
// takes the hosted session's pool secretaries down with it, Stop first records
// their exits (so the monitoring log shows no ghosts), then kills the child; the
// reader's finalize releases the seat lease and closes the clients. Errors when
// no session is hosted.
// CRC: crc-PtyHost.md | R3125
func (h *PtyHost) Stop() error {
	var session string
	var m *os.File
	var child *exec.Cmd
	_ = svcSyncVoid(h.jobs, func() error {
		session, m, child = h.sessionID, h.master, h.child
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
	// Kill the child; readLoop then hits EOF and finalizes (releases the seat,
	// closes the clients). Finalize synchronously too, so status reflects the
	// teardown the moment Stop returns — idempotent with the reader's call.
	if child != nil && child.Process != nil {
		_ = child.Process.Kill()
	}
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
// identity): closes every client, closes the master, clears the actor state,
// reaps the child, and releases the seat lease so status stays truthful. Called
// on child self-exit (readLoop) and by Stop; the second caller is a no-op.
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
		h.masterPtr.Store(nil)
		_ = m.Close()
		child := h.child
		h.master, h.child, h.sessionID, h.launching = nil, nil, "", false
		if child != nil {
			go func() { _, _ = child.Process.Wait() }() // reap; avoid a zombie
		}
		if session != "" {
			h.env.ReleaseSeat(session)
		}
		return nil
	})
}

// --- launch-confirmation helpers (R3126) ------------------------------------

// claudeProjectSlug encodes an absolute cwd the way Claude Code names its
// per-project directory under ~/.claude/projects: every '/' and '.' becomes '-'
// (e.g. /home/deck/.ark/luhmann → -home-deck--ark-luhmann).
func claudeProjectSlug(absDir string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(absDir)
}

// listSessionJSONL returns the set of *.jsonl basenames currently in a project
// directory — the pre-launch snapshot the second-record gate diffs against.
func listSessionJSONL(projDir string) map[string]struct{} {
	set := make(map[string]struct{})
	matches, _ := filepath.Glob(filepath.Join(projDir, "*.jsonl"))
	for _, p := range matches {
		set[filepath.Base(p)] = struct{}{}
	}
	return set
}

// waitSecondRecord blocks until a JSONL file absent from the pre-launch snapshot
// (the new session's log) has at least two records, or the timeout elapses
// (R3126 step 1). Best-effort liveness — the authoritative gate is the seat claim.
func waitSecondRecord(projDir string, before map[string]struct{}, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		matches, _ := filepath.Glob(filepath.Join(projDir, "*.jsonl"))
		for _, p := range matches {
			if _, seen := before[filepath.Base(p)]; seen {
				continue
			}
			if countLines(p) >= 2 {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return errors.New("timed out waiting for the session to start (no new JSONL activity)")
		}
		time.Sleep(ptyLaunchPoll)
	}
}

// countLines counts newline-terminated records in a file; 0 on any read error.
func countLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	buf := make([]byte, 64*1024)
	count := 0
	for {
		n, rerr := f.Read(buf)
		for _, b := range buf[:n] {
			if b == '\n' {
				count++
			}
		}
		if rerr != nil {
			break
		}
	}
	return count
}

// waitSeatClaim blocks until the Luhmann seat is claimed (owner becomes
// non-empty) or the timeout elapses (R3126 step 3), returning the claiming
// session id. This is the authoritative launch confirmation: it fires only once
// the launched session has loaded /luhmann and run `ark luhmann next --first`.
func waitSeatClaim(env PtyEnv, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		if owner := env.SeatOwner(); owner != "" {
			return owner, nil
		}
		if time.Now().After(deadline) {
			return "", errors.New("timed out waiting for the session to claim the Luhmann seat")
		}
		time.Sleep(ptyLaunchPoll)
	}
}

// ptyChildEnv is the child's environment: the server's, with TERM ensured so the
// TUI renders before any client attaches.
func ptyChildEnv() []string {
	env := os.Environ()
	for _, e := range env {
		if strings.HasPrefix(e, "TERM=") {
			return env
		}
	}
	return append(env, "TERM=xterm-256color")
}

// --- attach wire framing (client → host) ------------------------------------
//
// The host → client direction is raw pty output (the client streams it to
// stdout). The client → host direction multiplexes two frame kinds over the one
// hijacked connection: input data and resize. Each frame is a 1-byte kind then a
// kind-specific payload.

const (
	ptyFrameData   byte = 'd' // input: kind, uint32 length (BE), then length bytes
	ptyFrameResize byte = 'r' // resize: kind, uint16 cols (BE), uint16 rows (BE)
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

// ReadPtyFrame reads one client→host frame. On kind ptyFrameData it returns the
// input bytes in data; on ptyFrameResize it returns the size in ws.
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
	default:
		return 0, nil, ws, fmt.Errorf("unknown pty frame kind %q", k[0])
	}
}
