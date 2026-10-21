package main

// CRC: crc-Updater.md | Test: test-Updater.md | R3471, R3472, R3474, R3476, R3477, R3478, R3479, R3484, R3547, R3548
//
// Update's decision logic against a fake install root and a fake release source
// (no network). The whole class here is invisible when violated: a refresh that
// replaces a link with a copy, a downgrade, and a failed swap that ate the old
// binary all leave a green happy path while breaking the guarantee.

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/zot/ark"
)

// fakeFetcher is an in-memory ReleaseFetcher.
type fakeFetcher struct {
	version   string
	binary    []byte
	sum       string
	downloads int
	asked     []string // every asset name Download and Checksum were asked for
}

func (f *fakeFetcher) Latest() (string, error) { return f.version, nil }
func (f *fakeFetcher) Download(name string) ([]byte, error) {
	f.downloads++
	f.asked = append(f.asked, name)
	return f.binary, nil
}
func (f *fakeFetcher) Checksum(name string) (string, error) {
	f.asked = append(f.asked, name)
	return f.sum, nil
}

// fakeAssets is an in-memory AssetSource.
type fakeAssets struct{ files map[string][]byte }

func (a fakeAssets) List() ([]BundleAsset, error) {
	var out []BundleAsset
	for name := range a.files {
		out = append(out, BundleAsset{Name: name, Mode: fs.FileMode(0644)})
	}
	return out, nil
}
func (a fakeAssets) Read(name string) ([]byte, error) { return a.files[name], nil }

// TestRefreshNeverReplacesLinkWithCopy: a directory link under ~/.ark survives a
// manifest-aware refresh untouched. R3474, R3478
func TestRefreshNeverReplacesLinkWithCopy(t *testing.T) {
	arkRoot := t.TempDir()
	// skills/ui is a directory link into a real target dir.
	target := filepath.Join(arkRoot, "real-ui")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(arkRoot, "skills"), 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(arkRoot, "skills", "ui")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	u := &Updater{ArkDir: arkRoot, Out: &bytes.Buffer{}}
	assets := fakeAssets{files: map[string][]byte{"skills/ui/SKILL.md": []byte("new ui skill")}}
	if err := u.RefreshAssets(assets); err != nil {
		t.Fatalf("RefreshAssets: %v", err)
	}

	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("skills/ui vanished: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("skills/ui was replaced by a copied directory")
	}
}

