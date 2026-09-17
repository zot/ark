package ark

// CRC: test-PidPath.md | R3417, R3418

import (
	"path/filepath"
	"testing"
)

// TestPidFilePathInHomeDirIgnoresTMPDIR pins R3417: the PID file lives in the
// ark home directory (beside ark.sock/index.db), not os.TempDir(). The bug this
// guards: the old path honored TMPDIR, so a server and an `ark stop` run under
// different TMPDIRs disagreed on the location. Setting TMPDIR must not move it.
func TestPidFilePathInHomeDirIgnoresTMPDIR(t *testing.T) {
	home := t.TempDir()
	want := filepath.Join(home, PidFileName)

	if got := PidFilePath(home); got != want {
		t.Fatalf("PidFilePath(%q) = %q, want %q", home, got, want)
	}

	// TMPDIR must not affect the ark-home PID path.
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "elsewhere"))
	if got := PidFilePath(home); got != want {
		t.Errorf("PidFilePath moved with TMPDIR: got %q, want %q", got, want)
	}
}

// TestLegacyPidFilePathIsTempDerived pins R3418: the fallback path is the old
// TMPDIR-derived flat name, so `ark stop` can still find a server started by
// the pre-move binary.
func TestLegacyPidFilePathIsTempDerived(t *testing.T) {
	home := t.TempDir()
	legacy := LegacyPidFilePath(home)
	if filepath.Dir(legacy) == home {
		t.Errorf("LegacyPidFilePath should live in the temp dir, not the home dir: %q", legacy)
	}
	if filepath.Base(legacy) == PidFileName {
		t.Errorf("legacy path should be the flat TMPDIR-derived name, not %s", PidFileName)
	}
}
