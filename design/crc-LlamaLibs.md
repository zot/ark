# LlamaLibs
**Requirements:** R2966, R2967, R2968, R2969, R2970

Provisions the llama.cpp shared libraries the yzma engine `dlopen`s at
runtime. Selects the backend, downloads the matching prebuilt release into the
lib directory beside the database, and reports clearly when libs are missing.

## Knows
- libDir: string — `[embedding] lib_dir`, default `<dir>/lib` (R2966)
- backend: string — `[embedding] backend`: auto|cpu|vulkan|cuda|metal|rocm (R2967)
- version: string — `[embedding] llama_version`, pinned llama.cpp build (R2968)

Implemented with a **slim, dependency-free downloader** (a plain `net/http`
fetch + stdlib `archive/tar`/`archive/zip` extraction), **not** yzma's
`pkg/download` — that package drags in go-getter plus the AWS and GCP SDKs to
fetch a single tarball, none of which would link into the binary's value. The
release-asset URL/filename map is ported from yzma so the same archives are
pulled.

## Does
- Provision(force): if libDir lacks the libs (`llamaLibsInstalled`), resolve the
  (platform, backend, version) ggml-org release via `llamaAsset`, download and
  extract it into libDir (`downloadAndExtract` → tar.gz or zip); idempotent —
  skipped when libs are present unless `force` requests a re-download. Runs
  during `ark setup` (best-effort) and as `ark embed install`. (Model auto-fetch
  is permitted by R2969 but not implemented — the model is configured
  separately; see Gaps.) (R2969)
- resolveBackend(): when backend is `auto`/empty, detect CUDA (`nvidia-smi`),
  else ROCm (`rocminfo`), else Metal on darwin, else Vulkan when a GPU render
  node exists, else CPU. (R2967)
- LibDir() string: the directory yzma's `Load()` points at. (R2966)
- requireLlamaLibs(libDir) (package-level, embed.go): when a model is configured
  but libDir has no libs, return a clear error naming `ark embed install` —
  never a silent FTS fallback. (R2970)

## Collaborators
- Config: reads the `[embedding]` lib_dir/backend/llama_version keys
- net/http + archive/tar + archive/zip + compress/gzip (stdlib): the fetch and extract
- embed engine (embed.go): `ensureLlamaLoaded` calls yzma `Load(LibDir())`; the
  Librarian loads the model only after `requireLlamaLibs` passes
- Setup (`ark setup`) / `ark embed install`: invoke Provision()

## Sequences
- (none yet)
