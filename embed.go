package ark

// Embedding engine — binds llama.cpp via yzma (purego/ffi), which dlopens
// the shared library at runtime, so ark compiles CGO_ENABLED=0 and keeps
// in-process GPU inference. This file is the whole yzma surface: the
// once-guarded library load, model/context lifecycle, and the embed paths
// (single + multi-sequence batch). It presents the slice of the old
// gollama API the Librarian relies on, so librarian.go's call sites are
// unchanged by the migration.
//
// CRC: crc-Librarian.md | R2961, R2962, R2963, R2973
// R2961 yzma/purego binding (runtime dlopen, no CGO)
// R2962 tier context = ContextParams (NCtx/NSeqMax/NBatch/NUbatch, Mean pool, GPU offload)
// R2963 copy the aliasing GetEmbeddingsSeq result
// R2973 llama.cpp logging silenced by default; -vvv (verbosity ≥ 3) leaves it on

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	yz "github.com/hybridgroup/yzma/pkg/llama"
)

// llamaLibName is the platform's llama.cpp shared-library filename — the
// sentinel that marks a provisioned lib dir. Mirrors the name yzma's
// downloader installs and dlopens.
func llamaLibName() string {
	switch runtime.GOOS {
	case "windows":
		return "llama.dll"
	case "darwin":
		return "libllama.dylib"
	default:
		return "libllama.so"
	}
}

// llamaLibsInstalled reports whether libDir holds the platform's
// llama.cpp shared libraries.
func llamaLibsInstalled(libDir string) bool {
	_, err := os.Stat(filepath.Join(libDir, llamaLibName()))
	return err == nil
}

// requireLlamaLibs returns a clear, actionable error when the llama.cpp
// libs are not present in libDir — never a silent drop to FTS-only. R2970
func requireLlamaLibs(libDir string) error {
	if llamaLibsInstalled(libDir) {
		return nil
	}
	return fmt.Errorf("llama.cpp libraries not found in %s; run `ark embed install` to provision them", libDir)
}

// embedGpuLayers is the GPU-offload count requested at model load. 99 is
// "all layers" for any model we ship (nomic has 13); llama.cpp clamps to
// the model's layer count. The spike offloaded 13/13 on Vulkan.
const embedGpuLayers = 99

// llama.cpp is loaded once per process. yzma's Load (dlopen) + Init must
// precede any model load. The latch flips only on success, so a failed
// load (missing libs) can be retried after the libs are provisioned.
// R2961
var (
	llamaMu     sync.Mutex
	llamaLoaded bool
)

// ensureLlamaLoaded dlopens the llama.cpp shared libs from libDir exactly
// once per process and initializes the ggml backends. Idempotent. R2961
func ensureLlamaLoaded(libDir string) error {
	llamaMu.Lock()
	defer llamaMu.Unlock()
	if llamaLoaded {
		return nil
	}
	if err := yz.Load(libDir); err != nil {
		return fmt.Errorf("load llama.cpp libraries from %s: %w", libDir, err)
	}
	// llama.cpp logs the backend device, GPU offload, and per-tensor load
	// to stderr by default. Silence it unless the global verbosity is at
	// least llamaVerboseLevel (-vvv) — there the user has explicitly asked
	// for engine internals (confirming GPU offload, debugging). Set once;
	// LogSet is process-global, as is this load. R2973
	if verbosity < llamaVerboseLevel {
		yz.LogSet(yz.LogSilent())
	}
	yz.Init()
	llamaLoaded = true
	return nil
}

// llamaVerboseLevel is the global verbosity (-v count) at and above which
// llama.cpp's own stderr logging is left on. R2973
const llamaVerboseLevel = 3

// embedModel wraps a yzma-loaded GGUF model. Multiple contexts share the
// model's weights (one model + small per-context state). R2962
type embedModel struct {
	model yz.Model
	vocab yz.Vocab
	nEmbd int32
}

