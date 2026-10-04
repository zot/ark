package main

// CRC: crc-Updater.md | Seq: seq-install.md#3 | R3463, R3471, R3472, R3473, R3474, R3475, R3476, R3477, R3478, R3479, R3480, R3481, R3482, R3483
//
// `ark update` and the refresh machinery it lends to `ark setup`. The release
// fetch is behind ReleaseFetcher and the bundle behind AssetSource so the
// decision logic (version check, no-downgrade, failed-swap safety, link
// preservation) is testable without a network or a bundled binary.

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/zot/ark"
	cli "github.com/zot/ui-engine/cli"
)

const (
	// releaseAPIURL is the GitHub API for zot/ark's latest release. R3472
	releaseAPIURL = "https://api.github.com/repos/zot/ark/releases/latest"
	// checksumAssetName is the release asset holding sha256sum-format lines. R3472
	checksumAssetName = "SHA256SUMS"
	// arkExeUnix / arkExeWindows are the installed binary names. R3458, R3473
	arkExeUnix    = "ark"
	arkExeWindows = "ark.exe"
)

// refreshableAssetRoots are the top-level bundle directories a refresh touches.
// The exe and the database are never in this set. R3474
var refreshableAssetRoots = []string{"html", "lua", "viewdefs", "apps", "skills", "agents", "install"}

// ReleaseFetcher fetches the latest release metadata and its assets. The
// GitHub implementation is githubReleaseFetcher; tests inject a fake.
// CRC: crc-Updater.md | R3472
type ReleaseFetcher interface {
	Latest() (version string, err error)
	Download(assetName string) ([]byte, error)
	Checksum(assetName string) (sha256hex string, err error)
}

// AssetSource lists and reads bundled assets. bundleAssetSource wraps the
// grafted bundle; tests inject an in-memory fake. R3474
type AssetSource interface {
	List() ([]BundleAsset, error)
	Read(name string) ([]byte, error)
}

// BundleAsset mirrors the bundle's FileInfo for the refresh loop.
type BundleAsset struct {
	Name          string
	IsSymlink     bool
	SymlinkTarget string
	Mode          fs.FileMode
}

// Updater carries the refresh machinery. CRC: crc-Updater.md
type Updater struct {
	ArkDir  string
	Version string
	Fetcher ReleaseFetcher
	Assets  AssetSource
	// ExecNew re-execs the freshly-swapped binary to refresh assets from its
	// own bundle (the second phase). Injected so tests do not replace the
	// process. Nil defaults to the real re-exec.
	ExecNew func(arkExe string) error
	Out     io.Writer
}

func (u *Updater) out() io.Writer {
	if u.Out != nil {
		return u.Out
	}
	return os.Stderr
}

// arkExeName returns the installed binary's name for the current platform.
func arkExeName() string {
	if runtime.GOOS == "windows" {
		return arkExeWindows
	}
	return arkExeUnix
}

