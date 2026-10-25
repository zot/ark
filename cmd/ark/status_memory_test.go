package main

import (
	"strings"
	"testing"

	"github.com/zot/ark"
)

// CRC: crc-CLI.md | Test: test-MemoryRelease.md | R3605, R3606

// TestPrintStatusMemoryLine: a served status prints one memory line before the
// server line; an unserved one prints none. R3605
func TestPrintStatusMemoryLine(t *testing.T) {
	served := captureStdout(t, func() {
		printStatus(&ark.StatusInfo{Memory: &ark.MemoryStatus{
			HeapLive: 2 << 20, HeapInUse: 3 << 20, HeapIdle: 1 << 20, GoTotal: 5 << 20, RSS: 9 << 20,
		}}, true)
	})
	mem := strings.Index(served, "memory: go heap ")
	if !strings.Contains(served, " live, ") {
		t.Fatalf("served status lacks the live heap:\n%s", served)
	}
	if mem < 0 || !strings.Contains(served, "; rss ") {
		t.Fatalf("served status lacks the memory line:\n%s", served)
	}
	if srv := strings.Index(served, "server: "); srv < mem {
		t.Fatalf("memory line must come before server:\n%s", served)
	}
	unserved := captureStdout(t, func() { printStatus(&ark.StatusInfo{}, false) })
	if strings.Contains(unserved, "memory:") {
		t.Fatalf("unserved status printed a memory line:\n%s", unserved)
	}
}

// TestHeapNeedsServer: --heap refuses without a server and is a no-op when
// unset. R3606
func TestHeapNeedsServer(t *testing.T) {
	if err := heapNeedsServer(false, "h.pprof"); err != errHeapNeedsServer {
		t.Fatalf("no server + --heap: got %v, want the refusal", err)
	}
	if err := heapNeedsServer(true, "h.pprof"); err != nil {
		t.Fatalf("server + --heap: got %v, want nil", err)
	}
	if err := heapNeedsServer(false, ""); err != nil {
		t.Fatalf("no --heap: got %v, want nil", err)
	}
}
