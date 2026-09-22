//go:build windows

package main

// timeoutKill is the Windows graceful-stop timeout fallback (R3444): error, no
// kill. TerminateProcess is the very operation the graceful path exists to avoid
// on Windows — it can leave the socket or DB lock held — so a failed graceful
// stop is surfaced for the user to resolve rather than silently force-killed.
// `ark stop -f` (R3445) is the explicit, sanctioned way to TerminateProcess. The
// Unix build (stop_fallback_unix.go) kills; the shared cmdStop code never
// branches on GOOS. socketPath is unused on this platform (no re-poll after a
// kill that never happens).
//
// CRC: crc-CLI.md | R3444

import "fmt"

func timeoutKill(pid int, _ string) error {
	return fmt.Errorf("graceful stop of ark server (pid %d) timed out; the server did not shut down — resolve it manually or retry with `ark stop -f`", pid)
}
