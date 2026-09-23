//go:build windows

// Luhmann-specific. See specs/managed-pty.md.
package ark

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"syscall"

	"github.com/UserExistsError/conpty"
)

// startPty spawns argv attached to a Windows ConPTY of the given initial size and
// returns the master and child (R3447, R3448, R3449). The Windows backend is a
// pseudoconsole via UserExistsError/conpty (pure Go over CreatePseudoConsole, so
// the CGO-free build holds). conpty takes a command line rather than an argv, so
// argv is quoted and joined; the master and child are two views of one *ConPty.
// CRC: crc-PtyBackend.md | R3447, R3449
func startPty(argv []string, dir string, env []string, cols, rows uint16) (ptyMaster, ptyChild, error) {
	cpty, err := conpty.Start(
		composeCmdline(argv),
		conpty.ConPtyDimensions(int(cols), int(rows)),
		conpty.ConPtyWorkDir(dir),
		conpty.ConPtyEnv(env),
	)
	if err != nil {
		return nil, nil, err
	}
	m := &windowsMaster{cpty: cpty}
	c := &windowsChild{cpty: cpty, ctx: context.Background()}
	// Bridge a child self-exit to a master EOF. conpty's output pipe does not EOF
	// on child exit until the pseudoconsole is closed, so without this the host's
	// read chain would never see a self-exited child (R3343). On exit, close the
	// master; the read chain then EOFs and the host finalizes. Close is idempotent
	// (sync.Once), so a racing Stop that also closes is harmless.
	go func() {
		_, _ = cpty.Wait(c.ctx)
		_ = m.Close()
	}()
	return m, c, nil
}

// composeCmdline quotes and joins argv into a Windows command line, the way
// os/exec does, since conpty.Start takes a command line string.
func composeCmdline(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = syscall.EscapeArg(a)
	}
	return strings.Join(parts, " ")
}

// windowsMaster wraps the ConPTY. Read drains the output pipe, write fills the
// input pipe, resize is ResizePseudoConsole, and close tears the pseudoconsole
// down (R3449). Close is idempotent because both the self-exit watcher and the
// host's gracefulShutdown call it.
// CRC: crc-PtyBackend.md | R3449
type windowsMaster struct {
	cpty *conpty.ConPty
	once sync.Once
	cerr error
}

func (m *windowsMaster) Read(p []byte) (int, error)     { return m.cpty.Read(p) }
func (m *windowsMaster) Write(p []byte) (int, error)    { return m.cpty.Write(p) }
func (m *windowsMaster) Resize(cols, rows uint16) error { return m.cpty.Resize(int(cols), int(rows)) }
func (m *windowsMaster) Close() error {
	m.once.Do(func() { m.cerr = m.cpty.Close() })
	return m.cerr
}

// windowsChild wraps the ConPTY's process. Wait reaps it; there is no graceful
// signal on Windows, so SignalGraceful reports unsupported and the host's grace
// window simply elapses; Kill is TerminateProcess via the pid (R3450).
// CRC: crc-PtyBackend.md | R3450
type windowsChild struct {
	cpty *conpty.ConPty
	ctx  context.Context
}

func (c *windowsChild) Wait() error {
	_, err := c.cpty.Wait(c.ctx)
	return err
}

func (c *windowsChild) SignalGraceful() error { return errors.ErrUnsupported }

func (c *windowsChild) Kill() error {
	p, err := os.FindProcess(c.cpty.Pid())
	if err != nil {
		return err
	}
	return p.Kill() // TerminateProcess on Windows
}
