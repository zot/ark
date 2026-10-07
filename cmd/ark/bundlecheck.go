package main

// CRC: crc-CLI.md | R3518, R3519

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// checkBundleSource refuses a bundle source that ships a symlink (apps are
// activated by links ark makes at install time, junctions on Windows) or in
// which two apps ship a viewdef of the same file name (so the built-in app set
// is collision-free by construction).
// CRC: crc-CLI.md | R3518, R3519
func checkBundleSource(dir string) error {
	owners := map[string][]string{} // viewdef file name -> apps shipping it
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("bundle source holds a symlink: %s (the bundle ships none; apps are activated at install time)", rel)
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) == 4 && parts[0] == "apps" && parts[2] == "viewdefs" && strings.HasSuffix(parts[3], ".html") && !d.IsDir() {
			owners[parts[3]] = append(owners[parts[3]], parts[1])
		}
		return nil
	})
	if err != nil {
		return err
	}
	var collisions []string
	for name, apps := range owners {
		if len(apps) > 1 {
			sort.Strings(apps)
			collisions = append(collisions, fmt.Sprintf("%s (apps %s)", name, strings.Join(apps, ", ")))
		}
	}
	if len(collisions) > 0 {
		sort.Strings(collisions)
		return fmt.Errorf("built-in apps ship the same viewdef: %s", strings.Join(collisions, "; "))
	}
	return nil
}
