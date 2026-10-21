package main

// CRC: crc-StagedUpdate.md | Seq: seq-install.md#4, seq-install.md#5 | R3526-R3539
//
// A requested update in two halves. `ark update --stage` puts a verified binary
// beside the installed one as ark.new and records the request in update.json;
// the first thing `ark serve` does is take it: validate, swap, chain, and the
// chained binary refreshes the assets before serving. The server never
// restarts itself for this, and nothing here runs without a user's request.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/zot/ark"
)

const (
	// updateLockFile serializes the startup step between starting processes. R3534
	updateLockFile = "update.lock"
	// stagingSuffix names the staged binary's in-progress download. R3526
	stagingSuffix = ".tmp"
	// maxDownloadAttempts is how many failed validations abandon a request. R3536
	maxDownloadAttempts = 3
)

// updateRequest is update.json; the shape is the library's, shared with the
// UI bridge. R3527
type updateRequest = ark.UpdateRequest

// stagedExePaths returns the installed, staged and aside binary paths; the
// staged name is the library's, so the UI bridge sees the same file. R3526
func stagedExePaths(arkDir string) (exe, staged, aside string) {
	exe = filepath.Join(arkDir, arkExeName())
	return exe, filepath.Join(arkDir, ark.StagedExeName()), exe + asideSuffix
}

func readUpdateRequest(arkDir string) (updateRequest, bool) {
	return ark.ReadUpdateRequest(arkDir)
}

func writeUpdateRequest(arkDir string, req updateRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(arkDir, ark.UpdateRequestFile), data, 0644)
}

func removeUpdateRequest(arkDir string) {
	_ = os.Remove(filepath.Join(arkDir, ark.UpdateRequestFile))
}

// sameVersion compares release tags and `ark version` output, which may
// differ only by a leading "v". R3535, R3539
func sameVersion(a, b string) bool {
	return strings.TrimPrefix(a, "v") == strings.TrimPrefix(b, "v")
}

// Check reports the latest release and whether it is newer; it changes
// nothing. R3528
func (u *Updater) Check() (latest string, newer bool, err error) {
	latest, err = u.Fetcher.Latest()
	if err != nil {
		return "", false, fmt.Errorf("check latest release: %w", err)
	}
	return latest, compareVersions(u.Version, latest) < 0, nil
}

// Stage downloads and verifies the latest release as ark.new and records the
// request with the given attempt count. It never stops a server. R3526,
// R3527, R3529
func (u *Updater) Stage(attempts int) error {
	latest, newer, err := u.Check()
	if err != nil {
		return err
	}
	if !newer {
		fmt.Fprintf(u.out(), "already current (%s >= %s); nothing to stage\n", u.Version, latest)
		return nil
	}
	newBinary, err := u.fetchVerified(latest)
	if err != nil {
		return err
	}
	_, staged, _ := stagedExePaths(u.ArkDir)
	tmp := staged + stagingSuffix
	if err := os.WriteFile(tmp, newBinary, 0755); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, staged); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := writeUpdateRequest(u.ArkDir, updateRequest{Version: latest, DownloadAttempts: attempts}); err != nil {
		return err
	}
	fmt.Fprintf(u.out(), "staged ark %s; it takes effect the next time ark starts\n", latest)
	return nil
}

// StagedUpdate is the startup step. Its effects are injected so the decision
// logic runs in tests with no network, no real server, and no process
// replacement. CRC: crc-StagedUpdate.md
type StagedUpdate struct {
	ArkDir     string
	Version    string // the running binary's version
	RunningExe string // os.Executable()
	Log        io.Writer

	ServerRunning func() bool
	VersionOf     func(exe string) (string, error)
	Stage         func(attempts int) error // run in the background on a failed validation
	Refresh       func() error
	Chain         func(exe string, args []string) error
	Lock          func(path string) (unlock func(), ok bool)
}

func (s *StagedUpdate) logf(format string, a ...any) {
	fmt.Fprintf(s.Log, "update: "+format+"\n", a...)
}

