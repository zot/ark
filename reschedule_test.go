package ark

// CRC: crc-Server.md | Seq: seq-scheduling.md | R894

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRescheduleTag_DryRunPreservesDescription verifies the date rewrite keeps
// the trailing description text and applies the new start (and ..end), without
// writing when dryRun is set. R894
func TestRescheduleTag_DryRunPreservesDescription(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.md")
	if err := os.WriteFile(path, []byte("@meeting: 2026-03-01 standup with team  \n\nbody\n"), 0644); err != nil {
		t.Fatal(err)
	}
	srv := &Server{}

	// Single new date.
	res, err := srv.rescheduleTag(path, "meeting", "2026-04-01", "", true)
	if err != nil {
		t.Fatalf("rescheduleTag: %v", err)
	}
	if res.Old != "2026-03-01 standup with team" {
		t.Errorf("old = %q, want the unchanged original value", res.Old)
	}
	if res.New != "2026-04-01 standup with team" {
		t.Errorf("new = %q, want date swapped + description preserved", res.New)
	}

	// Date range (start..end).
	res, err = srv.rescheduleTag(path, "meeting", "2026-04-01", "2026-04-02", true)
	if err != nil {
		t.Fatalf("rescheduleTag range: %v", err)
	}
	if res.New != "2026-04-01..2026-04-02 standup with team" {
		t.Errorf("range new = %q, want start..end + description", res.New)
	}

	// dryRun must not have written the file.
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "2026-03-01") {
		t.Errorf("dryRun mutated the file: %s", data)
	}
}

// TestRescheduleTag_TagNotFound returns the sentinel when the tag is absent so
// the HTTP handler can map it to 404 and the Lua binding to an error. R925
func TestRescheduleTag_TagNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.md")
	if err := os.WriteFile(path, []byte("@other: 2026-03-01 x  \n\nbody\n"), 0644); err != nil {
		t.Fatal(err)
	}
	srv := &Server{}
	if _, err := srv.rescheduleTag(path, "meeting", "2026-04-01", "", true); !errors.Is(err, errScheduleTagNotFound) {
		t.Errorf("err = %v, want errScheduleTagNotFound", err)
	}
}
