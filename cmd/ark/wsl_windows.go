//go:build windows

package main

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// parentExeName returns the image name of this process's parent, from a
// toolhelp process snapshot; "" when it cannot be found.
// CRC: crc-WSLBridge.md | Seq: seq-wsl.md#1.2 | R3495
func parentExeName() string {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(snap)
	self := uint32(os.Getpid())
	names := map[uint32]string{}
	var parent uint32
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		names[pe.ProcessID] = windows.UTF16ToString(pe.ExeFile[:])
		if pe.ProcessID == self {
			parent = pe.ParentProcessID
		}
	}
	return names[parent]
}
