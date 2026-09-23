//go:build !windows

// Luhmann-specific. See specs/managed-pty.md.
package ark

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

// startPty spawns argv attached to a real Unix pty of the given initial size and
// returns the master and child (R3448, R3449). The Unix backend is creack/pty
// (pure Go, so the CGO-free build holds) plus a standard *exec.Cmd for the child.
// CRC: crc-PtyBackend.md | R3448, R3449
func startPty(argv []string, dir string, env []string, cols, rows uint16) (ptyMaster, ptyChild, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: rows, Cols: cols})
	if err != nil {
		return nil, nil, err
	}
	return &unixMaster{f: f}, &unixChild{cmd: cmd}, nil
}

// unixMaster wraps the creack/pty master *os.File. Read, write, and close pass
// through; resize is a real SIGWINCH via pty.Setsize (R3449).
// CRC: crc-PtyBackend.md | R3449
type unixMaster struct{ f *os.File }

func (m *unixMaster) Read(p []byte) (int, error)  { return m.f.Read(p) }
func (m *unixMaster) Write(p []byte) (int, error) { return m.f.Write(p) }
func (m *unixMaster) Close() error                { return m.f.Close() }
func (m *unixMaster) Resize(cols, rows uint16) error {
	return pty.Setsize(m.f, &pty.Winsize{Cols: cols, Rows: rows})
}

// unixChild wraps the *exec.Cmd child. The graceful signal is SIGTERM and the
// kill is SIGKILL (R3450). Reaping uses Process.Wait rather than Cmd.Wait: the
// pty owns the child's fds, so the extra fd/stream handling Cmd.Wait performs is
// neither wanted nor safe here — the host's own read chain drains the master.
// CRC: crc-PtyBackend.md | R3450
type unixChild struct{ cmd *exec.Cmd }

func (c *unixChild) Wait() error           { _, err := c.cmd.Process.Wait(); return err }
func (c *unixChild) SignalGraceful() error { return c.cmd.Process.Signal(syscall.SIGTERM) }
func (c *unixChild) Kill() error           { return c.cmd.Process.Kill() }
