package ark

// LlamaLibs provisions the llama.cpp shared libraries the yzma engine
// dlopens at runtime (embed.go). It selects a backend, downloads the
// matching ggml-org prebuilt release into the lib dir beside the database,
// and reports clearly when libs are missing. Dep-free by design: a plain
// HTTPS fetch + stdlib archive extraction, not yzma's pkg/download (which
// drags in go-getter + the AWS/GCP SDKs for a single tarball fetch).
//
// CRC: crc-LlamaLibs.md | R2966, R3487, R2968, R2969, R2970

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// defaultLlamaVersion pins a known-good llama.cpp build within yzma
// v1.16.1's tested range — the build the migration spike validated on
// Deck/Vulkan. Keeps provisioning reproducible (R2968).
const defaultLlamaVersion = "b9592"

// Backend names accepted by [embedding] backend. R3487
const (
	backendAuto   = "auto"
	backendCPU    = "cpu"
	backendVulkan = "vulkan"
	backendCUDA   = "cuda"
	backendMetal  = "metal"
	backendROCm   = "rocm"
)

// GOOS values the backend and asset choices branch on.
const (
	goosWindows = "windows"
	goosLinux   = "linux"
	goosDarwin  = "darwin"
)

// LlamaLibs is the lib provisioner. R2966, R3487, R2968
type LlamaLibs struct {
	libDir  string // resolved lib_dir (R2966)
	backend string // configured backend, "" or "auto" → resolveBackend (R3487)
	version string // pinned llama.cpp build (R2968)
}

// NewLlamaLibs builds a provisioner from the [embedding] config. R2966-R2968
func NewLlamaLibs(cfg EmbeddingConfig, dbPath string) *LlamaLibs {
	version := cfg.LlamaVersion
	if version == "" {
		version = defaultLlamaVersion
	}
	return &LlamaLibs{
		libDir:  cfg.ResolveLibDir(dbPath),
		backend: cfg.Backend,
		version: version,
	}
}

// LibDir is the directory yzma's Load() points at. R2966
func (l *LlamaLibs) LibDir() string { return l.libDir }

// Provision downloads the (platform, backend, version) llama.cpp release
// into the lib dir when it is not already present. Idempotent: a populated
// lib dir is skipped unless force requests a re-download. R2969
//
// The MSVC runtime step runs on every call, after the libs step, so an
// install whose libs are already present still gets its runtime. R3486
func (l *LlamaLibs) Provision(force bool) error {
	if force || !l.installed() {
		if err := l.provisionLibs(); err != nil {
			return err
		}
	}
	return ensureMSVCRuntime(l.libDir)
}

// installed reports whether libDir holds a complete install for the
// configured backend.
// CRC: crc-LlamaLibs.md | R2969, R3493
func (l *LlamaLibs) installed() bool {
	return l.installedOn(runtime.GOOS, runtime.GOARCH, cudaDriverVersion)
}

// installedOn is installed for a given platform and driver probe. The llama
// lib is always required; on Windows an explicit cuda backend also needs the
// CUDA runtime of the build the driver selects, so a lib dir holding the
// llama.cpp DLLs without it is provisioned again. A selection error counts as
// not installed, so provisionLibs reports it.
// CRC: crc-LlamaLibs.md | R2969, R3493
func (l *LlamaLibs) installedOn(goos, arch string, driver func() (cudaVersion, bool)) bool {
	if !llamaLibsInstalled(l.libDir) {
		return false
	}
	if goos != goosWindows || l.backend != backendCUDA {
		return true
	}
	rel, err := llamaAsset(arch, goos, backendCUDA, l.version, driver)
	if err != nil {
		return false
	}
	return rel.marker == "" || fileExists(l.libDir, rel.marker)
}

// provisionLibs downloads and extracts every archive of the llama.cpp
// release.
// CRC: crc-LlamaLibs.md | R2969, R3489
func (l *LlamaLibs) provisionLibs() error {
	backend := l.resolveBackend()
	rel, err := llamaAsset(runtime.GOARCH, runtime.GOOS, backend, l.version, cudaDriverVersion)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(l.libDir, 0o755); err != nil {
		return fmt.Errorf("create lib dir %s: %w", l.libDir, err)
	}
	log.Printf("llamalibs: provisioning %s/%s (%s) → %s: %s", l.version, backend, runtime.GOOS+"/"+runtime.GOARCH, l.libDir, strings.Join(rel.archives, ", "))
	return fetchRelease(rel, l.libDir)
}

