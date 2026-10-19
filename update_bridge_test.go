package ark

// CRC: crc-UpdateBridge.md | Test: test-StagedUpdate.md | R3540, R3541, R3542

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// waitState polls the bridge until it reports want, or fails after a second.
func waitState(t *testing.T, b *UpdateBridge, want string) UpdateStatus {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		st := b.Status()
		if st.State == want {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("state = %q (error %q), want %q", st.State, st.Error, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Test: the bridge reports check, download and staged states (R3540, R3541, R3542)
func TestUpdateBridgeStates(t *testing.T) {
	dir := t.TempDir()
	release := make(chan struct{})
	var gotArgs []string
	b := newUpdateBridge(dir, func(args ...string) (string, error) {
		gotArgs = args
		<-release
		return "0.9.0\nnewer than 0.5.0\n", nil
	})
	returned := make(chan struct{})
	go func() { b.CheckForUpdate(); close(returned) }()
	select {
	case <-returned:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("CheckForUpdate blocked until the check finished; it must return at once")
	}
	if st := b.Status(); st.State != UpdateChecking {
		t.Fatalf("state while the check runs = %q, want %q", st.State, UpdateChecking)
	}
	close(release)
	st := waitState(t, b, UpdateIdle)
	if st.Latest != "0.9.0" || !st.Newer || st.Current != Version {
		t.Errorf("status = %+v, want latest 0.9.0, newer, current %s", st, Version)
	}
	if len(gotArgs) != 2 || gotArgs[0] != "update" || gotArgs[1] != "--check" {
		t.Errorf("check ran %v, want [update --check]", gotArgs)
	}

	b.run = func(args ...string) (string, error) { return "", errors.New("download failed: HTTP 404") }
	b.RequestUpdate()
	if st := waitState(t, b, UpdateFailed); st.Error == "" {
		t.Error("failed state carries no error")
	}

	for _, name := range []string{UpdateRequestFile, StagedExeName()} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	fresh := newUpdateBridge(dir, nil)
	if st := fresh.Status(); st.State != UpdateStaged {
		t.Errorf("a request left on disk reads as %q, want %q", st.State, UpdateStaged)
	}
}
