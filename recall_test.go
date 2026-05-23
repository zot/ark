package ark

// CRC: crc-Librarian.md, crc-Server.md | Test: test-Recall.md

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// setupRecall sets up a test Indexer, Store, DB, and Librarian
// with a fake model path so EmbeddingAvailable() is true.
func setupRecall(t *testing.T) (*Librarian, *DB) {
	t.Helper()
	idx, dir := testIndexer(t)
	db := newTestDB(idx, dir)

	l := &Librarian{
		db:        db,
		modelPath: "fake-model.gguf",
	}

	db.search = &Searcher{
		fts:       db.fts,
		store:     db.store,
		config:    &Config{},
		librarian: l,
	}

	return l, db
}

// TestRecall_MergedScoringAndRanking validates that Vector-EC and Trigram-EC
// scores are merged using max across inputs and substrates and sorted descending.
// Refs: R2617, R2620, R2622, R2626, R2586, R2643
func TestRecall_MergedScoringAndRanking(t *testing.T) {
	l, db := setupRecall(t)

	c1, _ := indexLine(t, db, "1.txt", "apple banana cherry")
	c2, _ := indexLine(t, db, "2.txt", "apple banana grape")
	c3, _ := indexLine(t, db, "3.txt", "orange melon grape")

	db.store.WriteChunkEmbedding(c1, vecFrom(1.0, 0.0, 0.0, 0.0))
	db.store.WriteChunkEmbedding(c2, vecFrom(0.8, 0.6, 0.0, 0.0)) // Vector-EC score = normalizeCos(0.8) = 0.90
	db.store.WriteChunkEmbedding(c3, vecFrom(0.0, 1.0, 0.0, 0.0)) // Vector-EC score = normalizeCos(0.0) = 0.50

	inputs := []ConnectionsInput{{ChunkID: c1}}
	// KeepTagless: this test exercises substrate scoring, not the
	// tag filter; indexLine creates chunks without V records.
	res, err := l.Recall(inputs, RecallOpts{K: 10, IncludeContent: true, KeepTagless: true})
	if err != nil {
		t.Fatalf("Recall failed: %v", err)
	}

	// c1 must be excluded, leaving c2 and c3.
	if len(res.Chunks) < 2 {
		t.Fatalf("expected at least 2 recalled chunks, got %d", len(res.Chunks))
	}

	// Verify order: c2 (score 0.90) then c3 (score 0.50)
	if res.Chunks[0].ChunkID != c2 {
		t.Errorf("expected chunk c2 (%d) first, got %d", c2, res.Chunks[0].ChunkID)
	}
	if res.Chunks[1].ChunkID != c3 {
		t.Errorf("expected chunk c3 (%d) second, got %d", c3, res.Chunks[1].ChunkID)
	}

	if res.Chunks[0].Score < 0.89 {
		t.Errorf("expected c2 score >= 0.89, got %f", res.Chunks[0].Score)
	}
}

// TestRecall_SelfChunkExclusion verifies that when the input is a chunkID,
// that chunk is excluded from its own recall results.
// Refs: R2623
func TestRecall_SelfChunkExclusion(t *testing.T) {
	l, db := setupRecall(t)

	c1, _ := indexLine(t, db, "1.txt", "apple banana cherry")
	c2, _ := indexLine(t, db, "2.txt", "apple banana grape")

	db.store.WriteChunkEmbedding(c1, vecFrom(1.0, 0.0, 0.0, 0.0))
	db.store.WriteChunkEmbedding(c2, vecFrom(1.0, 0.0, 0.0, 0.0))

	inputs := []ConnectionsInput{{ChunkID: c1}}
	res, err := l.Recall(inputs, RecallOpts{K: 10, KeepTagless: true})
	if err != nil {
		t.Fatalf("Recall failed: %v", err)
	}

	for _, chunk := range res.Chunks {
		if chunk.ChunkID == c1 {
			t.Errorf("input chunk c1 (%d) was not excluded from results", c1)
		}
	}
}

