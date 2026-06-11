package ark

// Smoke test for the yzma embedding engine (R2961-R2963). Skips unless a
// real GGUF model and a provisioned llama.cpp lib dir are present, so it
// is a no-op in CI but a live GPU check on a developer box. A Sleeping
// Sentry: it marches distinct inputs past the engine and asserts they
// come back distinct, finite, and the right width — catching an aliasing
// regression (R2963) or a dead GPU path.
//
// Paths default to the dev box; override with ARK_TEST_MODEL / ARK_TEST_LIBDIR.

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func smokePaths(t *testing.T) (libDir, modelPath string) {
	home, _ := os.UserHomeDir()
	modelPath = os.Getenv("ARK_TEST_MODEL")
	if modelPath == "" {
		modelPath = filepath.Join(home, ".ark", "nomic-embed-text-v1.5.Q8_0.gguf")
	}
	libDir = os.Getenv("ARK_TEST_LIBDIR")
	if libDir == "" {
		for _, cand := range []string{
			filepath.Join(home, ".ark", "lib"),
			filepath.Join("..", "ark", ".scratch", "yzma-spike", "lib"),
			filepath.Join(".scratch", "yzma-spike", "lib"),
		} {
			if llamaLibsInstalled(cand) {
				libDir = cand
				break
			}
		}
	}
	if _, err := os.Stat(modelPath); err != nil {
		t.Skipf("no model at %s (set ARK_TEST_MODEL)", modelPath)
	}
	if libDir == "" || !llamaLibsInstalled(libDir) {
		t.Skip("no llama.cpp libs (set ARK_TEST_LIBDIR)")
	}
	return libDir, modelPath
}

func TestEmbedEngineSmoke(t *testing.T) {
	libDir, modelPath := smokePaths(t)

	model, err := loadEmbedModel(libDir, modelPath)
	if err != nil {
		t.Fatalf("loadEmbedModel: %v", err)
	}
	defer model.close()
	if model.nEmbd <= 0 {
		t.Fatalf("nEmbd = %d, want > 0", model.nEmbd)
	}

	// Standard tier (2048 ctx, 8 sequences), as ensureModel uses.
	ctx, err := model.newContext(embedParams{ctx: 2048, parallel: 8, embeddings: true})
	if err != nil {
		t.Fatalf("newContext: %v", err)
	}
	defer ctx.close()

	a, err := ctx.embed("search_document: the librarian knows to check under related headings")
	if err != nil {
		t.Fatalf("embed a: %v", err)
	}
	b, err := ctx.embed("search_document: a steam deck offloads embeddings to the vulkan gpu")
	if err != nil {
		t.Fatalf("embed b: %v", err)
	}
	if len(a) != int(model.nEmbd) || len(b) != int(model.nEmbd) {
		t.Fatalf("dims: len(a)=%d len(b)=%d want %d", len(a), len(b), model.nEmbd)
	}
	if !finite(a) || !finite(b) {
		t.Fatal("non-finite values in embeddings")
	}
	// R2963: if the GetEmbeddingsSeq alias were retained, a and b would be
	// identical (cosine == 1). Distinct texts must give distinct vectors.
	if cos := cosineSimilarity(a, b); cos >= 0.999 {
		t.Fatalf("cosine(a,b) = %.4f — embeddings not distinct (aliasing regression?)", cos)
	}

	// Batch path must agree with the single path (same packing, same vectors).
	batch, err := ctx.embedBatch([]string{
		"search_document: the librarian knows to check under related headings",
		"search_document: a steam deck offloads embeddings to the vulkan gpu",
	})
	if err != nil {
		t.Fatalf("embedBatch: %v", err)
	}
	if len(batch) != 2 {
		t.Fatalf("batch len = %d, want 2", len(batch))
	}
	if cos := cosineSimilarity(a, batch[0]); cos < 0.999 {
		t.Errorf("single vs batch[0] cosine = %.4f, want ~1.0", cos)
	}
	if cos := cosineSimilarity(b, batch[1]); cos < 0.999 {
		t.Errorf("single vs batch[1] cosine = %.4f, want ~1.0", cos)
	}
}

func finite(v []float32) bool {
	for _, x := range v {
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return false
		}
	}
	return true
}
