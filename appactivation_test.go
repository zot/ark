// CRC: crc-AppActivation.md | Test: test-AppActivation.md
package ark

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mkApp makes ~/.ark/apps/<name>/ under arkDir.
func mkApp(t *testing.T, arkDir, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(arkDir, "apps", name), 0o755); err != nil {
		t.Fatal(err)
	}
}

// Test: activate makes the one link, deactivate removes it (R3521)
func TestActivateAndDeactivateApp(t *testing.T) {
	arkDir := t.TempDir()
	mkApp(t, arkDir, "demo")
	if err := ActivateApp(arkDir, "demo"); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(arkDir, "lua", "demo")
	if info, err := os.Stat(link); err != nil || !info.IsDir() {
		t.Fatalf("lua/demo does not resolve to a directory: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(arkDir, "lua")); len(entries) != 1 {
		t.Errorf("lua/ holds %d entries, want only the activation link", len(entries))
	}
	if err := DeactivateApp(arkDir, "demo"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); err == nil {
		t.Error("lua/demo survived deactivation")
	}
	if _, err := os.Stat(filepath.Join(arkDir, "apps", "demo")); err != nil {
		t.Error("deactivation touched the app directory")
	}
}

// Test: repair re-makes a dangling link, leaves an unrepairable one, and never
// creates an absent entry (R3522)
func TestRepairActivationLinks(t *testing.T) {
	arkDir := t.TempDir()
	mkApp(t, arkDir, "moved")
	mkApp(t, arkDir, "off")
	luaDir := filepath.Join(arkDir, "lua")
	os.MkdirAll(luaDir, 0o755)
	// A link left pointing where ~/.ark used to be.
	oldHome := filepath.Join(t.TempDir(), "old-ark")
	mkApp(t, oldHome, "moved")
	if err := MakeDirLink(filepath.Join(luaDir, "moved"), filepath.Join(oldHome, "apps", "moved")); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(oldHome)
	// A dangling link with no app to repair to.
	mkApp(t, oldHome, "gone")
	if err := MakeDirLink(filepath.Join(luaDir, "gone"), filepath.Join(oldHome, "apps", "gone")); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(oldHome)

	var logs []string
	RepairActivationLinks(arkDir, func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) })

	if !IsOurLink(filepath.Join(luaDir, "moved"), filepath.Join(arkDir, "apps", "moved")) {
		t.Error("dangling lua/moved was not re-made to ~/.ark/apps/moved")
	}
	if _, err := os.Lstat(filepath.Join(luaDir, "gone")); err != nil {
		t.Error("unrepairable lua/gone was removed; it should be left and logged")
	}
	if !strings.Contains(strings.Join(logs, "\n"), "gone") {
		t.Errorf("no log line for lua/gone: %q", logs)
	}
	if _, err := os.Lstat(filepath.Join(luaDir, "off")); err == nil {
		t.Error("repair activated an app that had no entry")
	}
}
