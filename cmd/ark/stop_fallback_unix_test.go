//go:build !windows

package main

// Test: test-StopCommand.md
// CRC: crc-CLI.md | R3444

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestTimeoutKillKillsProcess pins R3444 (Linux fallback): timeoutKill
// force-kills the target PID and returns nil once the socket is released.
func TestTimeoutKillKillsProcess(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "ark.sock") // no listener → releases immediately

	if err := timeoutKill(cmd.Process.Pid, sockPath); err != nil {
		t.Fatalf("timeoutKill returned error: %v", err)
	}

	// The child must have died from the kill, not run its full sleep.
	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()
	select {
	case err := <-waitErr:
		if err == nil {
			t.Fatal("expected a non-nil wait error for a killed process")
		}
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("child still alive 2s after timeoutKill — kill did not fire")
	}
}
