// CRC: crc-AppActivation.md | Test: test-AppActivation.md
package ark

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// mkCollidingApps makes active apps aa and zz under arkDir, both shipping
// T.DEFAULT.html.
func mkCollidingApps(t *testing.T, arkDir string) {
	t.Helper()
	for _, name := range []string{"aa", "zz"} {
		vd := filepath.Join(arkDir, "apps", name, "viewdefs")
		if err := os.MkdirAll(vd, 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(vd, "T.DEFAULT.html"), []byte("<template>"+name+"</template>"), 0o644)
		if err := ActivateApp(arkDir, name); err != nil {
			t.Fatal(err)
		}
	}
}

// Test: conflicts computed from disk name the viewdef, winner and loser (R3525)
func TestViewdefConflictsFromDisk(t *testing.T) {
	arkDir := t.TempDir()
	mkCollidingApps(t, arkDir)
	got := ViewdefConflicts(arkDir)
	if len(got) != 1 || got[0].Key != "T.DEFAULT" || got[0].Winner != "aa" || got[0].Loser != "zz" {
		t.Errorf("ViewdefConflicts = %+v, want T.DEFAULT, aa over zz", got)
	}
}

// Test: the server answers /ui/conflicts from disk when its UI is not running (R3524)
func TestUIConflictsRouteWithoutUI(t *testing.T) {
	_, db := setupRecall(t)
	mkCollidingApps(t, db.Path())
	srv := &Server{db: db}
	rec := httptest.NewRecorder()
	srv.handleUIConflicts(rec, httptest.NewRequest(http.MethodGet, "/ui/conflicts", nil))
	var got []struct{ Key, Winner, Loser string }
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response %q: %v", rec.Body.String(), err)
	}
	if len(got) != 1 || got[0].Winner != "aa" || got[0].Loser != "zz" {
		t.Errorf("GET /ui/conflicts = %+v, want T.DEFAULT, aa over zz", got)
	}
}