// Run is the step `ark serve` takes before anything else. It returns when
// serve should carry on; a successful swap chains instead and, on Unix, never
// returns. CRC: crc-StagedUpdate.md | Seq: seq-install.md#5 | R3532-R3539
func (s *StagedUpdate) Run(args []string) error {
	// Seq: seq-install.md#5.2 | R3533
	exe, staged, aside := stagedExePaths(s.ArkDir)
	if isSymlink(exe) || !samePath(s.RunningExe, exe) {
		return nil
	}
	// Seq: seq-install.md#5.8 | R3534: the last update's aside copy, request or
	// not; under a live server the delete just fails, which is harmless
	_ = os.Remove(aside)
	// Seq: seq-install.md#5.1 | R3532: only a recorded request, never under a live server
	req, ok := readUpdateRequest(s.ArkDir)
	if !ok || s.ServerRunning() {
		return nil
	}
	// Seq: seq-install.md#5.3 | R3534
	unlock, ok := s.Lock(filepath.Join(s.ArkDir, updateLockFile))
	if !ok {
		return nil
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()

	// Seq: seq-install.md#5.4 | R3539: this is the chained new binary
	if sameVersion(req.Version, s.Version) {
		if err := s.Refresh(); err != nil {
			return fmt.Errorf("refresh assets for %s: %w", s.Version, err)
		}
		removeUpdateRequest(s.ArkDir)
		s.logf("finished updating to %s", s.Version)
		return nil
	}

	// Seq: seq-install.md#5.5 | R3535
	if got, err := s.VersionOf(staged); err != nil || !sameVersion(got, req.Version) {
		// Seq: seq-install.md#5.6
		os.Remove(staged)
		req.DownloadAttempts++
		if req.DownloadAttempts >= maxDownloadAttempts {
			removeUpdateRequest(s.ArkDir) // R3536
			s.logf("abandoned the update to %s after %d failed downloads", req.Version, req.DownloadAttempts)
			return nil
		}
		if err := writeUpdateRequest(s.ArkDir, req); err != nil {
			return err
		}
		s.logf("staged %s did not validate (got %q, %v); downloading again", req.Version, got, err)
		go func(attempts int) {
			if err := s.Stage(attempts); err != nil {
				s.logf("re-staging %s: %v", req.Version, err)
			}
		}(req.DownloadAttempts)
		return nil
	}

	// Seq: seq-install.md#5.7 | R3537
	if err := swapStaged(exe, staged, aside); err != nil {
		return err
	}
	s.logf("swapped in %s; restarting into it", req.Version)
	unlock()
	unlock = nil
	return s.Chain(exe, args)
}

// swapStaged renames exe aside and staged onto exe, restoring exe if the
// second rename fails, so ~/.ark/ark is never left missing. R3537
func swapStaged(exe, staged, aside string) error {
	if err := os.Rename(exe, aside); err != nil {
		return fmt.Errorf("move %s aside: %w", exe, err)
	}
	if err := os.Rename(staged, exe); err != nil {
		if rerr := os.Rename(aside, exe); rerr != nil {
			return errors.Join(fmt.Errorf("install %s: %w", staged, err), fmt.Errorf("restore %s: %w", exe, rerr))
		}
		return fmt.Errorf("install %s: %w", staged, err)
	}
	return nil
}

// samePath compares paths after cleaning, case-insensitively on Windows. R3533
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// binaryVersion runs `<exe> version` and returns the version it reports
// ("ark <v>"). R3535
func binaryVersion(exe string) (string, error) {
	out, err := exec.Command(exe, "version").Output()
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 || fields[0] != "ark" {
		return "", fmt.Errorf("unexpected version output %q", strings.TrimSpace(string(out)))
	}
	return fields[1], nil
}

// newUpdater is the production Updater, shared by update and the startup step.
func newUpdater() *Updater {
	return &Updater{
		ArkDir:  arkDir,
		Version: ark.Version,
		Fetcher: newReleaseFetcher(),
		Assets:  bundleAssetSource{},
	}
}

// runStagedUpdate wires the startup step to the real effects; cmdServe calls
// it first. CRC: crc-StagedUpdate.md | Seq: seq-install.md#5 | R3532
func runStagedUpdate() error {
	self, err := os.Executable()
	if err != nil {
		return nil // cannot tell which binary is running: leave the request for later
	}
	s := &StagedUpdate{
		ArkDir:        arkDir,
		Version:       ark.Version,
		RunningExe:    self,
		Log:           os.Stderr,
		ServerRunning: func() bool { return serverClient(arkDir) != nil },
		VersionOf:     binaryVersion,
		Stage:         func(attempts int) error { return newUpdater().Stage(attempts) },
		Refresh: func() error {
			u := newUpdater()
			if err := u.RefreshAssets(u.Assets); err != nil {
				return err
			}
			return u.provisionModel()
		},
		Chain: chainTo,
		Lock:  lockFile,
	}
	return s.Run(os.Args[1:])
}
