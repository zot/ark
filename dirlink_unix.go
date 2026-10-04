//go:build !windows

package ark

import "os"

// makeDirLink creates a symbolic link on Unix. No privilege is needed.
// CRC: crc-DirLink.md | Seq: seq-install.md#1.6.1 | R3453
func makeDirLink(linkPath, targetDir string) error {
	return os.Symlink(targetDir, linkPath)
}
