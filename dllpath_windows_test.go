//go:build windows

package ark

import (
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// getDllDirectoryBufLen is the UTF-16 buffer size for GetDllDirectoryW;
// ample for a temp dir path.
const getDllDirectoryBufLen = 1024

var (
	kernel32ForTest      = windows.NewLazySystemDLL("kernel32.dll")
	procGetDllDirectoryW = kernel32ForTest.NewProc("GetDllDirectoryW")
	procSetDllDirectoryW = kernel32ForTest.NewProc("SetDllDirectoryW")
)

// getDllDirectory reads the process DLL directory set by SetDllDirectoryW.
func getDllDirectory(t *testing.T) string {
	t.Helper()
	buf := make([]uint16, getDllDirectoryBufLen)
	n, _, err := procGetDllDirectoryW.Call(uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])))
	if n == 0 && err != windows.ERROR_SUCCESS {
		t.Fatalf("GetDllDirectoryW: %v", err)
	}
	if int(n) >= len(buf) {
		t.Fatalf("GetDllDirectoryW needs %d chars, buffer is %d", n, len(buf))
	}
	return windows.UTF16ToString(buf[:n])
}

// CRC: crc-LlamaLibs.md | Test: test-LlamaLibs.md | R3485
func TestAddDllSearchDirSetsDllDirectory(t *testing.T) {
	dir := t.TempDir()
	// Restore the default search order (NULL argument) afterwards.
	t.Cleanup(func() { procSetDllDirectoryW.Call(0) })

	if err := addDllSearchDir(dir); err != nil {
		t.Fatalf("addDllSearchDir(%s): %v", dir, err)
	}
	if got := getDllDirectory(t); !strings.EqualFold(got, dir) {
		t.Fatalf("GetDllDirectoryW = %q, want %q", got, dir)
	}
}