// releaseAssetName is the per-platform binary asset name, matching the existing
// ark-0.5.0-linux-amd64 convention. R3472
func releaseAssetName(version string) string {
	name := fmt.Sprintf("ark-%s-%s-%s", strings.TrimPrefix(version, "v"), runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// Update is the top-level flow. CRC: crc-Updater.md | Seq: seq-install.md#3 | R3471, R3472, R3476, R3477, R3478
func (u *Updater) Update(noExe bool) error {
	arkExe := filepath.Join(u.ArkDir, arkExeName())

	// R3477/R3478: a symlinked ~/.ark/ark is a developer install. Plain update
	// gripes and refreshes nothing; --no-exe proceeds normally.
	if !noExe && isSymlink(arkExe) {
		fmt.Fprintln(u.out(), "~/.ark/ark is a symlink (developer install); refusing to swap it.\n"+
			"  run `ark update --no-exe` to refresh assets from the running binary's own bundle.")
		return nil
	}

	if !noExe {
		// R3471: version check — no-op when current, never downgrade.
		latest, err := u.Fetcher.Latest()
		if err != nil {
			return fmt.Errorf("check latest release: %w", err)
		}
		if compareVersions(u.Version, latest) >= 0 {
			fmt.Fprintf(u.out(), "already current (%s >= %s); nothing to do\n", u.Version, latest)
			return nil
		}
		// R3472/R3476: download the new binary and verify before swapping.
		assetName := releaseAssetName(latest)
		newBinary, err := u.Fetcher.Download(assetName)
		if err != nil {
			return fmt.Errorf("download %s: %w", assetName, err)
		}
		wantSum, err := u.Fetcher.Checksum(assetName)
		if err != nil {
			return fmt.Errorf("fetch checksum: %w", err)
		}
		if got := ark.HashContent(newBinary); !strings.EqualFold(got, wantSum) {
			return fmt.Errorf("checksum mismatch for %s: got %s, want %s", assetName, got, wantSum)
		}
		// R3473/R3476: swap only after the download verified.
		if err := SwapExe(u.ArkDir, newBinary); err != nil {
			return fmt.Errorf("swap binary: %w", err)
		}
		fmt.Fprintf(u.out(), "updated ark to %s\n", latest)
		// R3472: the new binary refreshes the rest from its own bundle.
		return u.execNew(arkExe)
	}

	// --no-exe path (and the re-exec'd new binary): refresh from the running
	// binary's own bundle. R3478
	if err := u.RefreshAssets(u.Assets); err != nil {
		return err
	}
	return u.provisionModel()
}

// execNew runs the re-exec second phase, defaulting to the real re-exec.
func (u *Updater) execNew(arkExe string) error {
	if u.ExecNew != nil {
		return u.ExecNew(arkExe)
	}
	return execNewBinary(arkExe)
}

// provisionModel downloads the configured model if missing (R3475/R3483).
func (u *Updater) provisionModel() error {
	cfg, err := ark.LoadConfig(filepath.Join(u.ArkDir, "ark.toml"))
	if err != nil {
		return nil // no config yet — nothing to provision
	}
	if err := ark.ProvisionModel(cfg.Embedding, u.ArkDir); err != nil {
		var keys *ark.ErrModelKeysMissing
		if errors.As(err, &keys) {
			fmt.Fprintln(u.out(), err.Error()) // R3483: report, do not fail
			return nil
		}
		return err
	}
	return nil
}

// RefreshAssets extracts bundled assets through the install manifest so a
// user-edited file is never clobbered and a directory link is never replaced by
// a copy. CRC: crc-Updater.md | Seq: seq-install.md#3.3.1 | R3474, R3479
func (u *Updater) RefreshAssets(src AssetSource) error {
	files, err := src.List()
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(u.ArkDir, ark.ManifestFileName)
	m, err := ark.LoadManifest(manifestPath)
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.IsSymlink || !isRefreshableAsset(f.Name) {
			continue
		}
		dest := filepath.Join(u.ArkDir, filepath.FromSlash(f.Name))
		// R3474: never write through or over a directory link we own — leave
		// the link (and its target, an app dir edited in place) untouched.
		if ancestorIsLink(u.ArkDir, dest) {
			continue
		}
		newContent, err := src.Read(f.Name)
		if err != nil {
			return err
		}
		diskContent, statErr := os.ReadFile(dest)
		write, userModified := m.ShouldWrite(f.Name, diskContent, statErr == nil, newContent)
		if userModified {
			fmt.Fprintf(u.out(), "preserved user-modified %s\n", f.Name)
			continue
		}
		if !write {
			// Already-current on disk (bytes equal the bundle): record the
			// entry so an un-manifested install is seeded by one refresh,
			// but do NOT rewrite the file (its mtime must stay put). R3479
			if statErr == nil {
				m.Record(f.Name, newContent)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(dest, newContent, f.Mode.Perm()); err != nil {
			return err
		}
		m.Record(f.Name, newContent)
	}
	return m.Save(manifestPath)
}

// SeedManifest records the hash of every refreshable bundled asset into
// ~/.ark/install-manifest.json during a fresh setup, using the same
// Record/Save path and defaults update uses. Without it the manifest is empty
// after setup and the first update would see every changed file as an untracked
// user edit and refresh almost nothing.
// CRC: crc-Updater.md | Seq: seq-install.md#1.12 | R3484, R3479
func SeedManifest(arkDir string, src AssetSource) error {
	files, err := src.List()
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(arkDir, ark.ManifestFileName)
	m, err := ark.LoadManifest(manifestPath)
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.IsSymlink || !isRefreshableAsset(f.Name) {
			continue
		}
		content, err := src.Read(f.Name)
		if err != nil {
			return err
		}
		m.Record(f.Name, content)
	}
	return m.Save(manifestPath)
}

// isRefreshableAsset reports whether a bundle path is under a refreshable root.
func isRefreshableAsset(name string) bool {
	name = filepath.ToSlash(name)
	for _, root := range refreshableAssetRoots {
		if name == root || strings.HasPrefix(name, root+"/") {
			return true
		}
	}
	return false
}

// ancestorIsLink reports whether any directory from base down to dest's parent
// is a link (symlink or Windows junction/ModeIrregular).
func ancestorIsLink(base, dest string) bool {
	rel, err := filepath.Rel(base, filepath.Dir(dest))
	if err != nil {
		return false
	}
	cur := base
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		if fi, err := os.Lstat(cur); err == nil && fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return true
		}
	}
	return false
}

