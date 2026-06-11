# Migration: gollama (CGO) → yzma (purego) embedding engine

**Source spike:** `.scratch/YZMA.md` (proven on Steam Deck / Vulkan, 2026-06-11).

## Status (2026-06-11) — IN-FLIGHT, engine landed

- **Landed & validated:** the embedding engine (`embed.go` wraps yzma
  `pkg/llama`; `librarian.go` swapped), the `[embedding]` config table, the
  lib provisioner (`llamalibs.go` + `ark embed install`), go.mod (yzma in,
  gollama out). Smoke test green on Deck/Vulkan. R2961–R2970 satisfied.
- **Provisioner uses a slim, dependency-free downloader**, NOT yzma
  `pkg/download` — that package pulls go-getter + the AWS/GCP SDKs to fetch one
  tarball. A plain `net/http` + stdlib tar/zip fetch (URL map ported from yzma)
  links zero of that weight. `crc-LlamaLibs.md` reflects this.
- **CGO_ENABLED=0 is NOT achieved (R2971/R2972 deferred).** Removing gollama
  removed one CGO dep, but `bmatsuo/lmdb-go` (ark's own store **and**
  `microfts2`) still links C. The pure-Go binary + release sweep are blocked on
  the **LMDB → pure-Go (BBolt) migration** (a separate item) covering both
  modules. The Makefile is therefore left untouched for now.
- **Remaining before `migration-complete`:** (1) the LMDB→BBolt work, then
  R2971/R2972 + the Makefile `release` target; (2) the prose-supersede sweep —
  residual `tag_model`/`embed_tiers` key names and gollama engine descriptions
  across per-feature specs (`recall.md`, `derived-tags.md`, `config-tracking.md`,
  `vector-freshness.md`, `vec-bench.md`, `tag-embeddings.md` static-link note,
  `tag-def-embeddings.md`, `seq-tag-embed.md`, `main.md`) and personal notes.
  The authoritative summary specs (`config.md`, `cli-commands.md`,
  `record-formats.md`, `features.md`) and the copy-pasteable `ark.toml` examples
  are already reconciled.

## Problem

Ark's embedding engine links `github.com/godeps/gollama` → llama.cpp as a
**CGO** static library (our fork's `wrapper.cpp` + `libbinding.a` + ggml
backends). CGO makes ark impossible to cross-compile without a C
cross-toolchain *and* per-platform prebuilt native libs for every target — so
there is no frictionless-style `GOOS/GOARCH` release sweep, and we carry a
gollama fork pinned to a specific llama.cpp version.

## State B (target)

Bind llama.cpp via **`github.com/hybridgroup/yzma`** (`pkg/llama`), which uses
**purego + ffi** to `dlopen` the shared library at *runtime*. The ark binary
compiles `CGO_ENABLED=0` — pure Go — so it cross-compiles freely while keeping
**embedded, in-process, GPU-accelerated** inference. The llama.cpp shared libs
are provisioned at runtime (see `llama-libs.md`), not linked at build time.

The four-bucket adaptive tier design is unchanged — it maps 1:1 onto yzma's
`ContextParams` (`NCtx`/`NBatch`/`NUbatch`/`NSeqMax`). The `n_ubatch` encoder
fix that forced our gollama fork becomes a plain field, so **the fork is
retired**.

## Changes

### Embedding engine
- The embedding engine binds yzma `pkg/llama` instead of gollama. Model load
  sets GPU offload (`NGpuLayers`); each tier is a `ContextParams` with
  `NCtx`/`NBatch`/`NUbatch`/`NSeqMax`, `Embeddings=1`, `PoolingType=Mean`.
- Embedding reads must **copy** the result of `GetEmbeddingsSeq` — it aliases
  llama.cpp's internal buffer (see `chunk-embeddings.md`).
- A pooled encoder context has no KV cache (`GetMemory` is NULL); no
  between-text clear is needed.

### `[embedding]` config section (see `config.md`)
The scattered top-level embedding keys move into a new `[embedding]` table and
are joined by the lib-provisioning keys:
- `model` — **renamed from top-level `tag_model`** (R1274). The single GGUF
  embedding model for chunks, tags, and queries; the `tag_` prefix was
  historical and misleading.
- `tiers` — **renamed from top-level `embed_tiers`** (R1588). Same
  `{ctx, parallel}` entries, same load-time sort (R1594).
- `lib_dir`, `backend`, `llama_version` — new, owned by `llama-libs.md`.

No back-compat shim: there is a single `ark.toml` (ours) and no other users,
so the one config is rewritten directly to the new section. The old top-level
keys are removed outright, not aliased.

### Build & distribution
- Ark builds `CGO_ENABLED=0`. The Makefile drops the `gollama`/cmake/Vulkan
  section, and gains a frictionless-style `release` target (5-platform sweep +
  `ark bundle -src` graft + archives). See `features.md`.

## Supersede at source (Gaps phase)

Old-behavior prose to rewrite/remove so no agent reverts the migration:
- `Makefile` — the `gollama:` / `$(GOLLAMA_DIR)/libbinding.a` cmake+Vulkan
  recipe and the `build: gollama` dependency.
- `CLAUDE.md` / `CLAUDE.local.md` — CGO build notes, the `gollama` workspace
  entry rationale, `-buildvcs=false`-for-cgo framing.
- `config.md`, `requirements.md` — top-level `tag_model` / `embed_tiers`.
- Personal notes: the gollama-fork pattern + the embedding-benchmark memory
  reference the fork; annotate as historical.

## Out of scope (follow-ups)
- Lazy auto-provisioning on first embedding use (v1 provisions via command;
  see `llama-libs.md`).
- Retiring the `godeps/gollama` dependency from `go.work` once parity holds.
