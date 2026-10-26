package ark

import "path/filepath"

// respelled reports whether path no longer exists under its own spelling: the
// filesystem stores it spelled differently, which on a case-insensitive
// filesystem is how a case-only rename looks to a stat of the old name. Always
// false off Windows, where DiskSpelling is the identity.
// CRC: crc-FilenameCase.md | R3629
func respelled(path string) bool {
	p := filepath.Clean(path)
	return DiskSpelling(p) != p
}
