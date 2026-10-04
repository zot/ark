package ark

// CRC: crc-LlamaLibs.md | Test: test-LlamaLibs.md | R2969, R3486

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func touch(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMissingMSVCRuntime(t *testing.T) {
	sysDir, libDir := t.TempDir(), t.TempDir()
	if got := missingMSVCRuntime(sysDir, libDir); !reflect.DeepEqual(got, msvcRuntimeDLLs) {
		t.Fatalf("none present: got %v, want %v", got, msvcRuntimeDLLs)
	}
	touch(t, sysDir, "msvcp140.dll")
	touch(t, libDir, "vcruntime140.dll")
	if got := missingMSVCRuntime(sysDir, libDir); !reflect.DeepEqual(got, []string{"vcruntime140_1.dll"}) {
		t.Fatalf("two present: got %v, want [vcruntime140_1.dll]", got)
	}
	touch(t, sysDir, "vcruntime140_1.dll")
	if got := missingMSVCRuntime(sysDir, libDir); len(got) != 0 {
		t.Fatalf("all present: got %v, want none", got)
	}
}

// msvcRuntimeServer serves a flat zip of the three runtime DLL names with
// dummy contents and returns its URL and the zip's SHA-256.
func msvcRuntimeServer(t *testing.T) (url, sum string) {
	t.Helper()
	zp := filepath.Join(t.TempDir(), "rt.zip")
	content := map[string]string{}
	for _, name := range msvcRuntimeDLLs {
		content[name] = "dll:" + name
	}
	writeZip(t, zp, msvcRuntimeDLLs, content)
	data, err := os.ReadFile(zp)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(data)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/msvc-runtime-test-win-x64.zip", hex.EncodeToString(h[:])
}

func libDirNames(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names
}

func TestFetchMSVCRuntimeVerified(t *testing.T) {
	url, sum := msvcRuntimeServer(t)
	libDir := t.TempDir()
	if err := fetchMSVCRuntime(url, sum, libDir); err != nil {
		t.Fatal(err)
	}
	for _, name := range msvcRuntimeDLLs {
		got, err := os.ReadFile(filepath.Join(libDir, name))
		if err != nil {
			t.Fatalf("%s not extracted: %v", name, err)
		}
		if string(got) != "dll:"+name {
			t.Fatalf("%s = %q", name, got)
		}
	}
	if names := libDirNames(t, libDir); len(names) != len(msvcRuntimeDLLs) {
		t.Fatalf("libDir holds %v, want only the runtime DLLs (zip or temp file left behind)", names)
	}
}

func TestFetchMSVCRuntimeBadHash(t *testing.T) {
	url, _ := msvcRuntimeServer(t)
	wrong := sha256.Sum256([]byte("not the zip"))
	libDir := t.TempDir()
	err := fetchMSVCRuntime(url, hex.EncodeToString(wrong[:]), libDir)
	if err == nil {
		t.Fatal("wrong hash accepted")
	}
	for _, want := range []string{"ark embed install", "Visual C++ Redistributable"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name the remedy %q", err, want)
		}
	}
	if names := libDirNames(t, libDir); len(names) != 0 {
		t.Fatalf("mismatched download left %v in libDir", names)
	}
}
