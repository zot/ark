package main

// CRC: crc-CLI.md | Test: test-Setup.md | R3456, R3461, R3462, R3465, R3466, R3469, R3470, R3481, R3484
//
// Setup's decision logic against a fake install root (a temp ~/.ark and a temp
// ~/.claude) — no live home, no server. The marker check and the byte-identical
// retire guard are the invisible-when-violated properties.

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/zot/ark"
)

// seedInstall creates a fake ~/.ark with skills and agents, optionally with the
// database marker present.
func seedInstall(t *testing.T, withMarker bool) string {
	t.Helper()
	dir := t.TempDir()
	for _, skill := range []string{"ark", "ui", "ui-basics"} {
		sd := filepath.Join(dir, "skills", skill)
		if err := os.MkdirAll(sd, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sd, "SKILL.md"), []byte("skill "+skill), 0644); err != nil {
			t.Fatal(err)
		}
	}
	agents := filepath.Join(dir, "agents")
	if err := os.MkdirAll(agents, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agents, "ark-hermes.md"), []byte("agent hermes"), 0644); err != nil {
		t.Fatal(err)
	}
	if withMarker {
		if err := os.WriteFile(filepath.Join(dir, ark.IndexFileName), []byte("db"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestGlobalSetupNoOpWhenMarkerPresent: with the marker present, the global
// install does nothing. R3456 (marker test carried by runGlobalSetup).
func TestGlobalSetupNoOpWhenMarkerPresent(t *testing.T) {
	arkRoot := seedInstall(t, true)
	withArkDir(t, arkRoot)

	// A pre-existing ~/.claude sentinel that must survive untouched.
	claude := t.TempDir()
	sentinel := filepath.Join(claude, "skills", "sentinel")
	if err := os.MkdirAll(sentinel, 0755); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(sentinel)
	if err != nil {
		t.Fatal(err)
	}

	if err := runGlobalSetup(arkRoot); err != nil {
		t.Fatalf("runGlobalSetup should no-op with marker present: %v", err)
	}

	after, err := os.Stat(sentinel)
	if err != nil {
		t.Fatalf("sentinel touched: %v", err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("marker-present setup mutated ~/.claude")
	}
}

// TestInstallLinksAreLinksNotCopies: every skill/agent under .claude is a
// directory link, never a copied file. R3465, R3461, R3462
func TestInstallLinksAreLinksNotCopies(t *testing.T) {
	arkRoot := seedInstall(t, false)
	withArkDir(t, arkRoot)
	claude := t.TempDir()

	if err := installLinks(claude); err != nil {
		t.Fatalf("installLinks: %v", err)
	}

	// Every bundled skill directory is a link into ~/.ark/skills.
	for _, skill := range []string{"ark", "ui", "ui-basics"} {
		link := filepath.Join(claude, "skills", skill)
		fi, err := os.Lstat(link)
		if err != nil {
			t.Fatalf("skill link %s missing: %v", skill, err)
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("skill %s is not a link (a copy was written)", skill)
		}
		if !ark.IsOurLink(link, filepath.Join(arkRoot, "skills", skill)) {
			t.Fatalf("skill %s link does not resolve into ~/.ark", skill)
		}
	}
	// agents/ark is a single link carrying the whole set.
	agentsLink := filepath.Join(claude, "agents", "ark")
	fi, err := os.Lstat(agentsLink)
	if err != nil {
		t.Fatalf("agents link missing: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("agents/ark is not a link")
	}
	if !ark.IsOurLink(agentsLink, filepath.Join(arkRoot, "agents")) {
		t.Fatal("agents/ark link does not resolve into ~/.ark/agents")
	}
}

// TestRetireOldAgentCopies: a flat agents/<name>.md is removed only when it is
// a regular file identical to the bundle; a differing file or a link is left.
// R3469, R3470
func TestRetireOldAgentCopies(t *testing.T) {
	arkRoot := t.TempDir()
	withArkDir(t, arkRoot)
	agents := filepath.Join(arkRoot, "agents")
	if err := os.MkdirAll(agents, 0755); err != nil {
		t.Fatal(err)
	}
	// Bundled agents.
	bundled := map[string]string{
		"same.md":   "identical bytes",
		"edited.md": "bundled bytes",
		"linked.md": "bundled bytes",
	}
	for name, body := range bundled {
		if err := os.WriteFile(filepath.Join(agents, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}

	claude := t.TempDir()
	claudeAgents := filepath.Join(claude, "agents")
	if err := os.MkdirAll(claudeAgents, 0755); err != nil {
		t.Fatal(err)
	}
	// (a) identical copy → removed.
	os.WriteFile(filepath.Join(claudeAgents, "same.md"), []byte("identical bytes"), 0644)
	// (b) edited copy → left.
	os.WriteFile(filepath.Join(claudeAgents, "edited.md"), []byte("user edited these bytes"), 0644)
	// (c) hand-made symlink → left.
	linkTarget := filepath.Join(claude, "elsewhere.md")
	os.WriteFile(linkTarget, []byte("user's own"), 0644)
	if err := os.Symlink(linkTarget, filepath.Join(claudeAgents, "linked.md")); err != nil {
		t.Fatal(err)
	}

	retireOldAgentCopies(claude)

	if _, err := os.Stat(filepath.Join(claudeAgents, "same.md")); !os.IsNotExist(err) {
		t.Fatal("identical copy should have been retired")
	}
	if _, err := os.Stat(filepath.Join(claudeAgents, "edited.md")); err != nil {
		t.Fatal("edited copy should have been left in place")
	}
	fi, err := os.Lstat(filepath.Join(claudeAgents, "linked.md"))
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("hand-made symlink should have been left in place")
	}
}

// TestSeedManifestRecordsEachAsset: setup's manifest seeding records an entry
// per extracted refreshable asset with the right hash, and skips non-assets.
// R3484
func TestSeedManifestRecordsEachAsset(t *testing.T) {
	arkRoot := t.TempDir()
	assets := fakeAssets{files: map[string][]byte{
		"skills/ark/SKILL.md":  []byte("ark skill body"),
		"agents/ark-hermes.md": []byte("hermes agent body"),
		"html/index.html":      []byte("<html></html>"),
		"notes/scratch.txt":    []byte("not a refreshable root"),
	}}
	if err := SeedManifest(arkRoot, assets); err != nil {
		t.Fatalf("SeedManifest: %v", err)
	}
	m, err := ark.LoadManifest(filepath.Join(arkRoot, ark.ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range assets.files {
		entry, ok := m.Files[name]
		if !isRefreshableAsset(name) {
			if ok {
				t.Errorf("recorded a non-refreshable path: %s", name)
			}
			continue
		}
		if !ok {
			t.Fatalf("manifest missing entry for %s", name)
		}
		if entry.Hash != ark.HashContent(content) {
			t.Errorf("%s hash = %s, want %s", name, entry.Hash, ark.HashContent(content))
		}
	}
}

// TestStopServerForInstallAborts: a graceful stop that does not complete makes
// the install abort (returns an error) rather than continuing. R3481
func TestStopServerForInstallAborts(t *testing.T) {
	arkRoot := t.TempDir()
	withArkDir(t, arkRoot)

	// A live socket that accepts connections but never "releases" — the
	// server that will not stop.
	ln, err := net.Listen("unix", filepath.Join(arkRoot, "ark.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	if err := stopServerForInstall(arkRoot); err == nil {
		t.Fatal("stopServerForInstall should return an error when the server will not stop")
	}
}

// TestSetupLocalRefusesWithoutMarker: --local requires an existing install and
// creates no links on a bare machine. R3466
func TestSetupLocalRefusesWithoutMarker(t *testing.T) {
	arkRoot := seedInstall(t, false) // no marker
	withArkDir(t, arkRoot)
	cwd := t.TempDir()
	t.Chdir(cwd)

	if err := cmdSetupLocal(); err == nil {
		t.Fatal("cmdSetupLocal should refuse without the marker")
	}
	if _, err := os.Stat(filepath.Join(cwd, ".claude")); !os.IsNotExist(err) {
		t.Fatal("--local created .claude despite refusing")
	}
}
