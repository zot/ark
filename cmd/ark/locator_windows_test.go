//go:build windows

package main

import "testing"

// TestSplitPathLocatorKeepsItsDrive: a Windows drive's colon is not the
// locator's, so a drive path with no range is not a locator at all.
// CRC: crc-FilenameCase.md | R3637
func TestSplitPathLocatorKeepsItsDrive(t *testing.T) {
	if p, r, ok := splitPathLocator(`C:\x.md:3-5`); !ok || p != `C:\x.md` || r != "3-5" {
		t.Errorf("splitPathLocator(with range) = (%q, %q, %v)", p, r, ok)
	}
	if p, r, ok := splitPathLocator(`C:\x.md`); ok {
		t.Errorf("splitPathLocator(no range) = (%q, %q, true), want not a locator", p, r)
	}
}
