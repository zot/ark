package ark

import (
	"reflect"
	"testing"
)

// TestClaimRefreshPathsCoalesces verifies IndexPathsAsync's queue-level
// coalescing: a path with a refresh already queued is not claimed again until
// it is released, so a saturated write actor doesn't accumulate duplicate
// refresh closures for the same file. R3005.
func TestClaimRefreshPathsCoalesces(t *testing.T) {
	db := &DB{}

	// First claim returns all paths and marks them queued.
	if got := db.claimRefreshPaths([]string{"a", "b"}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("first claim = %v, want [a b]", got)
	}

	// Overlapping claim returns only the not-yet-queued path; a and b are
	// still queued, so they are skipped.
	if got := db.claimRefreshPaths([]string{"a", "b", "c"}); !reflect.DeepEqual(got, []string{"c"}) {
		t.Fatalf("overlapping claim = %v, want [c]", got)
	}

	// Releasing a path lets the next event re-queue it; b stays queued.
	db.releaseRefreshPath("a")
	if got := db.claimRefreshPaths([]string{"a", "b"}); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("post-release claim = %v, want [a]", got)
	}
}
