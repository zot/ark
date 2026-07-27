package main

// CRC: crc-CLITree.md | Test: test-Patterns.md

import (
	"os"
	"path/filepath"
	"testing"
)

// withArkDir points the arkDir global at a temp directory for one test and
// restores it after — the CLI resolves the collection through that global.
func withArkDir(t *testing.T, dir string) {
	t.Helper()
	prev := arkDir
	arkDir = dir
	t.Cleanup(func() { arkDir = prev })
}

// R3268: a configured patterns_dir is adopted in place, tilde expanded.
func TestPatternsCollectionUsesConfiguredDir(t *testing.T) {
	dir := t.TempDir()
	elsewhere := filepath.Join(t.TempDir(), "my-collection")
	cfg := "patterns_dir = " + `"` + elsewhere + `"` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "ark.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	withArkDir(t, dir)

	if got := patternsCollection().Dir(); got != elsewhere {
		t.Errorf("Dir() = %q, want %q", got, elsewhere)
	}
}

// R3268: with an ark.toml that sets no patterns_dir, the default is
// <db>/personal-patterns — which requires LoadConfig to have supplied dbPath.
func TestPatternsCollectionDefaultsUnderArkDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ark.toml"), []byte("dotfiles = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withArkDir(t, dir)

	want := filepath.Join(dir, "personal-patterns")
	if got := patternsCollection().Dir(); got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}
}

// R3271: a fresh install has no ark.toml at all, and `patterns list` must
// still work there rather than failing on the missing config.
func TestPatternsCollectionFallsBackWithoutConfig(t *testing.T) {
	dir := t.TempDir()
	withArkDir(t, dir)

	want := filepath.Join(dir, "personal-patterns")
	if got := patternsCollection().Dir(); got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}
	// And listing that nonexistent collection is silent success, not an error.
	if pats, err := patternsCollection().List(); err != nil || len(pats) != 0 {
		t.Errorf("List() = (%v, %v), want (empty, nil)", pats, err)
	}
}
