//go:build windows

package main

// Test: test-StopCommand.md
// CRC: crc-CLI.md | R3444

import (
	"strings"
	"testing"
)

// TestTimeoutKillErrorsWithoutKilling pins R3444 (Windows fallback): the default
// graceful-stop timeout errors and points the user at `ark stop -f` rather than
// calling TerminateProcess.
func TestTimeoutKillErrorsWithoutKilling(t *testing.T) {
	err := timeoutKill(999999, "nonexistent.sock")
	if err == nil {
		t.Fatal("windows timeoutKill must error on a graceful-stop timeout")
	}
	if !strings.Contains(err.Error(), "-f") {
		t.Fatalf("windows error should point at `ark stop -f`, got %q", err)
	}
}
