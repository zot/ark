# Test Design: LlamaLibs
**Source:** crc-LlamaLibs.md

Archive extraction over temp files: no network, no yzma load. The failure is
invisible when violated: an extractor that drops every entry still returns
nil, so provisioning "completes" and only the post-check notices an empty lib
dir. Found live on Windows 2026-10-04: ggml-org's Windows zips are flat
(`llama.dll` at the root), unlike the Linux tarballs' `llama-bXXXX/` wrapper.

## Test: a flat zip extracts its entries as-is
**Purpose:** extractZip keeps top-level entries when the archive has no shared release directory (R2969)
**Input:** a zip whose entries are `llama.dll` and `ggml.dll` at the root
**Expected:** both files exist in dest with their contents
**Refs:** crc-LlamaLibs.md
**Code:** llamalibs_test.go
**Fire alarm:** make extractZip flatten unconditionally (the pre-fix behavior); the test goes red because dest is empty
**Inject:** llamalibs.go:extractZip

## Test: a wrapped zip strips its release directory
**Purpose:** extractZip still flattens a zip whose entries all sit under one top-level directory (R2969)
**Input:** a zip with `llama-b1/` and `llama-b1/llama.dll`
**Expected:** `llama.dll` lands directly in dest, with no `llama-b1/` directory created
**Refs:** crc-LlamaLibs.md
**Code:** llamalibs_test.go

## Test: the lib dir joins the Windows DLL search path
**Purpose:** addDllSearchDir makes libDir the process DLL directory, so `lib\ggml.dll` resolves `lib\ggml-base.dll` beside it (R3485). Invisible when violated off the VM: nothing on Linux loads a DLL, and on Windows the failure only reads as "The specified module could not be found" at embed time.
**Input:** Windows only (`//go:build windows`). A temp dir passed to addDllSearchDir; the default search order is restored afterwards with `SetDllDirectoryW(NULL)`.
**Expected:** addDllSearchDir returns nil and `GetDllDirectoryW` reports the temp dir
**Refs:** crc-LlamaLibs.md
**Code:** dllpath_windows_test.go
**Fire alarm:** make the Windows addDllSearchDir return nil without calling SetDllDirectory (the pre-fix behavior); the test goes red with GetDllDirectoryW reporting an empty directory instead of the temp dir
**Inject:** dllpath_windows.go:addDllSearchDir

## Test: the MSVC runtime is missing only when in neither directory
**Purpose:** missingMSVCRuntime reports a runtime DLL as missing only when neither the system directory nor libDir holds it (R3486). Invisible when violated in the over-reporting direction: an extra fetch still succeeds.
**Input:** temp sysDir and libDir: (a) both empty; (b) `msvcp140.dll` in sysDir, `vcruntime140.dll` in libDir; (c) all three spread across the two
**Expected:** (a) all three names; (b) only `vcruntime140_1.dll`; (c) empty
**Refs:** crc-LlamaLibs.md
**Code:** llamalibs_test.go
**Fire alarm:** make missingMSVCRuntime ignore sysDir (prefix the sysDir check with `false &&`); red at case (b) with `msvcp140.dll` reported missing although System32 holds it
**Inject:** llamalibs.go:missingMSVCRuntime
**Pulled:** 2026-10-04 — rang: "two present: got [msvcp140.dll vcruntime140_1.dll], want [vcruntime140_1.dll]"

## Test: a verified runtime zip lands in the lib dir
**Purpose:** fetchMSVCRuntime downloads, verifies and extracts the flat runtime zip into libDir and leaves no zip or temp file behind (R3486)
**Input:** an httptest server serving a flat zip of the three DLL names with dummy contents; the correct SHA-256 of that zip
**Expected:** nil error; the three DLLs exist in libDir with their contents; libDir holds nothing else
**Refs:** crc-LlamaLibs.md
**Code:** llamalibs_test.go

