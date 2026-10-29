//go:build !windows

package main

// CRC: crc-Uninstaller.md | Seq: seq-install.md#8.9 | R3696
//
// On Unix a running executable's file can be removed: the process keeps its
// open inode until it exits.

// removeExe removes the installed executable. R3696
func removeExe(exe string) error { return removeIfPresent(exe) }