// fetchRelease downloads and extracts every archive of rel into libDir, then
// checks that the llama lib and rel's marker landed.
// CRC: crc-LlamaLibs.md | R2969, R3489
func fetchRelease(rel llamaRelease, libDir string) error {
	for _, name := range rel.archives {
		// R3492: a name the release does not publish fails here, naming the URL
		url := rel.baseURL + "/" + name
		if err := downloadAndExtract(url, libDir); err != nil {
			return fmt.Errorf("provision llama.cpp libs from %s: %w", url, err)
		}
	}
	if !llamaLibsInstalled(libDir) {
		return fmt.Errorf("provision completed but %s is missing from %s", llamaLibName(), libDir)
	}
	if rel.marker != "" && !fileExists(libDir, rel.marker) {
		return fmt.Errorf("provision completed but %s is missing from %s", rel.marker, libDir)
	}
	return nil
}

// The MSVC runtime the llama.cpp Windows DLLs import. ark publishes the three
// Microsoft-signed DLLs as a flat zip; the hash pins exactly that zip. Only an
// x64 build is published. R3486
const (
	msvcRuntimeURL    = "https://github.com/zot/ark/releases/download/msvc-runtime-14.51.36247/msvc-runtime-14.51.36247-win-x64.zip"
	msvcRuntimeSHA256 = "c8971390451eb8003035d8f0fa44b27e2fec200e96e5e662bd9d1ecd633b9083"
	msvcRuntimeGOOS   = "windows"
	msvcRuntimeGOARCH = "amd64"
	msvcRuntimeRemedy = "retry `ark embed install`, or install the Microsoft Visual C++ Redistributable (x64)"
)

// msvcRuntimeDLLs are the runtime DLLs every provisioned llama.cpp DLL
// imports (read from their PE import tables). R3486
var msvcRuntimeDLLs = []string{"msvcp140.dll", "vcruntime140.dll", "vcruntime140_1.dll"}

// ensureMSVCRuntime fetches the MSVC runtime into libDir when windows/amd64
// lacks it. A clean Windows has none of the three DLLs and ggml.dll then
// fails to load; most machines already have them, so nothing is fetched
// unless one is missing. A no-op on every other platform.
// CRC: crc-LlamaLibs.md | R3486
func ensureMSVCRuntime(libDir string) error {
	if runtime.GOOS != msvcRuntimeGOOS || runtime.GOARCH != msvcRuntimeGOARCH {
		return nil
	}
	sysDir, err := systemDir()
	if err != nil {
		// Without the system dir only libDir counts; an extra fetch is harmless.
		log.Printf("llamalibs: system directory unknown, checking %s only: %v", libDir, err)
		sysDir = ""
	}
	missing := missingMSVCRuntime(sysDir, libDir)
	if len(missing) == 0 {
		return nil
	}
	log.Printf("llamalibs: MSVC runtime missing (%s); fetching into %s", strings.Join(missing, ", "), libDir)
	if err := fetchMSVCRuntime(msvcRuntimeURL, msvcRuntimeSHA256, libDir); err != nil {
		return err
	}
	if still := missingMSVCRuntime(sysDir, libDir); len(still) > 0 {
		return fmt.Errorf("MSVC runtime fetched but %s still missing from %s; %s", strings.Join(still, ", "), libDir, msvcRuntimeRemedy)
	}
	return nil
}

// missingMSVCRuntime returns the runtime DLLs present in neither sysDir nor
// libDir. An empty sysDir is not searched. R3486
func missingMSVCRuntime(sysDir, libDir string) []string {
	var missing []string
	for _, name := range msvcRuntimeDLLs {
		if fileExists(libDir, name) || (sysDir != "" && fileExists(sysDir, name)) {
			continue
		}
		missing = append(missing, name)
	}
	return missing
}

func fileExists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

// fetchMSVCRuntime downloads the runtime zip at url, verifies it against
// wantSHA (SHA-256), and extracts it into libDir. All three DLLs are extracted even when
// only one was missing, so libDir holds one consistent runtime version. The
// download goes through a temp file in libDir and a mismatch never reaches
// the extract, so a bad zip writes no DLL. R3486
func fetchMSVCRuntime(url, wantSHA, libDir string) error {
	zipPath := filepath.Join(libDir, path.Base(url))
	defer os.Remove(zipPath)
	if err := downloadFileVerified(url, zipPath, wantSHA); err != nil {
		return fmt.Errorf("fetch MSVC runtime: %w; %s", err, msvcRuntimeRemedy)
	}
	if err := extractZip(zipPath, libDir); err != nil {
		return fmt.Errorf("extract MSVC runtime into %s: %w; %s", libDir, err, msvcRuntimeRemedy)
	}
	return nil
}

