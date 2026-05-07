package ark

// CRC: crc-Librarian.md | Test: test-SuggestTagNames.md

import (
	"math"
	"sort"
	"testing"
)

// suggestSetup returns a Librarian wired to a real microfts2 + Store
// with a fake modelPath so EmbeddingAvailable() is true. No model
// is loaded — SuggestTagNames doesn't need one.
func suggestSetup(t *testing.T) (*Librarian, *DB, string) {
	t.Helper()
	idx, dir := testIndexer(t)
	db := newTestDB(idx, dir)
	l := &Librarian{
		db:        db,
		modelPath: "fake-model.gguf",
	}
	return l, db, dir
}

// vecFrom returns a length-8 float32 vector built from the input,
// padded with zeros. Cosine math is stable on 8 elements.
func vecFrom(xs ...float32) []float32 {
	v := make([]float32, 8)
	copy(v, xs)
	return v
}

// TestSuggest_RanksByCosine — happy path. Three tags, three ED
// records, ordered output should follow cosine ordering.
// Refs: R2164, R2166
func TestSuggest_RanksByCosine(t *testing.T) {
	l, db, _ := suggestSetup(t)

	chunkVec := vecFrom(1, 0, 0, 0)
	db.store.WriteChunkEmbedding(1, chunkVec)
	db.store.WriteTagDefEmbedding("a", 10, vecFrom(1, 0, 0, 0))         // cos=1.0
	db.store.WriteTagDefEmbedding("b", 11, vecFrom(0, 1, 0, 0))         // cos=0.0
	db.store.WriteTagDefEmbedding("c", 12, vecFrom(0.5, 0.5, 0, 0))     // cos≈0.707

	got, err := l.SuggestTagNames(1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 suggestions, got %d", len(got))
	}
	wantOrder := []string{"a", "c", "b"}
	for i, want := range wantOrder {
		if got[i].Tag != want {
			t.Errorf("position %d: want %q got %q (full: %+v)", i, want, got[i].Tag, got)
		}
	}
	// Each TagSuggestion has exactly one motivating file equal to its score.
	for _, s := range got {
		if len(s.MotivatingFiles) != 1 {
			t.Errorf("tag %q: expected 1 motivating file, got %d", s.Tag, len(s.MotivatingFiles))
		}
		if s.MotivatingFiles[0].Score != s.Score {
			t.Errorf("tag %q: motivating-file score %v != tag score %v", s.Tag, s.MotivatingFiles[0].Score, s.Score)
		}
	}
}

// TestSuggest_MaxAggregatesAcrossFiles — same tag in two files; the
// better score wins, both motivating files kept ranked desc.
// Refs: R2165, R2166
func TestSuggest_MaxAggregatesAcrossFiles(t *testing.T) {
	l, db, _ := suggestSetup(t)

	chunkVec := vecFrom(1, 0, 0, 0)
	db.store.WriteChunkEmbedding(1, chunkVec)
	db.store.WriteTagDefEmbedding("decision", 10, vecFrom(0.5, 0.5, 0, 0)) // weaker
	db.store.WriteTagDefEmbedding("decision", 20, vecFrom(1, 0, 0, 0))     // stronger

	got, err := l.SuggestTagNames(1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected one tag, got %d (%+v)", len(got), got)
	}
	s := got[0]
	if s.Tag != "decision" {
		t.Errorf("tag: %q", s.Tag)
	}
	wantScore := cosineSimilarity(chunkVec, vecFrom(1, 0, 0, 0))
	if math.Abs(s.Score-wantScore) > 1e-6 {
		t.Errorf("aggregate score: got %v want %v", s.Score, wantScore)
	}
	if len(s.MotivatingFiles) != 2 {
		t.Fatalf("expected 2 motivating files, got %d", len(s.MotivatingFiles))
	}
	if s.MotivatingFiles[0].FileID != 20 || s.MotivatingFiles[1].FileID != 10 {
		t.Errorf("motivating files not sorted desc: %+v", s.MotivatingFiles)
	}
	if s.MotivatingFiles[0].Score < s.MotivatingFiles[1].Score {
		t.Errorf("motivating scores not descending: %+v", s.MotivatingFiles)
	}
}

// TestSuggest_CapsToK — k truncates after sort.
// Refs: R2164
func TestSuggest_CapsToK(t *testing.T) {
	l, db, _ := suggestSetup(t)

	db.store.WriteChunkEmbedding(1, vecFrom(1, 0, 0, 0))
	for i, tag := range []string{"a", "b", "c", "d", "e"} {
		// Distinct cosines so ordering is well-defined.
		db.store.WriteTagDefEmbedding(tag, uint64(10+i),
			vecFrom(float32(5-i), 0, 0, 0))
	}
	got, err := l.SuggestTagNames(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 (capped), got %d", len(got))
	}
}

