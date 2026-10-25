package ark

import (
	"bufio"
	"bytes"
	"log"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"runtime/pprof"
	"strconv"
	"time"
)

// Release thresholds (specs/memory-release.md). First guesses, not
// measurements: revisit once the status memory line has been watched across a
// few bursts.
const (
	releaseAfterScanBytes   = 256 << 20 // R3602, R3603: bytes chunked since the last release
	releaseAfterEmbedChunks = 512       // R3604: chunks one embed tier embedded
)

// releaseMemory forces a collection and hands freed pages back to the OS at
// once, instead of leaving them to the background scavenger. It stops the whole
// program while it runs, so callers use it only at quiet points after a burst.
// CRC: crc-DB.md | R3601
func releaseMemory(reason string) {
	start := time.Now()
	debug.FreeOSMemory()
	log.Printf("memory: released after %s (%s)", reason, time.Since(start).Round(time.Millisecond))
}

// writeHeapProfile collects, so the profile reflects what is still reachable,
// then writes this process's heap profile to path. R3606
// CRC: crc-Server.md | R3606
func writeHeapProfile(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	runtime.GC()
	if err := pprof.Lookup("heap").WriteTo(f, 0); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// writeCPUProfile profiles this process's CPU for d and writes the profile to
// path. pprof allows one CPU profile at a time; a second request fails. R3614
// CRC: crc-Server.md | R3614
func writeCPUProfile(path string, d time.Duration) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		f.Close()
		return err
	}
	time.Sleep(d)
	pprof.StopCPUProfile()
	return f.Close()
}

// MemoryStatus describes the serving process's memory for `ark status`.
type MemoryStatus struct {
	HeapLive  uint64 `json:"heapLive"`      // heap the last GC found reachable
	HeapInUse uint64 `json:"heapInUse"`     // Go heap spans in use
	HeapIdle  uint64 `json:"heapIdle"`      // Go heap idle and not yet returned to the OS
	GoTotal   uint64 `json:"goTotal"`       // everything Go holds from the OS
	RSS       uint64 `json:"rss,omitempty"` // resident size; Linux only
}

// memoryStatus reads the process's memory. RSS well above GoTotal is memory Go
// cannot see, chiefly the embedding model's C allocations.
// CRC: crc-DB.md | R3605
func memoryStatus() *MemoryStatus {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	live := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
	metrics.Read(live)
	return &MemoryStatus{
		HeapLive:  live[0].Value.Uint64(),
		HeapInUse: m.HeapInuse,
		HeapIdle:  m.HeapIdle - m.HeapReleased,
		GoTotal:   m.Sys - m.HeapReleased,
		RSS:       processRSS(),
	}
}

// processRSS returns VmRSS from /proc/self/status, or 0 where there is none.
func processRSS() uint64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		if rest, ok := bytes.CutPrefix(sc.Bytes(), []byte("VmRSS:")); ok {
			fields := bytes.Fields(rest) // "123456 kB"
			if len(fields) > 0 {
				if kb, err := strconv.ParseUint(string(fields[0]), 10, 64); err == nil {
					return kb << 10
				}
			}
		}
	}
	return 0
}
