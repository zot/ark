//go:build !windows

package main

// timeoutKill is the Linux graceful-stop timeout fallback (R3444): force-kill
// the PID (SIGKILL via proc.Kill) and wait for the socket to release. The OS
// frees the socket and DB lock on death, so a kill is a safe-enough last resort
// here. The Windows build (stop_fallback_windows.go) errors instead of killing,
// so the shared cmdStop code never has to branch on GOOS.
//
// CRC: crc-CLI.md | R3444

import (
	"fmt"
	"os"
)

func timeoutKill(pid int, socketPath string) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("graceful stop timed out and process %d not found: %w", pid, err)
	}
	if err := proc.Kill(); err != nil {
		return fmt.Errorf("graceful stop timed out and kill of process %d failed: %w", pid, err)
	}
	if !socketReleasedWithin(socketPath, stopKillTimeout) {
		return fmt.Errorf("process %d killed but socket %s still held", pid, socketPath)
	}
	return nil
}
