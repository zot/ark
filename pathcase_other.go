//go:build !windows

package ark

// foldFilenames: the filesystems ark targets here are case-sensitive, so two
// spellings are two files and microfts2 must not fold them.
// CRC: crc-FilenameCase.md | R3630
const foldFilenames = false

// DiskSpelling is the identity off Windows: on a case-sensitive filesystem the
// spelling given is the only one there is.
// CRC: crc-FilenameCase.md | R3626
func DiskSpelling(path string) string { return path }
