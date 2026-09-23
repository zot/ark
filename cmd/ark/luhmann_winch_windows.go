//go:build windows

package main

// Windows has no SIGWINCH, so the attach client cannot receive terminal-resize
// signals. notifyWinch instead polls the console window size and nudges ch on a
// change; the shared consumer re-reads and re-reports its size on any nudge
// (R3120, R3452). Polling the window rect (via term.GetSize) catches window-only
// resizes that a WINDOW_BUFFER_SIZE_EVENT misses on legacy conhost, and needs no
// rewrite of the single-reader input path. The Unix build (luhmann_winch_unix.go)
// does the real signal.Notify.
//
// sizeFd is stdout, not stdin: GetConsoleScreenBufferInfo (which term.GetSize
// wraps) rejects the console input handle, so term.GetSize(os.Stdin.Fd()) fails
// with "the handle is invalid" on Windows (measured 2026-09-23). stdin still
// serves IsTerminal and raw mode (R3451).
//
// CRC: crc-PtyAttach.md | R3120, R3451, R3452

import (
	"os"
	"time"

	"golang.org/x/term"
)

// winchPollInterval is how often the client re-reads its window size to
// synthesize a resize notification. Short enough to feel live, cheap enough to
// be negligible.
const winchPollInterval = 250 * time.Millisecond

// winchSignal is the value the poller sends on the winch channel. The consumer
// ignores the value and only re-reads the size on any send, so any os.Signal
// serves.
type winchSignal struct{}

func (winchSignal) String() string { return "winch" }
func (winchSignal) Signal()        {}

func notifyWinch(ch chan<- os.Signal) {
	go func() {
		lastW, lastH, _ := term.GetSize(sizeFd())
		t := time.NewTicker(winchPollInterval)
		defer t.Stop()
		for range t.C {
			w, h, err := term.GetSize(sizeFd())
			if err != nil || (w == lastW && h == lastH) {
				continue
			}
			lastW, lastH = w, h
			select {
			case ch <- winchSignal{}:
			default: // consumer still handling the previous nudge; the next tick re-fires
			}
		}
	}()
}

func sizeFd() int { return int(os.Stdout.Fd()) }
