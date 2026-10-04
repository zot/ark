//go:build !windows

package main

// CRC: crc-Updater.md | Seq: seq-install.md#3.2.3 | R3473
//
// SwapExe on Unix replaces the binary in place: a running process keeps its
// open file, so writing a new file at the same path is safe.

import (
	"os"
	"path/filepath"
)

// SwapExe writes newBinary over ~/.ark/ark, preserving the executable bit.
// CRC: crc-Updater.md | Seq: seq-install.md#3.2.3 | R3473
func SwapExe(arkDir string, newBinary []byte) error {
	dest := filepath.Join(arkDir, arkExeUnix)
	// Write to a temp then rename, so a crash mid-write cannot leave a
	// truncated binary. R3476
	tmp, err := os.CreateTemp(arkDir, arkExeUnix+".new-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(newBinary); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0755); err != nil {
		return err
	}
	return os.Rename(tmpName, dest)
}
