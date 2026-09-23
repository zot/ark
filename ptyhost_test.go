package ark

// Unit tests for the PtyHost fan-out + lifecycle logic (test-PtyHost.md). The
// deterministic parts — smallest-wins resize, the output cycle (order, one
// writer, coalescing, the pending-cap drop, gone-connection continuations),
// serialized input, launch reject-second, zero-client survival — run against a
// fake client, a fake env, and a fake master (an os.Pipe), no real pty or
// server. The launch confirmation protocol (R3126) touches a real JSONL + the
// seat lease and is left to integration (see design.md Gaps).
//
// CRC: crc-PtyHost.md | Test: test-PtyHost.md | R3118, R3119, R3120, R3121, R3122, R3136, R3139
// CRC: crc-PtyHost.md | Test: test-PtyHost.md | R3333, R3334, R3336, R3337, R3338, R3339, R3340, R3341, R3342, R3343

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakePtyEnv is an inert PtyEnv for the fan-out tests (no seat, no roster).
type fakePtyEnv struct{}

func (fakePtyEnv) SeatOwner() string                   { return "" }
func (fakePtyEnv) ForceReleaseSeat()                   {}
func (fakePtyEnv) ReleaseSeat(string)                  {}
func (fakePtyEnv) RecordHostedExits(string, string)    {}
func (fakePtyEnv) PublishLaunchOutcome(string, string) {}
func (fakePtyEnv) LuhmannDir() string                  { return "" }
func (fakePtyEnv) PoolRosterCount() int                { return 0 }

// LuhmannChildArgv returns a command that cannot exist, so a test that reaches
// the fork fails at exec rather than starting a real `claude` (R3329).
func (fakePtyEnv) LuhmannChildArgv(string) ([]string, error) {
	return []string{"/nonexistent/ark-test-child"}, nil
}

// fakePtyClient is a transport double for the host's fan-out (R3341). Write is
// blocking and complete, as the interface now requires: it records the bytes,
// returns failWith when set, and waits on gate when set — which is how a test
// stalls one connection without stalling the others. It also tracks how many
// Writes are in flight at once, which is what makes the one-writer-per-
// connection property (R3338) observable rather than merely argued.
type fakePtyClient struct {
	mu        sync.Mutex
	got       []byte
	closed    bool
	writes    int
	inFlight  int
	maxFlight int
	released  bool
	failWith  error
	gate      chan struct{}
}

func (c *fakePtyClient) Write(p []byte) error {
	c.mu.Lock()
	c.inFlight++
	if c.inFlight > c.maxFlight {
		c.maxFlight = c.inFlight
	}
	gate, fail := c.gate, c.failWith
	c.mu.Unlock()

	if gate != nil {
		<-gate // stalled until release() or Close()
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.inFlight--
	c.writes++
	if fail != nil {
		return fail
	}
	c.got = append(c.got, p...)
	return nil
}

// release unblocks a stalled Write. Idempotent, so Close may also call it.
func (c *fakePtyClient) release() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gate != nil && !c.released {
		c.released = true
		close(c.gate)
	}
}

// Close mirrors a real transport: closing is what unblocks a Write stuck on it
// (R3118), which is the mechanism that makes a drop take effect.
func (c *fakePtyClient) Close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.release()
}

func (c *fakePtyClient) bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.got...)
}

func (c *fakePtyClient) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *fakePtyClient) stats() (writes, maxFlight int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writes, c.maxFlight
}

// pushOutput appends one chunk to every attached connection the way onRead does,
// so the write cycle can be driven without a real pty behind it.
func pushOutput(h *PtyHost, chunk []byte) {
	_ = svcSyncVoid(h.jobs, func() error {
		h.distributeLocked(chunk)
		return nil
	})
}

