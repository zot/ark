//go:build !windows

package main

// notifyWinch registers ch for terminal-resize notifications so the attach
// client re-reports its size as a resize frame (R3120). SIGWINCH is a Unix
// signal; the Windows build (luhmann_winch_windows.go) supplies a no-op, so the
// shared attach code in luhmann_attach.go never names the constant directly.
//
// CRC: crc-PtyAttach.md | R3120

import (
	"os"
	"os/signal"
	"syscall"
)

func notifyWinch(ch chan<- os.Signal) {
	signal.Notify(ch, syscall.SIGWINCH)
}