// TestRefreshRecordsIdenticalWithoutRewrite: a file absent from the manifest
// but byte-identical to the bundle is recorded during a refresh (seeding an
// install that predates the manifest) without being rewritten. R3484, R3479
func TestRefreshRecordsIdenticalWithoutRewrite(t *testing.T) {
	arkRoot := t.TempDir()
	dir := filepath.Join(arkRoot, "skills", "ark")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "SKILL.md")
	content := []byte("identical bundle bytes")
	if err := os.WriteFile(dest, content, 0644); err != nil {
		t.Fatal(err)
	}
	// Backdate the mtime so a rewrite would be detectable.
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(dest, old, old); err != nil {
		t.Fatal(err)
	}

	u := &Updater{ArkDir: arkRoot, Out: &bytes.Buffer{}}
	assets := fakeAssets{files: map[string][]byte{"skills/ark/SKILL.md": content}}
	if err := u.RefreshAssets(assets); err != nil {
		t.Fatalf("RefreshAssets: %v", err)
	}

	m, err := ark.LoadManifest(filepath.Join(arkRoot, ark.ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := m.Files["skills/ark/SKILL.md"]
	if !ok {
		t.Fatal("identical file was not recorded in the manifest")
	}
	if entry.Hash != ark.HashContent(content) {
		t.Fatalf("recorded hash = %s, want %s", entry.Hash, ark.HashContent(content))
	}
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.ModTime().Equal(old) {
		t.Fatal("identical file was rewritten (mtime moved) — it should only be recorded")
	}
}

// TestNoDowngradeNoOpWhenCurrent: update does nothing when the running version
// is >= the latest release tag. R3471
func TestNoDowngradeNoOpWhenCurrent(t *testing.T) {
	arkRoot := t.TempDir()
	for _, latest := range []string{"0.5.0", "0.4.0"} {
		fetch := &fakeFetcher{version: latest, binary: []byte("new"), sum: ark.HashContent([]byte("new"))}
		u := &Updater{
			ArkDir:  arkRoot,
			Version: "0.5.0",
			Fetcher: fetch,
			Out:     &bytes.Buffer{},
			ExecNew: func(string) error { return nil },
		}
		if err := u.Update(false); err != nil {
			t.Fatalf("Update(latest=%s): %v", latest, err)
		}
		if fetch.downloads != 0 {
			t.Fatalf("latest=%s: downloaded %d times, want 0 (no downgrade)", latest, fetch.downloads)
		}
	}
}

// TestFailedSwapLeavesOldBinaryIntact: a checksum mismatch leaves the old binary
// and ~/.ark intact. R3476
func TestFailedSwapLeavesOldBinaryIntact(t *testing.T) {
	arkRoot := t.TempDir()
	oldBytes := []byte("the original binary")
	exe := filepath.Join(arkRoot, arkExeName())
	if err := os.WriteFile(exe, oldBytes, 0755); err != nil {
		t.Fatal(err)
	}

	// Checksum does not match the downloaded bytes → error before the swap.
	fetch := &fakeFetcher{version: "0.6.0", binary: []byte("corrupt download"), sum: ark.HashContent([]byte("different"))}
	u := &Updater{
		ArkDir:  arkRoot,
		Version: "0.5.0",
		Fetcher: fetch,
		Out:     &bytes.Buffer{},
		ExecNew: func(string) error { return nil },
	}
	if err := u.Update(false); err == nil {
		t.Fatal("Update should fail on a checksum mismatch")
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("old binary gone: %v", err)
	}
	if !bytes.Equal(got, oldBytes) {
		t.Fatal("old binary bytes were overwritten by a failed update")
	}
}

// TestSymlinkedBinaryGripesAndRefreshesNothing: plain update refreshes nothing
// on a symlinked binary; --no-exe proceeds. R3477, R3478
func TestSymlinkedBinaryGripesAndRefreshesNothing(t *testing.T) {
	arkRoot := t.TempDir()
	// ~/.ark/ark is a symlink into a source tree.
	src := filepath.Join(arkRoot, "built-ark")
	if err := os.WriteFile(src, []byte("dev binary"), 0755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(arkRoot, arkExeName())
	if err := os.Symlink(src, exe); err != nil {
		t.Fatal(err)
	}

	fetch := &fakeFetcher{version: "9.9.9", binary: []byte("new"), sum: ark.HashContent([]byte("new"))}
	assets := fakeAssets{files: map[string][]byte{"skills/x/SKILL.md": []byte("refreshed")}}
	var out bytes.Buffer
	u := &Updater{
		ArkDir:  arkRoot,
		Version: "0.5.0",
		Fetcher: fetch,
		Assets:  assets,
		Out:     &out,
		ExecNew: func(string) error { return nil },
	}

	// Plain update: gripes, no download, assets untouched.
	if err := u.Update(false); err != nil {
		t.Fatalf("plain update on symlink: %v", err)
	}
	if fetch.downloads != 0 {
		t.Fatal("plain update on a symlink downloaded a binary")
	}
	if _, err := os.Stat(filepath.Join(arkRoot, "skills", "x", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("plain update on a symlink refreshed assets")
	}

	// --no-exe: refreshes assets, leaves the symlink alone.
	if err := u.Update(true); err != nil {
		t.Fatalf("update --no-exe on symlink: %v", err)
	}
	if _, err := os.Stat(filepath.Join(arkRoot, "skills", "x", "SKILL.md")); err != nil {
		t.Fatalf("--no-exe did not refresh assets: %v", err)
	}
	fi, err := os.Lstat(exe)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("--no-exe replaced the symlinked binary")
	}
}

// TestRefreshActivatesOnlyNewApps: a refresh activates an app it extracts for
// the first time and leaves an existing, deactivated app deactivated. R3517
func TestRefreshActivatesOnlyNewApps(t *testing.T) {
	arkRoot := t.TempDir()
	// "old" is installed but the user deactivated it (no lua/old).
	if err := os.MkdirAll(filepath.Join(arkRoot, "apps", "old"), 0755); err != nil {
		t.Fatal(err)
	}
	src := fakeAssets{files: map[string][]byte{
		"apps/old/app.lua": []byte("-- old"),
		"apps/new/app.lua": []byte("-- new"),
	}}
	u := &Updater{ArkDir: arkRoot, Out: &bytes.Buffer{}}
	if err := u.RefreshAssets(src); err != nil {
		t.Fatal(err)
	}
	if !ark.IsOurLink(filepath.Join(arkRoot, "lua", "new"), filepath.Join(arkRoot, "apps", "new")) {
		t.Error("newly extracted app was not activated")
	}
	if _, err := os.Lstat(filepath.Join(arkRoot, "lua", "old")); err == nil {
		t.Error("refresh re-activated an app the user had deactivated")
	}
}

// Test: release asset names come from one function. R3547
func TestReleaseAssetNameOneFunction(t *testing.T) {
	cases := []struct{ version, goos, goarch, want string }{
		{"v0.6.0", "linux", "amd64", "ark-0.6.0-linux-amd64"},
		{"0.6.0", "windows", "amd64", "ark-0.6.0-windows-amd64.exe"},
		{"0.6.0", "darwin", "arm64", "ark-0.6.0-darwin-arm64"},
	}
	for _, c := range cases {
		if got := releaseAssetName(c.version, c.goos, c.goarch); got != c.want {
			t.Errorf("releaseAssetName(%q, %q, %q) = %q, want %q", c.version, c.goos, c.goarch, got, c.want)
		}
	}
	// The command's terse answer for the running platform is the name Update fetches.
	var out bytes.Buffer
	if err := releaseAssetReport(&out, "0.6.0", runtime.GOOS, runtime.GOARCH, true); err != nil {
		t.Fatal(err)
	}
	if want := releaseAssetName("0.6.0", runtime.GOOS, runtime.GOARCH) + "\n"; out.String() != want {
		t.Errorf("terse report = %q, want the name update downloads %q", out.String(), want)
	}
	out.Reset()
	if err := releaseAssetReport(&out, "0.6.0", "windows", "amd64", true); err != nil {
		t.Fatal(err)
	}
	if out.String() != "ark-0.6.0-windows-amd64.exe\n" {
		t.Errorf("terse windows report = %q, want ark-0.6.0-windows-amd64.exe", out.String())
	}
}

// Test: release asset report is baby food unless --terse. R3548
func TestReleaseAssetReportBabyFood(t *testing.T) {
	var out bytes.Buffer
	if err := releaseAssetReport(&out, "v0.6.0", "windows", "amd64", false); err != nil {
		t.Fatal(err)
	}
	want := "- asset: ark-0.6.0-windows-amd64.exe\n- version: 0.6.0\n- platform: windows/amd64\n"
	if out.String() != want {
		t.Errorf("default report =\n%s\nwant\n%s", out.String(), want)
	}
}

// Test: update fetches the running platform's release asset. The release names
// its binaries with `ark _release-asset`; this pins the other end, so the name
// Update asks for (binary and checksum both) is that same function's answer for
// the platform it runs on. R3472, R3547
func TestUpdateFetchesRunningPlatformAsset(t *testing.T) {
	fetch := &fakeFetcher{version: "v0.6.0", binary: []byte("new"), sum: ark.HashContent([]byte("new"))}
	u := &Updater{ArkDir: t.TempDir(), Version: "0.5.0", Fetcher: fetch, Out: &bytes.Buffer{}}
	if _, err := u.fetchVerified(fetch.version); err != nil {
		t.Fatal(err)
	}
	want := releaseAssetName("0.6.0", runtime.GOOS, runtime.GOARCH)
	if len(fetch.asked) != 2 || fetch.asked[0] != want || fetch.asked[1] != want {
		t.Errorf("fetch asked for %q, want binary and checksum both %q", fetch.asked, want)
	}
}
