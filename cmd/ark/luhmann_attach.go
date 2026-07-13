package main

// The managed-PTY CLI verbs (#35 phase 1): `ark luhmann launch|attach|status|
// stop`. launch/status/stop proxy to the server; attach is a raw-mode terminal
// client that hijacks the unix socket into a bidirectional pty stream. See
// specs/managed-pty.md, crc-PtyAttach.md.
//
// CRC: crc-PtyAttach.md | Seq: seq-pty-attach.md | R3122, R3123, R3124, R3125

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	ucli "github.com/urfave/cli/v3"
	"github.com/zot/ark"
	"golang.org/x/term"
)

// ptyDetachPrefix is the tmux-style detach lead key: Ctrl-] (GS). Followed by
// 'd' it detaches (leaving the session running); doubled it sends a literal
// Ctrl-]; any other key sends the prefix then that key (R3123).
const ptyDetachPrefix = 0x1d

// luhmannLaunchAction proxies the launch verb: the server forks the pty and runs
// the content-free confirmation, blocking until the session claims the seat
// (R3122, R3126). The consent gate for spend.
// CRC: crc-PtyAttach.md | R3122
func luhmannLaunchAction(_ context.Context, c *ucli.Command) error {
	client := requireServer("luhmann launch")
	body := map[string]any{}
	if bs := c.String("bootstrap"); bs != "" {
		body["bootstrap"] = bs
	}
	var resp struct {
		Session string `json:"session"`
	}
	if err := proxyDecode(client, "POST", "/luhmann/launch", body, &resp); err != nil {
		fatal(err)
	}
	fmt.Printf("Luhmann session launched (session %s). Attach with `ark luhmann attach`.\n", resp.Session)
	return nil
}

// luhmannStatusAction reports whether a session is hosted plus the pool-secretary
// roster count — the source of truth the UI lamp and the CLI test read (R3124).
// CRC: crc-PtyAttach.md | R3124
func luhmannStatusAction(_ context.Context, c *ucli.Command) error {
	client := requireServer("luhmann status")
	var resp struct {
		Hosted      bool   `json:"hosted"`
		Session     string `json:"session"`
		Secretaries int    `json:"secretaries"`
	}
	if err := proxyDecode(client, "GET", "/luhmann/pty-status", nil, &resp); err != nil {
		fatal(err)
	}
	if c.Bool("json") {
		fmt.Printf("{\"hosted\":%t,\"session\":%q,\"secretaries\":%d}\n", resp.Hosted, resp.Session, resp.Secretaries)
		return nil
	}
	if !resp.Hosted {
		fmt.Println("Luhmann: asleep (no session hosted)")
		return nil
	}
	fmt.Printf("Luhmann: awake (session %s), %d pool secretary(ies)\n", resp.Session, resp.Secretaries)
	return nil
}

// luhmannStopAction proxies the graceful teardown (R3125).
// CRC: crc-PtyAttach.md | R3125
func luhmannStopAction(_ context.Context, _ *ucli.Command) error {
	client := requireServer("luhmann stop")
	if err := proxyOK(client, "POST", "/luhmann/stop", nil); err != nil {
		fatal(err)
	}
	fmt.Println("Luhmann session stopped.")
	return nil
}

// luhmannAttachAction is the raw-mode attach client (R3123): it hijacks the unix
// socket into a bidirectional pty stream, puts the local terminal in raw mode,
// pipes stdin → host and host → stdout, propagates SIGWINCH as smallest-wins
// resize (R3120), and detaches on the escape sequence without stopping the
// session. Multiple attach clients may run at once (R3117).
// CRC: crc-PtyAttach.md | Seq: seq-pty-attach.md#1.1 | R3117, R3120, R3123
func luhmannAttachAction(_ context.Context, _ *ucli.Command) error {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		fatal(fmt.Errorf("attach requires an interactive terminal"))
	}

	socketPath := filepath.Join(arkDir, "ark.sock")
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		fatal(fmt.Errorf("server not running (luhmann attach requires server)"))
	}

	// Send the attach request; the server either hijacks (200 then raw stream)
	// or returns an HTTP error (e.g. no session hosted).
	if _, err = io.WriteString(conn, "GET /luhmann/attach HTTP/1.1\r\nHost: ark\r\n\r\n"); err != nil {
		conn.Close()
		fatal(err)
	}
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		conn.Close()
		fatal(fmt.Errorf("attach handshake failed: %w", err))
	}
	if !strings.Contains(status, " 200 ") {
		rest, _ := io.ReadAll(br)
		conn.Close()
		reason := strings.TrimSpace(status[strings.Index(status, " ")+1:])
		body := strings.TrimSpace(string(rest))
		fatal(fmt.Errorf("attach refused: %s %s", reason, body))
	}
	for { // consume response headers up to the blank line
		line, herr := br.ReadString('\n')
		if herr != nil {
			conn.Close()
			fatal(herr)
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}

	// Raw mode + a single restore-and-exit path shared by detach and disconnect.
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		conn.Close()
		fatal(err)
	}
	var once sync.Once
	exit := func(msg string) {
		once.Do(func() {
			_ = term.Restore(fd, oldState)
			conn.Close()
			if msg != "" {
				fmt.Fprint(os.Stdout, "\r\n"+msg+"\r\n")
			}
			os.Exit(0)
		})
	}

	// Report the initial size, then track SIGWINCH (R3120).
	sendSize(conn, fd)
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	go func() {
		for range winch {
			sendSize(conn, fd)
		}
	}()

	fmt.Fprint(os.Stdout, "Attached to Luhmann. Detach: Ctrl-] then d.\r\n")

	// host → stdout: stream until the server closes the connection (stop or the
	// child exiting), then exit.
	go func() {
		_, _ = io.Copy(os.Stdout, br)
		exit("Luhmann session ended.")
	}()

	// stdin → host, watching for the detach escape (R3123).
	pipeInput(conn, exit)
	return nil
}

// sendSize reports the local terminal size as a resize frame (R3120).
func sendSize(conn net.Conn, fd int) {
	w, h, err := term.GetSize(fd)
	if err != nil {
		return
	}
	_ = ark.WritePtyResize(conn, ark.PtyWinsize{Cols: uint16(w), Rows: uint16(h)})
}

// pipeInput forwards stdin to the host as input frames, consuming the detach
// escape locally: Ctrl-] then 'd' detaches; Ctrl-] doubled sends a literal
// Ctrl-]; Ctrl-] then any other key sends the prefix and that key (R3123).
func pipeInput(conn net.Conn, exit func(string)) {
	buf := make([]byte, 4096)
	inEscape := false
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			out := make([]byte, 0, n)
			for _, b := range buf[:n] {
				if inEscape {
					inEscape = false
					switch b {
					case 'd', 'D':
						if len(out) > 0 {
							_ = ark.WritePtyInput(conn, out)
						}
						exit("[detached]")
						return
					case ptyDetachPrefix:
						out = append(out, ptyDetachPrefix) // doubled → literal
					default:
						out = append(out, ptyDetachPrefix, b)
					}
					continue
				}
				if b == ptyDetachPrefix {
					inEscape = true
					continue
				}
				out = append(out, b)
			}
			if len(out) > 0 {
				if werr := ark.WritePtyInput(conn, out); werr != nil {
					exit("Luhmann session ended.")
					return
				}
			}
		}
		if err != nil {
			exit("")
			return
		}
	}
}
