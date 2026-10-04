//go:build !windows

package ark

// addDllSearchDir is a no-op off Windows: dlopen resolves a library's
// dependencies without a process-wide search-path change.
// CRC: crc-LlamaLibs.md | R3485
func addDllSearchDir(libDir string) error {
	return nil
}
