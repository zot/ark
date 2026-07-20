package ark

// CRC: crc-DB.md | Test: test-ExtMirror.md | R3171

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExtMirrorPathDefaultTree — test-ExtMirror.md "default tree when
// ext_mirror unset". R2392, R3171.
func TestExtMirrorPathDefaultTree(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	got, err := extMirrorPath("/src/root", "/src/root/notes/a.md", "")
	if err != nil {
		t.Fatalf("extMirrorPath: %v", err)
	}
	want := filepath.Join(tmp, ".ark", "external", "src-root", "notes", "a.md.md")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestExtMirrorPathRedirectsInTree — test-ExtMirror.md "ext_mirror redirects
// the base in-tree". R3171.
func TestExtMirrorPathRedirectsInTree(t *testing.T) {
	got, err := extMirrorPath("/src/root", "/src/root/books/mark.md", "mirrors")
	if err != nil {
		t.Fatalf("extMirrorPath: %v", err)
	}
	want := "/src/root/mirrors/books/mark.md.md"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestExtMirrorPathSelfMirrorRejected — test-ExtMirror.md "self-mirror
// rejected": a target already inside the ext_mirror dir has no mirror. R3171.
func TestExtMirrorPathSelfMirrorRejected(t *testing.T) {
	cases := []string{
		"/src/root/mirrors/books/mark.md.md", // nested under the dir
		"/src/root/mirrors",                  // the dir itself
	}
	for _, target := range cases {
		if _, err := extMirrorPath("/src/root", target, "mirrors"); err == nil {
			t.Errorf("target %q inside ext_mirror dir: want error, got none", target)
		}
	}
}

// TestExtMirrorPathOutsideRootRejected — test-ExtMirror.md "target outside
// source root rejected": the `..` guard still fires with an override. R2392.
func TestExtMirrorPathOutsideRootRejected(t *testing.T) {
	if _, err := extMirrorPath("/src/root", "/other/x.md", "mirrors"); err == nil {
		t.Error("target outside source root: want error, got none")
	}
}

// TestSourceForPath — test-ExtMirror.md "SourceForPath returns the owning
// source with its fields". R3171.
func TestSourceForPath(t *testing.T) {
	c := &Config{Sources: []Source{
		{Dir: "/glob/*", ExtMirror: "ignored"}, // glob source, must be skipped
		{Dir: "/a", ExtMirror: "m"},
		{Dir: "/b"},
	}}

	if src, ok := c.SourceForPath("/a/deep/x.md"); !ok || src.Dir != "/a" || src.ExtMirror != "m" {
		t.Errorf("/a/deep/x.md → %+v ok=%v, want /a with ExtMirror=m", src, ok)
	}
	if src, ok := c.SourceForPath("/b/y.md"); !ok || src.Dir != "/b" || src.ExtMirror != "" {
		t.Errorf("/b/y.md → %+v ok=%v, want /b with empty ExtMirror", src, ok)
	}
	if _, ok := c.SourceForPath("/none/z.md"); ok {
		t.Error("/none/z.md: want ok=false")
	}
}

// TestResolveGlobsPropagatesExtMirror — test-ExtMirror.md "glob expansion
// carries ext_mirror": each concrete source materialized from a glob keeps
// its own in-tree mirror. R3171.
func TestResolveGlobsPropagatesExtMirror(t *testing.T) {
	root := t.TempDir()
	projA := filepath.Join(root, "proj-a")
	if err := os.Mkdir(projA, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	c := &Config{Sources: []Source{{Dir: filepath.Join(root, "*"), ExtMirror: "mirrors"}}}
	if _, err := c.ResolveGlobs(); err != nil {
		t.Fatalf("ResolveGlobs: %v", err)
	}

	src, ok := c.SourceForPath(filepath.Join(projA, "notes", "x.md"))
	if !ok {
		t.Fatalf("expanded source for %s not found", projA)
	}
	if src.ExtMirror != "mirrors" {
		t.Errorf("expanded source ExtMirror = %q, want %q", src.ExtMirror, "mirrors")
	}

	got, err := extMirrorPath(src.Dir, filepath.Join(projA, "notes", "x.md"), src.ExtMirror)
	if err != nil {
		t.Fatalf("extMirrorPath: %v", err)
	}
	want := filepath.Join(projA, "mirrors", "notes", "x.md.md")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
