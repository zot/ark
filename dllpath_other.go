//go:build !windows

package ark

// addDllSearchDir is a no-op off Windows: dlopen resolves a library's
// dependencies without a process-wide search-path change.
// CRC: crc-LlamaLibs.md | R3485
func addDllSearchDir(libDir string) error {
	return nil
}

// systemDir has no meaning off Windows; ensureMSVCRuntime returns before
// asking for it.
// CRC: crc-LlamaLibs.md | R3486
func systemDir() (string, error) {
	return "", nil
}
