package main

// CRC: crc-StagedUpdate.md | Test: test-StagedUpdate.md | R3526-R3539
//
// The startup step's decisions against a temp ~/.ark with every effect
// injected. Most of what it guards is invisible when violated: a swap under a
// live server, a binary taken because it merely ran, and a retry that never
// stops all look like an update working.

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zot/ark"
)

// stageRig is a temp ark dir plus a StagedUpdate whose effects record calls.
type stageRig struct {
	dir                string
	exe, staged, aside string
	s                  *StagedUpdate
	log                bytes.Buffer
	refreshes          int
	stageCalls         chan int
	chains             int // chain calls; args can be nil, so count them
	chained            []string
	chainedExe         string
}

func newStageRig(t *testing.T, running, stagedVersion string) *stageRig {
	t.Helper()
	r := &stageRig{dir: t.TempDir(), stageCalls: make(chan int, 4)}
	r.exe, r.staged, r.aside = stagedExePaths(r.dir)
	writeStageFile(t, r.exe, "old binary")
	writeStageFile(t, r.staged, "new binary")
	r.s = &StagedUpdate{
		ArkDir:        r.dir,
		Version:       running,
		RunningExe:    r.exe,
		Log:           &r.log,
		ServerRunning: func() bool { return false },
		VersionOf:     func(string) (string, error) { return stagedVersion, nil },
		Stage:         func(attempts int) error { r.stageCalls <- attempts; return nil },
		Refresh:       func() error { r.refreshes++; return nil },
		Chain: func(exe string, args []string) error {
			r.chains++
			r.chainedExe, r.chained = exe, args
			return nil
		},
		Lock: lockFile,
	}
	return r
}

func writeStageFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
}

func readStageFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func stageExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func (r *stageRig) request(t *testing.T, version string, attempts int) {
	t.Helper()
	if err := writeUpdateRequest(r.dir, updateRequest{Version: version, DownloadAttempts: attempts}); err != nil {
		t.Fatal(err)
	}
}

// stageCalled waits briefly for a background re-stage and returns its attempts.
func (r *stageRig) stageCalled() (int, bool) {
	select {
	case n := <-r.stageCalls:
		return n, true
	case <-time.After(200 * time.Millisecond):
		return 0, false
	}
}

func (r *stageRig) unchanged(t *testing.T) {
	t.Helper()
	if r.chains != 0 || stageExists(r.aside) {
		t.Errorf("swapped or chained (%d chains, ark.old exists %v)", r.chains, stageExists(r.aside))
	}
	if got := readStageFile(t, r.exe); got != "old binary" {
		t.Errorf("~/.ark/ark = %q, want the old binary", got)
	}
}

// Test: stage writes ark.new and update.json, and nothing partial (R3526, R3527)
func TestStageWritesStagedBinaryAndRequest(t *testing.T) {
	dir := t.TempDir()
	bin := []byte("new release binary")
	u := &Updater{ArkDir: dir, Version: "0.5.0", Out: &bytes.Buffer{},
		Fetcher: &fakeFetcher{version: "0.9.0", binary: bin, sum: ark.HashContent(bin)}}
	if err := u.Stage(0); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	_, staged, _ := stagedExePaths(dir)
	if got := readStageFile(t, staged); got != string(bin) {
		t.Errorf("ark.new = %q, want the release binary", got)
	}
	if req, ok := readUpdateRequest(dir); !ok || req != (updateRequest{Version: "0.9.0"}) {
		t.Errorf("update.json = %+v (ok %v), want {0.9.0 0}", req, ok)
	}
	if stageExists(staged + stagingSuffix) {
		t.Error("the in-progress download was left behind")
	}

	bad := t.TempDir()
	u = &Updater{ArkDir: bad, Version: "0.5.0", Out: &bytes.Buffer{},
		Fetcher: &fakeFetcher{version: "0.9.0", binary: bin, sum: "0000"}}
	if err := u.Stage(0); err == nil {
		t.Error("Stage accepted a checksum mismatch")
	}
	_, staged, _ = stagedExePaths(bad)
	if stageExists(staged) || stageExists(filepath.Join(bad, ark.UpdateRequestFile)) {
		t.Error("a failed checksum still left ark.new or update.json")
	}
}

// Test: stage and check are no-ops when current (R3526, R3528)
func TestStageAndCheckWhenCurrent(t *testing.T) {
	dir := t.TempDir()
	f := &fakeFetcher{version: "0.9.0", binary: []byte("x"), sum: ark.HashContent([]byte("x"))}
	u := &Updater{ArkDir: dir, Version: "0.9.0", Out: &bytes.Buffer{}, Fetcher: f}
	if err := u.Stage(0); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	_, staged, _ := stagedExePaths(dir)
	if stageExists(staged) || stageExists(filepath.Join(dir, ark.UpdateRequestFile)) || f.downloads != 0 {
		t.Error("Stage downloaded or wrote while already current")
	}
	latest, newer, err := u.Check()
	if err != nil || latest != "0.9.0" || newer {
		t.Errorf("Check = %q, %v, %v; want 0.9.0, false, nil", latest, newer, err)
	}
}

