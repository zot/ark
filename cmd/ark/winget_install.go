package main

// CRC: crc-WingetInstall.md | Seq: seq-install.md#7 | R3686, R3687, R3688, R3689
//
// `ark _winget-install`: what the winget package runs. winget downloads
// ark.exe to a temporary folder and runs it with this verb on install and on
// upgrade alike, so the verb decides from what it finds. The download is a
// throwaway; ~\.ark\ark.exe stays the one installed copy.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	ucli "github.com/urfave/cli/v3"
	"github.com/zot/ark"
)

const wingetInstallVerb = "_winget-install"

// WingetInstall holds the decision's inputs and its effects as fields, so the
// decision is tested without a registry, a server or a second binary.
// CRC: crc-WingetInstall.md
type WingetInstall struct {
	ArkDir    string
	Version   string // the running binary's
	GOOS      string
	VersionOf func(exe string) (string, error)
	Setup     func() error
	Stop      func() error
	Swap      func() error
	Refresh   func() error
	Register  func() error
}

// Run picks one of the three cases. Every error is returned, so the command
// exits non-zero and winget reports the failure. R3686–R3689
func (w *WingetInstall) Run() error {
	if w.GOOS != "windows" {
		return fmt.Errorf("%s runs only on Windows; it is what the winget package runs", wingetInstallVerb)
	}
	exe := filepath.Join(w.ArkDir, arkExeName())
	if isSymlink(exe) {
		return fmt.Errorf("%s is a symlink (developer install); refusing to install over it", exe)
	}
	// R3687: nothing installed, or the data without its program.
	if installState(w.ArkDir) != stateInstalled {
		return w.Setup()
	}
	// R3688: an older install, one whose version cannot be read, or one that is
	// not a release (a plain `go build` reports dev) is upgraded.
	installed, err := w.VersionOf(exe)
	if err != nil || !isReleaseVersion(installed) || compareVersions(installed, w.Version) < 0 {
		for _, step := range []func() error{w.Stop, w.Swap, w.Refresh, w.Register} {
			if err := step(); err != nil {
				return err
			}
		}
		fmt.Printf("upgraded ark to %s\n", w.Version)
		return nil
	}
	// R3689: same version or newer: registration only, never a downgrade.
	return w.Register()
}

// isReleaseVersion reports whether v is a release version: dotted decimal
// numbers, with an optional leading v. R3688
func isReleaseVersion(v string) bool {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	for _, p := range parts {
		if _, err := strconv.Atoi(p); err != nil {
			return false
		}
	}
	return v != ""
}

// wingetInstallCommand is the hidden verb's node. CRC: crc-CLI.md | R3686
func wingetInstallCommand() *ucli.Command {
	return &ucli.Command{
		Name:   wingetInstallVerb,
		Usage:  "internal: what the winget package runs (setup, upgrade from this binary, or registration)",
		Hidden: true,
		Action: func(context.Context, *ucli.Command) error { return newWingetInstall().Run() },
	}
}

// newWingetInstall wires the real effects. The upgrade's refresh reads this
// binary's own bundle; it cannot register itself, because this binary is
// winget's download rather than the installed exe, so Register follows it.
// CRC: crc-WingetInstall.md | Seq: seq-install.md#7.3
func newWingetInstall() *WingetInstall {
	return &WingetInstall{
		ArkDir:    arkDir,
		Version:   ark.Version,
		GOOS:      runtime.GOOS,
		VersionOf: binaryVersion,
		Setup:     func() error { return runGlobalSetup(arkDir) },
		Stop:      func() error { return stopServerForInstall(arkDir) },
		Swap: func() error {
			self, err := os.Executable()
			if err != nil {
				return err
			}
			data, err := os.ReadFile(self)
			if err != nil {
				return err
			}
			return SwapExe(arkDir, data)
		},
		Refresh: func() error { return newUpdater().RefreshInstalled() },
		Register: func() error {
			_, err := registerInstall(arkDir, ark.Version)
			return err
		},
	}
}
