//go:build windows

package main

// notifyWinch is a no-op on Windows: the platform has no SIGWINCH, so an
// attached client does not receive live terminal-resize events (R3120 is
// Unix-only). The Unix build (luhmann_winch_unix.go) does the real registration.
//
// CRC: crc-PtyAttach.md | R3120

import "os"

func notifyWinch(_ chan<- os.Signal) {}
