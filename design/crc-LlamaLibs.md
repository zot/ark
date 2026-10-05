# LlamaLibs
**Requirements:** R2966, R2968-2970, R3485-3493

Provisions the llama.cpp shared libraries the yzma engine `dlopen`s at
runtime. Selects the backend, downloads the matching prebuilt release into the
lib directory beside the database, and reports clearly when libs are missing.

## Knows
- libDir: string — `[embedding] lib_dir`, default `<dir>/lib` (R2966)
- backend: string — `[embedding] backend`: auto|cpu|vulkan|cuda|metal|rocm (R3487)
- version: string — `[embedding] llama_version`, pinned llama.cpp build (R2968)
- msvcRuntimeURL / msvcRuntimeSHA256 / msvcRuntimeDLLs (package constants): ark's
  published Windows x64 MSVC runtime zip (14.51.36247), its pinned hash, and the
  three DLL names the llama.cpp DLLs import (R3486)
- winCUDABuilds / linuxCUDABuilds (package tables, newest first): the CUDA
  builds of the pinned release, each with the minimum driver CUDA version it
  needs and the fragment its asset names carry. Windows: 13.3, 12.4. Linux
  (hybridgroup builder): `cuda-13` (13.0), `cuda` (12.0). Written for
  `defaultLlamaVersion`; another `llama_version` reuses the names (R3489,
  R3490, R3492)

Implemented with a **slim, dependency-free downloader** (a plain `net/http`
fetch + stdlib `archive/tar`/`archive/zip` extraction), **not** yzma's
`pkg/download` — that package drags in go-getter plus the AWS and GCP SDKs to
fetch a single tarball, none of which would link into the binary's value. The
release-asset URL/filename map is ported from yzma so the same archives are
pulled.

## Does
- Provision(force): if libDir lacks the libs (`installed`), resolve the
  (platform, backend, version) release via `llamaAsset` and `fetchRelease`
  it into libDir (`downloadAndExtract` → tar.gz or zip); idempotent —
  skipped when libs are present unless `force` requests a re-download. Runs
  during `ark setup` (best-effort) and as `ark embed install`. (Model auto-fetch
  is permitted by R2969 but not implemented — the model is configured
  separately; see Gaps.) (R2969) After the libs step — whether it
  downloaded or found the libs already present — it always runs
  ensureMSVCRuntime, so an existing Windows install missing the runtime is
  repaired. (R3486)
- ensureMSVCRuntime(libDir) (package-level): on windows/amd64 only, asks
  `systemDir()` for the system directory and `missingMSVCRuntime` for the
  runtime DLLs present in neither; when any is missing, `fetchMSVCRuntime`
  with the pinned `msvcRuntimeURL`/`msvcRuntimeSHA256`. No-op on every other
  platform (windows/arm64 has no published zip). (R3486)
- missingMSVCRuntime(sysDir, libDir) []string (pure, untagged): which of
  `msvcp140.dll`, `vcruntime140.dll`, `vcruntime140_1.dll` exist in neither
  directory; three stats each. An empty sysDir is not searched. (R3486)
- fetchMSVCRuntime(url, sha256, libDir): `downloadFileVerified` the zip into
  libDir (temp file + rename; a mismatch leaves nothing), `extractZip` it into
  libDir, remove the zip. Every failure names the remedy: retry
  `ark embed install`, or install the Microsoft Visual C++ Redistributable
  (x64). It extracts all three DLLs even when only one was missing, so the
  lib dir holds one consistent runtime version rather than a mix. (R3486)
- systemDir() (package-level, `dllpath_windows.go` / `dllpath_other.go`):
  `windows.GetSystemDirectory()` on Windows; "" elsewhere. (R3486)
- fetchRelease(rel, libDir) (package-level): download and extract every
  archive of `rel` in order via `downloadAndExtract`, then require the llama
  lib and `rel.marker`; a failed download names its URL. (R2969, R3489, R3492)
- installed() → installedOn(goos, arch, driver probe), the injectable form:
  `llamaLibsInstalled(libDir)`, and on Windows with an explicit
  `cuda` backend also the marker of the build `llamaAsset` selects for the
  driver (`cudart64_<major>.dll`); a selection error counts as not installed
  so `provisionLibs` reports it. Other backends and platforms check the
  llama lib only. (R2969, R3493)
- resolveBackend(): explicit backends pass through; `auto`/empty goes to
  `autoBackend(runtime.GOOS, hostGPU)`. (R3487)
- autoBackend(goos, gpuProbe) (pure; probes injected): Windows → Vulkan when
  the probe reports a Vulkan device, else CPU, never CUDA. Elsewhere → CUDA
  (`nvidia-smi` runs), else ROCm (`rocminfo`), else Metal on darwin, else
  Vulkan when a GPU render node exists, else CPU. `hasVulkanDevice` assumes a
  GPU on Windows, so Windows `auto` resolves to Vulkan in practice. (R3487)
- cudaDriverVersion() (package var, injectable): runs plain `nvidia-smi` and
  parses it with `parseCUDAVersion`; false when it is absent or fails. (R3488)
- parseCUDAVersion(out) (pure): the `CUDA Version: X.Y` field as a
  cudaVersion; false when the field is missing. (R3488)
- chooseCUDABuild(builds, driver) (pure): the first (newest) build whose
  minimum the driver meets; otherwise an error naming the driver's CUDA
  version or its absence, the oldest build's minimum, and
  `backend = "vulkan"`. (R3489, R3490, R3491)
- llamaAsset(arch, goos, backend, version, cudaDriver) → llamaRelease (pure;
  the driver probe is called only for `cuda`): the base URL, the archives to
  fetch (llama.cpp build first), and an optional marker file. Windows `cuda`
  → the chosen build's llama zip plus `cudart-llama-bin-win-cuda-<cuda>-x64.zip`,
  marker `cudart64_<major>.dll`; Linux `cuda` → the builder's `cuda-13` or
  `cuda` tarball, no marker. A wrong asset name for a non-default version
  surfaces as the download's HTTP error naming the URL. (R2969, R3489-3492)
- LibDir() string: the directory yzma's `Load()` points at. (R2966)
- requireLlamaLibs(libDir) (package-level, embed.go): when a model is configured
  but libDir has no libs, return a clear error naming `ark embed install` —
  never a silent FTS fallback. (R2970)
- addDllSearchDir(libDir) (package-level, `dllpath_windows.go` /
  `dllpath_other.go`): on Windows, `SetDllDirectoryW(libDir)` so a DLL loaded
  by full path from libDir finds its sibling DLLs (`ggml-base.dll`, the MSVC
  runtime); Windows otherwise searches only the exe's dir, System32 and PATH.
  Process-wide, so it also covers yzma's later backend load. Called by
  `ensureLlamaLoaded` before yzma `Load`; a failure aborts the load with an
  error naming libDir. Never edits PATH. No-op off Windows. (R3485)

## Collaborators
- Config: reads the `[embedding]` lib_dir/backend/llama_version keys
- net/http + archive/tar + archive/zip + compress/gzip (stdlib): the fetch and extract
- embed engine (embed.go): `ensureLlamaLoaded` calls `addDllSearchDir` then
  yzma `Load(LibDir())`; the
  Librarian loads the model only after `requireLlamaLibs` passes
- golang.org/x/sys/windows: `SetDllDirectory`, `GetSystemDirectory` (Windows only)
- modelprovision.go `downloadFileVerified`: the SHA-256-checked fetch for the runtime zip
- Setup (`ark setup`) / `ark embed install`: invoke Provision()

## Sequences
- (none yet)
