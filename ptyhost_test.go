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
	"sync"
	"testing"
)

// fakePtyEnv is an inert PtyEnv for the fan-out tests (no seat, no roster).
type fakePtyEnv struct{}

func (fakePtyEnv) SeatOwner() string        { return "" }
func (fakePtyEnv) ReleaseSeat(string)       {}
func (fakePtyEnv) RecordHostedExits(string) {}
func (fakePtyEnv) LuhmannDir() string       { return "" }
func (fakePtyEnv) ProjectsDir() string      { return "" }
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
