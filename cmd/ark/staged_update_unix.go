//go:build !windows

package main

// CRC: crc-StagedUpdate.md | Seq: seq-install.md#5.3, seq-install.md#5.7.2 | R3534, R3538

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive, non-blocking flock on path. The kernel drops it
// when the process exits, so a crash never leaves it held. R3534
func lockFile(path string) (func(), bool) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, false
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, false
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, true
}

// chainTo replaces this process with exe, keeping the command line and the
// environment. It returns only if the exec fails. R3538
func chainTo(exe string, args []string) error {
	return syscall.Exec(exe, append([]string{exe}, args...), os.Environ())
}
