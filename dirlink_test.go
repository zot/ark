package ark

// CRC: crc-DirLink.md | Test: test-DirLink.md | R3453, R3454, R3455
//
// Pure filesystem logic over a temp dir — no server, no DB. On this host the
// link is a symlink; the Windows junction path is exercised by the cross-build.

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRemoveDirLinkRefusesRealDir: RemoveDirLink deletes only a link, never a
// real directory at the destination. R3454
func TestRemoveDirLinkRefusesRealDir(t *testing.T) {
	tmp := t.TempDir()
	dst := filepath.Join(tmp, "dst")
	if err := os.Mkdir(dst, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dst, "keep.txt")
	if err := os.WriteFile(file, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveDirLink(dst); err == nil {
		t.Fatal("expected RemoveDirLink to refuse a real directory")
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("real directory was removed: %v", err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("file inside real directory vanished: %v", err)
	}
}

// TestMakeDirLinkIdempotent: a re-link to the same target is a no-op, not an
// error, and does not churn the link. R3455
func TestMakeDirLinkIdempotent(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "target")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "link")
	if err := MakeDirLink(link, target); err != nil {
		t.Fatalf("first MakeDirLink: %v", err)
	}
	first, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("readlink after first: %v", err)
	}
	if err := MakeDirLink(link, target); err != nil {
		t.Fatalf("second MakeDirLink should be a no-op: %v", err)
	}
	second, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("readlink after second: %v", err)
	}
	if first != second {
		t.Fatalf("link churned: %q -> %q", first, second)
	}
	if !IsOurLink(link, target) {
		t.Fatal("IsOurLink should recognize the link it created")
	}
}

// TestMakeAndReadDirLink: happy path — a link is readable through to the target,
// and os.Readlink reports the target. R3453
func TestMakeAndReadDirLink(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "target")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "marker"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "link")
	if err := MakeDirLink(link, target); err != nil {
		t.Fatalf("MakeDirLink: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(link, "marker"))
	if err != nil || string(data) != "hello" {
		t.Fatalf("read through link: %v %q", err, data)
	}
	dest, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	want, _ := filepath.Abs(target)
	if dest != want {
		t.Fatalf("readlink = %q, want %q", dest, want)
	}
}