// TestSuggest_NoEC — chunk hasn't been embedded; not an error.
// Refs: R2169
func TestSuggest_NoEC(t *testing.T) {
	l, db, _ := suggestSetup(t)
	db.store.WriteTagDefEmbedding("a", 10, vecFrom(1, 0, 0, 0))

	got, err := l.SuggestTagNames(999, 5)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

// TestSuggest_NoED — empty corpus; not an error.
// Refs: R2171
func TestSuggest_NoED(t *testing.T) {
	l, db, _ := suggestSetup(t)
	db.store.WriteChunkEmbedding(1, vecFrom(1, 0, 0, 0))

	got, err := l.SuggestTagNames(1, 5)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

// TestSuggest_KNonPositive — k <= 0 returns (nil, nil).
// Refs: R2168
func TestSuggest_KNonPositive(t *testing.T) {
	l, db, _ := suggestSetup(t)
	db.store.WriteChunkEmbedding(1, vecFrom(1, 0, 0, 0))
	db.store.WriteTagDefEmbedding("a", 10, vecFrom(1, 0, 0, 0))

	for _, k := range []int{0, -1, -100} {
		got, err := l.SuggestTagNames(1, k)
		if err != nil {
			t.Fatalf("k=%d err: %v", k, err)
		}
		if got != nil {
			t.Errorf("k=%d: expected nil, got %+v", k, got)
		}
	}
}

// TestSuggest_DimensionMismatchSkipped — orphan ED at wrong dim is
// silently skipped; the rest are returned normally.
// Refs: R2172
func TestSuggest_DimensionMismatchSkipped(t *testing.T) {
	l, db, _ := suggestSetup(t)

	db.store.WriteChunkEmbedding(1, vecFrom(1, 0, 0, 0)) // 8 floats
	db.store.WriteTagDefEmbedding("good", 10, vecFrom(1, 0, 0, 0))
	// Hand-craft a 4-float vector to simulate a model swap leftover.
	db.store.WriteTagDefEmbedding("bad", 11, []float32{1, 0, 0, 0})

	got, err := l.SuggestTagNames(1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Tag != "good" {
		t.Errorf("expected only 'good', got %+v", got)
	}
}

// TestSuggest_PathResolution — fileid with FTS path entry gets a Path,
// fileid without one stays empty. No error either way.
// Refs: R2167
func TestSuggest_PathResolution(t *testing.T) {
	l, db, dir := suggestSetup(t)

	// Register a real file in microfts2 so FileIDPaths returns its path.
	fp := writeFile(t, dir, "definitions.md", "@decision: choosing tools\n")
	realFID, err := db.indexer.AddFile(fp, "line")
	if err != nil {
		t.Fatal(err)
	}

	db.store.WriteChunkEmbedding(1, vecFrom(1, 0, 0, 0))
	db.store.WriteTagDefEmbedding("decision", realFID, vecFrom(1, 0, 0, 0))
	db.store.WriteTagDefEmbedding("decision", 99999, vecFrom(0.9, 0.1, 0, 0)) // phantom fileid

	got, err := l.SuggestTagNames(1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected one tag, got %d", len(got))
	}
	mfs := got[0].MotivatingFiles
	if len(mfs) != 2 {
		t.Fatalf("expected 2 motivating files, got %d", len(mfs))
	}
	// Find by fileid; order is by score (real fileid scored higher).
	pathByFID := map[uint64]string{}
	for _, m := range mfs {
		pathByFID[m.FileID] = m.Path
	}
	if pathByFID[realFID] != fp {
		t.Errorf("real fileid path: want %q got %q", fp, pathByFID[realFID])
	}
	if pathByFID[99999] != "" {
		t.Errorf("phantom fileid path: want empty, got %q", pathByFID[99999])
	}
}

// TestSuggest_ReadOnly — call must not mutate LMDB record counts.
// Refs: R2173
func TestSuggest_ReadOnly(t *testing.T) {
	l, db, _ := suggestSetup(t)
	db.store.WriteChunkEmbedding(1, vecFrom(1, 0, 0, 0))
	db.store.WriteTagDefEmbedding("a", 10, vecFrom(1, 0, 0, 0))
	db.store.WriteTagDefEmbedding("b", 11, vecFrom(0.5, 0.5, 0, 0))

	before, err := db.store.RecordCounts()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.SuggestTagNames(1, 5); err != nil {
		t.Fatal(err)
	}
	after, err := db.store.RecordCounts()
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(before))
	for k := range before {
		keys = append(keys, k)
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		if before[k].Count != after[k].Count {
			t.Errorf("prefix %q count changed: before=%d after=%d",
				k, before[k].Count, after[k].Count)
		}
	}
}
