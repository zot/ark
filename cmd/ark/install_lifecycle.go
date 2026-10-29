package main

// CRC: crc-CLI.md | Seq: seq-install.md#1.1, seq-install.md#1.14 | R3683, R3684
//
// An install's three states. The README places the binary at ~/.ark/ark before
// running setup, so the executable alone is a fresh install; `ark uninstall`
// removes the program and keeps the database, so the marker alone is data
// waiting for its program.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/zot/ark"
)

type installStateKind int

const (
	stateFresh     installStateKind = iota // no marker: install from scratch
	stateReinstall                         // marker, no executable: reinstall over the kept data
	stateInstalled                         // marker and executable: installed
)

func (k installStateKind) String() string {
	return [...]string{"fresh", "reinstall over kept data", "installed"}[k]
}

// installState reads which of the three states arkDir is in. R3683, R3684
func installState(arkDir string) installStateKind {
	if !markerPresent(arkDir) {
		return stateFresh
	}
	if _, err := os.Lstat(filepath.Join(arkDir, arkExeName())); err != nil {
		return stateReinstall
	}
	return stateInstalled
}

// reinstallOverData writes the bundle over the data uninstall kept: through
// the manifest-aware refresh, so a file the user edited (and uninstall kept)
// is kept again, then every bundled app is activated, since uninstall removed
// the activations. The database already exists and is not created. R3684
func reinstallOverData(arkDir string, src AssetSource) error {
	u := &Updater{ArkDir: arkDir, Assets: src}
	if err := u.RefreshAssets(src); err != nil {
		return fmt.Errorf("refresh bundled assets: %w", err)
	}
	files, err := src.List()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.Name)
	}
	for _, app := range ark.AppNamesInListing(names) {
		if err := ark.ActivateApp(arkDir, app); err != nil {
			return fmt.Errorf("activate app %s: %w", app, err)
		}
	}
	return nil
}
