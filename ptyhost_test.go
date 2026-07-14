package ark

// Unit tests for the PtyHost fan-out + lifecycle logic (test-PtyHost.md). The
// deterministic parts — smallest-wins resize, broadcast drop-slow, serialized
// input, launch reject-second, zero-client survival — run against a fake client,
// a fake env, and a fake master (an os.Pipe), no real pty or server. The launch
// confirmation protocol (R3126) touches a real JSONL + the seat lease and is
// left to integration (see design.md Gaps).
//
// CRC: crc-PtyHost.md | Test: test-PtyHost.md | R3118, R3119, R3120, R3121, R3122

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
)

// fakePtyEnv is an inert PtyEnv for the fan-out tests (no seat, no roster).
type fakePtyEnv struct{}

func (fakePtyEnv) SeatOwner() string        { return "" }
func (fakePtyEnv) ForceReleaseSeat()        {}
func (fakePtyEnv) ReleaseSeat(string)       {}
func (fakePtyEnv) RecordHostedExits(string) {}
func (fakePtyEnv) LuhmannDir() string       { return "" }
func (fakePtyEnv) PoolRosterCount() int     { return 0 }

// fakePtyClient captures broadcast bytes through a bounded buffer: once cap is
// exceeded Write returns false (drop). cap == 0 means unbounded.
type fakePtyClient struct {
	mu     sync.Mutex
	got    []byte
	cap    int
	closed bool
}

func (c *fakePtyClient) Write(p []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cap > 0 && len(c.got)+len(p) > c.cap {
		return false
	}
	c.got = append(c.got, p...)
	return true
}

func (c *fakePtyClient) Close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
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

// fakeMaster returns a non-nil *os.File to stand in for the pty master.
func fakeMaster(t *testing.T) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	return w
}

// installMaster wires a fake master (and optional resize sink) through the actor.
func installMaster(h *PtyHost, m *os.File, sink func(PtyWinsize)) {
	_ = svcSyncVoid(h.jobs, func() error {
		h.master = m
		h.masterPtr.Store(m)
		h.resizeSink = sink
		return nil
	})
}

func clientCount(h *PtyHost) int {
	var n int
	_ = svcSyncVoid(h.jobs, func() error { n = len(h.clients); return nil })
	return n
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

// Test: broadcast drops a slow client, never blocks (R3118).
func TestPtyHostBroadcastDropsSlowClient(t *testing.T) {
	h := NewPtyHost(fakePtyEnv{})
	m := fakeMaster(t)
	installMaster(h, m, func(PtyWinsize) {})

	normal := &fakePtyClient{}        // unbounded
	stuck := &fakePtyClient{cap: 100} // overflows after 100 bytes
	if _, err := h.Attach(normal, PtyWinsize{Cols: 80, Rows: 24}); err != nil {
		t.Fatalf("attach normal: %v", err)
	}
	if _, err := h.Attach(stuck, PtyWinsize{Cols: 80, Rows: 24}); err != nil {
		t.Fatalf("attach stuck: %v", err)
	}

	chunk := []byte("0123456789") // 10 bytes
	for i := 0; i < 50; i++ {
		h.broadcast(m, chunk) // 500 bytes total — well past the stuck cap
	}

	if got := len(normal.bytes()); got != 500 {
		t.Errorf("normal client received %d bytes, want 500", got)
	}
	if !stuck.isClosed() {
		t.Error("stuck client was not closed on drop")
	}
	if n := clientCount(h); n != 1 {
		t.Errorf("client set has %d after drop, want 1 (only normal)", n)
	}
}

// Test: input merge is serialized (R3119).
func TestPtyHostInputSerialized(t *testing.T) {
	h := NewPtyHost(fakePtyEnv{})
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	h.masterPtr.Store(w) // Input drains through masterPtr

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
	h := NewPtyHost(fakePtyEnv{})
	installMaster(h, fakeMaster(t), func(PtyWinsize) {})

	_, err := h.Launch("")
	if err == nil {
		t.Fatal("expected launch to reject a second session, got nil")
	}
}

// Test: zero clients keeps the session running (R3121).
func TestPtyHostZeroClientsKeepsRunning(t *testing.T) {
	h := NewPtyHost(fakePtyEnv{})
	installMaster(h, fakeMaster(t), func(PtyWinsize) {})

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
	h := &PtyHost{inCh: make(chan []byte, 4)}
	h.trustArmed.Store(true)
	h.trustDone = make(chan struct{})

	h.maybeAcceptTrust([]byte(trustDialog))

	if h.trustArmed.Load() {
		t.Error("accepter should disarm after handling the dialog")
	}
	select {
	case <-h.trustDone:
	default:
		t.Error("trustDone should be closed after acceptance")
	}
	select {
	case got := <-h.inCh:
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
	h := &PtyHost{inCh: make(chan []byte, 4)}
	h.trustArmed.Store(true)
	h.trustDone = make(chan struct{})

	half := len(trustDialog) / 2
	h.maybeAcceptTrust([]byte(trustDialog[:half]))
	h.maybeAcceptTrust([]byte(trustDialog[half:]))

	if h.trustArmed.Load() {
		t.Error("dialog split across chunks should still be detected")
	}
}

// TestMaybeAcceptTrustWaitsWithoutDialog: ordinary early output leaves the
// accepter armed and quiet — it acts only on the actual dialog (R3128).
//
// CRC: crc-PtyHost.md | Test: test-PtyHost.md | R3128
func TestMaybeAcceptTrustWaitsWithoutDialog(t *testing.T) {
	h := &PtyHost{inCh: make(chan []byte, 4)}
	h.trustArmed.Store(true)
	h.trustDone = make(chan struct{})

	h.maybeAcceptTrust([]byte("\x1b[2mStarting up…\x1b[22m no menu here"))

	if !h.trustArmed.Load() {
		t.Error("accepter should stay armed until the dialog appears")
	}
	select {
	case <-h.trustDone:
		t.Error("trustDone should not close without a dialog")
	default:
	}
	select {
	case got := <-h.inCh:
		t.Errorf("no keystrokes should be sent, got %q", got)
	default:
	}
}
