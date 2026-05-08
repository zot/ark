package ark

// CRC: crc-Librarian.md | Test: test-SuggestTagNames.md, test-ChunksForTag.md

import (
	"math"
	"sort"
	"testing"

	"github.com/zot/microfts2"
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

// indexFileWithChunks indexes a file via microfts2 and captures the
// chunkIDs assigned by the chunker. Used by ChunksForTag tests to
// drive C-record creation while obtaining stable chunkIDs to attach
// EC records to.
func indexFileWithChunks(t *testing.T, l *Librarian, dir, name, content, strategy string) (uint64, []uint64) {
	t.Helper()
	fp := writeFile(t, dir, name, content)
	var chunkIDs []uint64
	cb := microfts2.WithIndexedChunkCallback(func(ic microfts2.IndexedChunk) {
		chunkIDs = append(chunkIDs, ic.CRecord.ChunkID)
	})
	fid, err := l.db.fts.AddFile(fp, strategy, cb)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunkIDs) == 0 {
		t.Fatalf("no chunks indexed for %s", fp)
	}
	return fid, chunkIDs
}

// TestChunksForTag_RanksByMaxAggregate — happy path with two defs;
// each chunk's score is the max cosine across the defs and chunks
// are ranked descending.
// Refs: R2194, R2197, R2202, R2205, R2206
func TestChunksForTag_RanksByMaxAggregate(t *testing.T) {
	l, db, dir := suggestSetup(t)

	fid, chunks := indexFileWithChunks(t, l, dir, "doc.md",
		"line one\nline two\nline three\n", "line")
	if len(chunks) < 3 {
		t.Skipf("line chunker produced %d chunks, need 3", len(chunks))
	}

	// Three chunks. Two defs: ED[10] aligned with x-axis, ED[20] with y-axis.
	// Pick chunk vectors so each chunk's max-cosine ranks them in a
	// known order: chunk0 strongest (1.0), chunk1 mid (~0.866), chunk2 lowest (0.5).
	db.store.WriteChunkEmbedding(chunks[0], vecFrom(1, 0, 0, 0))     // max via ED[10]: 1.0
	db.store.WriteChunkEmbedding(chunks[1], vecFrom(0.5, 0.866, 0, 0)) // max via ED[20]: ~0.866
	db.store.WriteChunkEmbedding(chunks[2], vecFrom(0.5, 0.5, 0.707, 0)) // max ~0.5

	db.store.WriteTagDefEmbedding("priority", 10, vecFrom(1, 0, 0, 0))
	db.store.WriteTagDefEmbedding("priority", 20, vecFrom(0, 1, 0, 0))

	got, err := l.ChunksForTag("priority", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 chunks, got %d (%+v)", len(got), got)
	}
	wantOrder := []uint64{chunks[0], chunks[1], chunks[2]}
	for i, want := range wantOrder {
		if got[i].ChunkID != want {
			t.Errorf("position %d: chunkID want %d got %d (full: %+v)", i, want, got[i].ChunkID, got)
		}
		if got[i].FileID != fid {
			t.Errorf("position %d: fileID want %d got %d", i, fid, got[i].FileID)
		}
		if len(got[i].MotivatingDefs) != 2 {
			t.Errorf("position %d: expected 2 motivating defs, got %d", i, len(got[i].MotivatingDefs))
		}
		if got[i].MotivatingDefs[0].Score < got[i].MotivatingDefs[1].Score {
			t.Errorf("position %d: motivating defs not sorted desc: %+v", i, got[i].MotivatingDefs)
		}
		if math.Abs(got[i].Score-got[i].MotivatingDefs[0].Score) > 1e-6 {
			t.Errorf("position %d: aggregate %v != top motivating %v", i, got[i].Score, got[i].MotivatingDefs[0].Score)
		}
	}
}