// waitFor polls cond until it holds. The write cycle is asynchronous by design
// (R3336), so assertions about delivered output cannot be synchronous.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// attachFake attaches c at a default size, failing the test if the host refuses.
func attachFake(t *testing.T, h *PtyHost, c *fakePtyClient) *ptyClientReg {
	t.Helper()
	reg, err := h.Attach(c, PtyWinsize{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	return reg
}

// testPipe returns a fresh pipe with both ends closed at the end of the test.
func testPipe(t *testing.T) (r, w *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	return r, w
}

// testMaster adapts an *os.File to a ptyMaster for tests. Resize is a no-op: the
// fan-out tests drive resize through resizeSink, never the real master.
type testMaster struct{ f *os.File }

func (m *testMaster) Read(p []byte) (int, error)     { return m.f.Read(p) }
func (m *testMaster) Write(p []byte) (int, error)    { return m.f.Write(p) }
func (m *testMaster) Close() error                   { return m.f.Close() }
func (m *testMaster) Resize(cols, rows uint16) error { return nil }

// fakeMaster returns a ptyMaster backed by a fresh pipe's write end, to stand in
// for the real pty master.
func fakeMaster(t *testing.T) ptyMaster {
	t.Helper()
	_, w := testPipe(t)
	return &testMaster{f: w}
}

// installMaster wires a fake master (and optional resize sink) through the actor.
func installMaster(h *PtyHost, m ptyMaster, sink func(PtyWinsize)) {
	_ = svcSyncVoid(h.jobs, func() error {
		h.master = m
		h.masterPtr.Store(&ptyMasterRef{m})
		h.resizeSink = sink
		return nil
	})
}

// hostWithMaster is the starting point for the fan-out tests: a host with a fake
// master installed and its resizes discarded. Tests that assert on the resizes
// call installMaster themselves with a recording sink.
func hostWithMaster(t *testing.T, env PtyEnv) *PtyHost {
	t.Helper()
	h := NewPtyHost(env)
	installMaster(h, fakeMaster(t), func(PtyWinsize) {})
	return h
}

func clientCount(h *PtyHost) int {
	var n int
	_ = svcSyncVoid(h.jobs, func() error { n = len(h.clients); return nil })
	return n
}

// Test: forceRepaint toggles the pty size a row and restores it, so the child
// gets a real SIGWINCH to redraw on — a same-size resize would be a kernel
// no-op (R3136).
func TestPtyHostForceRepaint(t *testing.T) {
	h := NewPtyHost(fakePtyEnv{})
	var mu sync.Mutex
	var sizes []PtyWinsize
	installMaster(h, fakeMaster(t), func(s PtyWinsize) {
		mu.Lock()
		sizes = append(sizes, s)
		mu.Unlock()
	})
	if _, err := h.Attach(&fakePtyClient{}, PtyWinsize{Cols: 80, Rows: 24}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	mu.Lock()
	sizes = nil // drop the attach's own resize; measure only the repaint
	mu.Unlock()

	h.ForceRepaint()
	time.Sleep(ptyRepaintNudge + 50*time.Millisecond) // the restore fires async

	mu.Lock()
	defer mu.Unlock()
	want := []PtyWinsize{{Cols: 80, Rows: 23}, {Cols: 80, Rows: 24}}
	if len(sizes) != len(want) || sizes[0] != want[0] || sizes[1] != want[1] {
		t.Errorf("forceRepaint sizes = %v, want %v (shrink a row, hold, then restore)", sizes, want)
	}
}

// Test: a repaint frame round-trips as kind-only (R3136).
func TestPtyRepaintFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := WritePtyRepaint(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	kind, data, _, err := ReadPtyFrame(&buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if kind != ptyFrameRepaint || data != nil {
		t.Errorf("got kind=%q data=%v, want ptyFrameRepaint + nil data", kind, data)
	}
}

// Test: the child env carries the managed-pty marker, still strips the session
// markers, and passes credentials through (R3139, R3127).
func TestFilterChildEnvManagedMarker(t *testing.T) {
	env := filterChildEnv([]string{"PATH=/usr/bin", "CLAUDECODE=1", "ANTHROPIC_API_KEY=sk"})
	var hasMarker, hasCreds, hasCode bool
	for _, e := range env {
		switch {
		case e == "ARK_MANAGED_PTY=1":
			hasMarker = true
		case e == "ANTHROPIC_API_KEY=sk":
			hasCreds = true
		case strings.HasPrefix(e, "CLAUDECODE="):
			hasCode = true
		}
	}
	if !hasMarker {
		t.Error("filterChildEnv must set ARK_MANAGED_PTY=1")
	}
	if !hasCreds {
		t.Error("filterChildEnv must pass credentials through")
	}
	if hasCode {
		t.Error("filterChildEnv must strip CLAUDECODE")
	}
}

// Test: smallest-wins resize, both directions (R3120).
func TestPtyHostSmallestWinsResize(t *testing.T) {
	h := NewPtyHost(fakePtyEnv{})
	var mu sync.Mutex
	var sizes []PtyWinsize
	installMaster(h, fakeMaster(t), func(s PtyWinsize) {
		mu.Lock()
		sizes = append(sizes, s)
		mu.Unlock()
	})

	regA, err := h.Attach(&fakePtyClient{}, PtyWinsize{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("attach A: %v", err)
	}
	if _, err = h.Attach(&fakePtyClient{}, PtyWinsize{Cols: 100, Rows: 40}); err != nil {
		t.Fatalf("attach B: %v", err)
	}
	h.Detach(regA)
	if _, err = h.Attach(&fakePtyClient{}, PtyWinsize{Cols: 60, Rows: 20}); err != nil {
		t.Fatalf("attach C: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []PtyWinsize{
		{Cols: 80, Rows: 24},  // A alone
		{Cols: 80, Rows: 24},  // min(A,B): B is larger, stays 80x24
		{Cols: 100, Rows: 40}, // A detaches → grows to B
		{Cols: 60, Rows: 20},  // C joins → shrinks to C
	}
	if len(sizes) != len(want) {
		t.Fatalf("resize fired %d times, want %d: %v", len(sizes), len(want), sizes)
	}
	for i := range want {
		if sizes[i] != want[i] {
			t.Errorf("resize[%d] = %+v, want %+v", i, sizes[i], want[i])
		}
	}
}

// Test: a stalled connection is dropped at the pending cap, and the fan-out
// carries on for everyone else (R3340, R3118). This is the successor to the
// bounded-channel drop test: the policy is unchanged, but the bound is now one
// byte cap in the host rather than a 256-deep channel per transport.
func TestPtyHostPendingCapDropsStalledClient(t *testing.T) {
	h := hostWithMaster(t, fakePtyEnv{})

	stuck := &fakePtyClient{gate: make(chan struct{})} // never completes a write
	normal := &fakePtyClient{}                         // drains freely
	attachFake(t, h, stuck)
	attachFake(t, h, normal)

	// Small chunks, many of them: the stalled connection accumulates past
	// ptyClientPendingMax while the draining one keeps its pending near zero.
	chunk := make([]byte, 4096)
	for i := range chunk {
		chunk[i] = 'z'
	}
	// A few chunks past the cap, not exactly one: the first append leaves in the
	// swapped-out buffer the stalled writer holds, so pending trails the total
	// sent by one chunk and an exact-cap total would stop just short of tripping
	// the strict comparison.
	for sent := 0; sent < ptyClientPendingMax+4*len(chunk); sent += len(chunk) {
		pushOutput(h, chunk)
	}

	waitFor(t, "the stalled client to be dropped", func() bool { return clientCount(h) == 1 })
	if !stuck.isClosed() {
		t.Error("stalled client was not closed on drop (R3118: closing is what unblocks its write)")
	}
	if normal.isClosed() {
		t.Error("the draining client was dropped too — one slow client stalled the fan-out (R3118)")
	}
	if got := len(normal.bytes()); got == 0 {
		t.Error("the draining client received nothing while the other stalled")
	}
}

// Test: output reaches a connection in order, and the source buffer may be
// rewritten behind it (R3333, R3339). The read buffer is a baton handed around
// rather than shared, so what a connection holds must be its own copy.
func TestPtyHostOutputOrderSurvivesBufferReuse(t *testing.T) {
	h := hostWithMaster(t, fakePtyEnv{})
	c := &fakePtyClient{}
	attachFake(t, h, c)

	// One buffer for every append, exactly as the read cycle reuses its baton.
	buf := make([]byte, 4)
	var want string
	for _, s := range []string{"aaaa", "bbbb", "cccc", "dddd"} {
		copy(buf, s)
		pushOutput(h, buf)
		want += s
	}
	copy(buf, "XXXX") // what the next read would do to it

	waitFor(t, "all output delivered", func() bool { return len(c.bytes()) == len(want) })
	if got := string(c.bytes()); got != want {
		t.Errorf("client received %q, want %q", got, want)
	}
}

// Test: at most one write goroutine per connection, and bytes appended during an
// in-flight write are picked up by its continuation rather than lost (R3337,
// R3338). The second write also shows the adaptive batching R3341 describes: it
// carries everything that accumulated, in one call.
func TestPtyHostOneWriterCoalescesPending(t *testing.T) {
	h := hostWithMaster(t, fakePtyEnv{})
	c := &fakePtyClient{gate: make(chan struct{})}
	attachFake(t, h, c)

	// The first append starts a writer that blocks; the rest accumulate behind
	// it. pushOutput is synchronous through the actor, so all 20 have landed
	// before the gate opens.
	for i := 0; i < 20; i++ {
		pushOutput(h, []byte("chunk"))
	}
	c.release()

	want := 20 * len("chunk")
	waitFor(t, "all output delivered", func() bool { return len(c.bytes()) == want })
	writes, maxFlight := c.stats()
	if maxFlight != 1 {
		t.Errorf("max concurrent writers = %d, want 1 (R3338)", maxFlight)
	}
	if writes != 2 {
		t.Errorf("writes = %d, want 2 — one in flight plus one carrying everything that accumulated (R3337, R3341)", writes)
	}
}

// Test: a failed transport write deregisters its connection (R3341, R3118).
func TestPtyHostWriteErrorDropsClient(t *testing.T) {
	h := hostWithMaster(t, fakePtyEnv{})
	c := &fakePtyClient{failWith: errors.New("transport gone")}
	attachFake(t, h, c)

	pushOutput(h, []byte("anything"))

	waitFor(t, "the failed client to be dropped", func() bool { return clientCount(h) == 0 })
	if !c.isClosed() {
		t.Error("a client whose write failed was not closed")
	}
}

// Test: a write continuation whose connection is already gone spawns nothing
// (R3342). Without this every teardown racing an in-flight write leaks a
// goroutine, and the host would keep writing into a detached transport.
func TestPtyHostContinuationForGoneConnection(t *testing.T) {
	h := hostWithMaster(t, fakePtyEnv{})
	c := &fakePtyClient{gate: make(chan struct{})}
	reg := attachFake(t, h, c)

	pushOutput(h, []byte("first"))  // starts the writer, which blocks
	pushOutput(h, []byte("second")) // accumulates behind it
	h.Detach(reg)                   // Close releases the stalled write

	waitFor(t, "the client set to empty", func() bool { return clientCount(h) == 0 })
	// The continuation now runs and must find the connection gone. Give it room
	// to do the wrong thing before asserting it did not.
	time.Sleep(50 * time.Millisecond)
	if writes, _ := c.stats(); writes != 1 {
		t.Errorf("writes = %d, want 1 — the continuation respawned into a detached connection (R3342)", writes)
	}
}

// Test: a read chain outliving its session drains without distributing (R3343).
// After a teardown the old master stays open through the stop grace, so its
// remaining output must not reach the clients of whatever launched next.
func TestPtyHostStaleReadChainDoesNotDistribute(t *testing.T) {
	h, w := hostOnPipe(t, fakePtyEnv{}, "")

	// Stand in for the teardown window: a later launch has installed its own
	// master while the old pipe is still open and still being read.
	installMaster(h, fakeMaster(t), func(PtyWinsize) {})
	c := &fakePtyClient{}
	attachFake(t, h, c)

	if _, err := w.Write([]byte("output from the dying session")); err != nil {
		t.Fatalf("write to the old master: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := c.bytes(); len(got) != 0 {
		t.Errorf("the new client received %q from a stale read chain (R3343)", got)
	}
	// The chain is still draining rather than wedged: a second write goes
	// through, and still reaches nobody.
	if _, err := w.Write([]byte("more")); err != nil {
		t.Fatalf("second write to the old master: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := c.bytes(); len(got) != 0 {
		t.Errorf("the new client received %q on the second write (R3343)", got)
	}
}

// Test: the read cycle carries one buffer around for many reads and delivers
// every chunk in order (R3333, R3334) — the goroutine blocks on the master, the
// actor distributes and spawns the successor.
func TestPtyHostReadCycleDeliversEveryChunk(t *testing.T) {
	h, w := hostOnPipe(t, fakePtyEnv{}, "")
	c := &fakePtyClient{}
	attachFake(t, h, c)

	var want string
	for i := 0; i < 8; i++ {
		s := fmt.Sprintf("chunk-%d;", i)
		if _, err := w.Write([]byte(s)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		want += s
		// One write per read keeps the assertion about order rather than framing.
		waitFor(t, "chunk "+s, func() bool { return len(c.bytes()) == len(want) })
	}
	if got := string(c.bytes()); got != want {
		t.Errorf("client received %q, want %q", got, want)
	}
}

// Test: input merge is serialized (R3119).
func TestPtyHostInputSerialized(t *testing.T) {
	h := NewPtyHost(fakePtyEnv{})
	r, w := testPipe(t)
	h.masterPtr.Store(&ptyMasterRef{&testMaster{f: w}}) // Input drains through masterPtr

	a := bytes.Repeat([]byte{'A'}, 1000)
	b := bytes.Repeat([]byte{'B'}, 1000)
	go h.Input(a)
	go h.Input(b)

	buf := make([]byte, 2000)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatalf("read: %v", err)
	}

	// Contiguous chunks → at most one A→B (or B→A) transition. An interleave
	// would produce many.
	transitions := 0
	for i := 1; i < len(buf); i++ {
		if buf[i] != buf[i-1] {
			transitions++
		}
	}
	if transitions > 1 {
		t.Fatalf("input interleaved: %d transitions (want ≤1)", transitions)
	}
	if bytes.Count(buf, []byte{'A'}) != 1000 || bytes.Count(buf, []byte{'B'}) != 1000 {
		t.Errorf("lost bytes: A=%d B=%d (want 1000 each)", bytes.Count(buf, []byte{'A'}), bytes.Count(buf, []byte{'B'}))
	}
}

// Test: launch rejects a second session (R3122).
func TestPtyHostLaunchRejectsSecond(t *testing.T) {
	h := hostWithMaster(t, fakePtyEnv{})

	_, err := h.Launch("", "")
	if err == nil {
		t.Fatal("expected launch to reject a second session, got nil")
	}
}

// Test: zero clients keeps the session running (R3121).
func TestPtyHostZeroClientsKeepsRunning(t *testing.T) {
	h := hostWithMaster(t, fakePtyEnv{})

	reg, err := h.Attach(&fakePtyClient{}, PtyWinsize{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	h.Detach(reg)

	if hosted, _ := h.Status(); !hosted {
		t.Fatal("session not hosted after last client detached")
	}
	if _, err := h.Attach(&fakePtyClient{}, PtyWinsize{Cols: 90, Rows: 30}); err != nil {
		t.Fatalf("re-attach after zero clients: %v", err)
	}
	if n := clientCount(h); n != 1 {
		t.Errorf("client set has %d after re-attach, want 1", n)
	}
}

// envHas reports whether env contains an entry with the given prefix.
func envHas(env []string, prefix string) bool {
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return true
		}
	}
	return false
}

// TestFilterChildEnvStripsMarkers: the hosted child launches as a fresh
// top-level session — the Claude Code session-identity markers are removed while
// credentials/config and a present TERM pass through untouched (R3127).
//
// CRC: crc-PtyHost.md | Test: test-PtyHost.md | R3127
func TestFilterChildEnvStripsMarkers(t *testing.T) {
	src := []string{
		"CLAUDECODE=1",
		"CLAUDE_CODE_SESSION_ID=abc",
		"CLAUDE_CODE_ENTRYPOINT=cli",
		"AI_AGENT=1",
		"ANTHROPIC_API_KEY=secret",
		"CLAUDE_EFFORT=high",
		"HOME=/home/deck",
		"TERM=xterm-256color",
	}
	got := filterChildEnv(src)

	for _, p := range []string{"CLAUDECODE=", "CLAUDE_CODE_SESSION_ID=", "CLAUDE_CODE_ENTRYPOINT=", "AI_AGENT="} {
		if envHas(got, p) {
			t.Errorf("marker %q must be stripped from the child env", p)
		}
	}
	for _, p := range []string{"ANTHROPIC_API_KEY=", "CLAUDE_EFFORT=", "HOME=", "TERM="} {
		if !envHas(got, p) {
			t.Errorf("%q must pass through to the child env", p)
		}
	}
}

// TestFilterChildEnvEnsuresTerm: TERM is appended when the server's env lacks it,
// so the child TUI still renders (R3127).
//
// CRC: crc-PtyHost.md | Test: test-PtyHost.md | R3127
func TestFilterChildEnvEnsuresTerm(t *testing.T) {
	got := filterChildEnv([]string{"HOME=/home/deck"})
	found := ""
	for _, e := range got {
		if strings.HasPrefix(e, "TERM=") {
			found = e
		}
	}
	if found != "TERM=xterm-256color" {
		t.Errorf("TERM ensured as xterm-256color, got %q", found)
	}
}

// trustDialog mirrors the real "trust this folder" render: cursor-move params
// carry digits (\x1b[14G lands right before "trust"), the number precedes the
// label in stream order, and the question itself contains "trust". A correct
// scan must strip the escapes, ignore the question, and read the option number.
const trustDialog = "\x1b[?25l\x1b[2K" +
	"\x1b[38;5;250mDo you trust the files in this folder?\x1b[39m\r\n" +
	"\x1b[38;5;153m❯\x1b[4G\x1b[38;5;246m1.\x1b[7G\x1b[38;5;153mYes,\x1b[12GI\x1b[14Gtrust\x1b[20Gthis\x1b[25Gfolder\x1b[39m\r\n" +
	"\x1b[4G\x1b[38;5;246m2.\x1b[7G\x1b[39mNo,\x1b[11Gexit\r\n" +
	"\x1b[2mEnter to confirm\x1b[22m"

// TestScanTrustAcceptReadsOptionNumber: the accept keystrokes are the option
// number read from the stream (not an assumed default), then Enter (R3128).
//
// CRC: crc-PtyHost.md | Test: test-PtyHost.md | R3128
func TestScanTrustAcceptReadsOptionNumber(t *testing.T) {
	keys, ok := scanTrustAccept([]byte(trustDialog))
	if !ok {
		t.Fatal("trust dialog not detected")
	}
	if string(keys) != "1\r" {
		t.Errorf("accept keys = %q, want \"1\\r\"", keys)
	}
}

// TestScanTrustAcceptReorderedMenu: when "Yes, I trust" is option 2, the scan
// yields "2" — the number is read, not assumed (R3128).
//
// CRC: crc-PtyHost.md | Test: test-PtyHost.md | R3128
func TestScanTrustAcceptReorderedMenu(t *testing.T) {
	reordered := "\x1b[38;5;250mDo you trust the files in this folder?\x1b[39m\r\n" +
		"\x1b[38;5;246m1.\x1b[7G\x1b[39mNo,\x1b[11Gexit\r\n" +
		"\x1b[38;5;153m❯\x1b[4G\x1b[38;5;246m2.\x1b[7G\x1b[38;5;153mYes,\x1b[12GI\x1b[14Gtrust\x1b[20Gthis\x1b[25Gfolder"
	keys, ok := scanTrustAccept([]byte(reordered))
	if !ok || string(keys) != "2\r" {
		t.Errorf("reordered menu: keys=%q ok=%v, want \"2\\r\", true", keys, ok)
	}
}

// TestScanTrustAcceptNoDialog: ordinary output (no menu) is not mistaken for the
// trust dialog, even when it contains the word "trust" (R3128).
//
// CRC: crc-PtyHost.md | Test: test-PtyHost.md | R3128
func TestScanTrustAcceptNoDialog(t *testing.T) {
	for _, s := range []string{
		"\x1b[38;5;246mOpus 4.8 (1M context)\x1b[39m manual mode on",
		"I don't think we should trust this input blindly.",
		"",
	} {
		if keys, ok := scanTrustAccept([]byte(s)); ok {
			t.Errorf("false positive on %q -> keys=%q", s, keys)
		}
	}
}

// TestClaudeProjectDirEncoding: cwd encodes to ~/.claude/projects/<'/' and '.' →
// '-'>, matching Claude Code's per-project log dir used by the new-project check
// (R3128).
//
// CRC: crc-PtyHost.md | Test: test-PtyHost.md | R3128
func TestClaudeProjectDirEncoding(t *testing.T) {
	got := claudeProjectDir("/home/deck/.ark/luhmann")
	const want = "/.claude/projects/-home-deck--ark-luhmann"
	if !strings.HasSuffix(got, want) {
		t.Errorf("claudeProjectDir = %q, want suffix %q", got, want)
	}
}

// TestMaybeAcceptTrustFires: on the dialog, the accepter disarms, closes
// trustDone, and sends the accept keystrokes through the input funnel (R3128).
//
// CRC: crc-PtyHost.md | Test: test-PtyHost.md | R3128
func TestMaybeAcceptTrustFires(t *testing.T) {
	sent := make(chan []byte, 4)
	ta := newTrustAccepter(func(p []byte) { sent <- p })

	ta.feed([]byte(trustDialog))

	if ta.armed.Load() {
		t.Error("accepter should disarm after handling the dialog")
	}
	select {
	case <-ta.done:
	default:
		t.Error("done should be closed after acceptance")
	}
	select {
	case got := <-sent:
		if string(got) != "1\r" {
			t.Errorf("sent %q, want \"1\\r\"", got)
		}
	default:
		t.Error("accept keystrokes were not sent")
	}
}

// TestMaybeAcceptTrustAccumulates: a dialog split across two reads is still
// detected — the accepter accumulates output across chunks (R3128).
//
// CRC: crc-PtyHost.md | Test: test-PtyHost.md | R3128
func TestMaybeAcceptTrustAccumulates(t *testing.T) {
	sent := make(chan []byte, 4)
	ta := newTrustAccepter(func(p []byte) { sent <- p })

	half := len(trustDialog) / 2
	ta.feed([]byte(trustDialog[:half]))
	ta.feed([]byte(trustDialog[half:]))

	if ta.armed.Load() {
		t.Error("dialog split across chunks should still be detected")
	}
}

// TestMaybeAcceptTrustWaitsWithoutDialog: ordinary early output leaves the
// accepter armed and quiet — it acts only on the actual dialog (R3128).
//
// CRC: crc-PtyHost.md | Test: test-PtyHost.md | R3128
func TestMaybeAcceptTrustWaitsWithoutDialog(t *testing.T) {
	sent := make(chan []byte, 4)
	ta := newTrustAccepter(func(p []byte) { sent <- p })

	ta.feed([]byte("\x1b[2mStarting up…\x1b[22m no menu here"))

	if !ta.armed.Load() {
		t.Error("accepter should stay armed until the dialog appears")
	}
	select {
	case <-ta.done:
		t.Error("done should not close without a dialog")
	default:
	}
	select {
	case got := <-sent:
		t.Errorf("no keystrokes should be sent, got %q", got)
	default:
	}
}

// recordingPtyEnv captures the teardown calls, so a test can assert which
// obligations ran, with what reason, and — the point of R3323's idempotence —
// how many times. Embeds fakePtyEnv for the methods it does not care about.
type recordingPtyEnv struct {
	fakePtyEnv
	mu        sync.Mutex
	released  []string
	exits     []string // one entry per RecordHostedExits call: "session/reason"
	published []string // one entry per PublishLaunchOutcome call: "session/failure"
}

func (e *recordingPtyEnv) ReleaseSeat(session string) {
	e.mu.Lock()
	e.released = append(e.released, session)
	e.mu.Unlock()
}

func (e *recordingPtyEnv) RecordHostedExits(session, reason string) {
	e.mu.Lock()
	e.exits = append(e.exits, session+"/"+reason)
	e.mu.Unlock()
}

func (e *recordingPtyEnv) PublishLaunchOutcome(session, failure string) {
	e.mu.Lock()
	e.published = append(e.published, session+"/"+failure)
	e.mu.Unlock()
}

func (e *recordingPtyEnv) publications() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.published...)
}

func (e *recordingPtyEnv) recorded() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.exits...)
}

// installSession gives the host a confirmed session id through the actor, as a
// successful launch's seat claim would (R3126).
func installSession(h *PtyHost, sid string) {
	_ = svcSyncVoid(h.jobs, func() error {
		h.sessionID = sid
		return nil
	})
}

// hostOnPipe wires a host whose master is the read end of a pipe, so a test can
// make the child "go away" by closing the write end. Returns the write end.
func hostOnPipe(t *testing.T, env PtyEnv, sid string) (*PtyHost, *os.File) {
	t.Helper()
	r, w := testPipe(t)
	rm := &testMaster{f: r}
	h := NewPtyHost(env)
	installMaster(h, rm, func(PtyWinsize) {})
	if sid != "" {
		installSession(h, sid)
	}
	h.startRead(rm, make([]byte, ptyReadChunk), nil)
	return h, w
}

// waitForExits polls until at least n exit recordings have landed; the teardown
// records off the actor, so the assertion cannot be synchronous.
func waitForExits(t *testing.T, env *recordingPtyEnv, n int) []string {
	t.Helper()
	waitFor(t, fmt.Sprintf("%d exit recording(s)", n), func() bool {
		return len(env.recorded()) >= n
	})
	return env.recorded()
}

// Test: a child that exits on its own still gets its pool secretaries exit-
// recorded (R3323). Recording only on Stop left them in the roster as ghosts,
// holding pool_max slots and drawing work to tubes nobody listens on.
func TestPtyHostSelfExitRecordsHostedExits(t *testing.T) {
	env := &recordingPtyEnv{}
	h, w := hostOnPipe(t, env, "sess-self")

	w.Close() // the child goes away on its own → EOF → the read chain finalizes

	got := waitForExits(t, env, 1)
	if want := "sess-self/" + ptyReasonSelfExit; got[0] != want {
		t.Errorf("recorded %q, want %q (the reason must say the child chose this, R3324)", got[0], want)
	}
	if hosted, _ := h.Status(); hosted {
		t.Error("host still reports a session after the child self-exited")
	}
	env.mu.Lock()
	defer env.mu.Unlock()
	if len(env.released) != 1 || env.released[0] != "sess-self" {
		t.Errorf("seat releases = %v, want exactly [sess-self]", env.released)
	}
}

// Test: Stop records exactly once and with its own reason, even though closing
// the master raises a second, racing finalize on the reader (R3323, R3324).
func TestPtyHostStopRecordsHostedExitsOnce(t *testing.T) {
	env := &recordingPtyEnv{}
	h, _ := hostOnPipe(t, env, "sess-stop")

	if err := h.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}

	got := waitForExits(t, env, 1)
	if want := "sess-stop/" + ptyReasonStopped; got[0] != want {
		t.Errorf("recorded %q, want %q (a stop is ark's teardown, not the child's)", got[0], want)
	}
	// The reader's finalize fires on the master close Stop triggers; give it room
	// to double-record before asserting it did not.
	time.Sleep(100 * time.Millisecond)
	if got := env.recorded(); len(got) != 1 {
		t.Errorf("recorded %d times (%v), want exactly 1 — the chokepoint must be idempotent", len(got), got)
	}
}

// Test: a teardown with no confirmed session records nothing (R3323) — an
// abandoned launch never claimed the seat, so it can have stood up no
// secretaries, and any roster entries belong to somebody else.
func TestPtyHostFinalizeWithoutSessionRecordsNothing(t *testing.T) {
	env := &recordingPtyEnv{}
	h := NewPtyHost(env)
	m := fakeMaster(t)
	installMaster(h, m, func(PtyWinsize) {})

	h.finalize(m, ptyReasonLaunchFailed)

	if got := env.recorded(); len(got) != 0 {
		t.Errorf("recorded %v, want nothing — no seat claim means no secretaries of ours", got)
	}
}

// Test: Reserve rejects a second launch while one is hosted or mid-flight
// (R3122), which is what lets the async binding report that synchronously
// instead of losing it inside a goroutine (R3325).
func TestPtyHostReserveRejectsSecond(t *testing.T) {
	h := NewPtyHost(&recordingPtyEnv{})

	if err := h.Reserve(); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	if err := h.Reserve(); err == nil {
		t.Error("second reserve succeeded; a launch is already mid-flight")
	}

	// A host with a session already up refuses too, by the other half of the
	// same check.
	h2 := hostWithMaster(t, &recordingPtyEnv{})
	if err := h2.Reserve(); err == nil {
		t.Error("reserve succeeded while a session is hosted")
	}
}

// Test: a launch rejected at the reservation publishes nothing (R3326). It
// never started, so announcing a failure would be a false record — and worse,
// would overwrite the hosted tag while that session is still up.
func TestPtyHostRejectedLaunchPublishesNothing(t *testing.T) {
	env := &recordingPtyEnv{}
	h := hostWithMaster(t, env)

	if _, err := h.Launch("", ""); err == nil {
		t.Fatal("launch succeeded while a session is hosted")
	}
	if got := env.publications(); len(got) != 0 {
		t.Errorf("published %v, want nothing — a rejected launch never started", got)
	}
}

// Test: the launch-outcome body is one line, whatever the reason looked like
// (R3326) — a tag's value ends at the newline, so a multi-line error would be
// truncated and its tail indexed as loose prose.
func TestLaunchOutcomeContent(t *testing.T) {
	for _, tc := range []struct {
		name             string
		session, failure string
		want             string
	}{
		{"confirmed", "sess-1", "", "@" + tagLuhmannHosted + ": sess-1\n"},
		{"failed", "", "seat claim timed out", "@" + tagLuhmannLaunchFailed + ": seat claim timed out\n"},
		{"multiline reason collapses", "", "launch: dial\nfailed\n  twice", "@" + tagLuhmannLaunchFailed + ": launch: dial failed twice\n"},
		{"nothing to say", "", "", ""},
		{"blank reason is nothing to say", "", "   \n ", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := launchOutcomeContent(tc.session, tc.failure)
			if string(got) != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// Test: ReleaseReservation frees a reservation whose launch never ran (R3325).
// The async caller's panic path needs it: without it, `launching` would stay set
// and every later launch would be refused as "already hosted".
func TestPtyHostReleaseReservation(t *testing.T) {
	h := NewPtyHost(&recordingPtyEnv{})

	if err := h.Reserve(); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	h.ReleaseReservation()
	if err := h.Reserve(); err != nil {
		t.Errorf("reserve after release: %v — the reservation stayed wedged", err)
	}
}

// Test: a detached session allocates nothing to broadcast, and an attached one
// allocates nothing at all (R3336). This is the successor to the R3328 test,
// and it asks for more than that one did: R3328 removed the per-read 32KB and
// copied exact-size only when someone would retain the bytes, whereas the two
// per-connection buffers reach their high-water mark and are then reused for the
// life of the connection, so there is no per-chunk allocation left to count.
//
// Measured inside a jobs closure so everything runs where it really runs — the
// actor — rather than being reached from the test goroutine.
func TestPtyHostSteadyStateAppendAllocatesNothing(t *testing.T) {
	h := hostWithMaster(t, &recordingPtyEnv{})

	// A stalled client keeps the write in flight, so the measured runs exercise
	// the append path alone: no writer is spawned and no continuation lands.
	c := &fakePtyClient{gate: make(chan struct{})}
	attachFake(t, h, c)
	chunk := make([]byte, 512)

	var allocs float64
	_ = svcSyncVoid(h.jobs, func() error {
		h.distributeLocked(chunk) // grow pending past its first append
		allocs = testing.AllocsPerRun(50, func() { h.distributeLocked(chunk) })
		return nil
	})
	if allocs != 0 {
		t.Errorf("steady-state append allocated %v times, want 0 — the buffers are reused (R3336)", allocs)
	}

	// And the bytes are real: releasing the stall delivers everything appended.
	c.release()
	waitFor(t, "the stalled output to drain", func() bool { return len(c.bytes()) == 52*len(chunk) })
}