// Test: startup ignores a stray ark.new, and a live server (R3532)
func TestStartupNeedsARequestAndNoServer(t *testing.T) {
	r := newStageRig(t, "0.5.0", "0.9.0")
	if err := r.s.Run(nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	r.unchanged(t)

	r = newStageRig(t, "0.5.0", "0.9.0")
	r.request(t, "0.9.0", 0)
	r.s.ServerRunning = func() bool { return true }
	if err := r.s.Run(nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	r.unchanged(t)
	if _, called := r.stageCalled(); called {
		t.Error("staged again under a live server")
	}
}

// Test: a later start sweeps ark.old with no request pending (R3534)
func TestStartupSweepsAsideWithoutRequest(t *testing.T) {
	r := newStageRig(t, "0.9.0", "")
	writeStageFile(t, r.aside, "the previous binary")
	if err := r.s.Run(nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stageExists(r.aside) {
		t.Error("ark.old survived a start with no update pending")
	}
}

// Test: startup skips a developer install (R3533)
func TestStartupSkipsSymlinkedExe(t *testing.T) {
	r := newStageRig(t, "0.5.0", "0.9.0")
	r.request(t, "0.9.0", 0)
	real := filepath.Join(r.dir, "real-ark")
	writeStageFile(t, real, "dev build")
	os.Remove(r.exe)
	if err := os.Symlink(real, r.exe); err != nil {
		t.Skipf("cannot symlink: %v", err)
	}
	if err := r.s.Run(nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !isSymlink(r.exe) || r.chains != 0 {
		t.Errorf("developer install was swapped (still a link %v, %d chains)", isSymlink(r.exe), r.chains)
	}
}

// Test: a valid staged binary is swapped and chained (R3534, R3537)
func TestStartupSwapsAndChains(t *testing.T) {
	r := newStageRig(t, "0.5.0", "0.9.0")
	r.request(t, "v0.9.0", 0)
	writeStageFile(t, r.aside, "older leftover")
	args := []string{"serve", "--no-scan"}
	if err := r.s.Run(args); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := readStageFile(t, r.exe); got != "new binary" {
		t.Errorf("~/.ark/ark = %q, want the staged binary", got)
	}
	if got := readStageFile(t, r.aside); got != "old binary" {
		t.Errorf("ark.old = %q, want the old binary", got)
	}
	if stageExists(r.staged) {
		t.Error("ark.new still exists after the swap")
	}
	if r.chains != 1 || r.chainedExe != r.exe || !reflect.DeepEqual(r.chained, args) {
		t.Errorf("chain(%q, %v), want chain(%q, %v)", r.chainedExe, r.chained, r.exe, args)
	}
}

// Test: a staged binary reporting another version is rejected (R3535)
func TestStartupRejectsWrongVersion(t *testing.T) {
	r := newStageRig(t, "0.5.0", "0.8.0")
	r.request(t, "0.9.0", 0)
	if err := r.s.Run(nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	r.unchanged(t)
	if stageExists(r.staged) {
		t.Error("the rejected ark.new was kept")
	}
	if req, _ := readUpdateRequest(r.dir); req.DownloadAttempts != 1 {
		t.Errorf("downloadAttempts = %d, want 1", req.DownloadAttempts)
	}
	if n, called := r.stageCalled(); !called || n != 1 {
		t.Errorf("re-stage called %v with %d, want true with 1", called, n)
	}
}

// Test: three failed validations abandon the request (R3535, R3536)
func TestStartupAbandonsAfterThreeFailures(t *testing.T) {
	r := newStageRig(t, "0.5.0", "")
	r.s.VersionOf = func(string) (string, error) { return "", errors.New("exec format error") }
	r.request(t, "0.9.0", 2)
	if err := r.s.Run(nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	r.unchanged(t)
	if stageExists(filepath.Join(r.dir, ark.UpdateRequestFile)) || stageExists(r.staged) {
		t.Error("update.json or ark.new survived the third failure")
	}
	if _, called := r.stageCalled(); called {
		t.Error("downloaded a fourth time")
	}
	if !strings.Contains(r.log.String(), "abandoned") || !strings.Contains(r.log.String(), "0.9.0") {
		t.Errorf("log %q does not say the update to 0.9.0 was abandoned", r.log.String())
	}
}

// Test: the chained binary finishes instead of swapping again (R3539)
func TestStartupNewBinaryRefreshes(t *testing.T) {
	r := newStageRig(t, "0.9.0", "")
	os.Remove(r.staged)
	r.s.VersionOf = func(string) (string, error) { return "", errors.New("no ark.new") }
	r.request(t, "v0.9.0", 0)
	if err := r.s.Run(nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.refreshes != 1 {
		t.Errorf("refresh called %d times, want 1", r.refreshes)
	}
	if stageExists(filepath.Join(r.dir, ark.UpdateRequestFile)) {
		t.Error("update.json survived the finishing refresh")
	}
	if _, called := r.stageCalled(); called || r.chains != 0 {
		t.Errorf("finishing binary staged (%v) or chained (%d)", called, r.chains)
	}
}

// Test: a failed second rename restores ark (R3537)
func TestStartupRollsBackFailedSwap(t *testing.T) {
	r := newStageRig(t, "0.5.0", "0.9.0")
	os.Remove(r.staged)
	r.request(t, "0.9.0", 0)
	if err := r.s.Run(nil); err == nil {
		t.Error("Run reported success with no ark.new to install")
	}
	if got := readStageFile(t, r.exe); got != "old binary" {
		t.Errorf("~/.ark/ark = %q after a failed swap, want the old binary", got)
	}
	if r.chains != 0 {
		t.Error("chained after a failed swap")
	}
}

// Test: the lock keeps a second starter out (R3534)
func TestStartupLockExcludes(t *testing.T) {
	r := newStageRig(t, "0.5.0", "0.9.0")
	r.request(t, "0.9.0", 0)
	unlock, ok := lockFile(filepath.Join(r.dir, updateLockFile))
	if !ok {
		t.Fatal("could not take the lock")
	}
	if err := r.s.Run(nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	r.unchanged(t)
	unlock()
	if err := r.s.Run(nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.chains != 1 {
		t.Errorf("Run chained %d times once the lock was free, want 1", r.chains)
	}
}
