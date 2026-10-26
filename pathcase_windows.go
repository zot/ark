//go:build windows

package ark

import (
	"path/filepath"
	"strings"
	"syscall"
)

// foldFilenames is the value ark creates a database with for microfts2's
// CaseInsensitiveFilenames: Windows filesystems are case-insensitive, so two
// spellings of a path are one file.
// CRC: crc-FilenameCase.md | R3630
const foldFilenames = true

// DiskSpelling rewrites an absolute path to the spelling the filesystem
// stores, one component at a time, the way Go's own EvalSymlinks normalizes
// case on Windows (FindFirstFile per component) but without following links:
// a junction must stay a junction, or a file would move out of its source.
//
// It stops at the first component that does not exist, keeping the rest as
// written, so a missing path is still reported as not found; and at the first
// glob character, because FindFirstFile treats `*` and `?` as wildcards and
// would hand back whichever file happened to match first. A relative path,
// or anything without a volume (a tmp:// overlay path), is returned unchanged.
// CRC: crc-FilenameCase.md | R3626
func DiskSpelling(path string) string {
	p := filepath.Clean(path)
	vol := filepath.VolumeName(p)
	if vol == "" {
		return path
	}
	if len(vol) == 2 && vol[1] == ':' {
		vol = strings.ToUpper(vol) // c: and C: are one drive
	}
	parts := strings.Split(strings.TrimPrefix(p[len(vol):], `\`), `\`)
	out := vol + `\`
	for i, part := range parts {
		if part == "" {
			continue
		}
		if strings.ContainsAny(part, "*?[") {
			return filepath.Join(append([]string{out}, parts[i:]...)...)
		}
		name, ok := storedName(filepath.Join(out, part))
		if !ok {
			return filepath.Join(append([]string{out}, parts[i:]...)...)
		}
		out = filepath.Join(out, name)
	}
	return out
}

// storedName asks the filesystem for the stored name of the last component of
// path, which FindFirstFile returns in its own case (and in its long form when
// path used an 8.3 short name).
func storedName(path string) (string, bool) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "", false
	}
	var data syscall.Win32finddata
	h, err := syscall.FindFirstFile(p, &data)
	if err != nil {
		return "", false
	}
	syscall.FindClose(h)
	return syscall.UTF16ToString(data.FileName[:]), true
}
