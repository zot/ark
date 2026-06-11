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
- `[embedding] lib_dir` (default `<dir>/lib`, beside the LMDB env) holds the
  llama.cpp shared libs. The engine loads from there at startup.

### Backend selection
- `[embedding] backend` chooses the llama.cpp build: `cpu`, `vulkan`, `cuda`,
  `metal`, `rocm`, or `auto`.
- `auto` detects the best available: CUDA if present, else ROCm, else Metal on
  macOS, else Vulkan when a Vulkan device exists, else CPU. The Steam Deck
  pins `vulkan` explicitly.

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
- Provisioning may also fetch the GGUF embedding model when absent.

### Missing-libs behavior
- If a `model` is configured but `lib_dir` has no libs, embedding operations
  fail with a clear error naming the provisioning command — not a silent
  drop to FTS-only. (No `model` configured remains the intended FTS-only
  path.)

## Out of scope
- Lazy provisioning on first embedding use (v1 provisions explicitly via the
  command / `ark setup`).