// TestChunksForTag_CapsToK — k truncates after sort.
// Refs: R2199
func TestChunksForTag_CapsToK(t *testing.T) {
	l, db, dir := suggestSetup(t)

	_, chunks := indexFileWithChunks(t, l, dir, "doc.md",
		"a\nb\nc\nd\ne\n", "line")
	if len(chunks) < 5 {
		t.Skipf("line chunker produced %d chunks, need 5", len(chunks))
	}

	// Five chunks with distinct cosines vs ED[10].
	for i, cid := range chunks[:5] {
		db.store.WriteChunkEmbedding(cid, vecFrom(float32(5-i), 0, 0, 0))
	}
	db.store.WriteTagDefEmbedding("priority", 10, vecFrom(1, 0, 0, 0))

	got, err := l.ChunksForTag("priority", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 (capped), got %d", len(got))
	}
}

// TestChunksForTag_KNonPositive — k <= 0 returns (nil, nil).
// Refs: R2207
func TestChunksForTag_KNonPositive(t *testing.T) {
	l, db, dir := suggestSetup(t)

	_, chunks := indexFileWithChunks(t, l, dir, "doc.md", "one\n", "line")
	db.store.WriteChunkEmbedding(chunks[0], vecFrom(1, 0, 0, 0))
	db.store.WriteTagDefEmbedding("priority", 10, vecFrom(1, 0, 0, 0))

	for _, k := range []int{0, -1, -100} {
		got, err := l.ChunksForTag("priority", k)
		if err != nil {
			t.Fatalf("k=%d err: %v", k, err)
		}
		if got != nil {
			t.Errorf("k=%d: expected nil, got %+v", k, got)
		}
	}
}

