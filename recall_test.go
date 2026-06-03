package ark

// CRC: crc-Librarian.md, crc-Server.md | Test: test-Recall.md

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// setupRecall sets up a test Indexer, Store, DB, and Librarian
// with a fake model path so EmbeddingAvailable() is true. The DB
// actor (svc) is started so paths that route writes through
// SyncVoid (e.g. the --propose derivation pass) don't hang.
func setupRecall(t *testing.T) (*Librarian, *DB) {
	t.Helper()
	idx, dir := testIndexer(t)
	db := newTestDB(idx, dir)
	db.svc = make(chan func(), 16)
	go runSvc(db.svc)
	t.Cleanup(func() {
		if db.svc != nil {
			close(db.svc)
			db.svc = nil
		}
	})

	l := &Librarian{
		db:        db,
		modelPath: "fake-model.gguf",
	}

	db.config = &Config{}

	db.search = &Searcher{
		fts:       db.fts,
		store:     db.store,
		config:    db.config,
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

// TestRecall_TagAxisRetrieval verifies the part-2 tag axis: an input
// matching a tag *value* surfaces a chunk carrying that value via its V
// record, even when the chunk's prose doesn't match and it has no content
// embedding — so it can only enter the candidate set through the tag axis
// (the @cuisine: italian case). R2905, R2906
func TestRecall_TagAxisRetrieval(t *testing.T) {
	l, db := setupRecall(t)

	// The query chunk's EC vector doubles as the "about italian" query.
	cQuery, _ := indexLine(t, db, "query.txt", "what should we cook tonight")
	italianVec := vecFrom(0.0, 0.0, 1.0, 0.0)
	if err := db.store.WriteChunkEmbedding(cQuery, italianVec); err != nil {
		t.Fatalf("WriteChunkEmbedding(query): %v", err)
	}

	// The target chunk's prose shares nothing with the query and it gets
	// NO content embedding, so neither content pass can surface it. It
	// carries @cuisine: italian via a V record only.
	cTarget, _ := indexLine(t, db, "recipe.txt", "zzz qqq wibble")
	if err := db.store.UpdateTagValues([]ChunkTagValues{
		{ChunkID: cTarget, Values: []TagValue{{Tag: "cuisine", Value: "italian"}}},
	}); err != nil {
		t.Fatalf("UpdateTagValues: %v", err)
	}

	// EV record for the value "italian", aligned with the query vector so
	// the tag-axis vector leg scores it high.
	tvid, ok := db.store.TvidMap().Lookup("cuisine", "italian")
	if !ok {
		t.Fatalf("tvid for cuisine:italian not allocated")
	}
	if err := db.store.WriteTagValueEmbedding(tvid, italianVec); err != nil {
		t.Fatalf("WriteTagValueEmbedding: %v", err)
	}

	inputs := []ConnectionsInput{{ChunkID: cQuery}}
	res, err := l.Recall(inputs, RecallOpts{K: 10})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}

	var got *RecalledChunk
	for i := range res.Chunks {
		if res.Chunks[i].ChunkID == cTarget {
			got = &res.Chunks[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("target chunk did not surface via the tag axis")
	}
	if got.PerSubstrate.TagVector < 0.9 {
		t.Errorf("expected high TagVector (vector leg), got %.3f", got.PerSubstrate.TagVector)
	}
	if got.PerSubstrate.VectorEC != 0 || got.PerSubstrate.TrigramEC != 0 {
		t.Errorf("expected zero content components (entered via tag axis only), got vectorEC=%.3f trigramEC=%.3f",
			got.PerSubstrate.VectorEC, got.PerSubstrate.TrigramEC)
	}
	if got.Score < 0.9 {
		t.Errorf("expected overall score folded from the tag axis >= 0.9, got %.3f", got.Score)
	}
}

// mkCand builds a recallCandidate with a synthetic score accumulator —
// for the pure allocate2x2 unit tests (no DB). Component order:
// vectorEC, trigramEC, tagVector, tagTrigram.
func mkCand(id, file uint64, src recallSource, size uint64, vec, tri, tvec, ttri float64) recallCandidate {
	return recallCandidate{
		acc:     &chunkScoresAcc{vectorEC: vec, trigramEC: tri, tagVector: tvec, tagTrigram: ttri},
		key:     fmt.Sprintf("%d", id),
		chunkID: id,
		fileID:  file,
		source:  src,
		size:    size,
	}
}

// TestAllocate2x2_FillsFourCells: one candidate dominant in each of the
// four (source × axis) cells lands in its own cell. R2907
func TestAllocate2x2_FillsFourCells(t *testing.T) {
	cands := []recallCandidate{
		mkCand(1, 1, sourceMain, 10, 0.9, 0, 0, 0),         // main-meaning
		mkCand(2, 2, sourceMain, 10, 0, 0, 0.9, 0),         // main-tags
		mkCand(3, 3, sourceConversation, 10, 0.9, 0, 0, 0), // conversation-meaning
		mkCand(4, 4, sourceConversation, 10, 0, 0, 0.9, 0), // conversation-tags
	}
	got := allocate2x2(cands, 1, 20)
	if len(got) != 4 {
		t.Fatalf("want 4 surfaced, got %d", len(got))
	}
	cellByID := map[uint64]string{}
	for _, s := range got {
		cellByID[s.cand.chunkID] = s.cell
	}
	want := map[uint64]string{
		1: cellMainMeaning, 2: cellMainTags,
		3: cellConversationMeaning, 4: cellConversationTags,
	}
	for id, w := range want {
		if cellByID[id] != w {
			t.Errorf("chunk %d: want cell %s, got %s", id, w, cellByID[id])
		}
	}
}

// TestAllocate2x2_TwoPerFileWithinCell: a cell admits at most two chunks
// from the same file, even with perCell budget to spare. R2907
func TestAllocate2x2_TwoPerFileWithinCell(t *testing.T) {
	cands := []recallCandidate{
		mkCand(1, 7, sourceMain, 10, 0.9, 0, 0, 0),
		mkCand(2, 7, sourceMain, 10, 0.8, 0, 0, 0),
		mkCand(3, 7, sourceMain, 10, 0.7, 0, 0, 0),
	}
	got := allocate2x2(cands, 3, 20)
	if len(got) != 2 {
		t.Fatalf("want 2 (≤2/file within cell), got %d", len(got))
	}
	for _, s := range got {
		if s.cand.chunkID == 3 {
			t.Errorf("third same-file chunk should be capped out")
		}
	}
}

// TestAllocate2x2_DedupKeepsStrongerCell: a chunk eligible in both its
// meaning and tags cells surfaces once, in the higher-scoring cell. R2908
func TestAllocate2x2_DedupKeepsStrongerCell(t *testing.T) {
	cands := []recallCandidate{
		mkCand(1, 1, sourceMain, 10, 0.9, 0, 0.5, 0), // meaning 0.9 > tags 0.5
	}
	got := allocate2x2(cands, 3, 20)
	if len(got) != 1 {
		t.Fatalf("want 1 (surfaced once), got %d", len(got))
	}
	if got[0].cell != cellMainMeaning {
		t.Errorf("want stronger cell %s, got %s", cellMainMeaning, got[0].cell)
	}
}

// TestAllocate2x2_BackfillStarvedCell: when three cells are empty, their
// budget redistributes to the populated cell up to the 4×perCell target. R2908
func TestAllocate2x2_BackfillStarvedCell(t *testing.T) {
	var cands []recallCandidate
	for i := uint64(1); i <= 5; i++ {
		cands = append(cands, mkCand(i, i, sourceMain, 10, 0.9, 0, 0, 0))
	}
	got := allocate2x2(cands, 1, 20)
	if len(got) != 4 {
		t.Fatalf("want 4 (target 4×1 backfilled into one cell), got %d", len(got))
	}
	for _, s := range got {
		if s.cell != cellMainMeaning {
			t.Errorf("want all main-meaning, got %s", s.cell)
		}
	}
}

// TestAllocate2x2_SizeTiebreakVector: among equal vector-won scores, larger
// chunks win the size tiebreak — so when the target caps the cell, the
// smallest is dropped. R2907 (SIGNAL Q2.1)
func TestAllocate2x2_SizeTiebreakVector(t *testing.T) {
	var cands []recallCandidate
	for _, sz := range []uint64{10, 20, 30, 40, 50} {
		cands = append(cands, mkCand(sz, sz, sourceMain, sz, 0.8, 0, 0, 0)) // vector-won
	}
	got := allocate2x2(cands, 1, 20)
	ids := map[uint64]bool{}
	for _, s := range got {
		ids[s.cand.chunkID] = true
	}
	if len(got) != 4 || ids[10] {
		t.Errorf("vector tiebreak: want 4 surfaced excluding smallest(10), got ids=%v", ids)
	}
}

// TestAllocate2x2_SizeTiebreakTrigram: among equal trigram-won scores,
// smaller chunks win the size tiebreak — so the largest is dropped at the
// cap. R2907 (SIGNAL Q2.1)
func TestAllocate2x2_SizeTiebreakTrigram(t *testing.T) {
	var cands []recallCandidate
	for _, sz := range []uint64{10, 20, 30, 40, 50} {
		cands = append(cands, mkCand(sz, sz, sourceMain, sz, 0, 0.8, 0, 0)) // trigram-won
	}
	got := allocate2x2(cands, 1, 20)
	ids := map[uint64]bool{}
	for _, s := range got {
		ids[s.cand.chunkID] = true
	}
	if len(got) != 4 || ids[50] {
		t.Errorf("trigram tiebreak: want 4 surfaced excluding largest(50), got ids=%v", ids)
	}
}

// TestChatSubchunks verifies the deterministic, document-order re-chunk that
// underpins the path:range:N locator (funnel and `ark chunks PATH:RANGE:N`
// must agree on which paragraph N names). R2910
func TestChatSubchunks(t *testing.T) {
	content := "First paragraph about apples.\n\nSecond paragraph about zebras.\n\nThird paragraph about oranges."
	subs := chatSubchunks(content)
	if len(subs) != 3 {
		t.Fatalf("want 3 sub-chunks, got %d", len(subs))
	}
	for i, w := range []string{"apples", "zebras", "oranges"} {
		if !bytes.Contains(subs[i].Content, []byte(w)) {
			t.Errorf("sub-chunk %d: want content containing %q, got %q", i, w, subs[i].Content)
		}
	}
	subs2 := chatSubchunks(content)
	for i := range subs {
		if string(subs[i].Content) != string(subs2[i].Content) {
			t.Errorf("sub-chunk %d not deterministic across re-chunks", i)
		}
	}
}

// TestRecall_ChatFunnel verifies the conversation funnel (R2910): a long
// matched turn surfaces the relevant paragraph as a path:range:N sub-chunk in
// the conversation-meaning cell, not the whole turn. Runs trigram-only here
// (the test's fake model can't embed), exercising the no-model funnel path.
func TestRecall_ChatFunnel(t *testing.T) {
	l, db := setupRecall(t)
	if err := db.fts.AddChunker("chat-jsonl", JSONLChunker{}); err != nil {
		t.Fatalf("register chat-jsonl chunker: %v", err)
	}

	// One chat-jsonl turn, three paragraphs; only the middle one matches.
	turn := `{"type":"user","content":"The orchard notes cover apples and pears in careful detail.\n\nWe also discussed zebras and giraffes roaming the savanna grasslands.\n\nFinally the talk turned to oranges and citrus cultivation methods."}`
	fp := filepath.Join(db.dbPath, "session-abc.jsonl")
	if err := os.WriteFile(fp, []byte(turn+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.indexer.AddFile(fp, "chat-jsonl"); err != nil {
		t.Fatalf("AddFile chat-jsonl: %v", err)
	}

	inputs := []ConnectionsInput{{Text: "zebras and giraffes roaming the savanna grasslands"}}
	res, err := l.Recall(inputs, RecallOpts{K: 10, IncludeContent: true, KeepTagless: true})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}

	var sub *RecalledChunk
	for i := range res.Chunks {
		if strings.Contains(res.Chunks[i].Range, ":") { // turnRange:N sub-chunk locator
			sub = &res.Chunks[i]
			break
		}
	}
	if sub == nil {
		t.Fatalf("no sub-chunk surfaced via the funnel; chunks=%+v", res.Chunks)
	}
	if !strings.Contains(sub.Content, "zebras") {
		t.Errorf("sub-chunk should be the matching paragraph, got %q", sub.Content)
	}
	if strings.Contains(sub.Content, "apples") || strings.Contains(sub.Content, "oranges") {
		t.Errorf("sub-chunk should be just the matched paragraph, not the whole turn: %q", sub.Content)
	}
	if sub.Cell != cellConversationMeaning {
		t.Errorf("want cell %s, got %s", cellConversationMeaning, sub.Cell)
	}
}

// TestChatSubchunkResolve verifies the resolve side of the path:range:N
// locator (`ark chunks PATH:RANGE:N` → DB.ChatSubchunk): the N-th paragraph
// of the turn, deterministic and indexed-faithful; dropping N would fetch
// the whole turn. R2914
func TestChatSubchunkResolve(t *testing.T) {
	_, db := setupRecall(t)
	if err := db.fts.AddChunker("chat-jsonl", JSONLChunker{}); err != nil {
		t.Fatalf("register chat-jsonl: %v", err)
	}
	turn := `{"type":"user","content":"Alpha paragraph here.\n\nBeta paragraph here.\n\nGamma paragraph here."}`
	fp := filepath.Join(db.dbPath, "sess.jsonl")
	if err := os.WriteFile(fp, []byte(turn+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fileid, err := db.indexer.AddFile(fp, "chat-jsonl")
	if err != nil {
		t.Fatalf("AddFile: %v", err)
	}
	info, err := db.fts.FileInfoByID(fileid)
	if err != nil || len(info.Chunks) == 0 {
		t.Fatalf("FileInfoByID: %v", err)
	}
	ci, err := db.ChunkInfo(info.Chunks[0].ChunkID)
	if err != nil {
		t.Fatalf("ChunkInfo: %v", err)
	}

	if got, ok := db.ChatSubchunk(ci.Path, ci.Range, "Beta"); !ok || !strings.Contains(got, "Beta") {
		t.Errorf("anchor Beta = %q ok=%v; want the Beta paragraph", got, ok)
	}
	if got, ok := db.ChatSubchunk(ci.Path, ci.Range, "Alpha"); !ok || !strings.Contains(got, "Alpha") {
		t.Errorf("anchor Alpha = %q ok=%v; want the Alpha paragraph", got, ok)
	}
	if _, ok := db.ChatSubchunk(ci.Path, ci.Range, "no-such-text-xyz"); ok {
		t.Errorf("missing anchor should be ok=false")
	}
}
