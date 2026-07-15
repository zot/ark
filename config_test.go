package ark

// CRC: crc-Config.md | Test: test-Config.md

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEnsureLuhmannSource verifies ark auto-indexes the orchestrator's own
// session by adding its Claude Code project directory as a chat-jsonl source,
// idempotently, with no user config (R3135).
func TestEnsureLuhmannSource(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	want := claudeProjectDir(luhmannCwd())
	if want == "" {
		t.Fatal("claudeProjectDir(luhmannCwd()) returned empty")
	}
	c := &Config{}
	c.EnsureLuhmannSource()
	found := false
	for _, s := range c.Sources {
		if s.Dir == want {
			found = true
			if len(s.Include.Replace) != 1 || s.Include.Replace[0] != "*.jsonl" {
				t.Errorf("include = %v, want [*.jsonl]", s.Include.Replace)
			}
		}
	}
	if !found {
		t.Fatalf("EnsureLuhmannSource added no source for %s", want)
	}
	// Idempotent: a second call must not duplicate.
	n := len(c.Sources)
	c.EnsureLuhmannSource()
	if len(c.Sources) != n {
		t.Errorf("second EnsureLuhmannSource duplicated: %d → %d", n, len(c.Sources))
	}
}

func TestLoadValidConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ark.toml")
	content := `dotfiles = true

default_include = ["*.md", "*.txt"]
default_exclude = [".git/", ".env"]

[[source]]
dir = "` + dir + `"
strategies = {"*.txt" = "lines"}
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Dotfiles {
		t.Error("dotfiles should be true")
	}
	if len(cfg.Sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(cfg.Sources))
	}
	if cfg.Sources[0].Strategies["*.txt"] != "lines" {
		t.Errorf("expected per-source strategy for *.txt = 'lines', got %v", cfg.Sources[0].Strategies)
	}
	if cfg.HasErrors() {
		t.Errorf("should have no errors, got %v", cfg.Errors)
	}
}

// R2146: per-source include.add extends default_include
func TestPerSourceIncludeAddExtendsDefault(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ark.toml")
	content := `dotfiles = true
default_include = ["*.md", "*.go"]

[[source]]
dir = "` + dir + `"
include.add = ["*.lua"]
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	inc, _ := cfg.EffectivePatterns(cfg.Sources[0])
	if len(inc) != 3 {
		t.Fatalf("expected 3 include patterns (default + add), got %d: %v", len(inc), inc)
	}
	want := map[string]bool{"*.md": true, "*.go": true, "*.lua": true}
	for _, p := range inc {
		if !want[p] {
			t.Errorf("unexpected pattern %q in result", p)
		}
		delete(want, p)
	}
	if len(want) != 0 {
		t.Errorf("missing patterns: %v", want)
	}
}

// R2146: per-source exclude.add extends default_exclude
func TestPerSourceExcludeAddExtendsDefault(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ark.toml")
	content := `dotfiles = true
default_include = ["*.md"]
default_exclude = [".git/"]

[[source]]
dir = "` + dir + `"
exclude.add = ["drafts/"]
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	_, exc := cfg.EffectivePatterns(cfg.Sources[0])
	if len(exc) != 2 {
		t.Fatalf("expected 2 exclude patterns (default + add), got %d: %v", len(exc), exc)
	}
}

// R2143, R2144: per-source include replaces default_include
func TestPerSourceIncludeReplacesDefault(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ark.toml")
	content := `dotfiles = true
default_include = ["*.md", "*.go"]
default_exclude = []

[[source]]
dir = "` + dir + `"

include = ["*.txt"]
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	inc, _ := cfg.EffectivePatterns(cfg.Sources[0])
	if len(inc) != 1 || inc[0] != "*.txt" {
		t.Errorf("expected per-source include to replace default; got %v", inc)
	}
}

// R2144: when source omits include, default_include applies
func TestPerSourceOmittedInheritsDefault(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ark.toml")
	content := `dotfiles = true
default_include = ["*.md", "*.go"]

[[source]]
dir = "` + dir + `"
exclude = ["drafts/"]
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	inc, exc := cfg.EffectivePatterns(cfg.Sources[0])
	if len(inc) != 2 {
		t.Errorf("expected default_include to apply; got %v", inc)
	}
	if len(exc) != 1 || exc[0] != "drafts/" {
		t.Errorf("expected per-source exclude to replace default_exclude; got %v", exc)
	}
}

func TestIdenticalIncludeExcludeIsError(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ark.toml")
	content := `dotfiles = true
default_include = ["*.md"]
default_exclude = ["*.md"]
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HasErrors() {
		t.Error("should have validation error for identical include/exclude")
	}
}

func TestWriteDefaultConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ark.toml")
	if err := WriteDefaultConfig(configPath, nil); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Dotfiles {
		t.Error("default config should have dotfiles=true")
	}
	excSet := map[string]bool{}
	for _, e := range cfg.DefaultExclude {
		excSet[e] = true
	}
	if !excSet[".git/"] {
		t.Error("default excludes should contain .git/")
	}
	if !excSet[".env"] {
		t.Error("default excludes should contain .env")
	}
}

// Test: test-Config.md — add-include per-source round-trip
func TestAddIncludePerSourceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ark.toml")
	content := `dotfiles = true
default_include = ["*.md"]
default_exclude = [".git/"]

[[source]]
dir = "` + dir + `"

`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := cfg.AddInclude("*.org", dir); err != nil {
		t.Fatalf("AddInclude: %v", err)
	}
	if err := cfg.SaveConfig(configPath); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	cfg2, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(cfg2.Sources) == 0 {
		t.Fatal("no sources after round-trip")
	}
	found := false
	for _, p := range cfg2.Sources[0].Include.Replace {
		if p == "*.org" {
			found = true
		}
	}
	if !found {
		t.Errorf("per-source include missing *.org after round-trip, got %v", cfg2.Sources[0].Include.Replace)
	}
}

// Test: test-Config.md — default add-include round-trip
func TestAddIncludeDefaultRoundTrip(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ark.toml")
	content := `dotfiles = true
default_include = ["*.md"]
default_exclude = [".git/"]
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddInclude("*.txt", ""); err != nil {
		t.Fatalf("AddInclude: %v", err)
	}
	if err := cfg.SaveConfig(configPath); err != nil {
		t.Fatal(err)
	}

	cfg2, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range cfg2.DefaultInclude {
		if p == "*.txt" {
			found = true
		}
	}
	if !found {
		t.Errorf("global include missing *.txt after round-trip, got %v", cfg2.DefaultInclude)
	}
}

func TestMissingSourceDirNotError(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ark.toml")
	content := `dotfiles = true
default_include = []
default_exclude = []

[[source]]
dir = "/nonexistent/path/that/will/never/exist"

`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HasErrors() {
		t.Errorf("should load without errors, got %v", cfg.Errors)
	}
}
