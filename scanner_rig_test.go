package ark

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Stamped walk-timing rig for Item 6 (carves/indexing-latency.md). Opt-in:
//
//	ARK_WALK_RIG=1 go test -run TestWalkSourcesTimingRig -count=1 -v .
//
// Measures walkSources wall time over the live ~/.ark corpus, parallel
// (runtime.NumCPU()) vs a single worker. Cold-cache timing needs root to drop
// the page cache; without it the rig reports warm only and says so. Read-only
// (walkSources only stats and reads directory entries). R3437
//
// Last run: 2026-09-17 over the live ~8.1k-file corpus (19357 leaves, 606 link
// candidates): parallel(8) ~390ms warm vs sequential(1) ~580ms (~1.5x); well under
// the 1s warm target. Cold needs root to drop caches, not measured. Item 6, uncommitted.
func TestWalkSourcesTimingRig(t *testing.T) {
	if os.Getenv("ARK_WALK_RIG") == "" {
		t.Skip("walk-timing rig; set ARK_WALK_RIG=1 to run against ~/.ark")
	}
	home, _ := os.UserHomeDir()
	cfgPath := filepath.Join(home, ".ark", "ark.toml")
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig(%s): %v", cfgPath, err)
	}
	matcher := &Matcher{Dotfiles: true}

	run := func(workers int) (time.Duration, int, int) {
		start := time.Now()
		leaves, links, _, werr := walkSources(cfg, matcher, workers)
		if werr != nil {
			t.Fatalf("walkSources(%d): %v", workers, werr)
		}
		return time.Since(start), len(leaves), len(links)
	}

	_, nl, nk := run(runtime.NumCPU()) // prime the page cache
	t.Logf("corpus: %d leaves, %d link candidates (workers=%d)", nl, nk, runtime.NumCPU())
	for i := 0; i < 5; i++ {
		d1, _, _ := run(1)
		dN, _, _ := run(runtime.NumCPU())
		t.Logf("warm %d: sequential(1)=%v  parallel(%d)=%v  speedup=%.2fx",
			i+1, d1, runtime.NumCPU(), dN, float64(d1)/float64(dN))
	}
}
