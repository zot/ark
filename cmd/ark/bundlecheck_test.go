// CRC: crc-CLI.md | Test: test-AppActivation.md
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bundleDir builds a bundle source with apps a and b, each shipping viewdefs.
func bundleDir(t *testing.T, aDefs, bDefs []string) string {
	t.Helper()
	dir := t.TempDir()
	for app, defs := range map[string][]string{"a": aDefs, "b": bDefs} {
		vd := filepath.Join(dir, "apps", app, "viewdefs")
		os.MkdirAll(vd, 0o755)
		for _, d := range defs {
			os.WriteFile(filepath.Join(vd, d), []byte("<template/>"), 0o644)
		}
	}
	return dir
}

// Test: a clean bundle source passes (R3518, R3519)
func TestCheckBundleSourceClean(t *testing.T) {
	if err := checkBundleSource(bundleDir(t, []string{"A.DEFAULT.html"}, []string{"B.DEFAULT.html"})); err != nil {
		t.Errorf("clean source refused: %v", err)
	}
}

// Test: a symlink in the bundle source is refused, named (R3518)
func TestCheckBundleSourceRefusesSymlink(t *testing.T) {
	dir := bundleDir(t, []string{"A.DEFAULT.html"}, nil)
	os.MkdirAll(filepath.Join(dir, "lua"), 0o755)
	if err := os.Symlink("../apps/a", filepath.Join(dir, "lua", "a")); err != nil {
		t.Skipf("cannot create symlinks: %v", err)
	}
	err := checkBundleSource(dir)
	if err == nil || !strings.Contains(err.Error(), filepath.Join("lua", "a")) {
		t.Errorf("symlink not refused by name: %v", err)
	}
}

// Test: two built-in apps shipping one viewdef name are refused, named (R3519)
func TestCheckBundleSourceRefusesViewdefCollision(t *testing.T) {
	err := checkBundleSource(bundleDir(t, []string{"T.DEFAULT.html"}, []string{"T.DEFAULT.html"}))
	if err == nil || !strings.Contains(err.Error(), "T.DEFAULT.html") || !strings.Contains(err.Error(), "a, b") {
		t.Errorf("collision not refused with names: %v", err)
	}
}
