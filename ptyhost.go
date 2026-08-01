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

	// ptyReadChunk bounds one master read. One buffer of this size serves the
	// whole session: it is handed read goroutine → actor → successor goroutine,
	// never shared and never reallocated (R3333).
	ptyReadChunk = 32 * 1024

	// ptyClientPendingMax bounds one connection's undelivered output in bytes.
	// This is where R3118's "a client that cannot keep up is dropped" is enforced
	// now — on the append path, in the host, one number instead of a bounded
	// channel per transport (R3340). Bytes rather than a count of undelivered
	// chunks: a count bounds memory only when chunks have a bounded size, and a
	// pty read ranges from a few bytes to ptyReadChunk, so the old 256-chunk bound
	// described a window anywhere between kilobytes and megabytes depending on
	// what the child happened to be emitting.
	ptyClientPendingMax = 1 << 20

	// ptyClientBufInit sizes a connection's two buffers at attach so the ordinary
	// case never reallocates: one full read fits without a grow.
	ptyClientBufInit = ptyReadChunk

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

	// The teardown reasons (R3324). A teardown ark performed and one the child
	// chose are different events, and these are what keep the monitoring log from
	// reporting one as the other — frequently the only trace, since a child that
	// dies abruptly writes no exit record of its own (that verb is the session's).
	//
	// ptyReasonStopped is an `ark luhmann stop` (R3125).
	ptyReasonStopped = "luhmann-stopped"
	// ptyReasonSelfExit is the child exiting on its own — the human quit it, or
	// the process died (R3323).
	ptyReasonSelfExit = "luhmann-exited"
	// ptyReasonLaunchFailed is a launch abandoned at the confirmation gate
	// (R3126). It reaches no record today, since a session that never claimed the
	// seat has no id and can have stood up no secretaries; it is named so the
	// third teardown path states its cause rather than borrowing another's.
	ptyReasonLaunchFailed = "luhmann-launch-failed"

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
	// Write writes p to this client's transport **in full**, blocking until it is
	// written, and returns the error if it is not (R3341). A transport buffers
	// nothing of its own: the bounded buffering and the R3118 drop live in the
	// host, written once rather than once per transport. Blocking is correct
	// here because this runs on the host's per-connection write goroutine, off
	// the actor — a stalled client blocks only its own writer, its pending buffer
	// grows, and it is dropped at ptyClientPendingMax while every other
	// connection and the child carry on untouched.
	Write(p []byte) error
	// Close tears down the client's transport (on drop, detach, or host stop).
	// It is also what unblocks a Write stuck on a stalled transport.
	Close()
}

// ptyClientReg is the host's per-client record: the client, its last reported
// size, and the double buffer driving its output. Every field is owned by the
// actor and touched only inside jobs closures — except `client` itself, which is
// immutable after construction and so is safe for the write goroutine to read.
//
// pending is what the actor appends into; spare is the idle buffer, nil exactly
// while a write goroutine holds it; writing says a writer is in flight. Because
// all three live in the actor, `writing` needs no atomic: its only false→true
// transition happens inside a jobs closure (R3336, R3338).
type ptyClientReg struct {
	client  PtyClient
	size    PtyWinsize
	pending []byte
	spare   []byte
	writing bool
}

