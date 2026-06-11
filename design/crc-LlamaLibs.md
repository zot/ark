# LlamaLibs
**Requirements:** R2966, R2967, R2968, R2969, R2970

Provisions the llama.cpp shared libraries the yzma engine `dlopen`s at
runtime. Selects the backend, downloads the matching prebuilt release into the
lib directory beside the database, and reports clearly when libs are missing.

## Knows
- libDir: string — `[embedding] lib_dir`, default `<dir>/lib` (R2966)
- backend: string — `[embedding] backend`: auto|cpu|vulkan|cuda|metal|rocm (R2967)
- version: string — `[embedding] llama_version`, pinned llama.cpp build (R2968)

## Does
- Provision(): if libDir lacks the libs (`download.AlreadyInstalled`), download
  the (platform, backend, version) release into libDir via yzma `pkg/download`;
  idempotent — skipped when present unless an upgrade is requested; may also
  fetch the GGUF model when absent. Runs during `ark setup` and as a standalone
  command. (R2969)
- resolveBackend(): when backend is `auto`, detect CUDA (`download.HasCUDA`),
  else ROCm (`download.HasROCm`), else Metal on darwin, else Vulkan when a
  device exists, else CPU. (R2967)
- LibDir() string: the directory yzma's `Load()` points at. (R2966)
- requireLibs(): when a model is configured but libDir has no libs, return a
  clear error naming the provisioning command — never a silent FTS fallback.
  (R2970)

## Collaborators
- Config: reads the `[embedding]` lib_dir/backend/llama_version keys
- yzma `pkg/download`: AlreadyInstalled, Get/GetWithContext, HasCUDA, HasROCm, GetModel
- Librarian: loads the model only after libs are provisioned; calls `Load(LibDir())`
- Setup (`ark setup`): invokes Provision() during bootstrap

## Sequences
- (none yet)
