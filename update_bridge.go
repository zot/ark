package ark

// CRC: crc-UpdateBridge.md | Seq: seq-install.md#6 | R3531, R3540-R3542
//
// The UI's side of a requested update. The bridge never fetches or swaps
// anything itself: it runs the server's own executable as `update --check` /
// `update --stage` (an argv, no shell) in the background and keeps the outcome
// where Lua reads it synchronously. The server never restarts for an update;
// the next `ark serve` takes a staged one (specs/main.md, "A requested update").

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
)

const (
	// UpdateRequestFile records a requested update in the ark dir. R3527
	UpdateRequestFile = "update.json"

	// Update states reported by UpdateStatus. R3542
	UpdateIdle        = "idle"
	UpdateChecking    = "checking"
	UpdateDownloading = "downloading"
	UpdateStaged      = "staged"
	UpdateFailed      = "failed"

	// newerPrefix starts `update --check`'s second line when the latest is newer.
	newerPrefix = "newer than"
)

// StagedExeName is the staged binary's name in the ark dir: ark.new, or
// ark.new.exe on Windows (keeping .exe last so it stays runnable). R3526
func StagedExeName() string {
	if runtime.GOOS == "windows" {
		return "ark.new.exe"
	}
	return "ark.new"
}

// UpdateRequest is update.json: the version staged and how many staged
// downloads have failed validation. One shape for the CLI and the UI bridge.
// R3527, R3543
type UpdateRequest struct {
	Version          string `json:"version"`
	DownloadAttempts int    `json:"downloadAttempts"`
}

// ReadUpdateRequest reads arkDir's update.json; false when absent or unusable.
// R3527, R3543
func ReadUpdateRequest(arkDir string) (UpdateRequest, bool) {
	var req UpdateRequest
	data, err := os.ReadFile(filepath.Join(arkDir, UpdateRequestFile))
	if err != nil || json.Unmarshal(data, &req) != nil || req.Version == "" {
		return req, false
	}
	return req, true
}

// UpdateStatus is one immutable snapshot of where an update stands. R3542, R3543
type UpdateStatus struct {
	Current       string
	Latest        string
	Newer         bool
	State         string
	Error         string
	StagedVersion string // set only while State is staged
}

// UpdateBridge runs update checks and stagings for the UI. The status is an
// immutable snapshot replaced whole through an atomic pointer, so the Lua
// executor reads it with no lock and no actor round trip; the in-flight flag
// keeps one check or staging at a time. CRC: crc-UpdateBridge.md
type UpdateBridge struct {
	arkDir   string
	status   atomic.Pointer[UpdateStatus]
	inFlight atomic.Bool
	// run executes the server's own binary with args and returns stdout, or an
	// error carrying stderr. Injected so tests need no real binary.
	run func(args ...string) (string, error)
}

// NewUpdateBridge builds a bridge whose child processes run exe.
func NewUpdateBridge(arkDir, exe string) *UpdateBridge {
	return newUpdateBridge(arkDir, func(args ...string) (string, error) {
		return runExe(exe, args...)
	})
}

func newUpdateBridge(arkDir string, run func(args ...string) (string, error)) *UpdateBridge {
	b := &UpdateBridge{arkDir: arkDir, run: run}
	b.status.Store(&UpdateStatus{Current: Version, State: UpdateIdle})
	return b
}

// runExe runs exe with args directly, no shell. R3540, R3541
func runExe(exe string, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}
	return stdout.String(), nil
}

// with stores a copy of the current snapshot changed by edit.
func (b *UpdateBridge) with(edit func(*UpdateStatus)) {
	next := *b.status.Load()
	edit(&next)
	b.status.Store(&next)
}

// start claims the in-flight slot and sets state; false when one is running.
func (b *UpdateBridge) start(state string) bool {
	if !b.inFlight.CompareAndSwap(false, true) {
		return false
	}
	b.with(func(s *UpdateStatus) { s.State, s.Error = state, "" })
	return true
}

func (b *UpdateBridge) fail(err error) {
	b.with(func(s *UpdateStatus) { s.State, s.Error = UpdateFailed, err.Error() })
}

// CheckForUpdate starts `update --check` in the background and returns at
// once; it downloads nothing. CRC: crc-UpdateBridge.md | Seq: seq-install.md#6.1 | R3540, R3531
func (b *UpdateBridge) CheckForUpdate() {
	if !b.start(UpdateChecking) {
		return
	}
	go func() {
		defer b.inFlight.Store(false)
		out, err := b.run("update", "--check")
		if err != nil {
			b.fail(err)
			return
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) != 2 {
			b.fail(fmt.Errorf("unexpected `update --check` output %q", out))
			return
		}
		b.with(func(s *UpdateStatus) {
			s.Latest = strings.TrimSpace(lines[0])
			s.Newer = strings.HasPrefix(strings.TrimSpace(lines[1]), newerPrefix)
			s.State = UpdateIdle
		})
	}()
}

// RequestUpdate starts `update --stage` in the background and returns at
// once: the user's request, the only thing that downloads a binary here.
// CRC: crc-UpdateBridge.md | Seq: seq-install.md#6.2 | R3541, R3531
func (b *UpdateBridge) RequestUpdate() {
	if !b.start(UpdateDownloading) {
		return
	}
	go func() {
		defer b.inFlight.Store(false)
		if _, err := b.run("update", "--stage"); err != nil {
			b.fail(err)
			return
		}
		b.with(func(s *UpdateStatus) { s.State = UpdateIdle })
	}()
}

// Status returns the snapshot, reporting staged, with the staged version,
// whenever a request and its staged binary are both on disk, including from
// an earlier session. CRC: crc-UpdateBridge.md | Seq: seq-install.md#6.3 | R3542, R3543
func (b *UpdateBridge) Status() UpdateStatus {
	s := *b.status.Load()
	if s.State != UpdateIdle {
		return s
	}
	if req, ok := ReadUpdateRequest(b.arkDir); ok {
		if _, err := os.Stat(filepath.Join(b.arkDir, StagedExeName())); err == nil {
			s.State, s.StagedVersion = UpdateStaged, req.Version
		}
	}
	return s
}
