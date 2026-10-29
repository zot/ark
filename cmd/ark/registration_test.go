package main

// CRC: crc-Registration.md | Test: test-InstallLifecycle.md | R3680, R3681, R3695
//
// The registration's decisions are pure, so they run on every platform; the
// registry calls are verified live on Windows.

import (
	"path/filepath"
	"testing"
)

// TestPathEntryAddedOnce: the dir joins Path only when no entry names it,
// whatever the entry's case, a %VAR% spelling or a trailing separator. R3680
func TestPathEntryAddedOnce(t *testing.T) {
	t.Setenv("USERPROFILE", `C:\Users\Ann`)
	dir := `C:\Users\Ann\.ark`
	other := `C:\Tools;C:\Users\Ann\AppData\Local\Microsoft\WindowsApps`
	for _, tc := range []struct {
		name, path string
		changed    bool
	}{
		{"other case", other + `;c:\users\ann\.ARK`, false},
		{"percent var", other + `;%USERPROFILE%\.ark`, false},
		{"trailing separator", other + `;C:\Users\Ann\.ark\`, false},
		{"absent", other, true},
		{"empty", "", true},
	} {
		got, changed := pathWithEntry(tc.path, dir)
		if changed != tc.changed {
			t.Errorf("%s: changed = %v, want %v (got %q)", tc.name, changed, tc.changed, got)
			continue
		}
		if !changed && got != tc.path {
			t.Errorf("%s: an unchanged Path was rewritten to %q", tc.name, got)
		}
	}
	if got, _ := pathWithEntry(other, dir); got != other+";"+dir {
		t.Errorf("appended Path = %q, want the other entries in order then %s", got, dir)
	}
}

// TestPathEntryRemovedInEverySpelling: unregistering drops each entry naming
// the dir and keeps the rest in order. R3695
func TestPathEntryRemovedInEverySpelling(t *testing.T) {
	t.Setenv("USERPROFILE", `C:\Users\Ann`)
	path := `C:\Tools;%USERPROFILE%\.ark;C:\Go\bin;c:\users\ann\.ark\`
	got, changed := pathWithoutEntry(path, `C:\Users\Ann\.ark`)
	if !changed || got != `C:\Tools;C:\Go\bin` {
		t.Fatalf("pathWithoutEntry = %q, %v; want C:\\Tools;C:\\Go\\bin, true", got, changed)
	}
	if _, changed := pathWithoutEntry(`C:\Tools`, `C:\Users\Ann\.ark`); changed {
		t.Fatal("a Path without the entry reported a change")
	}
}

// TestInstalledAppsEntryNamesUninstall: the entry quotes the exe in both
// uninstall commands and gives the quiet one --yes. R3681
func TestInstalledAppsEntryNamesUninstall(t *testing.T) {
	arkDir := filepath.Join("C:", "Users", "Ann Lee", ".ark")
	strs, dwords := arpValues(arkDir, "1.2.3")
	exe := filepath.Join(arkDir, arkExeWindows)
	want := map[string]string{
		"DisplayName":          "Zot.Ark",
		"DisplayVersion":       "1.2.3",
		"InstallLocation":      arkDir,
		"UninstallString":      `"` + exe + `" uninstall`,
		"QuietUninstallString": `"` + exe + `" uninstall --yes`,
	}
	for k, v := range want {
		if strs[k] != v {
			t.Errorf("%s = %q, want %q", k, strs[k], v)
		}
	}
	if dwords["NoModify"] != 1 || dwords["NoRepair"] != 1 {
		t.Errorf("NoModify/NoRepair = %v, want 1", dwords)
	}
}
