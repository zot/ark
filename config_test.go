package ark

// CRC: crc-Config.md | Test: test-Config.md

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadValidConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ark.toml")
	content := `dotfiles = true

include = ["*.md", "*.txt"]
exclude = [".git/", ".env"]

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

func TestPerSourcePatternsAdditive(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ark.toml")
	content := `dotfiles = true
include = ["*.md"]
exclude = []

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
	if len(inc) != 2 {
		t.Fatalf("expected 2 include patterns, got %d: %v", len(inc), inc)
	}
	found := map[string]bool{}
	for _, p := range inc {
		found[p] = true
	}
	if !found["*.md"] || !found["*.txt"] {
		t.Errorf("expected [*.md, *.txt], got %v", inc)
	}
}

func TestIdenticalIncludeExcludeIsError(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ark.toml")
	content := `dotfiles = true
include = ["*.md"]
exclude = ["*.md"]
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
	for _, e := range cfg.GlobalExclude {
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
include = ["*.md"]
exclude = [".git/"]

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
	for _, p := range cfg2.Sources[0].Include {
		if p == "*.org" {
			found = true
		}
	}
	if !found {
		t.Errorf("per-source include missing *.org after round-trip, got %v", cfg2.Sources[0].Include)
	}
}

// Test: test-Config.md — global add-include round-trip
func TestAddIncludeGlobalRoundTrip(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ark.toml")
	content := `dotfiles = true
include = ["*.md"]
exclude = [".git/"]
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
	for _, p := range cfg2.GlobalInclude {
		if p == "*.txt" {
			found = true
		}
	}
	if !found {
		t.Errorf("global include missing *.txt after round-trip, got %v", cfg2.GlobalInclude)
	}
}

func TestMissingSourceDirNotError(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ark.toml")
	content := `dotfiles = true
include = []
exclude = []

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