// isSymlink reports whether path is a symbolic link. R3477
func isSymlink(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}

// compareVersions compares dotted numeric versions (leading v stripped).
// Returns -1, 0, or 1. Non-numeric segments fall back to string comparison.
func compareVersions(a, b string) int {
	as := strings.Split(strings.TrimPrefix(a, "v"), ".")
	bs := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var av, bv string
		if i < len(as) {
			av = as[i]
		}
		if i < len(bs) {
			bv = bs[i]
		}
		an, aerr := strconv.Atoi(av)
		bn, berr := strconv.Atoi(bv)
		if aerr == nil && berr == nil {
			if an != bn {
				return cmp.Compare(an, bn)
			}
			continue
		}
		if av != bv {
			return cmp.Compare(av, bv)
		}
	}
	return 0
}

// execNewBinary runs the freshly-swapped binary's own refresh (phase two),
// streaming its output. Run as a subprocess rather than an in-place image
// replacement so the behaviour is identical on Windows. R3472
func execNewBinary(arkExe string) error {
	cmd := exec.Command(arkExe, "update", "--no-exe")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// --- default GitHub / bundle implementations ---

// githubReleaseFetcher fetches the latest zot/ark release over the GitHub API,
// caching the release document so assets and checksums cost one call. R3472
type githubReleaseFetcher struct {
	release *githubRelease
}

type githubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func (g *githubReleaseFetcher) load() (*githubRelease, error) {
	if g.release != nil {
		return g.release, nil
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(releaseAPIURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API %s: HTTP %d", releaseAPIURL, resp.StatusCode)
	}
	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	g.release = &rel
	return &rel, nil
}

func (g *githubReleaseFetcher) Latest() (string, error) {
	rel, err := g.load()
	if err != nil {
		return "", err
	}
	return rel.TagName, nil
}

func (g *githubReleaseFetcher) assetURL(name string) (string, error) {
	rel, err := g.load()
	if err != nil {
		return "", err
	}
	for _, a := range rel.Assets {
		if a.Name == name {
			return a.URL, nil
		}
	}
	return "", fmt.Errorf("release asset %q not found", name)
}

func (g *githubReleaseFetcher) Download(assetName string) ([]byte, error) {
	url, err := g.assetURL(assetName)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func (g *githubReleaseFetcher) Checksum(assetName string) (string, error) {
	sums, err := g.Download(checksumAssetName)
	if err != nil {
		return "", err
	}
	return parseChecksums(string(sums), assetName)
}

// parseChecksums finds assetName's sha256 in sha256sum-format text
// ("<hex>  <name>" per line). R3472
func parseChecksums(text, assetName string) (string, error) {
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && filepath.Base(fields[len(fields)-1]) == assetName {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("no checksum for %q in %s", assetName, checksumAssetName)
}

// bundleAssetSource reads the grafted bundle through the cli helpers. R3474
type bundleAssetSource struct{}

func (bundleAssetSource) List() ([]BundleAsset, error) {
	infos, err := cli.BundleListFilesWithInfo()
	if err != nil {
		return nil, err
	}
	out := make([]BundleAsset, 0, len(infos))
	for _, i := range infos {
		out = append(out, BundleAsset{Name: i.Name, IsSymlink: i.IsSymlink, SymlinkTarget: i.SymlinkTarget, Mode: i.Mode})
	}
	return out, nil
}

func (bundleAssetSource) Read(name string) ([]byte, error) {
	return cli.BundleReadFile(name)
}
