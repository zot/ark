//go:build windows

package main

// CRC: crc-Uninstaller.md | Seq: seq-install.md#8.9 | R3696
//
// Windows refuses to delete a running exe's file, and `ark uninstall` normally
// runs as ~\.ark\ark.exe itself. So it hands the deletion to a detached,
// windowless helper that waits for this process to exit and then removes the
// file. The helper is PowerShell, present on every supported Windows, run with
// -Command (an execution policy governs script files, not -Command). It asks to
// break away from any job this process runs in (winget's, or an ssh session's),
// so ending that job does not kill it before it deletes; where the job forbids
// breaking away, it starts inside the job instead.

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// removeExe removes exe, through the helper when exe is the running process.
// R3696
func removeExe(exe string) error {
	self, err := os.Executable()
	if err != nil || !samePath(self, exe) {
		return removeIfPresent(exe)
	}
	script := fmt.Sprintf("Wait-Process -Id %d -ErrorAction SilentlyContinue; Remove-Item -LiteralPath '%s' -Force",
		os.Getpid(), strings.ReplaceAll(exe, "'", "''"))
	start := func(flags uint32) error {
		cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: flags}
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	}
	base := uint32(windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP)
	if err := start(base | windows.CREATE_BREAKAWAY_FROM_JOB); err != nil {
		return start(base)
	}
	return nil
}
