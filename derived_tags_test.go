package ark

// CRC: crc-Store.md, crc-Librarian.md | Test: test-DerivedTags.md

import (
	"strings"
	"testing"

	"go.etcd.io/bbolt"
)

// --- Dormant RF store methods ---
// RF is retired by #36 (recall-proposals-for-display); the compute-for-display
// pass no longer reads or writes it. The Store methods and record class are
// retained pending the banked full-teardown O-gap; these tests guard the
// retained code until then.

// TestStore_WriteAndReadDerivedFreshness round-trips a serial value.
// Refs: R2666, R2669 (dormant — see above)
func TestStore_WriteAndReadDerivedFreshness(t *testing.T) {
	_, db := setupRecall(t)
	const chunkID = uint64(42)
	const serial = uint64(12345)

	if err := db.store.bolt.Update(func(txn *bbolt.Tx) error {
		return db.store.WriteDerivedFreshness(txn, chunkID, serial)
	}); err != nil {
		t.Fatalf("WriteDerivedFreshness: %v", err)
	}

	var got uint64
	var found bool
	if err := db.store.bolt.View(func(txn *bbolt.Tx) error {
		s, ok, err := db.store.ReadDerivedFreshness(txn, chunkID)
		got, found = s, ok
		return err
	}); err != nil {
		t.Fatalf("ReadDerivedFreshness: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if got != serial {
		t.Errorf("serial: got %d want %d", got, serial)
	}
}

// TestStore_ReadDerivedFreshness_MissingReturnsZero verifies a fresh
// DB returns (0, false) for any chunk.
// Refs: R2669, R2682 (dormant — see above)
func TestStore_ReadDerivedFreshness_MissingReturnsZero(t *testing.T) {
	_, db := setupRecall(t)
	var got uint64
	var found bool
	if err := db.store.bolt.View(func(txn *bbolt.Tx) error {
		s, ok, err := db.store.ReadDerivedFreshness(txn, 999)
		got, found = s, ok
		return err
	}); err != nil {
		t.Fatalf("ReadDerivedFreshness: %v", err)
	}
	if found {
		t.Errorf("expected found=false; got serial=%d", got)
	}
	if got != 0 {
		t.Errorf("serial: got %d want 0", got)
	}
}

// TestStore_MaxEDSerial_TracksHighWater verifies MaxEDSerial reflects
// the highest stamped ED serial. The method is retained (no caller after
// #36 dropped the RF freshness comparator); this guards it.
func TestStore_MaxEDSerial_TracksHighWater(t *testing.T) {
	_, db := setupRecall(t)

	if err := db.store.WriteTagDefEmbedding("a", 10, vecFrom(1, 0, 0, 0)); err != nil {
		t.Fatalf("WriteTagDefEmbedding a: %v", err)
	}
	firstMax, err := db.store.MaxEDSerial()
	if err != nil {
		t.Fatalf("MaxEDSerial first: %v", err)
	}
	if firstMax == 0 {
		t.Error("expected non-zero max after first ED write")
	}

	if err := db.store.WriteTagDefEmbedding("b", 11, vecFrom(0, 1, 0, 0)); err != nil {
		t.Fatalf("WriteTagDefEmbedding b: %v", err)
	}
	secondMax, err := db.store.MaxEDSerial()
	if err != nil {
		t.Fatalf("MaxEDSerial second: %v", err)
	}
	if secondMax <= firstMax {
		t.Errorf("expected max to advance: first=%d second=%d", firstMax, secondMax)
	}
}

// --- Compute-for-display recall tests (state B, #36) ---

// findSurfaced returns the surfaced RecalledChunk with the given ID, or nil.
func findSurfaced(res *RecallResult, id uint64) *RecalledChunk {
	for i := range res.Chunks {
		if res.Chunks[i].ChunkID == id {
			return &res.Chunks[i]
		}
	}
	return nil
}

// assertNoDerivedWrites asserts the compute-for-display pass authored no
// @ext-candidate and wrote no RC/RF records — the core state-B guarantee
// (R3079). Pass the chunks whose RF absence should be checked.
func assertNoDerivedWrites(t *testing.T, db *DB, chunks ...uint64) {
	t.Helper()
	_ = db.store.bolt.View(func(txn *bbolt.Tx) error {
		n := 0
		_ = db.store.ScanAllDerivedCandidates(txn, func(_, _, _ uint64) error { n++; return nil })
		if n != 0 {
			t.Errorf("expected no RC records from the compute-for-display pass; got %d", n)
		}
		for _, c := range chunks {
			if _, ok, _ := db.store.ReadDerivedFreshness(txn, c); ok {
				t.Errorf("chunk %d: expected no RF stamp (compute-for-display writes none)", c)
			}
		}
		return nil
	})
	if m := externalMirrors(t); strings.Contains(m, "@ext-candidate") || strings.Contains(m, "@food") {
		t.Errorf("compute-for-display must author no @ext-candidate; mirrors:\n%s", m)
	}
}

// TestRecall_Propose_ComputesNoWrite: a single --propose pass surfaces the
// computed proposal in the SAME call via ProposedTags, and writes nothing to
// the index (no RC, no RF, no @ext-candidate). R3079, R3080.
func TestRecall_Propose_ComputesNoWrite(t *testing.T) {
	l, db := setupFileBackedRecall(t)
	cInput, _ := indexLine(t, db, "input.txt", "apple banana cherry")
	cTarget, _ := indexLine(t, db, "target.txt", "apple banana grape")
	db.store.WriteChunkEmbedding(cInput, vecFrom(1, 0, 0, 0))
	db.store.WriteChunkEmbedding(cTarget, vecFrom(1, 0, 0, 0))
	db.store.WriteTagDefEmbedding("food", 10, vecFrom(1, 0, 0, 0)) // cos=1.0

	res, err := l.Recall(
		[]ConnectionsInput{{ChunkID: cInput}},
		RecallOpts{K: 5, Propose: true, KeepTagless: true},
	)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	target := findSurfaced(res, cTarget)
	if target == nil {
		t.Fatalf("target chunk %d not surfaced", cTarget)
	}
	if len(target.ProposedTags) == 0 || target.ProposedTags[0] != "food" {
		t.Fatalf("expected food proposed in the same call; got %v", target.ProposedTags)
	}
	assertNoDerivedWrites(t, db, cInput, cTarget)
}

// TestRecall_Propose_SurfacesOrdered: proposals surface similarity-descending
// in the same call (food cos=1.0 before style cos≈0.7). R3080, R2684, R2686.
func TestRecall_Propose_SurfacesOrdered(t *testing.T) {
	l, db := setupFileBackedRecall(t)
	cInput, _ := indexLine(t, db, "input.txt", "apple banana cherry")
	cTarget, _ := indexLine(t, db, "target.txt", "apple banana grape")
	db.store.WriteChunkEmbedding(cInput, vecFrom(1, 0, 0, 0))
	db.store.WriteChunkEmbedding(cTarget, vecFrom(1, 0, 0, 0))
	db.store.WriteTagDefEmbedding("food", 10, vecFrom(1, 0, 0, 0))
	db.store.WriteTagDefEmbedding("style", 11, vecFrom(0.7, 0.7, 0, 0))

	res, err := l.Recall(
		[]ConnectionsInput{{ChunkID: cInput}},
		RecallOpts{K: 5, Propose: true, KeepTagless: true},
	)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	target := findSurfaced(res, cTarget)
	if target == nil {
		t.Fatalf("target chunk %d not surfaced", cTarget)
	}
	if len(target.ProposedTags) == 0 || target.ProposedTags[0] != "food" {
		t.Errorf("expected food first (highest cosine); got %v", target.ProposedTags)
	}
	if len(target.ProposedTagScores) != len(target.ProposedTags) {
		t.Errorf("scores must align with tags; tags=%v scores=%v",
			target.ProposedTags, target.ProposedTagScores)
	}
}

// TestRecall_Propose_EVLeg verifies R2911: a chunk resembling an existing tag
// *value* (EV) — with no definition (ED) for that tag — still earns the tag as
// a computed proposal, surfaced in the same call.
func TestRecall_Propose_EVLeg(t *testing.T) {
	l, db := setupFileBackedRecall(t)
	cInput, _ := indexLine(t, db, "input.txt", "apple banana cherry")
	cTarget, _ := indexLine(t, db, "target.txt", "apple banana grape")
	cHolder, _ := indexLine(t, db, "holder.txt", "unrelated note text")
	db.store.WriteChunkEmbedding(cInput, vecFrom(1, 0, 0, 0))
	db.store.WriteChunkEmbedding(cTarget, vecFrom(1, 0, 0, 0))

	// A tag VALUE "cuisine: italian" carried by another chunk, with an EV
	// embedding aligned to cTarget — and NO definition (ED) for "cuisine".
	// Only the EV leg can propose it.
	if err := db.store.UpdateTagValues([]ChunkTagValues{
		{ChunkID: cHolder, Values: []TagValue{{Tag: "cuisine", Value: "italian"}}},
	}); err != nil {
		t.Fatalf("UpdateTagValues: %v", err)
	}
	tvid, ok := db.store.TvidMap().Lookup("cuisine", "italian")
	if !ok {
		t.Fatalf("tvid for cuisine:italian not allocated")
	}
	if err := db.store.WriteTagValueEmbedding(tvid, vecFrom(1, 0, 0, 0)); err != nil {
		t.Fatalf("WriteTagValueEmbedding: %v", err)
	}

	res, err := l.Recall(
		[]ConnectionsInput{{ChunkID: cInput}},
		RecallOpts{K: 5, Propose: true, KeepTagless: true},
	)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	target := findSurfaced(res, cTarget)
	if target == nil {
		t.Fatalf("target chunk %d not surfaced", cTarget)
	}
	found := false
	for _, tag := range target.ProposedTags {
		if tag == "cuisine" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected cuisine proposed via the EV leg; got %v", target.ProposedTags)
	}
}

// TestRecall_Propose_FiltersAlreadyAttached verifies the alreadyOn filter — a
// chunk that already carries @food gets no derived @food proposal (R2671).
func TestRecall_Propose_FiltersAlreadyAttached(t *testing.T) {
	l, db := setupFileBackedRecall(t)
	cInput, _ := indexLine(t, db, "input.txt", "apple banana cherry")
	cTarget, _ := indexLine(t, db, "target.txt", "apple banana grape")
	db.store.WriteChunkEmbedding(cInput, vecFrom(1, 0, 0, 0))
	db.store.WriteChunkEmbedding(cTarget, vecFrom(1, 0, 0, 0))
	db.store.WriteTagDefEmbedding("food", 10, vecFrom(1, 0, 0, 0))

	// Attach @food: pasta to the target already.
	if err := db.store.UpdateTagValues([]ChunkTagValues{
		{ChunkID: cTarget, Values: []TagValue{{Tag: "food", Value: "pasta"}}},
	}); err != nil {
		t.Fatalf("UpdateTagValues: %v", err)
	}

	res, err := l.Recall(
		[]ConnectionsInput{{ChunkID: cInput}},
		RecallOpts{K: 5, Propose: true, KeepTagless: true},
	)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if target := findSurfaced(res, cTarget); target != nil {
		for _, tag := range target.ProposedTags {
			if tag == "food" {
				t.Errorf("@food should be filtered (already attached); got %v", target.ProposedTags)
			}
		}
	}
}

// TestRecall_Propose_SkipsRJRejected verifies the reject filter — a
// net-rejected (chunkID, tagname) in rejectByChunk is not proposed (R3070).
func TestRecall_Propose_SkipsRJRejected(t *testing.T) {
	l, db := setupFileBackedRecall(t)
	cInput, _ := indexLine(t, db, "input.txt", "apple banana cherry")
	cTarget, _ := indexLine(t, db, "target.txt", "apple banana grape")
	db.store.WriteChunkEmbedding(cInput, vecFrom(1, 0, 0, 0))
	db.store.WriteChunkEmbedding(cTarget, vecFrom(1, 0, 0, 0))
	db.store.WriteTagDefEmbedding("food", 10, vecFrom(1, 0, 0, 0))

	// Pre-reject @food on the target chunk (reject map, as a derived
	// @ext-judgment would populate).
	db.extmap.mu.Lock()
	db.extmap.rejectByChunk[cTarget] = map[string]int64{"food": -1}
	db.extmap.mu.Unlock()

	res, err := l.Recall(
		[]ConnectionsInput{{ChunkID: cInput}},
		RecallOpts{K: 5, Propose: true, KeepTagless: true},
	)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if target := findSurfaced(res, cTarget); target != nil {
		for _, tag := range target.ProposedTags {
			if tag == "food" {
				t.Errorf("@food should be filtered (net-rejected); got %v", target.ProposedTags)
			}
		}
	}
}

// TestRecall_Propose_ProposedTagsOmittedWithoutPropose verifies the field
// stays empty when --propose isn't set — and, since a derived RC record +
// reverse-lookup map are pre-seeded, that the recall path does NOT read RC
// (R3080: enrich reads the transient compute, not DerivedProposals).
// Refs: R2686, R3080
func TestRecall_Propose_ProposedTagsOmittedWithoutPropose(t *testing.T) {
	l, db := setupRecall(t)
	cInput, _ := indexLine(t, db, "input.txt", "apple banana cherry")
	cTarget, _ := indexLine(t, db, "target.txt", "apple banana grape")
	db.store.WriteChunkEmbedding(cInput, vecFrom(1, 0, 0, 0))
	db.store.WriteChunkEmbedding(cTarget, vecFrom(1, 0, 0, 0))

	// Pre-existing derived proposal on the target (map + RC record). Even with
	// this present, no --propose ⇒ no computed proposals ⇒ empty ProposedTags,
	// and the recall path never consults RC.
	tvid := db.store.tvids.AllocOverlay(extCandidateTag, "x @leftover:")
	if err := db.store.bolt.Update(func(txn *bbolt.Tx) error {
		return db.store.WriteDerivedCandidate(txn, tvid, cTarget, 1)
	}); err != nil {
		t.Fatalf("WriteDerivedCandidate: %v", err)
	}
	db.extmap.mu.Lock()
	db.extmap.candidateSourcesByChunk[cTarget] = []uint64{tvid}
	db.extmap.mu.Unlock()

	res, err := l.Recall(
		[]ConnectionsInput{{ChunkID: cInput}},
		RecallOpts{K: 5, KeepTagless: true},
	)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	for _, c := range res.Chunks {
		if len(c.ProposedTags) != 0 {
			t.Errorf("chunk %d: expected empty ProposedTags without --propose; got %v",
				c.ChunkID, c.ProposedTags)
		}
	}
}

// TestStore_ClearAllRecall_WipesAcrossSubstrates verifies the four
// ClearAll* helpers each remove every record under their respective
// prefix without touching the others. (RF is dormant but its clear still
// operates on residual records.)
// Refs: R2744
func TestStore_ClearAllRecall_WipesAcrossSubstrates(t *testing.T) {
	_, db := setupRecall(t)

	// Seed RC + RF + RJ records (source_tvid + target_chunkid keys).
	if err := db.store.bolt.Update(func(txn *bbolt.Tx) error {
		if err := db.store.WriteDerivedCandidate(txn, 100, 1, 1); err != nil {
			return err
		}
		if err := db.store.WriteDerivedCandidate(txn, 101, 2, 1); err != nil {
			return err
		}
		if err := db.store.WriteDerivedFreshness(txn, 1, 10); err != nil {
			return err
		}
		if err := db.store.WriteDerivedFreshness(txn, 2, 11); err != nil {
			return err
		}
		if err := db.store.WriteDerivedJudgment(txn, 102, 1, -1, 12345); err != nil {
			return err
		}
		return db.store.WriteDerivedJudgment(txn, 103, 2, -1, 12346)
	}); err != nil {
		t.Fatalf("seed RC/RF/RJ: %v", err)
	}
	countRC := func() int {
		n := 0
		_ = db.store.bolt.View(func(txn *bbolt.Tx) error {
			return db.store.ScanAllDerivedCandidates(txn, func(_, _, _ uint64) error { n++; return nil })
		})
		return n
	}
	countRJ := func() int {
		n := 0
		_ = db.store.bolt.View(func(txn *bbolt.Tx) error {
			return db.store.ScanAllDerivedJudgments(txn, func(_, _ uint64, _, _ int64) error { n++; return nil })
		})
		return n
	}
	if err := db.store.AddDiscussed("sess-A", "topic", "x"); err != nil {
		t.Fatalf("AddDiscussed A: %v", err)
	}
	if err := db.store.AddDiscussed("sess-B", "topic", "y"); err != nil {
		t.Fatalf("AddDiscussed B: %v", err)
	}

	// ClearAllDerivedProposals — wipes RC for both chunks, leaves
	// RF/RJ/RD intact.
	n, err := db.store.ClearAllDerivedProposals()
	if err != nil {
		t.Fatalf("ClearAllDerivedProposals: %v", err)
	}
	if n != 2 {
		t.Errorf("RC deleted = %d, want 2", n)
	}
	if countRC() != 0 {
		t.Errorf("RC still present after ClearAllDerivedProposals")
	}
	if countRJ() != 2 {
		t.Errorf("RJ should be intact after RC clear; got %d", countRJ())
	}

	// ClearAllDerivedFreshness — wipes RF for both chunks.
	n, err = db.store.ClearAllDerivedFreshness()
	if err != nil {
		t.Fatalf("ClearAllDerivedFreshness: %v", err)
	}
	if n != 2 {
		t.Errorf("RF deleted = %d, want 2", n)
	}
	if err := db.store.bolt.View(func(txn *bbolt.Tx) error {
		for _, cid := range []uint64{1, 2} {
			_, ok, _ := db.store.ReadDerivedFreshness(txn, cid)
			if ok {
				t.Errorf("chunk %d: RF still present after ClearAllDerivedFreshness", cid)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("verify RF: %v", err)
	}

	// ClearAllDerivedRejections — wipes RJ for both chunks.
	n, err = db.store.ClearAllDerivedRejections()
	if err != nil {
		t.Fatalf("ClearAllDerivedRejections: %v", err)
	}
	if n != 2 {
		t.Errorf("RJ deleted = %d, want 2", n)
	}
	if countRJ() != 0 {
		t.Errorf("RJ still present after ClearAllDerivedRejections")
	}

	// ClearAllDiscussed — wipes RD across every session.
	n, err = db.store.ClearAllDiscussed()
	if err != nil {
		t.Fatalf("ClearAllDiscussed: %v", err)
	}
	if n != 2 {
		t.Errorf("RD deleted = %d, want 2", n)
	}
	for _, sess := range []string{"sess-A", "sess-B"} {
		entries, _ := db.store.ListDiscussed(sess, 0, 0)
		if len(entries) != 0 {
			t.Errorf("session %s: RD still has %d entries after ClearAllDiscussed", sess, len(entries))
		}
	}
}

// TestRecall_Propose_MinSimilarityFloor verifies the chunk-EC ↔ tag-ED cosine
// floor ([recall].min_propose_similarity) drops sub-threshold candidates from
// the computed proposals, and surfaces scores via ProposedTagScores aligned to
// ProposedTags. R2742, R2743.
func TestRecall_Propose_MinSimilarityFloor(t *testing.T) {
	l, db := setupFileBackedRecall(t)
	floor := 0.5
	db.config.Recall.MinProposeSimilarity = &floor

	cInput, _ := indexLine(t, db, "input.txt", "apple banana cherry")
	cTarget, _ := indexLine(t, db, "target.txt", "apple banana grape")
	db.store.WriteChunkEmbedding(cInput, vecFrom(1, 0, 0, 0))
	db.store.WriteChunkEmbedding(cTarget, vecFrom(1, 0, 0, 0))
	// Aligned with chunk vector: cosine 1.0, well above floor.
	db.store.WriteTagDefEmbedding("food", 10, vecFrom(1, 0, 0, 0))
	// Mostly orthogonal: cosine 0.3 / √1.09 ≈ 0.287, below floor.
	db.store.WriteTagDefEmbedding("noise", 11, vecFrom(0.3, 1, 0, 0))

	res, err := l.Recall(
		[]ConnectionsInput{{ChunkID: cInput}},
		RecallOpts{K: 5, Propose: true, KeepTagless: true},
	)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	target := findSurfaced(res, cTarget)
	if target == nil {
		t.Fatalf("target chunk %d not surfaced", cTarget)
	}
	if len(target.ProposedTags) != 1 || target.ProposedTags[0] != "food" {
		t.Fatalf("expected exactly [food] (noise below floor); got %v", target.ProposedTags)
	}
	if len(target.ProposedTagScores) != 1 {
		t.Fatalf("expected one score aligned to one tag; got %v", target.ProposedTagScores)
	}
	if target.ProposedTagScores[0] < 0.99 {
		t.Errorf("expected food cosine ≈ 1.0; got %v", target.ProposedTagScores[0])
	}
}

// TestRecall_Propose_NoModelIsNoOp verifies --propose without
// EmbeddingAvailable is silent: no computed proposals, recall result
// unaffected, and nothing written. R2676.
func TestRecall_Propose_NoModelIsNoOp(t *testing.T) {
	_, db := setupRecall(t)
	// Reset Librarian without modelPath so EmbeddingAvailable() = false.
	l := &Librarian{db: db} // modelPath empty
	db.search = &Searcher{
		fts:       db.fts,
		store:     db.store,
		config:    &Config{},
		librarian: l,
	}

	cInput, _ := indexLine(t, db, "input.txt", "apple banana cherry")
	cTarget, _ := indexLine(t, db, "target.txt", "apple banana grape")
	res, err := l.Recall(
		[]ConnectionsInput{{ChunkID: cInput}},
		RecallOpts{K: 5, Propose: true, KeepTagless: true},
	)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	for _, c := range res.Chunks {
		if len(c.ProposedTags) != 0 {
			t.Errorf("chunk %d: expected no proposals without embedding; got %v", c.ChunkID, c.ProposedTags)
		}
	}
	_ = cTarget
}

// TestRecall_Propose_InjectsConversationChunks verifies R3082: a chunk passed
// in RecallOpts.ConversationChunks earns computed proposals and is appended to
// the result even when A66 self-exclusion would otherwise drop it (it is the
// query input); without the injection it does not appear. Authors nothing.
func TestRecall_Propose_InjectsConversationChunks(t *testing.T) {
	l, db := setupFileBackedRecall(t)
	// cConv is BOTH the query input (so A66 self-excludes it from search
	// results) and the injected conversation chunk. Its EC aligns with the
	// "food" tag def, so the compute-for-display pass earns it a tag.
	cConv, _ := indexLine(t, db, "conv.txt", "zebra xylophone quartz")
	cOther, _ := indexLine(t, db, "other.txt", "apple banana cherry")
	db.store.WriteChunkEmbedding(cConv, vecFrom(0, 1, 0, 0))
	db.store.WriteChunkEmbedding(cOther, vecFrom(1, 0, 0, 0))
	db.store.WriteTagDefEmbedding("food", 10, vecFrom(0, 1, 0, 0)) // aligns cConv

	// With injection: cConv is self-excluded from search but re-admitted by the
	// conversation injection (A66 bypassed), earns "food", and is appended.
	res, err := l.Recall(
		[]ConnectionsInput{{ChunkID: cConv}},
		RecallOpts{K: 5, Propose: true, KeepTagless: true, ConversationChunks: []uint64{cConv}},
	)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	conv := findSurfaced(res, cConv)
	if conv == nil {
		t.Fatalf("injected conversation chunk %d not in result (A66 bypass failed)", cConv)
	}
	if len(conv.ProposedTags) == 0 || conv.ProposedTags[0] != "food" {
		t.Errorf("expected food proposed on the conversation chunk; got %v", conv.ProposedTags)
	}
	assertNoDerivedWrites(t, db, cConv, cOther)

	// Without injection: cConv is the query input, self-excluded (A66), so it
	// does not appear even though it has an EC vector.
	res2, err := l.Recall(
		[]ConnectionsInput{{ChunkID: cConv}},
		RecallOpts{K: 5, Propose: true, KeepTagless: true},
	)
	if err != nil {
		t.Fatalf("Recall (no inject): %v", err)
	}
	if c := findSurfaced(res2, cConv); c != nil {
		t.Errorf("conversation chunk %d should be self-excluded without injection; got %+v", cConv, c)
	}
}