// embedParams are the per-context knobs a tier carries. The four-bucket
// tier design maps 1:1: ctx→NCtx, parallel→NSeqMax. R2962
type embedParams struct {
	ctx        int  // context window (NCtx); also sizes NBatch/NUbatch
	parallel   int  // parallel sequences (NSeqMax)
	embeddings bool // embedding mode + mean pooling
}

// embedContext wraps a yzma inference context. Not safe for concurrent
// use — the Librarian serializes access under its mutex, as before.
type embedContext struct {
	parent  *embedModel
	ctx     yz.Context
	mem     yz.Memory // 0 for a pooled encoder (no KV cache — nothing to clear)
	nBatch  int       // cached llama_n_batch
	nSeqMax int       // cached llama_n_seq_max
}

// loadEmbedModel ensures the libs are loaded, then loads the GGUF model
// with GPU offload. R2961, R2962
func loadEmbedModel(libDir, modelPath string) (*embedModel, error) {
	if err := ensureLlamaLoaded(libDir); err != nil {
		return nil, err
	}
	mp := yz.ModelDefaultParams()
	mp.NGpuLayers = embedGpuLayers
	model, err := yz.ModelLoadFromFile(modelPath, mp)
	if err != nil || model == 0 {
		return nil, fmt.Errorf("load model %s: %w", modelPath, err)
	}
	return &embedModel{
		model: model,
		vocab: yz.ModelGetVocab(model),
		nEmbd: yz.ModelNEmbd(model),
	}, nil
}

// runBatch evaluates a batch through the right path. A context with no KV
// cache (mem == 0) is a pooled encoder: llama.cpp's own decode() redirects
// such a batch to encode() and logs a warning on every call, so we call
// encode() directly — quiet, and the exact condition llama.cpp uses
// (`if (!memory) return encode(...)`). A context with a KV cache decodes.
// R2962
func (c *embedContext) runBatch(batch yz.Batch) (int32, error) {
	if c.mem == 0 {
		return yz.Encode(c.ctx, batch)
	}
	return yz.Decode(c.ctx, batch)
}

// newContext creates an inference context from the model. For embedding
// contexts NBatch/NUbatch are pinned to NCtx so the whole batch is one
// physical ubatch — the encoder requirement the gollama fork patched, now
// a plain field on stock llama.cpp. R2962
func (m *embedModel) newContext(p embedParams) (*embedContext, error) {
	cp := yz.ContextDefaultParams()
	cp.NCtx = uint32(p.ctx)
	if p.embeddings {
		cp.NBatch = uint32(p.ctx)
		cp.NUbatch = uint32(p.ctx)
		cp.NSeqMax = uint32(p.parallel)
		cp.Embeddings = 1
		cp.PoolingType = yz.PoolingTypeMean
	}
	ctx, err := yz.InitFromModel(m.model, cp)
	if err != nil {
		return nil, fmt.Errorf("create context: %w", err)
	}
	// A pooled encoder context has no KV cache: GetMemory returns 0 and
	// there is nothing to clear between texts.
	mem, _ := yz.GetMemory(ctx)
	return &embedContext{
		parent:  m,
		ctx:     ctx,
		mem:     mem,
		nBatch:  int(yz.NBatch(ctx)),
		nSeqMax: int(yz.NSeqMax(ctx)),
	}, nil
}

func (m *embedModel) close() {
	if m != nil && m.model != 0 {
		yz.ModelFree(m.model)
		m.model = 0
	}
}

func (c *embedContext) close() {
	if c != nil && c.ctx != 0 {
		yz.Free(c.ctx)
		c.ctx = 0
	}
}

// clearMemory wipes the KV cache when the context has one. A pooled
// encoder context (mem == 0) computes each pooled embedding fresh, so the
// call is a no-op there.
func (c *embedContext) clearMemory() {
	if c.mem != 0 {
		yz.MemoryClear(c.mem, true)
	}
}