## Test: a runtime zip with the wrong hash writes nothing
**Purpose:** a checksum mismatch aborts with an error naming the remedy and extracts no DLL into libDir (R3486). Invisible when violated: an unverified zip extracts fine, so only the hash check stands between a tampered download and a DLL Windows will load.
**Input:** the same server; a SHA-256 of other bytes
**Expected:** an error mentioning `ark embed install` and the Visual C++ Redistributable; libDir holds no DLL and no leftover file
**Refs:** crc-LlamaLibs.md
**Code:** llamalibs_test.go
**Fire alarm:** skip the hash check: drop the checksum-mismatch return in downloadFileVerified, so the unverified zip is renamed into place and extracted; the test goes red with a nil error and the three DLLs present in libDir
**Inject:** modelprovision.go:downloadFileVerified
**Pulled:** 2026-10-04 — rang: TestFetchMSVCRuntimeBadHash "wrong hash accepted" (injected `if false && !strings.EqualFold(got, want)`)

## Test: Windows auto never picks CUDA
**Purpose:** on Windows, `auto` resolves to Vulkan whenever a Vulkan device is present, NVIDIA included, and to CPU otherwise; CUDA comes only from an explicit `backend = "cuda"` (R3487). Invisible when violated: the old order picked CUDA on any NVIDIA machine and failed only at download time on the user's Windows box, never on Linux CI.
**Input:** autoBackend("windows", probe) with injected detectors: NVIDIA + Vulkan device; Vulkan only; NVIDIA without a Vulkan device; no GPU
**Expected:** vulkan, vulkan, cpu, cpu
**Refs:** crc-LlamaLibs.md
**Code:** llamalibs_test.go
**Fire alarm:** restore the old behavior on Windows: in autoBackend, return cuda when p.cuda() reports NVIDIA before the Vulkan check; red at "nvidia with vulkan device" with auto = "cuda"
**Inject:** llamalibs.go:autoBackend
**Pulled:** 2026-10-04 — rang: `windows nvidia with vulkan device: auto = "cuda", want "vulkan"` (injected `if p.cuda() { return backendCUDA }` ahead of the Vulkan check)

## Test: Linux and macOS auto keep their order
**Purpose:** off Windows, `auto` still detects CUDA, else ROCm, else Metal on macOS, else Vulkan, else CPU (R3487)
**Input:** autoBackend("linux", probe) for NVIDIA, ROCm, Vulkan-only and no GPU; autoBackend("darwin") with nothing detected
**Expected:** cuda, rocm, vulkan, cpu; metal
**Refs:** crc-LlamaLibs.md
**Code:** llamalibs_test.go

## Test: the driver's CUDA version parses from nvidia-smi
**Purpose:** parseCUDAVersion reads the `CUDA Version: X.Y` field of plain nvidia-smi output, not the driver version beside it (R3488)
**Input:** the real header line from the Windows VM (`| NVIDIA-SMI 556.12 ... Driver Version: 556.12 ... CUDA Version: 12.5 |`); a major-only field; a line with no CUDA Version field
**Expected:** 12.5; 13.0; not found
**Refs:** crc-LlamaLibs.md
**Code:** llamalibs_test.go
**Fire alarm:** loosen cudaVersionRE to `Version:\s*([0-9]+)(?:\.([0-9]+))?`, so it matches the Driver Version field first; red with the sample parsing as 556.12
**Inject:** llamalibs.go:cudaVersionRE
**Pulled:** 2026-10-04 — rang: "parse sample: got 556.12 ok=true, want 12.5"

## Test: Windows CUDA picks the newest build the driver supports, with its runtime
**Purpose:** with an explicit cuda backend on Windows, llamaAsset picks the newest build the driver supports and fetches its cudart zip too, with `cudart64_<major>.dll` as the marker (R3489)
**Input:** llamaAsset("amd64", "windows", "cuda", b9592) with driver probes 12.5, 13.4, 13.3, 13.0
**Expected:** 12.5 → `llama-b9592-bin-win-cuda-12.4-x64.zip` + `cudart-llama-bin-win-cuda-12.4-x64.zip`, marker `cudart64_12.dll`; 13.4 and 13.3 → the 13.3 pair, marker `cudart64_13.dll`; 13.0 → the 12.4 pair; base URL the ggml-org release
**Refs:** crc-LlamaLibs.md
**Code:** llamalibs_test.go
**Fire alarm:** make chooseCUDABuild ignore the driver and return the newest build (`return builds[0], nil` at the top); red at driver 12.5 with the 13.3 pair chosen
**Inject:** llamalibs.go:chooseCUDABuild
**Pulled:** 2026-10-04 — rang: "driver 12.5: got [llama-b9592-bin-win-cuda-13.3-x64.zip cudart-llama-bin-win-cuda-13.3-x64.zip] marker \"cudart64_13.dll\""

