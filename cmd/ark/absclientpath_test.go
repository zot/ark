package main

// CRC: crc-CLI.md | Test: test-AbsClientPath.md | R3403

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAbsClientPathRelativeResolvesAgainstCwd is the fetch case that proved
// the bug: a relative path argument must absolutize against the client's own
// working directory, never the server's home. Before the fix the relative
// path crossed the socket unchanged and the server joined it to `~`,
// producing `404 not indexed: /home/deck/requests/foo.md`.
// R3403
func TestAbsClientPathRelativeResolvesAgainstCwd(t *testing.T) {
	dir := t.TempDir() // a non-home cwd
	t.Chdir(dir)

	// t.TempDir may hand back a symlinked path (/tmp → …); resolve both
	// sides through the same os.Getwd the helper's filepath.Abs uses.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	const rel = "requests/pending-reader-defects.md"
	got := absClientPath(rel)

	if !filepath.IsAbs(got) {
		t.Fatalf("absClientPath(%q) = %q, want an absolute path", rel, got)
	}
	want := filepath.Join(cwd, rel)
	if got != want {
		t.Errorf("absClientPath(%q) = %q, want %q (resolved against the client cwd)", rel, got, want)
	}

	// The reported defect precisely: the server joined the relative path to
	// its own home. Whatever HOME is, the result must not be that join.
	if home, err := os.UserHomeDir(); err == nil {
		if bad := filepath.Join(home, rel); got == bad {
			t.Errorf("absClientPath(%q) resolved against home %q — the exact 404 bug", rel, bad)
		}
	}
}

// TestAbsClientPathPassThrough covers the two arguments that must never be
// absolutized: a tmp:// virtual key and the empty string (which filepath.Abs
// would turn into the cwd).
// R3403
func TestAbsClientPathPassThrough(t *testing.T) {
	// Run from a non-home cwd so a mistaken Abs would visibly change these.
	t.Chdir(t.TempDir())

	cases := []string{
		"tmp://luhmann/launch.md",
		"",
	}
	for _, in := range cases {
		if got := absClientPath(in); got != in {
			t.Errorf("absClientPath(%q) = %q, want unchanged", in, got)
		}
	}
}

// TestAbsClientPathIdempotent confirms an already-absolute path is returned
// unchanged, so the local-dispatch arm (which passes absolute paths) is
// unaffected.
// R3403
func TestAbsClientPathIdempotent(t *testing.T) {
	t.Chdir(t.TempDir())

	abs := filepath.Join(t.TempDir(), "requests", "foo.md")
	if got := absClientPath(abs); got != abs {
		t.Errorf("absClientPath(%q) = %q, want unchanged", abs, got)
	}
}
