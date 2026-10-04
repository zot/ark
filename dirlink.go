package ark

// CRC: crc-DirLink.md | Seq: seq-install.md | R3453, R3454, R3455
//
// Portable directory-link primitive: a symbolic link on Unix, a directory
// junction on Windows (see dirlink_unix.go / dirlink_windows.go for the
// platform makeDirLink). Shared by setup, --local, and the updater.

import (
	"fmt"
	"os"
	"path/filepath"
)

// MakeDirLink creates a directory link at linkPath pointing at targetDir.
// Idempotent: when linkPath is already a link this helper owns, it is left in
// place. A foreign link is replaced; a real directory in the way is left for
// the platform primitive to fail on loudly rather than silently clobbered.
// CRC: crc-DirLink.md | Seq: seq-install.md#1.6.1 | R3453, R3455
func MakeDirLink(linkPath, targetDir string) error {
	abs, err := filepath.Abs(targetDir)
	if err != nil {
		return err
	}
	// R3455: already our link → no-op.
	if IsOurLink(linkPath, abs) {
		return nil
	}
	// Replace an existing link (symlink or junction) that points elsewhere,
	// but never a real directory: RemoveDirLink refuses that.
	if fi, statErr := os.Lstat(linkPath); statErr == nil && isLink(fi) {
		_ = os.Remove(linkPath)
	}
	if err := os.MkdirAll(filepath.Dir(linkPath), 0755); err != nil {
		return err
	}
	return makeDirLink(linkPath, abs)
}

// RemoveDirLink removes the link at linkPath with os.Remove. It refuses to
// delete a real (non-link) directory there, since a non-link directory is the
// user's data. A missing path is not an error.
// CRC: crc-DirLink.md | R3454
func RemoveDirLink(linkPath string) error {
	fi, err := os.Lstat(linkPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	// A real directory (not a link) at the destination is the user's data.
	// On Windows a junction reads as ModeIrregular under Lstat (measured,
	// build 26200), so the link test cannot rely on ModeSymlink alone.
	if fi.IsDir() && !isLink(fi) {
		return fmt.Errorf("refusing to remove real directory %s (not a link)", linkPath)
	}
	return os.Remove(linkPath)
}

// IsOurLink reports whether linkPath is a link whose target is targetDir. On
// Windows os.Readlink returns a junction's absolute target, so this
// distinguishes "already our link" from someone else's link or a real
// directory.
// CRC: crc-DirLink.md | R3455
func IsOurLink(linkPath, targetDir string) bool {
	dest, err := os.Readlink(linkPath)
	if err != nil {
		return false
	}
	want, err := filepath.Abs(targetDir)
	if err != nil {
		want = targetDir
	}
	if dest == want || dest == targetDir {
		return true
	}
	if got, err := filepath.Abs(dest); err == nil {
		return got == want
	}
	return false
}

// isLink reports whether fi is a symbolic link (Unix) or a junction/reparse
// point (Windows Lstat reports ModeIrregular for a junction).
func isLink(fi os.FileInfo) bool {
	return fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0
}
