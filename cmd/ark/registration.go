package main

// CRC: crc-Registration.md | Seq: seq-install.md#1.13 | R3680, R3681, R3682, R3695
//
// The per-user Windows registration of an install: ~\.ark on the user Path and
// ark's entry in the installed-apps list (how winget recognizes the install).
// The registry work is in registration_windows.go; off Windows it is a no-op.
// The decisions (which Path entries name the directory, what the entry holds)
// are pure functions here, so they are tested on every platform.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// pathListSep separates Windows Path entries (always ';', whatever the
	// platform the pure functions run on). R3680
	pathListSep = ";"
	// arpSubkey is ark's installed-apps entry under HKCU; winget reads the key
	// name as the product code. R3681
	arpSubkey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\` + arpDisplayName
	// arpDisplayName and arpPublisher are what Windows and winget show. Plain
	// "ark" is KDE's archive manager in winget's catalog, and winget matched an
	// entry named ark to KDE.Ark (measured 2026-10-29). R3681
	arpDisplayName = "Zot.Ark"
	arpPublisher   = "Bill Burdick"
	// uninstallVerb and uninstallYesFlag form the entry's uninstall commands. R3681, R3690
	uninstallVerb    = "uninstall"
	uninstallYesFlag = "yes"
)

// registerInstall and unregisterInstall are the platform registration, held in
// variables so tests can count calls without touching a registry.
var (
	registerInstall   = Register
	unregisterInstall = Unregister
)

// pathWithEntry appends dir to a Path value unless an entry already names it.
// Empty entries are dropped only when the value is rewritten. R3680
func pathWithEntry(path, dir string) (string, bool) {
	entries := splitPath(path)
	for _, e := range entries {
		if sameDirEntry(e, dir) {
			return path, false
		}
	}
	return strings.Join(append(entries, dir), pathListSep), true
}

// pathWithoutEntry drops every Path entry that names dir. R3695
func pathWithoutEntry(path, dir string) (string, bool) {
	var kept []string
	changed := false
	for _, e := range splitPath(path) {
		if sameDirEntry(e, dir) {
			changed = true
			continue
		}
		kept = append(kept, e)
	}
	if !changed {
		return path, false
	}
	return strings.Join(kept, pathListSep), true
}

// splitPath splits a Path value into its non-empty entries.
func splitPath(path string) []string {
	var entries []string
	for _, e := range strings.Split(path, pathListSep) {
		if e = strings.TrimSpace(e); e != "" {
			entries = append(entries, e)
		}
	}
	return entries
}

// sameDirEntry reports whether a Path entry names dir: compared
// case-insensitively, as Windows does, after expanding %VAR% references and
// dropping a trailing separator. R3680
func sameDirEntry(entry, dir string) bool {
	return strings.EqualFold(normalizeDirEntry(expandPercentVars(entry)), normalizeDirEntry(dir))
}

func normalizeDirEntry(s string) string {
	return strings.TrimRight(strings.Trim(s, `"`), `\/`)
}

// expandPercentVars expands Windows-style %NAME% references from the
// environment; an unknown name is left as written.
func expandPercentVars(s string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '%')
		if i < 0 {
			break
		}
		j := strings.IndexByte(s[i+1:], '%')
		if j < 0 {
			break
		}
		name := s[i+1 : i+1+j]
		b.WriteString(s[:i])
		if v, ok := os.LookupEnv(name); ok && name != "" {
			b.WriteString(v)
		} else {
			b.WriteString(s[i : i+j+2])
		}
		s = s[i+j+2:]
	}
	b.WriteString(s)
	return b.String()
}

// arpValues are the installed-apps entry's string and DWORD values. The
// uninstall commands quote the exe, since ~\.ark sits under a user profile
// whose name may hold a space. R3681
func arpValues(arkDir, version string) (strs map[string]string, dwords map[string]uint32) {
	exe := filepath.Join(arkDir, arkExeWindows)
	uninstall := `"` + exe + `" ` + uninstallVerb
	return map[string]string{
			"DisplayName":          arpDisplayName,
			"DisplayVersion":       version,
			"Publisher":            arpPublisher,
			"InstallLocation":      arkDir,
			"DisplayIcon":          exe,
			"UninstallString":      uninstall,
			"QuietUninstallString": uninstall + " --" + uninstallYesFlag,
		}, map[string]uint32{
			"NoModify": 1,
			"NoRepair": 1,
		}
}

// registerForSetup writes the registration for setup and reports it: a failure
// is a warning, since the install itself is complete, and a new Path entry
// tells the user to open a new terminal. R3680
func registerForSetup(arkDir, version string) {
	changed, err := registerInstall(arkDir, version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not register the install with Windows: %v\n", err)
		return
	}
	if changed {
		fmt.Printf("added %s to your PATH; open a new terminal to run `ark` there\n", arkDir)
	}
}
