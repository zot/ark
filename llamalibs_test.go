package ark

// CRC: crc-LlamaLibs.md | Test: test-LlamaLibs.md | R2969

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

// writeZip builds a zip at path from name→content; a name ending in "/" is a
// directory entry.
func writeZip(t *testing.T, path string, entries []string, content map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, name := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func TestExtractZipFlat(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "flat.zip")
	writeZip(t, zp, []string{"llama.dll", "ggml.dll"},
		map[string]string{"llama.dll": "L", "ggml.dll": "G"})
	dest := filepath.Join(dir, "lib")
	if err := extractZip(zp, dest); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"llama.dll": "L", "ggml.dll": "G"} {
		got, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil {
			t.Fatalf("%s not extracted: %v", name, err)
		}
		if string(got) != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestExtractZipWrapped(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "wrapped.zip")
	writeZip(t, zp, []string{"llama-b1/", "llama-b1/llama.dll"},
		map[string]string{"llama-b1/llama.dll": "L"})
	dest := filepath.Join(dir, "lib")
	if err := extractZip(zp, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "llama.dll")); err != nil {
		t.Fatalf("llama.dll not flattened into dest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "llama-b1")); err == nil {
		t.Fatal("release directory was not stripped")
	}
}