## Test: a driver too old for every CUDA build is a clear error
**Purpose:** when the driver supports no build, or reports no version, the error names the driver's version (or its absence), the minimum a build needs, and `backend = "vulkan"` (R3491)
**Input:** driver 11.8 on Windows and Linux; driver 12.2 on Windows; a probe reporting no version
**Expected:** errors containing 11.8 / 12.2, the minimum (12.4 on Windows, 12.0 on Linux), and `backend = "vulkan"`; the no-version error says "no CUDA version"
**Refs:** crc-LlamaLibs.md
**Code:** llamalibs_test.go

## Test: Linux CUDA picks cuda-13 or the unnumbered CUDA 12 build
**Purpose:** on Linux the builder's `cuda-13` build serves drivers with CUDA major 13 or newer, the unnumbered `cuda` build serves major 12, on x64 and arm64 (R3490)
**Input:** llamaAsset(arch, "linux", "cuda", b9592) for amd64 13.0, amd64 12.5, arm64 13.2, arm64 12.0
**Expected:** `ubuntu-cuda-13-x64`, `ubuntu-cuda-x64`, `ubuntu-cuda-13-arm64`, `ubuntu-cuda-arm64` tarballs from the hybridgroup builder, no marker
**Refs:** crc-LlamaLibs.md
**Code:** llamalibs_test.go
**Fire alarm:** make cudaVersion.atLeast strict on the minor (`v.minor > min.minor`), so a driver exactly at a build's minimum is refused; red at amd64 13.0 with the unnumbered CUDA 12 build chosen
**Inject:** llamalibs.go:cudaVersion.atLeast
**Pulled:** 2026-10-04 — rang: "amd64 driver 13.0: got … [llama-b9592-bin-ubuntu-cuda-x64.tar.gz]", and arm64 12.0 refused

## Test: a Windows CUDA lib dir without its runtime is not installed
**Purpose:** on Windows with an explicit cuda backend, the lib dir counts as installed only with the selected build's `cudart64_<major>.dll` beside the llama lib, so a half install is provisioned again; Vulkan needs no runtime (R3493)
**Input:** installedOn("windows", "amd64", probe) over a temp lib dir: empty; the llama lib only; plus `cudart64_13.dll` with driver 12.5; plus `cudart64_12.dll`; the same dir with driver 11.8; a Vulkan lib dir with only the llama lib. The llama lib uses the host's name (`libllama.so` on Linux), since the marker logic is what is under test.
**Expected:** false, false, false, true, false; true
**Refs:** crc-LlamaLibs.md
**Code:** llamalibs_test.go
**Fire alarm:** drop the marker check in installedOn (`return true` after the llama-lib check); red at "llama libs without cudart counted as a complete CUDA install"
**Inject:** llamalibs.go:LlamaLibs.installedOn
**Pulled:** 2026-10-04 — rang: "llama libs without cudart counted as a complete CUDA install"

## Test: every archive of a release lands in the lib dir
**Purpose:** fetchRelease downloads and extracts each archive of a release, so a Windows CUDA install gets the cudart zip as well as the llama zip, then checks the llama lib and the marker; an archive the server does not publish fails with its URL (R2969, R3489, R3492). Invisible when violated: fetching only the first archive still extracts a working-looking llama.dll.
**Input:** an httptest file server with `llama.zip` (the host's llama lib name) and `cudart.zip` (`cudart64_12.dll`); a release naming both, marker `cudart64_12.dll`; then a release naming `missing.zip`
**Expected:** nil error and both files in the lib dir; then an error containing the server URL of `missing.zip`
**Refs:** crc-LlamaLibs.md
**Code:** llamalibs_test.go
**Fire alarm:** fetch only the first archive (`range rel.archives[:1]`); red with "provision completed but cudart64_12.dll is missing"
**Inject:** llamalibs.go:fetchRelease
**Pulled:** 2026-10-04 — rang: "provision completed but cudart64_12.dll is missing from …/002"
