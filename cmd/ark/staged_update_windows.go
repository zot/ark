//go:build windows

package main

// CRC: crc-StagedUpdate.md | Seq: seq-install.md#5.3, seq-install.md#5.7.2 | R3534, R3538

import (
	"errors"
	"os"
	"os/exec"

	"golang.org/x/sys/windows"
)

// lockFile takes an exclusive, fail-immediately LockFileEx on path. Windows
// releases it when the handle closes, including at process exit. R3534
func lockFile(path string) (func(), bool) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, false
	}
	h := windows.Handle(f.Fd())
	ol := new(windows.Overlapped)
	if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol); err != nil {
		f.Close()
		return nil, false
	}
	return func() {
		windows.UnlockFileEx(h, 0, 1, 0, ol)
		f.Close()
	}, true
}

// chainTo stands in for exec, which Windows lacks: run exe with the same
// arguments and standard streams, wait, and exit with its code. R3538
func chainTo(exe string, args []string) error {
	cmd := exec.Command(exe, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		os.Exit(exit.ExitCode())
	}
	if err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
