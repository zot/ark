package ark

// CRC: crc-Server.md | Test: test-StopCommand.md | R3446

import (
	"errors"
	"fmt"
	"net"
	"testing"
)

// TestServeExitErrorShutdownLatch pins the shutdown-race fix: when the
// shuttingDown latch is set, the main serve loop's error is an expected
// listener-close and Serve must return nil (so gracefulShutdown's os.Exit(0)
// is the sole exit and the caller's fatal()/exit-1 never fires); when the
// latch is clear, only the net.ErrClosed sentinel is swallowed and every other
// error propagates. R3446
func TestServeExitErrorShutdownLatch(t *testing.T) {
	otherErr := fmt.Errorf("accept unix /x/ark.sock: some other failure")

	cases := []struct {
		name    string
		latched bool
		in      error
		want    error
	}{
		{"latched swallows accept error", true, otherErr, nil},
		{"latched swallows ErrClosed", true, net.ErrClosed, nil},
		{"latched swallows nil", true, nil, nil},
		{"unlatched swallows ErrClosed", false, net.ErrClosed, nil},
		{"unlatched wrapped ErrClosed", false, fmt.Errorf("serve: %w", net.ErrClosed), nil},
		{"unlatched propagates other error", false, otherErr, otherErr},
		{"unlatched nil stays nil", false, nil, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var srv Server // zero value: latch starts false
			if tc.latched {
				srv.shuttingDown.Store(true)
			}
			got := srv.serveExitError(tc.in)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("serveExitError = %v, want nil", got)
				}
				return
			}
			if !errors.Is(got, tc.want) {
				t.Fatalf("serveExitError = %v, want %v", got, tc.want)
			}
		})
	}
}