// TestChunksForTag_NoEDForTag — tag has no ED records (other tags exist).
// Refs: R2209
func TestChunksForTag_NoEDForTag(t *testing.T) {
	l, db, dir := suggestSetup(t)
	_, chunks := indexFileWithChunks(t, l, dir, "doc.md", "one\n", "line")
	db.store.WriteChunkEmbedding(chunks[0], vecFrom(1, 0, 0, 0))
	db.store.WriteTagDefEmbedding("other", 10, vecFrom(1, 0, 0, 0))

	got, err := l.ChunksForTag("priority", 5)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

// TestChunksForTag_NoEC — empty EC prefix; tag has defs but no chunks
// embedded. Returns (nil, nil) without resolving anything.
// Refs: R2211
func TestChunksForTag_NoEC(t *testing.T) {
	l, db, _ := suggestSetup(t)
	db.store.WriteTagDefEmbedding("priority", 10, vecFrom(1, 0, 0, 0))

	got, err := l.ChunksForTag("priority", 5)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

// TestChunksForTag_DimensionMismatchSkipped — orphan EC at wrong dim
// is silently skipped; the rest are returned normally.
// Refs: R2198
func TestChunksForTag_DimensionMismatchSkipped(t *testing.T) {
	l, db, dir := suggestSetup(t)

	_, chunks := indexFileWithChunks(t, l, dir, "doc.md", "one\ntwo\n", "line")
	if len(chunks) < 2 {
		t.Skipf("line chunker produced %d chunks, need 2", len(chunks))
	}
	// chunk[0]: 8-dim, matches ED dim. chunk[1]: 4-dim, mismatch.
	db.store.WriteChunkEmbedding(chunks[0], vecFrom(1, 0, 0, 0))
	db.store.WriteChunkEmbedding(chunks[1], []float32{1, 0, 0, 0})
	db.store.WriteTagDefEmbedding("priority", 10, vecFrom(1, 0, 0, 0))

	got, err := l.ChunksForTag("priority", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d (%+v)", len(got), got)
	}
	if got[0].ChunkID != chunks[0] {
		t.Errorf("expected chunk %d, got %d", chunks[0], got[0].ChunkID)
	}
}

// TestChunksForTag_OrphanECDropped — chunk has EC but no CRecord.
// Mirrors a stale EC left after microfts2 dropped its C-record.
// Refs: R2200
func TestChunksForTag_OrphanECDropped(t *testing.T) {
	l, db, dir := suggestSetup(t)

	_, chunks := indexFileWithChunks(t, l, dir, "doc.md", "one\n", "line")
	db.store.WriteChunkEmbedding(chunks[0], vecFrom(1, 0, 0, 0))
	// Phantom chunkID with EC but no CRecord (microfts2 never saw it).
	db.store.WriteChunkEmbedding(99999, vecFrom(0.9, 0.1, 0, 0))
	db.store.WriteTagDefEmbedding("priority", 10, vecFrom(1, 0, 0, 0))

	got, err := l.ChunksForTag("priority", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 (orphan dropped), got %d (%+v)", len(got), got)
	}
	if got[0].ChunkID != chunks[0] {
		t.Errorf("expected chunk %d, got %d", chunks[0], got[0].ChunkID)
	}
}

// TestChunksForTag_PathResolution — chunk's primary file path resolves;
// def fileid without an FTS path entry leaves DefMatch.Path empty.
// Refs: R2201
func TestChunksForTag_PathResolution(t *testing.T) {
	l, db, dir := suggestSetup(t)

	fp, chunks := indexFileWithChunks(t, l, dir, "doc.md", "alpha\n", "line")
	_ = fp
	fpath := writeFile(t, dir, "definitions.md", "@priority: ranking\n")
	defFID, err := db.indexer.AddFile(fpath, "line")
	if err != nil {
		t.Fatal(err)
	}

	db.store.WriteChunkEmbedding(chunks[0], vecFrom(1, 0, 0, 0))
	db.store.WriteTagDefEmbedding("priority", defFID, vecFrom(1, 0, 0, 0))
	db.store.WriteTagDefEmbedding("priority", 99999, vecFrom(0.9, 0.1, 0, 0))

	got, err := l.ChunksForTag("priority", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	if got[0].Path == "" {
		t.Errorf("chunk Path should be set")
	}
	pathByFID := map[uint64]string{}
	for _, m := range got[0].MotivatingDefs {
		pathByFID[m.FileID] = m.Path
	}
	if pathByFID[defFID] != fpath {
		t.Errorf("def fileid %d path: want %q got %q", defFID, fpath, pathByFID[defFID])
	}
	if pathByFID[99999] != "" {
		t.Errorf("phantom def path: want empty, got %q", pathByFID[99999])
	}
}

// TestChunksForTagDef_RanksBySingleDef — happy path for the
// single-def flavor.
// Refs: R2195, R2204
func TestChunksForTagDef_RanksBySingleDef(t *testing.T) {
	l, db, dir := suggestSetup(t)

	_, chunks := indexFileWithChunks(t, l, dir, "doc.md",
		"a\nb\nc\n", "line")
	if len(chunks) < 3 {
		t.Skipf("line chunker produced %d chunks, need 3", len(chunks))
	}

	db.store.WriteChunkEmbedding(chunks[0], vecFrom(1, 0, 0, 0))     // cos 1.0
	db.store.WriteChunkEmbedding(chunks[1], vecFrom(0, 1, 0, 0))     // cos 0.0
	db.store.WriteChunkEmbedding(chunks[2], vecFrom(0.5, 0.5, 0, 0)) // cos ~0.707

	db.store.WriteTagDefEmbedding("priority", 10, vecFrom(1, 0, 0, 0))

	got, err := l.ChunksForTagDef("priority", 10, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d", len(got))
	}
	wantOrder := []uint64{chunks[0], chunks[2], chunks[1]}
	for i, want := range wantOrder {
		if got[i].ChunkID != want {
			t.Errorf("position %d: chunkID want %d got %d", i, want, got[i].ChunkID)
		}
		if len(got[i].MotivatingDefs) != 1 {
			t.Fatalf("position %d: MotivatingDefs len want 1, got %d", i, len(got[i].MotivatingDefs))
		}
		if got[i].MotivatingDefs[0].FileID != 10 {
			t.Errorf("position %d: motivating fileid want 10, got %d", i, got[i].MotivatingDefs[0].FileID)
		}
		if got[i].MotivatingDefs[0].Score != got[i].Score {
			t.Errorf("position %d: motivating score %v != suggestion score %v", i, got[i].MotivatingDefs[0].Score, got[i].Score)
		}
	}
}

// TestChunksForTagDef_AbsentDef — ED[tag, fileid] absent → (nil, nil).
// Refs: R2210
func TestChunksForTagDef_AbsentDef(t *testing.T) {
	l, db, dir := suggestSetup(t)
	_, chunks := indexFileWithChunks(t, l, dir, "doc.md", "a\n", "line")
	db.store.WriteChunkEmbedding(chunks[0], vecFrom(1, 0, 0, 0))
	db.store.WriteTagDefEmbedding("priority", 10, vecFrom(1, 0, 0, 0))

	got, err := l.ChunksForTagDef("priority", 999, 5)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

// TestChunksForTagDef_KNonPositive — k <= 0 returns (nil, nil).
// Refs: R2207
func TestChunksForTagDef_KNonPositive(t *testing.T) {
	l, db, dir := suggestSetup(t)
	_, chunks := indexFileWithChunks(t, l, dir, "doc.md", "a\n", "line")
	db.store.WriteChunkEmbedding(chunks[0], vecFrom(1, 0, 0, 0))
	db.store.WriteTagDefEmbedding("priority", 10, vecFrom(1, 0, 0, 0))

	got, err := l.ChunksForTagDef("priority", 10, 0)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

// TestChunksForTag_ReadOnly — neither call mutates LMDB record counts.
// Refs: R2212
func TestChunksForTag_ReadOnly(t *testing.T) {
	l, db, dir := suggestSetup(t)
	_, chunks := indexFileWithChunks(t, l, dir, "doc.md", "a\nb\n", "line")
	db.store.WriteChunkEmbedding(chunks[0], vecFrom(1, 0, 0, 0))
	if len(chunks) > 1 {
		db.store.WriteChunkEmbedding(chunks[1], vecFrom(0, 1, 0, 0))
	}
	db.store.WriteTagDefEmbedding("priority", 10, vecFrom(1, 0, 0, 0))
	db.store.WriteTagDefEmbedding("priority", 11, vecFrom(0.5, 0.5, 0, 0))

	before, err := db.store.RecordCounts()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.ChunksForTag("priority", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := l.ChunksForTagDef("priority", 10, 5); err != nil {
		t.Fatal(err)
	}
	after, err := db.store.RecordCounts()
	if err != nil {
		t.Fatal(err)
	}
	for k, b := range before {
		if a := after[k]; a.Count != b.Count {
			t.Errorf("prefix %q count changed: before=%d after=%d", string(k), b.Count, a.Count)
		}
	}
}

// TestChunksForTag_NoOrphanFilter — chunks already carrying the tag
// are still returned. Orphan-detection policy lives in the caller,
// not in this API.
// Refs: R2214
func TestChunksForTag_NoOrphanFilter(t *testing.T) {
	l, db, dir := suggestSetup(t)
	_, chunks := indexFileWithChunks(t, l, dir, "doc.md", "a\nb\n", "line")
	if len(chunks) < 2 {
		t.Skipf("line chunker produced %d chunks, need 2", len(chunks))
	}

	// chunk[0] carries V record for ("priority", "high"); chunk[1] does not.
	if err := db.store.UpdateTagValues([]ChunkTagValues{
		{ChunkID: chunks[0], Values: []TagValue{{Tag: "priority", Value: "high"}}},
	}); err != nil {
		t.Fatal(err)
	}
	db.store.WriteChunkEmbedding(chunks[0], vecFrom(1, 0, 0, 0))
	db.store.WriteChunkEmbedding(chunks[1], vecFrom(0.9, 0.436, 0, 0))
	db.store.WriteTagDefEmbedding("priority", 10, vecFrom(1, 0, 0, 0))

	got, err := l.ChunksForTag("priority", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("expected both chunks (no orphan filter), got %d: %+v", len(got), got)
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