// TestRecall_MetadataAndTagResolution verifies path, range, and tags are resolved.
// Refs: R2624
func TestRecall_MetadataAndTagResolution(t *testing.T) {
	l, db := setupRecall(t)

	c1, _ := indexLine(t, db, "1.txt", "apple banana cherry")
	c2, p2 := indexLine(t, db, "2.txt", "apple banana grape")

	db.store.WriteChunkEmbedding(c1, vecFrom(1.0, 0.0, 0.0, 0.0))
	db.store.WriteChunkEmbedding(c2, vecFrom(1.0, 0.0, 0.0, 0.0))

	err := db.store.UpdateTagValues([]ChunkTagValues{
		{ChunkID: c2, Values: []TagValue{{Tag: "color", Value: "purple"}}},
	})
	if err != nil {
		t.Fatalf("UpdateTagValues failed: %v", err)
	}

	inputs := []ConnectionsInput{{ChunkID: c1}}
	res, err := l.Recall(inputs, RecallOpts{K: 10})
	if err != nil {
		t.Fatalf("Recall failed: %v", err)
	}

	if len(res.Chunks) == 0 {
		t.Fatalf("expected results")
	}

	c2Res := res.Chunks[0]
	if c2Res.Path != p2 {
		t.Errorf("expected path %q, got %q", p2, c2Res.Path)
	}
	if c2Res.Range != "1-1" {
		t.Errorf("expected range 1-1, got %q", c2Res.Range)
	}
	if len(c2Res.Tags) != 1 || c2Res.Tags[0].Tag != "color" || c2Res.Tags[0].Value != "purple" {
		t.Errorf("expected tag color:purple, got %+v", c2Res.Tags)
	}
}

// TestRecall_IncludeContentOption verifies that IncludeContent option controls chunk loading.
// Refs: R2625
func TestRecall_IncludeContentOption(t *testing.T) {
	l, db := setupRecall(t)

	c1, _ := indexLine(t, db, "1.txt", "apple banana cherry")
	c2, _ := indexLine(t, db, "2.txt", "apple banana grape")

	db.store.WriteChunkEmbedding(c1, vecFrom(1.0, 0.0, 0.0, 0.0))
	db.store.WriteChunkEmbedding(c2, vecFrom(1.0, 0.0, 0.0, 0.0))

	// IncludeContent = true (KeepTagless: substrate-only test).
	resTrue, err := l.Recall([]ConnectionsInput{{ChunkID: c1}}, RecallOpts{K: 10, IncludeContent: true, KeepTagless: true})
	if err != nil {
		t.Fatalf("Recall failed: %v", err)
	}
	if len(resTrue.Chunks) == 0 || resTrue.Chunks[0].Content != "apple banana grape" {
		t.Errorf("expected content to be populated, got %q", resTrue.Chunks[0].Content)
	}

	// IncludeContent = false
	resFalse, err := l.Recall([]ConnectionsInput{{ChunkID: c1}}, RecallOpts{K: 10, IncludeContent: false, KeepTagless: true})
	if err != nil {
		t.Fatalf("Recall failed: %v", err)
	}
	if len(resFalse.Chunks) == 0 || resFalse.Chunks[0].Content != "" {
		t.Errorf("expected empty content, got %q", resFalse.Chunks[0].Content)
	}
}

// TestRecall_TrigramOnlyFallback verifies fallback behavior when embedding is unavailable.
// Refs: R2634
func TestRecall_TrigramOnlyFallback(t *testing.T) {
	idx, dir := testIndexer(t)
	db := newTestDB(idx, dir)

	// modelPath is empty -> EmbeddingAvailable() returns false
	l := &Librarian{
		db:        db,
		modelPath: "",
	}
	db.search = &Searcher{
		fts:       db.fts,
		store:     db.store,
		config:    &Config{},
		librarian: l,
	}

	c1, _ := indexLine(t, db, "1.txt", "apple banana cherry")
	c2, _ := indexLine(t, db, "2.txt", "apple banana grape")

	db.store.WriteChunkEmbedding(c1, vecFrom(1.0, 0.0, 0.0, 0.0))
	db.store.WriteChunkEmbedding(c2, vecFrom(1.0, 0.0, 0.0, 0.0))

	inputs := []ConnectionsInput{{Text: "apple banana"}}
	res, err := l.Recall(inputs, RecallOpts{K: 10, KeepTagless: true})
	if err != nil {
		t.Fatalf("Recall failed: %v", err)
	}

	if res.Warning != "embedding unavailable" {
		t.Errorf("expected warning 'embedding unavailable', got %q", res.Warning)
	}

	if len(res.Chunks) == 0 {
		t.Errorf("expected trigram-only matches, got 0 results")
	} else if res.Chunks[0].ChunkID != c1 && res.Chunks[0].ChunkID != c2 {
		t.Errorf("expected match c1 or c2, got %d", res.Chunks[0].ChunkID)
	}

	if res.Chunks[0].PerSubstrate.VectorEC != 0 {
		t.Errorf("expected VectorEC score 0, got %f", res.Chunks[0].PerSubstrate.VectorEC)
	}
}