// resolveBackend maps "auto"/"" to a concrete backend for this host;
// an explicit backend passes through. R3487
func (l *LlamaLibs) resolveBackend() string {
	switch l.backend {
	case "", backendAuto:
		return autoBackend(runtime.GOOS, hostGPU)
	default:
		return l.backend
	}
}

// gpuProbe is the set of GPU detectors autoBackend consults, injectable
// for tests.
type gpuProbe struct {
	cuda, rocm, vulkan func() bool
}

// hostGPU probes the real machine.
var hostGPU = gpuProbe{cuda: hasCUDA, rocm: hasROCm, vulkan: hasVulkanDevice}

// autoBackend picks the backend "auto" means on goos. Windows prefers Vulkan
// whenever a Vulkan device exists, NVIDIA included, and never picks CUDA: the
// embedding model is small, so CUDA's gain is modest at ~16x the download.
// Elsewhere: CUDA, else ROCm, else Metal on darwin, else Vulkan when a GPU
// device exists, else CPU.
// CRC: crc-LlamaLibs.md | R3487
func autoBackend(goos string, p gpuProbe) string {
	if goos == goosWindows {
		if p.vulkan() {
			return backendVulkan
		}
		return backendCPU
	}
	switch {
	case p.cuda():
		return backendCUDA
	case p.rocm():
		return backendROCm
	case goos == goosDarwin:
		return backendMetal
	case p.vulkan():
		return backendVulkan
	default:
		return backendCPU
	}
}

// nvidiaSMI is the NVIDIA driver's status tool; it reports the driver's
// maximum CUDA version. R3488
const nvidiaSMI = "nvidia-smi"

func hasCUDA() bool {
	if runtime.GOOS == goosDarwin {
		return false
	}
	return commandSucceeds(nvidiaSMI)
}

// cudaVersion is a CUDA major.minor version.
type cudaVersion struct{ major, minor int }

// atLeast reports whether v is min or newer.
func (v cudaVersion) atLeast(min cudaVersion) bool {
	return v.major > min.major || (v.major == min.major && v.minor >= min.minor)
}

func (v cudaVersion) String() string { return fmt.Sprintf("%d.%d", v.major, v.minor) }

// cudaVersionRE matches the driver's maximum CUDA version in plain nvidia-smi
// output, e.g. "CUDA Version: 12.5". R3488
var cudaVersionRE = regexp.MustCompile(`CUDA Version:\s*([0-9]+)(?:\.([0-9]+))?`)

// parseCUDAVersion extracts the driver's maximum CUDA version from nvidia-smi
// output; false when the field is absent.
// CRC: crc-LlamaLibs.md | R3488
func parseCUDAVersion(out string) (cudaVersion, bool) {
	m := cudaVersionRE.FindStringSubmatch(out)
	if m == nil {
		return cudaVersion{}, false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2]) // "" (no minor) reads as 0
	return cudaVersion{major, minor}, true
}

// cudaDriverVersion runs plain nvidia-smi and returns the driver's maximum
// CUDA version; false when nvidia-smi is absent, fails, or prints none.
// CRC: crc-LlamaLibs.md | R3488
func cudaDriverVersion() (cudaVersion, bool) {
	if runtime.GOOS == goosDarwin {
		return cudaVersion{}, false
	}
	if _, err := exec.LookPath(nvidiaSMI); err != nil {
		return cudaVersion{}, false
	}
	out, err := exec.Command(nvidiaSMI).Output()
	if err != nil {
		return cudaVersion{}, false
	}
	return parseCUDAVersion(string(out))
}

// cudaBuild is one published CUDA build of the pinned llama.cpp release.
type cudaBuild struct {
	min cudaVersion // oldest driver CUDA version the build loads on
	tag string      // the fragment the build's asset names carry
}

// The CUDA builds of defaultLlamaVersion, newest first. Another
// llama_version reuses these names; if its release publishes different ones
// the download fails naming the URL. R3489, R3490, R3492
var (
	winCUDABuilds   = []cudaBuild{{cudaVersion{13, 3}, "13.3"}, {cudaVersion{12, 4}, "12.4"}}
	linuxCUDABuilds = []cudaBuild{{cudaVersion{13, 0}, "cuda-13"}, {cudaVersion{12, 0}, "cuda"}}
)

