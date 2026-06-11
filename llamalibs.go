package ark

// LlamaLibs provisions the llama.cpp shared libraries the yzma engine
// dlopens at runtime (embed.go). It selects a backend, downloads the
// matching ggml-org prebuilt release into the lib dir beside the database,
// and reports clearly when libs are missing. Dep-free by design: a plain
// HTTPS fetch + stdlib archive extraction, not yzma's pkg/download (which
// drags in go-getter + the AWS/GCP SDKs for a single tarball fetch).
//
// CRC: crc-LlamaLibs.md | R2966, R2967, R2968, R2969, R2970

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
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// defaultLlamaVersion pins a known-good llama.cpp build within yzma
// v1.16.1's tested range — the build the migration spike validated on
// Deck/Vulkan. Keeps provisioning reproducible (R2968).
const defaultLlamaVersion = "b9592"

// LlamaLibs is the lib provisioner. R2966, R2967, R2968
type LlamaLibs struct {
	libDir  string // resolved lib_dir (R2966)
	backend string // configured backend, "" or "auto" → resolveBackend (R2967)
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
func (l *LlamaLibs) Provision(force bool) error {
	if !force && llamaLibsInstalled(l.libDir) {
		return nil
	}
	backend := l.resolveBackend()
	baseURL, filename, err := llamaAsset(runtime.GOARCH, runtime.GOOS, backend, l.version)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(l.libDir, 0o755); err != nil {
		return fmt.Errorf("create lib dir %s: %w", l.libDir, err)
	}
	url := baseURL + "/" + filename
	log.Printf("llamalibs: provisioning %s/%s (%s) → %s", l.version, backend, runtime.GOOS+"/"+runtime.GOARCH, l.libDir)
	if err := downloadAndExtract(url, l.libDir); err != nil {
		return fmt.Errorf("provision llama.cpp libs from %s: %w", url, err)
	}
	if !llamaLibsInstalled(l.libDir) {
		return fmt.Errorf("provision completed but %s is missing from %s", llamaLibName(), l.libDir)
	}
	return nil
}

// resolveBackend maps "auto"/"" to a concrete backend by detecting the
// platform GPU: CUDA, else ROCm, else Metal on darwin, else Vulkan when a
// GPU device exists, else CPU. R2967
func (l *LlamaLibs) resolveBackend() string {
	switch l.backend {
	case "", "auto":
		switch {
		case hasCUDA():
			return "cuda"
		case hasROCm():
			return "rocm"
		case runtime.GOOS == "darwin":
			return "metal"
		case hasVulkanDevice():
			return "vulkan"
		default:
			return "cpu"
		}
	default:
		return l.backend
	}
}

func hasCUDA() bool {
	if runtime.GOOS == "darwin" {
		return false
	}
	return commandSucceeds("nvidia-smi")
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

// llamaAsset maps (arch, os, backend, version) to the ggml-org (or
// hybridgroup builder, for arch/backend combos ggml-org lacks) release
// base URL and asset filename. Ported from yzma's download package so the
// slim fetcher pulls the same archives. R2969
func llamaAsset(arch, goos, backend, version string) (baseURL, filename string, err error) {
	const ggml = "https://github.com/ggml-org/llama.cpp/releases/download"
	const builder = "https://github.com/hybridgroup/llama-cpp-builder/releases/download"
	baseURL = ggml + "/" + version

	bad := func(reason string) (string, string, error) {
		return "", "", fmt.Errorf("no prebuilt llama.cpp libs for %s/%s/%s: %s", goos, arch, backend, reason)
	}

	switch goos {
	case "linux":
		switch backend {
		case "cpu":
			if arch == "arm64" {
				return builder + "/" + version, fmt.Sprintf("llama-%s-bin-ubuntu-cpu-arm64.tar.gz", version), nil
			}
			filename = fmt.Sprintf("llama-%s-bin-ubuntu-x64.tar.gz", version)
		case "cuda":
			baseURL = builder + "/" + version
			if arch == "arm64" {
				filename = fmt.Sprintf("llama-%s-bin-ubuntu-cuda-arm64.tar.gz", version)
			} else {
				filename = fmt.Sprintf("llama-%s-bin-ubuntu-cuda-13-x64.tar.gz", version)
			}
		case "vulkan":
			if arch == "arm64" {
				return builder + "/" + version, fmt.Sprintf("llama-%s-bin-ubuntu-vulkan-arm64.tar.gz", version), nil
			}
			filename = fmt.Sprintf("llama-%s-bin-ubuntu-vulkan-x64.tar.gz", version)
		case "rocm":
			if arch != "amd64" {
				return bad("linux arm64 rocm unavailable")
			}
			filename = fmt.Sprintf("llama-%s-bin-ubuntu-rocm-7.2-x64.tar.gz", version)
		default:
			return bad("unknown backend")
		}
	case "darwin":
		switch backend {
		case "metal", "cpu":
			if arch != "arm64" {
				return bad("macOS x64 metal unavailable")
			}
			filename = fmt.Sprintf("llama-%s-bin-macos-arm64.tar.gz", version)
		default:
			return bad("unknown backend")
		}
	case "windows":
		switch backend {
		case "cpu":
			filename = fmt.Sprintf("llama-%s-bin-win-cpu-x64.zip", version)
		case "vulkan":
			filename = fmt.Sprintf("llama-%s-bin-win-vulkan-x64.zip", version)
		case "cuda":
			filename = fmt.Sprintf("llama-%s-bin-win-cuda-13.1-x64.zip", version)
		case "rocm":
			filename = fmt.Sprintf("llama-%s-bin-win-hip-radeon-x64.zip", version)
		default:
			return bad("unknown backend")
		}
		if arch != "amd64" {
			return bad("windows arm64 unavailable")
		}
	default:
		return bad("unknown OS")
	}
	return baseURL, filename, nil
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
	for _, f := range zr.File {
		name := flattenArchivePath(f.Name)
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