// PtyEnv is the narrow slice of the server the host depends on: the Luhmann seat
// lease (launch waits on a claim, the teardown releases it), the pool roster
// (status count + the teardown's exit recording), and the two directories the
// confirmation protocol reads. Kept small so the host stays decoupled and
// testable with a fake env. R3124, R3125, R3126
type PtyEnv interface {
	SeatOwner() string          // current Luhmann seat owner ("" = unowned)
	ForceReleaseSeat()          // clear any seat claim before a launch (R3126)
	ReleaseSeat(session string) // release the lease this session holds (R3125)
	// RecordHostedExits records + deregisters the hosted pool secretaries on
	// either path down, with reason naming which one it was (R3125, R3323, R3324).
	RecordHostedExits(session, reason string)
	// PublishLaunchOutcome announces a launch on the pub/sub integration plane
	// (R3326). Exactly one argument is set: session on a confirmed launch,
	// failure on an abandoned one.
	PublishLaunchOutcome(session, failure string)
	LuhmannDir() string   // ~/.ark/luhmann — the hosted session's cwd
	PoolRosterCount() int // pool-secretary roster size (R3124)
	// LuhmannChildArgv resolves the child's command line from the launch's
	// optional model parameter plus config (R3329, R3330), erroring when the
	// two cannot both be honored (R3331). The host asks rather than composes,
	// so it never depends on ark.toml and a fake env still drives it.
	LuhmannChildArgv(model string) ([]string, error)
}

// PtyHost owns one hosted Claude Code pty session (R3116). A Closure Actor: the
// master, child, and client set are touched only inside jobs closures, so no
// lock is held across pty I/O. The streaming I/O stays off the actor: the read
// chain blocks on the master one goroutine at a time (R3333), a per-connection
// write goroutine blocks on its transport (R3336), and inputLoop serializes
// client input onto the master (R3119). masterPtr mirrors the current master for
// the lock-free input path.
type PtyHost struct {
	env  PtyEnv
	jobs *ChanSvc    // actor: client set + lifecycle state
	inCh chan []byte // serialized input funnel → inputLoop → master (R3119)

	masterPtr atomic.Pointer[os.File] // current master, for inputLoop's lock-free read

	// There is no client-count mirror. R3328 kept one so the reader could skip a
	// *synchronous* actor round-trip when nobody was attached; the reader posts
	// now and never waits (R3335), so there is no round-trip left to skip and the
	// actor is once again the only place the client set is known.

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

	// No trust-dialog state lives here: it is per-launch and owned by that
	// launch's read chain (trustAccepter, R3327).
}

// trustAccepter watches one launch's early child output for the "trust this
// folder" dialog and answers it (R3128). **Per-launch, and owned by exactly one
// read chain**, which is why `buf` needs no guard (R3327).
//
// It used to be three fields on PtyHost, justified by a comment calling the
// reader their sole owner. That was not true: `finalize` clears `h.master`
// inside the actor but hands the master to `gracefulShutdown`, which holds it
// open for up to `ptyStopGrace`, so the old read chain keeps draining. `Reserve`
// checks only `master`/`launching`, both already clear, so a launch started in
// that window ran a second read chain alongside the first — two goroutines
// appending to one buffer, and the old one able to win the CAS and report a
// dialog acceptance to the *new* launch from the dying session's output. Giving
// each launch its own accepter removes the sharing rather than guarding it, so
// the race cannot be reintroduced by a future caller who does not know the rule.
type trustAccepter struct {
	// armed is the one genuinely cross-goroutine field: the launch clears it on
	// timeout while its read chain may be reading it. Atomic, and per-launch, so
	// two launches can never contend.
	armed atomic.Bool
	buf   []byte        // read-chain-only: early output accumulated while hunting
	done  chan struct{} // closed once the dialog is answered
	input func([]byte)  // where the accept keystrokes go (the host's input funnel)
}

// newTrustAccepter arms an accepter for one launch (R3128, R3327).
func newTrustAccepter(input func([]byte)) *trustAccepter {
	ta := &trustAccepter{done: make(chan struct{}), input: input}
	ta.armed.Store(true)
	return ta
}

// feed accumulates early child output and, on detecting the dialog, selects the
// "Yes, I trust" option by the number read from the stream and disarms — so the
// bootstrap that follows lands in the message box, not the menu (R3128). Runs on
// the read-chain goroutine, which owns this accepter outright (R3327).
// CRC: crc-PtyHost.md | Seq: seq-pty-launch.md#1.3.1 | R3128, R3327
func (ta *trustAccepter) feed(chunk []byte) {
	ta.buf = append(ta.buf, chunk...)
	if len(ta.buf) > ptyTrustScanCap {
		ta.buf = ta.buf[len(ta.buf)-ptyTrustScanCap:]
	}
	keys, ok := scanTrustAccept(ta.buf)
	if !ok {
		return
	}
	if !ta.armed.CompareAndSwap(true, false) {
		return // the launch already disarmed on timeout
	}
	ta.buf = nil
	ta.input(keys)
	close(ta.done)
}