// embed computes the embedding for one text. Tokens are decoded in
// n_batch chunks (a single chunk for any normal-length input), then the
// pooled embedding for sequence 0 is copied out. Mirrors gollama's
// GetEmbeddings. R2963
func (c *embedContext) embed(text string) ([]float32, error) {
	c.clearMemory()

	tokens := yz.Tokenize(c.parent.vocab, text, true, true)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("tokenize produced no tokens")
	}

	n := len(tokens)
	for i := 0; i < n; i += c.nBatch {
		chunk := min(c.nBatch, n-i)
		batch := yz.BatchInit(int32(chunk), 0, 1)
		for j := 0; j < chunk; j++ {
			// global position, sequence 0, logits on every token (pooling)
			batch.Add(tokens[i+j], yz.Pos(i+j), []yz.SeqId{0}, true)
		}
		if ret, err := c.runBatch(batch); err != nil || ret != 0 {
			yz.BatchFree(batch)
			return nil, fmt.Errorf("embed eval failed (ret=%d): %w", ret, err)
		}
		yz.BatchFree(batch)
	}
	return c.readSeq(0)
}

// embedBatch computes embeddings for many texts in as few GPU dispatches
// as the context allows. Texts are packed into the batch until adding the
// next would exceed n_batch tokens or n_seq_max sequences, then the batch
// is decoded and each sequence's pooled embedding is read. Mirrors
// gollama's GetEmbeddingsBatch (and the fork's wrapper.cpp packing).
// R2962, R2963
func (c *embedContext) embedBatch(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("no texts provided")
	}
	c.clearMemory()

	tokenized := make([][]yz.Token, len(texts))
	for i, t := range texts {
		toks := yz.Tokenize(c.parent.vocab, t, true, true)
		if len(toks) == 0 {
			return nil, fmt.Errorf("tokenize produced no tokens for text %d", i)
		}
		tokenized[i] = toks
	}

	out := make([][]float32, 0, len(texts))
	batch := yz.BatchInit(int32(c.nBatch), 0, int32(c.nSeqMax))
	defer yz.BatchFree(batch)

	s := 0 // current sequence id within the in-flight batch
	flush := func() error {
		if s == 0 {
			return nil
		}
		if ret, err := c.runBatch(batch); err != nil || ret != 0 {
			return fmt.Errorf("embed batch eval failed (ret=%d): %w", ret, err)
		}
		for seq := 0; seq < s; seq++ {
			vec, err := c.readSeq(yz.SeqId(seq))
			if err != nil {
				return err
			}
			out = append(out, vec)
			if c.mem != 0 {
				yz.MemorySeqRm(c.mem, yz.SeqId(seq), -1, -1)
			}
		}
		s = 0
		batch.Clear()
		return nil
	}

	for _, toks := range tokenized {
		nTok := min(len(toks), c.nBatch) // truncate to batch capacity, as the fork did
		if int(batch.NTokens)+nTok > c.nBatch || s >= c.nSeqMax {
			if err := flush(); err != nil {
				return nil, err
			}
		}
		for j := 0; j < nTok; j++ {
			// position is per-sequence (starts at 0), logits on every token
			batch.Add(toks[j], yz.Pos(j), []yz.SeqId{yz.SeqId(s)}, true)
		}
		s++
	}
	if err := flush(); err != nil {
		return nil, err
	}

	if len(out) != len(texts) {
		return nil, fmt.Errorf("embedding count mismatch: expected %d, got %d", len(texts), len(out))
	}
	return out, nil
}

// readSeq copies the pooled embedding for one sequence out of llama.cpp's
// internal buffer. GetEmbeddingsSeq returns a slice ALIASING that buffer,
// so the copy is mandatory — the next decode overwrites it. R2963
func (c *embedContext) readSeq(seq yz.SeqId) ([]float32, error) {
	vec, err := yz.GetEmbeddingsSeq(c.ctx, seq, c.parent.nEmbd)
	if err != nil {
		return nil, fmt.Errorf("get embeddings for sequence %d: %w", seq, err)
	}
	cp := make([]float32, len(vec))
	copy(cp, vec)
	return cp, nil
}

// countTokens returns the token count for text — tokenization needs only
// the model vocab, no inference context. R1529, R1530
func (m *embedModel) countTokens(text string) int {
	return len(yz.Tokenize(m.vocab, text, true, true))
}
