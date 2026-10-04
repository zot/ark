//go:build windows

package main

// CRC: crc-Updater.md | Seq: seq-install.md#3.2.3 | R3473
//
// SwapExe on Windows cannot overwrite a running exe in place (measured, build
// 26200: "being used by another process"). It renames the running ark.exe
// aside, writes the new binary at the original name, and deletes the aside copy
// on a later run — the aside copy cannot be deleted while its process runs.

import (
	"os"
	"path/filepath"
)

// asideSuffix marks the renamed-aside old binary awaiting deletion. R3473
const asideSuffix = ".old"

// SwapExe performs the Windows rename-aside swap. CRC: crc-Updater.md | Seq: seq-install.md#3.2.3 | R3473
func SwapExe(arkDir string, newBinary []byte) error {
	dest := filepath.Join(arkDir, arkExeWindows)
	aside := dest + asideSuffix

	// Sweep a prior run's aside copy now that its process has exited. R3473
	_ = os.Remove(aside)

	// Rename the running binary aside, write the new one at the original name.
	if _, err := os.Stat(dest); err == nil {
		if err := os.Rename(dest, aside); err != nil {
			return err
		}
	}
	if err := os.WriteFile(dest, newBinary, 0755); err != nil {
		// Roll back the rename so a failed write leaves the old binary in place. R3476
		_ = os.Rename(aside, dest)
		return err
	}
	return nil
}