// CUDA asset names. The Windows CUDA runtime ships as its own zip, named
// without the build tag, and carries cudart64_<major>.dll. R3489, R3490
const (
	winCUDALlamaAsset  = "llama-%s-bin-win-cuda-%s-x64.zip"     // version, cuda
	winCUDARTAsset     = "cudart-llama-bin-win-cuda-%s-x64.zip" // cuda
	winCUDARTMarker    = "cudart64_%d.dll"                      // cuda major
	linuxCUDALlamaFile = "llama-%s-bin-ubuntu-%s-%s.tar.gz"     // version, tag, arch
	cudaAlternative    = "set `[embedding] backend = \"vulkan\"` (or update the NVIDIA driver)"
)

// chooseCUDABuild picks the newest build the driver probe supports (a nil
// probe reads as no version). When none fits, or the driver reported no
// version, the error names the driver's version, the minimum a build needs,
// and the Vulkan alternative.
// CRC: crc-LlamaLibs.md | R3489, R3490, R3491
func chooseCUDABuild(builds []cudaBuild, probe func() (cudaVersion, bool)) (cudaBuild, error) {
	var driver cudaVersion
	ok := false
	if probe != nil {
		driver, ok = probe()
	}
	if ok {
		for _, b := range builds {
			if driver.atLeast(b.min) {
				return b, nil
			}
		}
	}
	oldest := builds[len(builds)-1].min
	if !ok {
		return cudaBuild{}, fmt.Errorf("backend %q: %s reported no CUDA version, and the llama.cpp CUDA builds need CUDA %s or newer; %s", backendCUDA, nvidiaSMI, oldest, cudaAlternative)
	}
	return cudaBuild{}, fmt.Errorf("backend %q: the NVIDIA driver supports CUDA %s, but the llama.cpp CUDA builds need CUDA %s or newer; %s", backendCUDA, driver, oldest, cudaAlternative)
}

func hasROCm() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	return commandSucceeds("rocminfo")
}

// hasVulkanDevice reports whether a GPU render node is present (Linux) or
// assumes a GPU on Windows. A dep-free stand-in for probing the Vulkan ICD.
func hasVulkanDevice() bool {
	switch runtime.GOOS {
	case "windows":
		return true
	case "linux":
		nodes, _ := filepath.Glob("/dev/dri/renderD*")
		return len(nodes) > 0
	default:
		return false
	}
}

func commandSucceeds(name string, args ...string) bool {
	if _, err := exec.LookPath(name); err != nil {
		return false
	}
	return exec.Command(name, args...).Run() == nil
}

// llamaRelease is what provisioning fetches for one (platform, backend,
// version): archives under baseURL, the llama.cpp build first, and an
// optional marker file whose presence proves the install complete.
type llamaRelease struct {
	baseURL  string
	archives []string
	marker   string
}