// TestRecall_InputValidation verifies validation rules.
// Refs: R2639, R2640
func TestRecall_InputValidation(t *testing.T) {
	l, _ := setupRecall(t)

	// Case A: Empty inputs
	_, err := l.Recall(nil, RecallOpts{K: 10})
	if err == nil || err.Error() != "chunkIDs/text/range empty" {
		t.Errorf("expected chunkIDs/text/range empty error, got %v", err)
	}

	// Case B: Unknown chunkID
	_, err = l.Recall([]ConnectionsInput{{ChunkID: 9999999}}, RecallOpts{K: 10})
	if err == nil || err.Error() != "unknown chunk 9999999" {
		t.Errorf("expected unknown chunk 9999999 error, got %v", err)
	}
}

// TestRecall_OptionClamping verifies K option clamping.
// Refs: R2641
func TestRecall_OptionClamping(t *testing.T) {
	l, db := setupRecall(t)

	c1, _ := indexLine(t, db, "1.txt", "apple")
	c2, _ := indexLine(t, db, "2.txt", "banana")
	db.store.WriteChunkEmbedding(c1, vecFrom(1.0, 0.0, 0.0, 0.0))
	db.store.WriteChunkEmbedding(c2, vecFrom(1.0, 0.0, 0.0, 0.0))

	res0, err := l.Recall([]ConnectionsInput{{ChunkID: c1}}, RecallOpts{K: 0, KeepTagless: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res0.Chunks) != 1 {
		t.Errorf("expected K=0 clamping to yield 1 result, got %d", len(res0.Chunks))
	}

	res1, err := l.Recall([]ConnectionsInput{{ChunkID: c1}}, RecallOpts{K: 1, KeepTagless: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res1.Chunks) != 1 {
		t.Errorf("expected K=1 to limit output to 1 chunk, got %d", len(res1.Chunks))
	}
}

// TestRecall_TrigramCoverageFloor verifies that candidates whose
// query-coverage falls below trigramCoverageFloor are short-circuited
// to score 0 by the Jaccard pass.
// Refs: R2643, R2644
func TestRecall_TrigramCoverageFloor(t *testing.T) {
	l, db := setupRecall(t)

	// "near" chunk shares most of its trigrams with the query.
	cNear, _ := indexLine(t, db, "near.txt", "asparagus risotto recipe with white wine")
	// "far" chunk shares essentially no trigrams with the query.
	cFar, _ := indexLine(t, db, "far.txt", "zzz qqq xxx vvv bbb")

	db.store.WriteChunkEmbedding(cNear, vecFrom(0.0, 0.0, 0.0, 0.0))
	db.store.WriteChunkEmbedding(cFar, vecFrom(0.0, 0.0, 0.0, 0.0))

	res, err := l.Recall(
		[]ConnectionsInput{{Text: "asparagus risotto recipe"}},
		RecallOpts{K: 10, KeepTagless: true},
	)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}

	var nearScore, farScore float64
	var farPresent bool
	for _, c := range res.Chunks {
		switch c.ChunkID {
		case cNear:
			nearScore = c.PerSubstrate.TrigramEC
		case cFar:
			farScore = c.PerSubstrate.TrigramEC
			farPresent = true
		}
	}
	if nearScore <= 0 {
		t.Errorf("expected near chunk to have non-zero trigram-EC, got %f", nearScore)
	}
	if farPresent && farScore != 0 {
		t.Errorf("expected far chunk to be floored to 0 (or absent), got trigram-EC %f", farScore)
	}
}

// TestRecall_TaglessFilter verifies that with KeepTagless=false (the
// default), candidate chunks that carry no V records are dropped
// from results, and with KeepTagless=true they are retained.
// Refs: R2647
func TestRecall_TaglessFilter(t *testing.T) {
	l, db := setupRecall(t)

	cTagged, _ := indexLine(t, db, "tagged.txt", "apple banana cherry")
	cBare, _ := indexLine(t, db, "bare.txt", "apple banana grape")

	db.store.WriteChunkEmbedding(cTagged, vecFrom(1.0, 0.0, 0.0, 0.0))
	db.store.WriteChunkEmbedding(cBare, vecFrom(1.0, 0.0, 0.0, 0.0))

	if err := db.store.UpdateTagValues([]ChunkTagValues{
		{ChunkID: cTagged, Values: []TagValue{{Tag: "fruit", Value: "yes"}}},
	}); err != nil {
		t.Fatalf("UpdateTagValues: %v", err)
	}

	// Default (KeepTagless=false): cBare should be excluded.
	resDefault, err := l.Recall(
		[]ConnectionsInput{{Text: "apple banana"}},
		RecallOpts{K: 10},
	)
	if err != nil {
		t.Fatalf("Recall default: %v", err)
	}
	for _, c := range resDefault.Chunks {
		if c.ChunkID == cBare {
			t.Errorf("default KeepTagless=false: tagless chunk %d should be dropped", cBare)
		}
	}
	var sawTagged bool
	for _, c := range resDefault.Chunks {
		if c.ChunkID == cTagged {
			sawTagged = true
		}
	}
	if !sawTagged {
		t.Errorf("expected tagged chunk %d in default results", cTagged)
	}

	// KeepTagless=true: both chunks visible.
	resKept, err := l.Recall(
		[]ConnectionsInput{{Text: "apple banana"}},
		RecallOpts{K: 10, KeepTagless: true},
	)
	if err != nil {
		t.Fatalf("Recall KeepTagless: %v", err)
	}
	var bareSeen bool
	for _, c := range resKept.Chunks {
		if c.ChunkID == cBare {
			bareSeen = true
		}
	}
	if !bareSeen {
		t.Errorf("KeepTagless=true: tagless chunk %d should be present", cBare)
	}
}

// TestRecall_NewLibrarianWithoutClaude verifies the constructor returns
// a usable Librarian even when claude is not on PATH; non-claude paths
// (recall via trigram) still function.
// Refs: R2642
func TestRecall_NewLibrarianWithoutClaude(t *testing.T) {
	idx, dir := testIndexer(t)
	db := newTestDB(idx, dir)
	db.config = &Config{} // newTestDB leaves config nil; NewLibrarian needs it.

	// Hide claude by emptying PATH for the duration of NewLibrarian.
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", "")
	lib := NewLibrarian(db, dir)
	t.Setenv("PATH", origPath)

	if lib == nil {
		t.Fatal("NewLibrarian returned nil with claude not on PATH; should always return a Librarian")
	}
	if lib.Available() {
		t.Errorf("Available() should be false when claude is not on PATH")
	}

	c1, _ := indexLine(t, db, "1.txt", "apple banana cherry")
	indexLine(t, db, "2.txt", "apple banana grape")
	db.search = &Searcher{fts: db.fts, store: db.store, config: &Config{}, librarian: lib}

	res, err := lib.Recall([]ConnectionsInput{{ChunkID: c1}}, RecallOpts{K: 5, KeepTagless: true})
	if err != nil {
		t.Fatalf("Recall without claude: %v", err)
	}
	if len(res.Chunks) == 0 {
		t.Errorf("expected non-empty Recall result without claude")
	}
}

// TestRecall_HTTPServerAndLuaBridge validates the HTTP POST /recall endpoint
// and the Lua sys.recall bridge conversion logic.
// Refs: R2628, R2629
func TestRecall_HTTPServerAndLuaBridge(t *testing.T) {
	l, db := setupRecall(t)

	c1, _ := indexLine(t, db, "1.txt", "apple banana")
	c2, _ := indexLine(t, db, "2.txt", "apple cherry")
	db.store.WriteChunkEmbedding(c1, vecFrom(1.0, 0.0, 0.0, 0.0))
	db.store.WriteChunkEmbedding(c2, vecFrom(1.0, 0.0, 0.0, 0.0))

	// Case A: HTTP POST /recall
	body := map[string]any{
		"inputs": []ConnectionsInput{{ChunkID: c1}},
		"opts":   RecallOpts{K: 10, KeepTagless: true},
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/recall", bytes.NewReader(bodyBytes))
	rec := httptest.NewRecorder()

	l.HandleRecall(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var res RecallResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(res.Chunks) == 0 || res.Chunks[0].ChunkID != c2 {
		t.Errorf("expected recall results containing c2, got %+v", res)
	}

	// Case B: Lua sys.recall bridge
	L := lua.NewState()
	defer L.Close()

	recallFn := L.NewFunction(func(L *lua.LState) int {
		arr := L.CheckTable(1)
		inputs := luaTableToConnectionsInputs(arr)
		opts := RecallOpts{
			IncludeContent: true,
		}
		if optsTbl, ok := L.Get(2).(*lua.LTable); ok && optsTbl != nil {
			if v, ok := optsTbl.RawGetString("includeContent").(lua.LBool); ok {
				opts.IncludeContent = bool(v)
			}
			if v, ok := optsTbl.RawGetString("k").(lua.LNumber); ok {
				opts.K = int(v)
			}
			if v, ok := optsTbl.RawGetString("keepTagless").(lua.LBool); ok {
				opts.KeepTagless = bool(v)
			}
		}
		res, err := l.Recall(inputs, opts)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}

		resTbl := L.NewTable()
		if res.Warning != "" {
			L.SetField(resTbl, "warning", lua.LString(res.Warning))
		}
		chunksTbl := L.NewTable()
		for _, chunk := range res.Chunks {
			chunkTbl := L.NewTable()
			L.SetField(chunkTbl, "chunkID", lua.LNumber(chunk.ChunkID))
			L.SetField(chunkTbl, "path", lua.LString(chunk.Path))
			L.SetField(chunkTbl, "range", lua.LString(chunk.Range))
			L.SetField(chunkTbl, "score", lua.LNumber(chunk.Score))

			subTbl := L.NewTable()
			L.SetField(subTbl, "vectorEc", lua.LNumber(chunk.PerSubstrate.VectorEC))
			L.SetField(subTbl, "trigramEc", lua.LNumber(chunk.PerSubstrate.TrigramEC))
			L.SetField(chunkTbl, "perSubstrate", subTbl)

			tagsTbl := L.NewTable()
			for _, tv := range chunk.Tags {
				tagTbl := L.NewTable()
				L.SetField(tagTbl, "tag", lua.LString(tv.Tag))
				if tv.Value != "" {
					L.SetField(tagTbl, "value", lua.LString(tv.Value))
				}
				tagsTbl.Append(tagTbl)
			}
			L.SetField(chunkTbl, "tags", tagsTbl)

			if chunk.Content != "" {
				L.SetField(chunkTbl, "content", lua.LString(chunk.Content))
			}
			chunksTbl.Append(chunkTbl)
		}
		L.SetField(resTbl, "chunks", chunksTbl)

		L.Push(resTbl)
		return 1
	})

	L.SetGlobal("recall", recallFn)

	script := fmt.Sprintf(`
		local res, err = recall({ %d }, { k = 5, keepTagless = true })
		if err then error(err) end
		assert(res ~= nil, "res was nil")
		assert(res.chunks ~= nil, "chunks was nil")
		assert(#res.chunks > 0, "chunks empty")
		return res.chunks[1].chunkID
	`, c1)

	if err := L.DoString(script); err != nil {
		t.Fatalf("Lua error: %v", err)
	}

	retVal := L.Get(-1)
	if num, ok := retVal.(lua.LNumber); !ok || uint64(num) != c2 {
		t.Errorf("expected return chunkID %d, got %v", c2, retVal)
	}
}

// --- Discussed-tag filter tests ---
// Test: test-Discussed.md

// tagChunk attaches one (tag, value) to chunkID via UpdateTagValues.
func tagChunk(t *testing.T, db *DB, chunkID uint64, pairs ...TagValue) {
	t.Helper()
	if err := db.store.UpdateTagValues([]ChunkTagValues{
		{ChunkID: chunkID, Values: pairs},
	}); err != nil {
		t.Fatal(err)
	}
}

// recallSetupWithTaggedChunks returns three chunks where c1 is the
// query (excluded by self-chunk rule) and c2/c3 are candidates with
// different tag profiles. All three carry overlapping content so
// trigram-EC alone surfaces them.
func recallSetupWithTaggedChunks(t *testing.T) (l *Librarian, db *DB, c1, c2, c3 uint64) {
	t.Helper()
	l, db = setupRecall(t)
	c1, _ = indexLine(t, db, "1.txt", "apple banana cherry")
	c2, _ = indexLine(t, db, "2.txt", "apple banana grape")
	c3, _ = indexLine(t, db, "3.txt", "apple banana melon")
	tagChunk(t, db, c2,
		TagValue{Tag: "topic", Value: "messaging"},
		TagValue{Tag: "ext", Value: "other"},
	)
	tagChunk(t, db, c3,
		TagValue{Tag: "topic", Value: "auth"},
	)
	return l, db, c1, c2, c3
}

// TestRecall_DiscussedStripsTag verifies the substrate strips a
// discussed (tag, value) from a chunk's tag list but keeps the chunk
// when other tags survive. R2655, R2656, R2657
func TestRecall_DiscussedStripsTag(t *testing.T) {
	l, _, c1, c2, _ := recallSetupWithTaggedChunks(t)

	inputs := []ConnectionsInput{{ChunkID: c1}}
	res, err := l.Recall(inputs, RecallOpts{
		K:         10,
		Discussed: []Discussed{{Tag: "topic", Value: "messaging"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var got *RecalledChunk
	for i := range res.Chunks {
		if res.Chunks[i].ChunkID == c2 {
			got = &res.Chunks[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("expected c2 to surface, got chunks=%+v", res.Chunks)
	}
	for _, tv := range got.Tags {
		if tv.Tag == "topic" {
			t.Errorf("expected @topic to be stripped, found %+v", tv)
		}
	}
	var sawExt bool
	for _, tv := range got.Tags {
		if tv.Tag == "ext" {
			sawExt = true
		}
	}
	if !sawExt {
		t.Errorf("expected @ext to survive, tags=%+v", got.Tags)
	}
}

// TestRecall_DiscussedDropEmptied verifies a chunk emptied by the
// exclusion is dropped. R2656
func TestRecall_DiscussedDropEmptied(t *testing.T) {
	l, _, c1, _, c3 := recallSetupWithTaggedChunks(t)

	inputs := []ConnectionsInput{{ChunkID: c1}}
	res, err := l.Recall(inputs, RecallOpts{
		K:         10,
		Discussed: []Discussed{{Tag: "topic", Value: "auth"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range res.Chunks {
		if ch.ChunkID == c3 {
			t.Errorf("expected c3 dropped (only tag was discussed), got %+v", ch)
		}
	}
}

// TestRecall_DiscussedBareNameMatchesAny verifies a bare-name entry
// matches any value under that tag. R2657
func TestRecall_DiscussedBareNameMatchesAny(t *testing.T) {
	l, _, c1, c2, c3 := recallSetupWithTaggedChunks(t)

	inputs := []ConnectionsInput{{ChunkID: c1}}
	res, err := l.Recall(inputs, RecallOpts{
		K:         10,
		Discussed: []Discussed{{Tag: "topic"}}, // bare-name
	})
	if err != nil {
		t.Fatal(err)
	}
	// c2 keeps @ext (surviving), c3 had only @topic:auth (dropped).
	var sawC2, sawC3 bool
	for _, ch := range res.Chunks {
		switch ch.ChunkID {
		case c2:
			sawC2 = true
			for _, tv := range ch.Tags {
				if tv.Tag == "topic" {
					t.Errorf("expected all @topic stripped on c2, got %+v", tv)
				}
			}
		case c3:
			sawC3 = true
		}
	}
	if !sawC2 {
		t.Errorf("expected c2 to surface (still has @ext)")
	}
	if sawC3 {
		t.Errorf("expected c3 dropped (bare-name stripped its only tag)")
	}
}

// TestRecall_DiscussedExactPairPrecision verifies @name:value matches
// only the exact pair, not other values under the same name. R2657
func TestRecall_DiscussedExactPairPrecision(t *testing.T) {
	l, _, c1, _, c3 := recallSetupWithTaggedChunks(t)

	inputs := []ConnectionsInput{{ChunkID: c1}}
	res, err := l.Recall(inputs, RecallOpts{
		K: 10,
		// Exact-pair: only suppress @topic:messaging.
		Discussed: []Discussed{{Tag: "topic", Value: "messaging"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var sawC3 bool
	for _, ch := range res.Chunks {
		if ch.ChunkID == c3 {
			sawC3 = true
			var sawAuth bool
			for _, tv := range ch.Tags {
				if tv.Tag == "topic" && tv.Value == "auth" {
					sawAuth = true
				}
			}
			if !sawAuth {
				t.Errorf("expected @topic:auth preserved on c3, tags=%+v", ch.Tags)
			}
		}
	}
	if !sawC3 {
		t.Errorf("expected c3 to surface (its @topic:auth is not excluded)")
	}
}

// TestRecall_DiscussedOverridesKeepTagless verifies that a chunk
// emptied by the discussed filter is dropped even with KeepTagless
// set — `-all` does not override the discussed exclusion. R2658
func TestRecall_DiscussedOverridesKeepTagless(t *testing.T) {
	l, _, c1, _, c3 := recallSetupWithTaggedChunks(t)

	inputs := []ConnectionsInput{{ChunkID: c1}}
	res, err := l.Recall(inputs, RecallOpts{
		K:           10,
		KeepTagless: true,
		Discussed:   []Discussed{{Tag: "topic", Value: "auth"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range res.Chunks {
		if ch.ChunkID == c3 {
			t.Errorf("KeepTagless must not save chunk emptied by discussed, got %+v", ch)
		}
	}
}

// TestRecall_SessionLoadsRDSet verifies that opts.Session causes the
// substrate to load the session's RD records into the exclusion set.
// R2655
func TestRecall_SessionLoadsRDSet(t *testing.T) {
	l, db, c1, c2, _ := recallSetupWithTaggedChunks(t)

	// Mark @topic:messaging as discussed for session "S1".
	if err := db.store.AddDiscussed("S1", "topic", "messaging"); err != nil {
		t.Fatal(err)
	}

	inputs := []ConnectionsInput{{ChunkID: c1}}
	res, err := l.Recall(inputs, RecallOpts{
		K:       10,
		Session: "S1",
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, ch := range res.Chunks {
		if ch.ChunkID == c2 {
			for _, tv := range ch.Tags {
				if tv.Tag == "topic" && tv.Value == "messaging" {
					t.Errorf("expected @topic:messaging stripped via session, got %+v", tv)
				}
			}
		}
	}
}

// TestRecall_SessionUnionWithDiscussed verifies opts.Session and
// opts.Discussed combine via union. R2655
func TestRecall_SessionUnionWithDiscussed(t *testing.T) {
	l, db, c1, c2, c3 := recallSetupWithTaggedChunks(t)

	// Session marks @topic:messaging.
	if err := db.store.AddDiscussed("S1", "topic", "messaging"); err != nil {
		t.Fatal(err)
	}

	inputs := []ConnectionsInput{{ChunkID: c1}}
	res, err := l.Recall(inputs, RecallOpts{
		K:         10,
		Session:   "S1",
		Discussed: []Discussed{{Tag: "ext", Value: "other"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	// c2 had {topic:messaging, ext:other} — both discussed → dropped.
	// c3 had {topic:auth} — survives unchanged.
	for _, ch := range res.Chunks {
		if ch.ChunkID == c2 {
			t.Errorf("expected c2 dropped (both tags in union), got %+v", ch)
		}
	}
	var sawC3 bool
	for _, ch := range res.Chunks {
		if ch.ChunkID == c3 {
			sawC3 = true
		}
	}
	if !sawC3 {
		t.Errorf("expected c3 to surface")
	}
}

// TestRecall_EmptyDiscussedNoFilter verifies the substrate behaves
// exactly as before when Discussed and Session are both empty.
// R2655, R2660
func TestRecall_EmptyDiscussedNoFilter(t *testing.T) {
	l, _, c1, c2, c3 := recallSetupWithTaggedChunks(t)

	inputs := []ConnectionsInput{{ChunkID: c1}}
	res, err := l.Recall(inputs, RecallOpts{K: 10})
	if err != nil {
		t.Fatal(err)
	}
	var sawC2, sawC3 bool
	for _, ch := range res.Chunks {
		if ch.ChunkID == c2 {
			sawC2 = true
		}
		if ch.ChunkID == c3 {
			sawC3 = true
		}
	}
	if !sawC2 || !sawC3 {
		t.Errorf("expected both c2 and c3 to surface with no filter (sawC2=%v sawC3=%v)", sawC2, sawC3)
	}
}
