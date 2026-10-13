package ark

// CRC: crc-AppActivation.md | Seq: seq-install.md#1.4.1 | R3515, R3516, R3517, R3521, R3522
//
// App activation: an app is active when ~/.ark/lua/<app> is a directory link
// to ~/.ark/apps/<app>. ui-engine reads the app's entry file, modules,
// viewdefs and static files through that one link.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zot/ui-engine/cli"
)

// bundleAppsDir is the bundle directory whose children are apps.
const bundleAppsDir = "apps"

// activationDir is the ~/.ark directory holding the activation links.
const activationDir = "lua"

// ActivateApp makes ~/.ark/lua/<app> a directory link to ~/.ark/apps/<app>
// (a junction on Windows). Idempotent.
// CRC: crc-AppActivation.md | R3516, R3521
func ActivateApp(arkDir, app string) error {
	return MakeDirLink(filepath.Join(arkDir, activationDir, app), filepath.Join(arkDir, bundleAppsDir, app))
}

// DeactivateApp removes ~/.ark/lua/<app>. It refuses a real directory there.
// CRC: crc-AppActivation.md | R3521
func DeactivateApp(arkDir, app string) error {
	return RemoveDirLink(filepath.Join(arkDir, activationDir, app))
}

// RepairActivationLinks re-makes every activation link that no longer
// resolves, pointing it at ~/.ark/apps/<entry> when that directory exists
// (a junction stores an absolute target, so a moved ~/.ark needs this), and
// logs the ones it cannot repair. It never creates an absent entry, so a
// deactivated app stays deactivated.
// CRC: crc-AppActivation.md | R3522
func RepairActivationLinks(arkDir string, logf func(format string, args ...any)) {
	luaDir := filepath.Join(arkDir, activationDir)
	entries, err := os.ReadDir(luaDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		link := filepath.Join(luaDir, entry.Name())
		fi, err := os.Lstat(link)
		if err != nil || !isLink(fi) {
			continue
		}
		if _, err := os.Stat(link); err == nil {
			continue // resolves
		}
		appDir := filepath.Join(arkDir, bundleAppsDir, entry.Name())
		if info, err := os.Stat(appDir); err != nil || !info.IsDir() {
			logf("app activation: %s is dangling and %s does not exist", link, appDir)
			continue
		}
		if err := ActivateApp(arkDir, entry.Name()); err != nil {
			logf("app activation: could not repair %s: %v", link, err)
			continue
		}
		logf("app activation: repaired %s -> %s", link, appDir)
	}
}

// BundledAppNames lists the apps ark's bundle ships: the top-level
// directories under the bundle's apps/. Empty for an unbundled binary.
// CRC: crc-AppActivation.md | R3515, R3516, R3517
func BundledAppNames() []string {
	files, err := cli.BundleListFilesRecursive(bundleAppsDir)
	if err != nil {
		return nil
	}
	return builtinAppNames(files)
}

// ViewdefConflicts computes the live viewdef-name conflicts from disk with
// the ui-engine's own resolver, ranking ark's bundled apps as built-ins.
// CRC: crc-AppActivation.md | R3525
func ViewdefConflicts(arkDir string) []cli.ViewdefCollision {
	return cli.ResolveViewdefCollisions(arkDir, BundledAppNames())
}

// AppNamesInListing is builtinAppNames for callers outside the package that
// already hold a bundle listing (the updater).
// CRC: crc-AppActivation.md | R3517
func AppNamesInListing(files []string) []string { return builtinAppNames(files) }

// builtinAppNames returns the distinct top-level directory names under
// apps/ in a bundle listing, sorted.
// CRC: crc-AppActivation.md | R3515
func builtinAppNames(files []string) []string {
	prefix := bundleAppsDir + "/"
	seen := map[string]bool{}
	var names []string
	for _, f := range files {
		rest, ok := strings.CutPrefix(f, prefix)
		if !ok {
			continue
		}
		name, _, isNested := strings.Cut(rest, "/")
		if !isNested || name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
