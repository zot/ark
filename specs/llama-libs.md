# llama.cpp library provisioning

**Related:** `yzma-embedding.md` (the engine that loads these libs),
`config.md` (the `[embedding]` keys).

## Motivation

With the yzma engine, the ark binary is pure Go and carries no llama.cpp code;
it `dlopen`s a llama.cpp **shared library** at runtime. Rather than make a user
hunt down or build the right `.so`/`.dylib`/`.dll` for their machine, ark
provisions them itself — fetching the official prebuilt llama.cpp release for
the detected platform and chosen backend into a directory beside the database.
The user downloads one binary; ark assembles the rest.

## Behavior

### Lib directory
- `[embedding] lib_dir` (default `<dir>/lib`, beside the index) holds the
  llama.cpp shared libs. The engine loads from there at startup.
- On Windows, a DLL loaded by full path does not get its own folder searched
  for *its* dependencies (Windows looks in the executable's directory,
  System32 and `PATH`), so `lib\ggml.dll` could not find `lib\ggml-base.dll`
  or the MSVC runtime beside it. Before loading, ark therefore adds `lib_dir`
  to the process DLL search path (`SetDllDirectoryW`). This is process-wide,
  so it also covers the backend DLLs llama.cpp loads later. If the call
  fails, loading fails with an error naming `lib_dir`. Ark does not edit
  `PATH`. Other platforms need no step.

### MSVC runtime (Windows)
- Every llama.cpp DLL ark provisions on Windows imports three Microsoft
  Visual C++ runtime DLLs: `msvcp140.dll`, `vcruntime140.dll` and
  `vcruntime140_1.dll`. A clean Windows install has none of them, and
  without them `ggml.dll` fails to load. (The `api-ms-win-crt-*` imports are
  the Universal CRT, part of Windows 10 and later, and need nothing.)
- Most machines already have the runtime, so ark fetches it only when it is
  missing. On every provisioning run, including one where the llama.cpp libs
  are already present (so an existing install gets repaired), ark checks the
  system directory (`GetSystemDirectory`, normally `System32`) and `lib_dir`
  for each of the three. When any one is in neither, ark downloads its own
  published runtime zip (Microsoft-signed DLLs, a pinned version, checked
  against a pinned SHA-256) and extracts the three DLLs into `lib_dir`, where
  the DLL search path above finds them. When all three are present nothing is
  fetched, `force` included.
- A failed download or a checksum mismatch aborts provisioning with an error
  saying what failed and how to fix it: retry `ark embed install`, or install
  the Microsoft Visual C++ Redistributable (x64). Nothing from a mismatched
  download is extracted.
- Only an x64 runtime zip is published. On Windows on ARM64 the step does
  nothing, and other platforms need no step.

### Backend selection
- `[embedding] backend` chooses the llama.cpp build: `cpu`, `vulkan`, `cuda`,
  `metal`, `rocm`, or `auto`.
- `auto` detects the best available. On Linux and macOS: CUDA if present,
  else ROCm, else Metal on macOS, else Vulkan when a Vulkan device exists,
  else CPU. The Steam Deck pins `vulkan` explicitly.
- On Windows, `auto` picks Vulkan whenever a Vulkan device is present, NVIDIA
  GPUs included, and CPU otherwise. It never picks CUDA there: ark's embedding
  model is small, so CUDA's speedup over Vulkan is modest, while the CUDA
  build plus its runtime is roughly sixteen times the download (hundreds of
  MB against tens). Windows gets CUDA only from an explicit `backend = "cuda"`.

### CUDA builds
- A CUDA build only loads when the NVIDIA driver supports its CUDA version.
  The driver's maximum is the `CUDA Version: X.Y` field of plain
  `nvidia-smi` output, and ark reads it before choosing a CUDA build.
- On Windows, ggml-org publishes several CUDA builds per release. For the
  pinned release (`b9592`) these are CUDA 13.3 and CUDA 12.4. Ark picks the
  newest one the driver supports (driver 13.3 or newer gets 13.3, 12.4 or
  newer gets 12.4) and downloads two archives into `lib_dir`: the llama.cpp
  build (`llama-<build>-bin-win-cuda-<cuda>-x64.zip`) and the matching CUDA
  runtime (`cudart-llama-bin-win-cuda-<cuda>-x64.zip`, which carries
  `cudart64_<major>.dll` and the cuBLAS DLLs). The CUDA build does not load
  without that runtime.
- On Linux, CUDA builds come from the hybridgroup builder. A driver whose
  CUDA major version is 13 or newer gets the `cuda-13` build; major 12 gets
  the unnumbered `cuda` build (CUDA 12). Both exist for x64 and arm64.
- When the driver supports none of the builds, or `nvidia-smi` reports no
  CUDA version, provisioning fails with an error naming the driver's CUDA
  version (or its absence), the minimum CUDA version a build needs, and the
  alternative: set `backend = "vulkan"`.
- The CUDA build table is written for the pinned release. A `llama_version`
  set to another build uses the same asset names; when that build publishes
  different ones, the download fails with an error naming the URL it tried.

### Version pin
- `[embedding] llama_version` names the llama.cpp release build to provision
  (e.g. `b9592`), kept within yzma's tested range so the loaded ABI matches.
  Pinning (vs. always-latest) keeps release builds reproducible.

### Provisioning
- A provisioning step downloads the libs for `(platform, backend, version)`
  into `lib_dir`. It runs as part of `ark setup` and is also available as a
  standalone command for re-provisioning or switching backends.
- Provisioning is idempotent: if `lib_dir` already holds the libs it is
  skipped, unless an upgrade is requested.
- On Windows with `backend = "cuda"`, the libs count as present only when
  `lib_dir` also holds the CUDA runtime of the build the driver selects
  (`cudart64_12.dll` for the 12.4 build, `cudart64_13.dll` for 13.3). A lib
  dir with the llama.cpp libs but no runtime, or a runtime for a different
  CUDA major, is provisioned again.
- Provisioning may also fetch the GGUF embedding model when absent.

### Missing-libs behavior
- If a `model` is configured but `lib_dir` has no libs, embedding operations
  fail with a clear error naming the provisioning command — not a silent
  drop to FTS-only. (No `model` configured remains the intended FTS-only
  path.)

## Out of scope
- Lazy provisioning on first embedding use (v1 provisions explicitly via the
  command / `ark setup`).