// llamaAsset maps (arch, os, backend, version) to the ggml-org (or
// hybridgroup builder, for arch/backend combos ggml-org lacks) release.
// Ported from yzma's download package so the slim fetcher pulls the same
// archives. cudaDriver is consulted only for the cuda backend. R2969
// CRC: crc-LlamaLibs.md | R3489, R3490, R3491, R3492
func llamaAsset(arch, goos, backend, version string, cudaDriver func() (cudaVersion, bool)) (llamaRelease, error) {
	const ggml = "https://github.com/ggml-org/llama.cpp/releases/download"
	const builder = "https://github.com/hybridgroup/llama-cpp-builder/releases/download"
	rel := llamaRelease{baseURL: ggml + "/" + version}
	one := func(base, filename string) (llamaRelease, error) {
		return llamaRelease{baseURL: base, archives: []string{filename}}, nil
	}
	bad := func(reason string) (llamaRelease, error) {
		return llamaRelease{}, fmt.Errorf("no prebuilt llama.cpp libs for %s/%s/%s: %s", goos, arch, backend, reason)
	}

	switch goos {
	case goosLinux:
		switch backend {
		case backendCPU:
			if arch == "arm64" {
				return one(builder+"/"+version, fmt.Sprintf("llama-%s-bin-ubuntu-cpu-arm64.tar.gz", version))
			}
			return one(rel.baseURL, fmt.Sprintf("llama-%s-bin-ubuntu-x64.tar.gz", version))
		case backendCUDA:
			b, err := chooseCUDABuild(linuxCUDABuilds, cudaDriver)
			if err != nil {
				return llamaRelease{}, err
			}
			archName := "x64"
			if arch == "arm64" {
				archName = "arm64"
			}
			return one(builder+"/"+version, fmt.Sprintf(linuxCUDALlamaFile, version, b.tag, archName))
		case backendVulkan:
			if arch == "arm64" {
				return one(builder+"/"+version, fmt.Sprintf("llama-%s-bin-ubuntu-vulkan-arm64.tar.gz", version))
			}
			return one(rel.baseURL, fmt.Sprintf("llama-%s-bin-ubuntu-vulkan-x64.tar.gz", version))
		case backendROCm:
			if arch != "amd64" {
				return bad("linux arm64 rocm unavailable")
			}
			return one(rel.baseURL, fmt.Sprintf("llama-%s-bin-ubuntu-rocm-7.2-x64.tar.gz", version))
		default:
			return bad("unknown backend")
		}
	case goosDarwin:
		switch backend {
		case backendMetal, backendCPU:
			if arch != "arm64" {
				return bad("macOS x64 metal unavailable")
			}
			return one(rel.baseURL, fmt.Sprintf("llama-%s-bin-macos-arm64.tar.gz", version))
		default:
			return bad("unknown backend")
		}
	case goosWindows:
		if arch != "amd64" {
			return bad("windows arm64 unavailable")
		}
		switch backend {
		case backendCPU:
			return one(rel.baseURL, fmt.Sprintf("llama-%s-bin-win-cpu-x64.zip", version))
		case backendVulkan:
			return one(rel.baseURL, fmt.Sprintf("llama-%s-bin-win-vulkan-x64.zip", version))
		case backendCUDA:
			b, err := chooseCUDABuild(winCUDABuilds, cudaDriver)
			if err != nil {
				return llamaRelease{}, err
			}
			rel.archives = []string{
				fmt.Sprintf(winCUDALlamaAsset, version, b.tag),
				fmt.Sprintf(winCUDARTAsset, b.tag),
			}
			rel.marker = fmt.Sprintf(winCUDARTMarker, b.min.major)
			return rel, nil
		case backendROCm:
			return one(rel.baseURL, fmt.Sprintf("llama-%s-bin-win-hip-radeon-x64.zip", version))
		default:
			return bad("unknown backend")
		}
	default:
		return bad("unknown OS")
	}
}

// downloadAndExtract fetches the archive at url and unpacks its shared
// libraries (flattening the top-level release directory) into dest. Handles
// both .tar.gz (Linux/macOS) and .zip (Windows). R2969
func downloadAndExtract(url, dest string) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}

	if strings.HasSuffix(url, ".zip") {
		tmp, err := os.CreateTemp(dest, "llama-*.zip")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		if _, err := io.Copy(tmp, resp.Body); err != nil {
			tmp.Close()
			return err
		}
		tmp.Close()
		return extractZip(tmp.Name(), dest)
	}
	return extractTarGz(resp.Body, dest)
}

// flattenArchivePath strips the leading "llama-bXXXX/" release directory so
// the libs land directly in dest. Returns "" for the bare top-level entry.
func flattenArchivePath(name string) string {
	if i := strings.Index(name, "/"); i != -1 {
		return name[i+1:]
	}
	return ""
}

func extractTarGz(r io.Reader, dest string) error {
	gzr, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := flattenArchivePath(hdr.Name)
		if name == "" {
			continue
		}
		target := filepath.Join(dest, filepath.Clean(name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := extractFile(target, tr, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeSymlink:
			os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil && !os.IsExist(err) {
				return err
			}
		}
	}
	return nil
}

func extractZip(zipPath, dest string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	wrapped := zipWrapped(zr.File)
	for _, f := range zr.File {
		name := f.Name
		if wrapped {
			name = flattenArchivePath(f.Name)
		}
		if name == "" {
			continue
		}
		target := filepath.Join(dest, filepath.Clean(name))
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = extractFile(target, rc, f.Mode())
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// zipWrapped reports whether every entry sits under one shared top-level
// directory. ggml-org's Windows zips are flat (llama.dll at the root), unlike
// the Linux tarballs, so flattening must be decided per archive or every entry
// is dropped and provisioning "completes" with an empty lib dir. R2969
func zipWrapped(files []*zip.File) bool {
	root := ""
	for _, f := range files {
		i := strings.Index(f.Name, "/")
		if i == -1 {
			return false
		}
		if root == "" {
			root = f.Name[:i]
		} else if f.Name[:i] != root {
			return false
		}
	}
	return root != ""
}

func extractFile(target string, r io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}
