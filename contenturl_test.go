package ark

// CRC: crc-Server.md, crc-FilenameCase.md | Test: test-FilenameCase.md

import (
	"runtime"
	"testing"
)

// R3654: a path's URL form keeps a Windows drive behind a slash, and the route
// reads the file path back.
func TestContentURLPath(t *testing.T) {
	if got := ContentURLPath("/home/me/x.md"); got != "/home/me/x.md" {
		t.Errorf("unix path = %q, want it unchanged", got)
	}
	if runtime.GOOS != "windows" {
		// Off Windows `/C:/x` is an ordinary path and must stay one.
		if got := filePathFromURL("/C:/x.md"); got != "/C:/x.md" {
			t.Errorf("filePathFromURL(/C:/x.md) = %q off Windows, want it unchanged", got)
		}
		return
	}
	if got := ContentURLPath(`C:\Users\me\x.md`); got != "/C:/Users/me/x.md" {
		t.Errorf("ContentURLPath(C:\\Users\\me\\x.md) = %q, want /C:/Users/me/x.md", got)
	}
	if got := filePathFromURL("/C:/Users/me/x.md"); got != "C:/Users/me/x.md" {
		t.Errorf("filePathFromURL(/C:/Users/me/x.md) = %q, want C:/Users/me/x.md", got)
	}
}
