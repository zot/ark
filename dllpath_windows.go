//go:build windows

package ark

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// addDllSearchDir adds libDir to the process DLL search path. A DLL loaded by
// full path does not get its own folder searched for its dependencies:
// Windows looks in the executable's directory, System32 and PATH. Without
// this, lib\ggml.dll cannot find lib\ggml-base.dll or the MSVC runtime beside
// it. SetDllDirectoryW is process-wide, so it also covers the backend DLLs
// yzma loads later from libDir. PATH is never edited.
// CRC: crc-LlamaLibs.md | R3485
func addDllSearchDir(libDir string) error {
	if err := windows.SetDllDirectory(libDir); err != nil {
		return fmt.Errorf("add %s to the DLL search path: %w", libDir, err)
	}
	return nil
}

// systemDir is the Windows system directory (normally System32), where an
// installed MSVC runtime lives.
// CRC: crc-LlamaLibs.md | R3486
func systemDir() (string, error) {
	return windows.GetSystemDirectory()
}
