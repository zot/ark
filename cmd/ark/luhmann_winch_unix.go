//go:build !windows

package main

// notifyWinch registers ch for terminal-resize notifications so the attach
// client re-reports its size as a resize frame (R3120, R3452). SIGWINCH is a Unix
// signal; the Windows build (luhmann_winch_windows.go) polls the window rect
// instead, so the shared attach code never names the constant directly.
//
// sizeFd is the fd the client reads its terminal size from. On Unix the
// controlling terminal is a single fd, so stdin serves (R3451).
//
// CRC: crc-PtyAttach.md | R3120, R3451, R3452

import (
	"os"
	"os/signal"
	"syscall"
)

func notifyWinch(ch chan<- os.Signal) {
	signal.Notify(ch, syscall.SIGWINCH)
}

func sizeFd() int { return int(os.Stdin.Fd()) }