// NewPtyHost constructs the host and starts its actor + input goroutines. The
// host idles with no session until Launch (R3114: never proactive — construction
// starts no `claude`).
func NewPtyHost(env PtyEnv) *PtyHost {
	h := &PtyHost{
		env:     env,
		jobs:    newChanSvc(chanSvcSlots),
		inCh:    make(chan []byte, 64),
		clients: make(map[*ptyClientReg]struct{}),
	}
	h.jobs.start()
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
//
// The synchronous entry point, and a thin pair over Reserve + LaunchReserved —
// the split exists for the asynchronous caller (R3325), not for this one.
// CRC: crc-PtyHost.md | Seq: seq-pty-launch.md#1.1 | R3114, R3122, R3126, R3128
func (h *PtyHost) Launch(bootstrap, model string) (string, error) {
	if err := h.Reserve(); err != nil {
		return "", err
	}
	return h.LaunchReserved(bootstrap, model)
}

// Reserve claims the host for one launch, rejecting a second while a session is
// hosted or a launch is mid-flight (R3122 — one at a time).
//
// It is split out of Launch so an *asynchronous* caller can still report the
// rejection to its user synchronously (R3325): the launch's publication speaks
// only for launches that actually started, so "already hosted" has to come back
// as a return value or it is lost inside a goroutine with nobody to surface it.
// CRC: crc-PtyHost.md | R3122, R3325
func (h *PtyHost) Reserve() error {
	return svcSyncVoid(h.jobs, func() error {
		if h.master != nil || h.launching {
			return errors.New("a Luhmann session is already hosted")
		}
		h.launching = true
		return nil
	})
}

// ReleaseReservation clears a reservation whose launch is over or will never
// run. It is the single place `launching` is cleared on a non-success path, with
// two callers: LaunchReserved's error defer, and the asynchronous caller's panic
// recovery (R3325) — the one exit LaunchReserved's own defer cannot cover, since
// the panic unwinds past it. Either way, a reservation left set would refuse
// every later launch as "already hosted".
//
// **Synchronous deliberately, though nothing observes the result.** Both callers
// are already-failing paths, and one round-trip on a path taken once in a blue
// moon buys a guarantee worth having: when this returns, the reservation is
// provably clear rather than clear soon. A fire-and-forget release that arrived
// late would recreate exactly the bug this function exists to prevent, and would
// do it invisibly. Note also that the project's fire-and-forget helper (`svc`)
// sends from a fresh goroutine and so does not preserve order between posts —
// an asynchronous version here would have to use a raw send, not that helper.
// CRC: crc-PtyHost.md | R3122, R3325
func (h *PtyHost) ReleaseReservation() {
	_ = svcSyncVoid(h.jobs, func() error { h.launching = false; return nil })
}

// LaunchReserved runs the launch protocol for a caller that already holds the
// reservation (Reserve). Blocks for the length of the confirmation, so an
// asynchronous caller runs it on its own goroutine. Releases the reservation on
// every exit path, and announces the outcome (R3326).
// CRC: crc-PtyHost.md | Seq: seq-pty-launch.md#1.1 | R3114, R3122, R3126, R3128, R3326
func (h *PtyHost) LaunchReserved(bootstrap, model string) (sessionID string, err error) {
	if strings.TrimSpace(bootstrap) == "" {
		bootstrap = ptyDefaultBootstrap
	}
	// Announce the outcome on the way out, whichever way that is (R3326). This
	// defer is installed *after* the reservation deliberately: a launch rejected
	// because one is already hosted never started, so it must not publish — a
	// failure tag written while a session is up would be a lie, and would wake
	// every UI for an event that did not happen.
	defer func() {
		if err != nil {
			h.ReleaseReservation()
			h.env.PublishLaunchOutcome("", err.Error())
			return
		}
		h.env.PublishLaunchOutcome(sessionID, "")
	}()

	// Clear any prior seat claim so the new session's --first is the one we
	// observe (R3126). A stale owner — a prior session that died without
	// releasing the in-memory lease — would otherwise block the new claim; a
	// live rival is resolved by the lease protocol's --first race. The managed
	// launch is the authoritative start, so it takes the seat.
	h.env.ForceReleaseSeat()

	// The child's argv is configuration, not a literal (R3329, R3330): the
	// launch's optional model, else [luhmann] model, else the compiled-in
	// default — or a wholesale [luhmann] command override. Resolved *before*
	// the fork so a request that cannot be honored (R3331) fails with no child
	// started and nothing to unwind.
	argv, aerr := h.env.LuhmannChildArgv(model)
	if aerr != nil {
		return "", aerr
	}
	if len(argv) == 0 {
		return "", fmt.Errorf("launch: empty child command")
	}
	// Fork the pty and start the child as a child of the server (R3116).
	cmd := exec.Command(argv[0], argv[1:]...)
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
	// trust accepter before the reader starts, so the read chain clears the
	// dialog first. Armed only for new projects, so it cannot misfire on a
	// normal session's later output.
	var trust *trustAccepter
	if isNewClaudeProject(cmd.Dir) {
		trust = newTrustAccepter(h.Input)
	}
	// One buffer, allocated once for this launch, then handed around for the life
	// of the session (R3333).
	h.startRead(master, make([]byte, ptyReadChunk), trust)

	// For a new project, wait for the trust dialog to be accepted before the
	// bootstrap so it lands in the message box, not the menu (R3128). Best
	// effort: if the dialog never appears (the heuristic was wrong), proceed
	// after the timeout rather than block the launch.
	if trust != nil {
		select {
		case <-trust.done:
		case <-time.After(ptyTrustTimeout):
			trust.armed.Store(false) // close the scan window; no dialog came
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
		h.finalize(master, ptyReasonLaunchFailed)
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

// --- fan-out: the output cycle, input merge, resize (R3117–R3121) -----------
//
// The output path below is two named patterns composed, and the names are worth
// carrying because they say in two words what the mechanism takes a page to
// spell out. See design/design.md, "Actor-Resident I/O — Baton and Sewing".
/*
@pattern: baton — the pty read buffer has exactly one holder at any instant, handed read goroutine → actor → successor goroutine, each holder terminating as it passes it on, so ownership needs no lock and no comment asserting it (R3333)
@pattern: sewing — the write cycle passes serialized control actor → write goroutine → actor as posted continuations rather than calls, holding nothing at any instant: post, don't call (R3336)
@pattern: actor-queue-ordering — the successor read is spawned from inside the closure that finished with the buffer, so "the buffer is free again" is expressed by queue position instead of by a flag (R3333)
@pattern: closure-actor — the substrate both of those sit on (ChanSvc, svc.go)
*/

// startRead spawns the goroutine that owns buf and blocks on the master. On
// return it hands buf to the actor in a posted closure and terminates; the actor
// distributes the bytes and only then spawns the successor with the same buffer
// (onRead). So the buffer has exactly one owner at every instant, by
// construction rather than by a comment that has to stay true, and it is one
// allocation for the life of the session (R3333).
//
// The read must not run on the actor: it blocks indefinitely while the child is
// quiet, and an idle orchestrator goes minutes without a byte, so a read inside
// a jobs closure would stall every attach, detach, resize, status and stop —
// worst exactly when the system looks idlest (R3334). The goroutine waits; the
// actor changes state.
//
// The hand-off is a **post, not a call** (R3335). The reader never waits for the
// distribution to finish, which is what R3328's client-count mirror existed to
// avoid paying; a blocking send on a full jobs queue is the only back-pressure,
// and it cannot deadlock because the actor never waits on a reader.
// CRC: crc-PtyHost.md | Seq: seq-pty-attach.md#1.3.1 | R3333, R3334, R3335
func (h *PtyHost) startRead(m *os.File, buf []byte, trust *trustAccepter) {
	go func() {
		n, rerr := m.Read(buf)
		if n > 0 && trust != nil && trust.armed.Load() {
			// R3128/R3327: the accepter belongs to this launch's read chain, and
			// exactly one goroutine of that chain exists at a time, so its scan
			// buffer needs no guard. Fed here rather than in the actor because
			// accepting types into the input funnel, which can block.
			trust.feed(buf[:n])
		}
		svc(h.jobs, func() { h.onRead(m, buf, n, rerr, trust) })
	}()
}

// onRead is the actor half of the read cycle: distribute, then hand the buffer
// back to a fresh reader (R3333). Output is never parsed for session state
// (R3115); it is opaque bytes to fan out.
//
// A chain whose master is no longer the hosted one keeps reading and distributes
// nothing (R3343). Both halves matter: the output must still be drained, because
// an undrained pty blocks the child (R3121) and finalize deliberately leaves the
// old master open through the stop grace so claude's flush-on-exit output is
// still read; and it must not reach the clients of a launch that started in that
// window, which is the same overlap R3327 addresses for the trust accepter.
//
// The EOF finalize is the *only* teardown a child that dies on its own ever
// gets, so it discharges that path's obligations (R3323) with the self-exit
// reason, since nobody asked for this teardown (R3324). It calls finalizeLocked
// rather than finalize: this closure is already running on the actor, and the
// wrapper would post to the actor it is running on and self-deadlock.
// CRC: crc-PtyHost.md | Seq: seq-pty-attach.md#1.3.3 | R3115, R3323, R3333, R3343
func (h *PtyHost) onRead(m *os.File, buf []byte, n int, rerr error, trust *trustAccepter) {
	live := h.master == m
	if n > 0 && live {
		h.distributeLocked(buf[:n])
	}
	if rerr != nil {
		if live {
			if session := h.finalizeLocked(m); session != "" {
				// Off the actor: these are DB appends (R3323).
				go h.env.RecordHostedExits(session, ptyReasonSelfExit)
			}
		}
		return // the chain ends with the master
	}
	h.startRead(m, buf, trust)
}

// distributeLocked appends one output chunk to every attached connection and
// drops those that have fallen too far behind (R3118, R3340). Must run inside a
// jobs closure. Deleting the entry the range is currently on is defined
// behavior in Go, so a drop takes effect in place rather than being collected
// for a second pass; only the resize is deferred, so a range that drops several
// connections recomputes once.
// CRC: crc-PtyHost.md | Seq: seq-pty-attach.md#1.3.2 | R3118, R3340
func (h *PtyHost) distributeLocked(chunk []byte) {
	var dropped bool
	for reg := range h.clients {
		if !h.appendOutputLocked(reg, chunk) {
			h.dropLocked(reg)
			dropped = true
		}
	}
	if dropped {
		h.recomputeSizeLocked() // R3120: a drop is a detach — recompute the min
	}
}

// appendOutputLocked copies one chunk into a connection's pending buffer and
// kicks its writer, reporting false when the connection has exceeded the cap and
// must be dropped (R3340) — the one number that replaced a bounded channel per
// transport. Steady state allocates nothing: the two buffers reach their
// high-water mark and are reused for the life of the connection.
// CRC: crc-PtyHost.md | Seq: seq-pty-attach.md#1.3.2 | R3336, R3340
func (h *PtyHost) appendOutputLocked(reg *ptyClientReg, chunk []byte) bool {
	if len(reg.pending)+len(chunk) > ptyClientPendingMax {
		return false // R3118: cannot keep up — drop rather than stall the fan-out
	}
	reg.pending = append(reg.pending, chunk...)
	h.kickWriterLocked(reg)
	return true
}

// kickWriterLocked starts a write for a connection that has output and no writer
// in flight: swap the buffers, mark it writing, and spawn the goroutine that
// owns the filled one (R3336). A connection already writing is left alone — the
// in-flight writer picks up whatever accumulates, which is what makes at most
// one writer per connection possible (R3338).
// CRC: crc-PtyHost.md | Seq: seq-pty-attach.md#1.3.4 | R3336, R3338
func (h *PtyHost) kickWriterLocked(reg *ptyClientReg) {
	if reg.writing || len(reg.pending) == 0 {
		return
	}
	buf := reg.pending
	reg.pending, reg.spare = reg.spare[:0], nil
	reg.writing = true
	go h.writeOut(reg, buf)
}

// writeOut writes one buffer to a connection's transport, blocking and off the
// actor, then empties it and posts the continuation back **carrying the buffer**
// (R3336, R3341). Blocking here is the design rather than a tolerance: a stalled
// client blocks only its own writer, its pending grows, and it is dropped at the
// cap, while every other connection and the child are untouched. reg.client is
// immutable after construction, so reading it off the actor is safe.
// CRC: crc-PtyHost.md | Seq: seq-pty-attach.md#1.3.4 | R3336, R3341
func (h *PtyHost) writeOut(reg *ptyClientReg, buf []byte) {
	werr := reg.client.Write(buf)
	buf = buf[:0]
	svc(h.jobs, func() { h.onWritten(reg, buf, werr) })
}

// onWritten is the continuation: take the emptied buffer back, clear the flag,
// and spawn again if output accumulated while the write was in flight (R3336).
// No wakeup can be lost whichever order this and an append run in, because the
// actor serializes them — an append during the write sees `writing` and does
// nothing, and this then finds pending non-empty and respawns; if this runs
// first it clears the flag and the next append spawns (R3337).
//
// A continuation whose connection is already gone spawns nothing and returns,
// or every teardown racing an in-flight write would leak a goroutine (R3342).
// A failed write deregisters instead of continuing (R3118, R3341).
// CRC: crc-PtyHost.md | Seq: seq-pty-attach.md#1.3.5 | R3336, R3337, R3342
func (h *PtyHost) onWritten(reg *ptyClientReg, buf []byte, werr error) {
	if _, ok := h.clients[reg]; !ok {
		return // R3342: dropped, detached, or torn down — spawn nothing
	}
	reg.writing = false
	reg.spare = buf
	if werr != nil {
		h.dropLocked(reg)
		h.recomputeSizeLocked() // R3120: a drop is a detach
		return
	}
	h.kickWriterLocked(reg)
}

// dropLocked deregisters one connection and closes its transport. Closing is
// also what unblocks a Write stuck on a stalled client (R3118). Any continuation
// still in flight for it finds it gone and stops (R3342). Must run inside a jobs
// closure.
// CRC: crc-PtyHost.md | R3118, R3342
func (h *PtyHost) dropLocked(reg *ptyClientReg) {
	delete(h.clients, reg)
	reg.client.Close()
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
		// Both buffers up front, so `spare` is non-nil whenever no writer holds
		// it and the ordinary case never reallocates (R3336).
		reg = &ptyClientReg{
			client:  c,
			size:    size,
			pending: make([]byte, 0, ptyClientBufInit),
			spare:   make([]byte, 0, ptyClientBufInit),
		}
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
			// Any write still in flight for it finds it gone and stops (R3342).
			h.dropLocked(reg)
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

// Stop is a graceful teardown, not a bare kill (R3125): finalize SIGTERMs the
// child (SIGKILL only if it hangs), letting claude flush its transcript, then
// releases the seat lease and records the hosted pool secretaries' exits, so the
// monitoring log shows no ghosts. Errors when no session is hosted.
//
// What Stop itself contributes is the *reason* — this teardown was ark's, not
// the child's choice (R3324). The obligations belong to finalize, the one
// chokepoint both paths down pass through, because a child that exits on its own
// never reaches this function at all (R3323).
// CRC: crc-PtyHost.md | R3125, R3324
func (h *PtyHost) Stop() error {
	var m *os.File
	_ = svcSyncVoid(h.jobs, func() error {
		m = h.master
		return nil
	})
	if m == nil {
		return errors.New("no Luhmann session is hosted")
	}
	// Idempotent with the reader's finalize on the resulting EOF. Synchronous, so
	// status reflects the teardown the moment Stop returns.
	h.finalize(m, ptyReasonStopped)
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
// so status stays truthful, records the hosted pool secretaries' exits, and hands
// the child + master to an off-actor graceful shutdown. Called on child self-exit
// (the read chain's EOF) and by Stop; the second caller is a no-op.
//
// It is the single teardown chokepoint, which is why BOTH obligations live here
// rather than at a caller (R3323): recording exits in Stop alone meant a child
// that died on its own left its secretaries in the roster as ghosts, holding
// slots against pool_max and drawing routed work to tubes nobody listens on. The
// idempotence is what lets both callers ask unconditionally — a stop racing the
// child's own exit records once, not twice.
//
// The actor clears state immediately; the SIGTERM grace wait, the master close,
// and the exit records all happen off the actor so it never stalls (the records
// are DB appends). Recording *after* the seat release is deliberate: a requeue
// driven by deregistration then finds no seat owner, so it cannot stand a fresh
// secretary up into the session being torn down.
// CRC: crc-PtyHost.md | R3114, R3116, R3125, R3323, R3324
func (h *PtyHost) finalize(m *os.File, reason string) {
	// Set only by the call that actually finalizes, so it doubles as the
	// exactly-once guard for the off-actor recording below.
	var session string
	_ = svcSyncVoid(h.jobs, func() error {
		session = h.finalizeLocked(m)
		return nil
	})
	if session != "" {
		h.env.RecordHostedExits(session, reason)
	}
}

// finalizeLocked is finalize's in-actor body, returning the session it
// finalized — empty when another caller got there first, which is what makes the
// pair idempotent on the master identity. It exists separately because the
// second caller is now already on the actor: onRead's EOF branch would
// self-deadlock calling the wrapper, since that posts to the actor it is running
// on. The file already carries this convention (recomputeSizeLocked,
// minSizeLocked). The caller owns the recording, which must stay off the actor
// (DB appends) and after the seat release (R3323).
// CRC: crc-PtyHost.md | R3114, R3116, R3125, R3323
func (h *PtyHost) finalizeLocked(m *os.File) string {
	if h.master != m {
		return "" // already finalized (self-exit + Stop race)
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
	// Off the actor: leave the master open through the grace window so claude's
	// flush-on-exit output is still drained by the read chain — which by then is
	// reading a master that is no longer the hosted one, so it drains and
	// discards (R3343) — then close.
	go gracefulShutdown(child, m)
	return session
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
	return filepath.Join(home, ".claude", "projects", claudeProjectSlug(cwd))
}

// claudeProjectSlug encodes a cwd the way Claude Code names its project log
// directory: `/` and `.` become `-`, so /home/deck/work/ark → -home-deck-work-ark
// and /home/deck/.ark → -home-deck--ark.
//
// **This is the single place the vendor scheme appears.** It is reached from two
// callers with different standing: the launch path above, which must match what
// Claude Code actually does, and Config.ProposeLogsFor, where it is a one-shot
// *populator* rather than a rule (R3289) — no lookup path may call it, or one
// vendor's naming scheme would be hardcoded where every caller inherits it.
// Deleting the populator use costs `ark sources pair --auto` and nothing else,
// because the pairing itself lives in the declared logs_for field.
// CRC: crc-Config.md | R3289, R3290
func claudeProjectSlug(cwd string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
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
