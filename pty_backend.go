// Luhmann-specific. See specs/managed-pty.md.
package ark

import "io"

// The pty backend seam: the platform-neutral boundary between PtyHost and the
// operating system's terminal primitive (crc-PtyBackend.md). The host spawns,
// reads, writes, resizes, and stops the hosted `claude` child entirely through
// these types, so it names no OS and runs on both Unix and Windows (R3447). The
// concrete backend is build-tag-split — creack/pty on Unix, ConPTY on Windows
// (R3449) — constructed by startPty in pty_backend_unix.go / pty_backend_windows.go.

// ptyMaster is the hosted terminal's master side. It exposes read, write, close,
// and resize as distinct operations rather than a single OS file, because a
// Windows ConPTY splits the byte stream (two pipes) from the resize control (the
// pseudoconsole handle): the host cannot assume the master is one *os.File.
// CRC: crc-PtyBackend.md | R3448
type ptyMaster interface {
	io.ReadWriteCloser
	// Resize applies a new terminal size: a real SIGWINCH on Unix, a
	// ResizePseudoConsole on Windows (R3449).
	Resize(cols, rows uint16) error
}

// ptyChild is the hosted child's process handle. Wait reaps it; the stop
// primitives are platform-correct: a graceful signal that exists only on Unix,
// and a kill that is SIGKILL on Unix and TerminateProcess on Windows. The host's
// gracefulShutdown owns the timing (signal, wait the grace window, then kill);
// the child owns only the platform-correct primitives (R3450).
// CRC: crc-PtyBackend.md | R3450
type ptyChild interface {
	// Wait blocks until the child has exited and reaps it.
	Wait() error
	// SignalGraceful asks the child to exit gracefully. SIGTERM on Unix; on
	// Windows there is no equivalent, so it is a no-op that reports unsupported.
	SignalGraceful() error
	// Kill terminates the child hard: SIGKILL on Unix, TerminateProcess on Windows.
	Kill() error
}

// ptyMasterRef wraps a ptyMaster so masterPtr (the lock-free input path, R3335)
// can be an atomic.Pointer over a concrete type and still clear to nil: an
// interface cannot be the atomic.Pointer element directly, and a nil interface
// is not a nil pointer. The embedded interface promotes Read/Write/Close/Resize.
type ptyMasterRef struct{ ptyMaster }
