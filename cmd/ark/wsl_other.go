//go:build !windows

package main

// parentExeName has no use off Windows: WSL mode is always off there (R3494).
// CRC: crc-WSLBridge.md | R3494
func parentExeName() string { return "" }
