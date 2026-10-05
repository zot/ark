package ark

// CRC: crc-LlamaLibs.md | Test: test-LlamaLibs.md | R2969, R3486, R3487, R3488, R3489, R3490, R3491, R3493

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

// fixedGPU builds a gpuProbe whose detectors return fixed answers.
func fixedGPU(cuda, rocm, vulkan bool) gpuProbe {
	return gpuProbe{
		cuda:   func() bool { return cuda },
		rocm:   func() bool { return rocm },
		vulkan: func() bool { return vulkan },
	}
}

func TestAutoBackendWindows(t *testing.T) {
	cases := []struct {
		name         string
		cuda, vulkan bool
		want         string
	}{
		{"nvidia with vulkan device", true, true, backendVulkan},
		{"vulkan device only", false, true, backendVulkan},
		{"nvidia without vulkan device", true, false, backendCPU},
		{"no gpu", false, false, backendCPU},
	}
	for _, c := range cases {
		if got := autoBackend(goosWindows, fixedGPU(c.cuda, false, c.vulkan)); got != c.want {
			t.Errorf("windows %s: auto = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestAutoBackendLinuxOrder(t *testing.T) {
	cases := []struct {
		name               string
		cuda, rocm, vulkan bool
		want               string
	}{
		{"nvidia", true, true, true, backendCUDA},
		{"amd rocm", false, true, true, backendROCm},
		{"vulkan only", false, false, true, backendVulkan},
		{"no gpu", false, false, false, backendCPU},
	}
	for _, c := range cases {
		if got := autoBackend(goosLinux, fixedGPU(c.cuda, c.rocm, c.vulkan)); got != c.want {
			t.Errorf("linux %s: auto = %q, want %q", c.name, got, c.want)
		}
	}
	if got := autoBackend(goosDarwin, fixedGPU(false, false, false)); got != backendMetal {
		t.Errorf("darwin: auto = %q, want %q", got, backendMetal)
	}
}

// nvidiaSMISample is the header of plain nvidia-smi output from the Windows
// test VM (driver 556.12, RTX 2060 Max-Q).
const nvidiaSMISample = `+-----------------------------------------------------------------------------------------+
| NVIDIA-SMI 556.12                 Driver Version: 556.12         CUDA Version: 12.5     |
|-----------------------------------------+------------------------+----------------------+
`

func TestParseCUDAVersion(t *testing.T) {
	got, ok := parseCUDAVersion(nvidiaSMISample)
	if !ok || got != (cudaVersion{12, 5}) {
		t.Fatalf("parse sample: got %v ok=%v, want 12.5", got, ok)
	}
	if got, ok := parseCUDAVersion("| CUDA Version: 13 |"); !ok || got != (cudaVersion{13, 0}) {
		t.Fatalf("parse major only: got %v ok=%v, want 13.0", got, ok)
	}
	if _, ok := parseCUDAVersion("| NVIDIA-SMI 556.12   Driver Version: 556.12 |"); ok {
		t.Fatal("no CUDA Version field parsed as a version")
	}
}

// driverAt is a cuda driver probe reporting major.minor.
func driverAt(major, minor int) func() (cudaVersion, bool) {
	return func() (cudaVersion, bool) { return cudaVersion{major, minor}, true }
}

func noDriver() (cudaVersion, bool) { return cudaVersion{}, false }

func TestLlamaAssetWindowsCUDA(t *testing.T) {
	const v = defaultLlamaVersion
	cases := []struct {
		major, minor int
		archives     []string
		marker       string
	}{
		{12, 5, []string{"llama-b9592-bin-win-cuda-12.4-x64.zip", "cudart-llama-bin-win-cuda-12.4-x64.zip"}, "cudart64_12.dll"},
		{13, 4, []string{"llama-b9592-bin-win-cuda-13.3-x64.zip", "cudart-llama-bin-win-cuda-13.3-x64.zip"}, "cudart64_13.dll"},
		{13, 3, []string{"llama-b9592-bin-win-cuda-13.3-x64.zip", "cudart-llama-bin-win-cuda-13.3-x64.zip"}, "cudart64_13.dll"},
		{13, 0, []string{"llama-b9592-bin-win-cuda-12.4-x64.zip", "cudart-llama-bin-win-cuda-12.4-x64.zip"}, "cudart64_12.dll"},
	}
	for _, c := range cases {
		rel, err := llamaAsset("amd64", goosWindows, backendCUDA, v, driverAt(c.major, c.minor))
		if err != nil {
			t.Fatalf("driver %d.%d: %v", c.major, c.minor, err)
		}
		if rel.baseURL != "https://github.com/ggml-org/llama.cpp/releases/download/"+v {
			t.Errorf("driver %d.%d: baseURL %s", c.major, c.minor, rel.baseURL)
		}
		if !reflect.DeepEqual(rel.archives, c.archives) || rel.marker != c.marker {
			t.Errorf("driver %d.%d: got %v marker %q, want %v marker %q", c.major, c.minor, rel.archives, rel.marker, c.archives, c.marker)
		}
	}
}

func TestLlamaAssetCUDADriverTooOld(t *testing.T) {
	for _, goos := range []string{goosWindows, goosLinux} {
		_, err := llamaAsset("amd64", goos, backendCUDA, defaultLlamaVersion, driverAt(11, 8))
		if err == nil {
			t.Fatalf("%s: driver 11.8 accepted", goos)
		}
		for _, want := range []string{"11.8", "12.", `backend = "vulkan"`} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: error %q does not name %q", goos, err, want)
			}
		}
	}
	_, err := llamaAsset("amd64", goosWindows, backendCUDA, defaultLlamaVersion, driverAt(12, 2))
	if err == nil || !strings.Contains(err.Error(), "12.2") || !strings.Contains(err.Error(), "12.4") || !strings.Contains(err.Error(), "vulkan") {
		t.Fatalf("windows driver 12.2: error %v should name 12.2, the 12.4 minimum, and vulkan", err)
	}
	_, err = llamaAsset("amd64", goosWindows, backendCUDA, defaultLlamaVersion, noDriver)
	if err == nil || !strings.Contains(err.Error(), "no CUDA version") || !strings.Contains(err.Error(), "vulkan") {
		t.Fatalf("no driver version: error %v should say so and name vulkan", err)
	}
}

func TestLlamaAssetLinuxCUDA(t *testing.T) {
	const builder = "https://github.com/hybridgroup/llama-cpp-builder/releases/download/b9592"
	cases := []struct {
		arch         string
		major, minor int
		want         string
	}{
		{"amd64", 13, 0, "llama-b9592-bin-ubuntu-cuda-13-x64.tar.gz"},
		{"amd64", 12, 5, "llama-b9592-bin-ubuntu-cuda-x64.tar.gz"},
		{"arm64", 13, 2, "llama-b9592-bin-ubuntu-cuda-13-arm64.tar.gz"},
		{"arm64", 12, 0, "llama-b9592-bin-ubuntu-cuda-arm64.tar.gz"},
	}
	for _, c := range cases {
		rel, err := llamaAsset(c.arch, goosLinux, backendCUDA, defaultLlamaVersion, driverAt(c.major, c.minor))
		if err != nil {
			t.Fatalf("%s driver %d.%d: %v", c.arch, c.major, c.minor, err)
		}
		if rel.baseURL != builder || !reflect.DeepEqual(rel.archives, []string{c.want}) || rel.marker != "" {
			t.Errorf("%s driver %d.%d: got %s %v marker %q, want %s [%s]", c.arch, c.major, c.minor, rel.baseURL, rel.archives, rel.marker, builder, c.want)
		}
	}
}

// TestInstalledWindowsCUDANeedsRuntime runs on any host: the llama lib name is
// the host's, while the cudart marker logic is driven by the goos argument.
func TestInstalledWindowsCUDANeedsRuntime(t *testing.T) {
	dir := t.TempDir()
	l := &LlamaLibs{libDir: dir, backend: backendCUDA, version: defaultLlamaVersion}
	if l.installedOn(goosWindows, "amd64", driverAt(12, 5)) {
		t.Fatal("empty lib dir counted as installed")
	}
	touch(t, dir, llamaLibName())
	if l.installedOn(goosWindows, "amd64", driverAt(12, 5)) {
		t.Fatal("llama libs without cudart counted as a complete CUDA install")
	}
	touch(t, dir, "cudart64_13.dll")
	if l.installedOn(goosWindows, "amd64", driverAt(12, 5)) {
		t.Fatal("cudart for CUDA 13 counted as the runtime of the 12.4 build")
	}
	touch(t, dir, "cudart64_12.dll")
	if !l.installedOn(goosWindows, "amd64", driverAt(12, 5)) {
		t.Fatal("llama libs plus cudart64_12.dll not counted as installed")
	}
	if l.installedOn(goosWindows, "amd64", driverAt(11, 8)) {
		t.Fatal("unsupported driver counted as installed; provisionLibs must get to report it")
	}
	vk := &LlamaLibs{libDir: t.TempDir(), backend: backendVulkan, version: defaultLlamaVersion}
	touch(t, vk.libDir, llamaLibName())
	if !vk.installedOn(goosWindows, "amd64", noDriver) {
		t.Fatal("vulkan install needs no CUDA runtime")
	}
}

// TestFetchReleaseAllArchives serves a llama zip and a cudart zip, the shape
// of a Windows CUDA release, and checks both land. The llama lib uses the
// host's name so llamaLibsInstalled sees it.
func TestFetchReleaseAllArchives(t *testing.T) {
	src := t.TempDir()
	lib := llamaLibName()
	writeZip(t, filepath.Join(src, "llama.zip"), []string{lib}, map[string]string{lib: "L"})
	writeZip(t, filepath.Join(src, "cudart.zip"), []string{"cudart64_12.dll"}, map[string]string{"cudart64_12.dll": "C"})
	srv := httptest.NewServer(http.FileServer(http.Dir(src)))
	t.Cleanup(srv.Close)
	rel := llamaRelease{baseURL: srv.URL, archives: []string{"llama.zip", "cudart.zip"}, marker: "cudart64_12.dll"}
	libDir := t.TempDir()
	if err := fetchRelease(rel, libDir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{lib, "cudart64_12.dll"} {
		if !fileExists(libDir, name) {
			t.Fatalf("%s not extracted into the lib dir", name)
		}
	}
	rel.archives = []string{"llama.zip", "missing.zip"}
	err := fetchRelease(rel, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), srv.URL+"/missing.zip") {
		t.Fatalf("unpublished archive: error %v should name its URL", err)
	}
}
