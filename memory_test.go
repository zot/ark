package ark

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"weak"
)

// CRC: crc-DB.md | Test: test-MemoryRelease.md | R3601-R3607

// TestReleaseAfterScanBurstThreshold: below the threshold nothing is released
// and the count is kept; at it, the count resets. R3602
func TestReleaseAfterScanBurstThreshold(t *testing.T) {
	db := &DB{scanBytesSinceRelease: releaseAfterScanBytes - 1}
	db.releaseAfterScanBurst()
	if db.scanBytesSinceRelease != releaseAfterScanBytes-1 {
		t.Fatalf("below threshold: count = %d, want it kept", db.scanBytesSinceRelease)
	}
	db.scanBytesSinceRelease = releaseAfterScanBytes
	db.releaseAfterScanBurst()
	if db.scanBytesSinceRelease != 0 {
		t.Fatalf("at threshold: count = %d, want reset to 0", db.scanBytesSinceRelease)
	}
}

// TestReleaseWaitsForEmptyRegistry: retiring a scan while another is in flight
// releases nothing; retiring the last one does. R3602
func TestReleaseWaitsForEmptyRegistry(t *testing.T) {
	db := &DB{scanBytesSinceRelease: releaseAfterScanBytes}
	a, b := db.stampFileScan("/a"), db.stampFileScan("/b")
	db.retireFileScanIfCurrent("/a", a)
	if db.scanBytesSinceRelease == 0 {
		t.Fatalf("released with a scan still in flight")
	}
	db.retireFileScanIfCurrent("/b", b)
	if db.scanBytesSinceRelease != 0 {
		t.Fatalf("registry empty past the threshold, but no release (count %d)", db.scanBytesSinceRelease)
	}
}

// TestChunkedBytes: an append counts its new bytes, a full chunk the file, an
// unchanged skip nothing. R3602, R3603
func TestChunkedBytes(t *testing.T) {
	if got := (&scanPrep{isAppend: true, newBytes: make([]byte, 7), fileSize: 100}).chunkedBytes(); got != 7 {
		t.Errorf("scan append = %d, want 7", got)
	}
	if got := (&scanPrep{fileSize: 100}).chunkedBytes(); got != 100 {
		t.Errorf("scan full = %d, want 100", got)
	}
	data := make([]byte, 50)
	if got := (&refreshPrep{unchanged: true, data: data}).chunkedBytes(); got != 0 {
		t.Errorf("refresh unchanged = %d, want 0", got)
	}
	if got := (&refreshPrep{isAppend: true, newBytes: make([]byte, 5), data: data}).chunkedBytes(); got != 5 {
		t.Errorf("refresh append = %d, want 5", got)
	}
	if got := (&refreshPrep{data: data}).chunkedBytes(); got != 50 {
		t.Errorf("refresh full = %d, want 50", got)
	}
}

// TestMemoryStatus: the heap figures are populated and consistent, and Linux
// reports a resident size. R3605
func TestMemoryStatus(t *testing.T) {
	m := memoryStatus()
	if m.HeapLive == 0 || m.HeapInUse == 0 || m.GoTotal < m.HeapInUse {
		t.Fatalf("implausible heap figures: %+v", m)
	}
	if runtime.GOOS == "linux" && m.RSS == 0 {
		t.Fatalf("linux: no resident size read: %+v", m)
	}
}

// TestWriteHeapProfile: the profile file is written and non-empty. R3606
func TestWriteHeapProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "heap.pprof")
	if err := writeHeapProfile(path); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
		t.Fatalf("no heap profile written: %v, %v", fi, err)
	}
}

// TestStatusHeapRefusesRelativePath: the server writes only to an absolute
// path, since its working directory is not the caller's. R3606
func TestStatusHeapRefusesRelativePath(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/status/heap", strings.NewReader(`{"path":"heap.pprof"}`))
	(&Server{}).handleStatusHeap(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("relative path: status %d, want 400", rec.Code)
	}
}

// TestPopFrontReleasesPopped: once popped, a closure and what it captured are
// no longer reachable through the queue's backing array, though the queue
// itself stays alive. R3607
func TestPopFrontReleasesPopped(t *testing.T) {
	payload := new([1 << 20]byte)
	w := weak.Make(payload)
	// A heap-held queue grown by append, the shape of DB.writeQueue: a local
	// literal slice would live on the stack, where the GC's precise stack maps
	// say nothing about a heap backing array.
	h := &queueHolder{}
	h.q = append(h.q, capturing(payload))
	h.q = append(h.q, func() int { return 0 })
	queueSink = h
	payload = nil
	popFront(&h.q)
	runtime.GC()
	if w.Value() != nil {
		t.Fatalf("popped closure's capture is still reachable through the queue")
	}
	queueSink = nil
}

type queueHolder struct{ q []func() int }

// queueSink keeps the holder on the heap and reachable across the collection.
var queueSink *queueHolder

// capturing returns a closure holding p by value, the way a store closure holds
// its scan prep.
func capturing(p *[1 << 20]byte) func() int { return func() int { return int(p[0]) } }
