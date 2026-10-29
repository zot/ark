//go:build !windows

package main

// CRC: crc-Registration.md | R3682
//
// Off Windows there is no Path entry or installed-apps list to keep.

// Register does nothing off Windows. R3682
func Register(arkDir, version string) (bool, error) { return false, nil }

// Unregister does nothing off Windows. R3682
func Unregister(arkDir string) error { return nil }
