package ark

// CRC: crc-Store.md, crc-Librarian.md | Test: test-DerivedTags.md

import (
	"strings"
	"testing"

	"go.etcd.io/bbolt"
)

// TestStore_WriteAndReadDerivedFreshness round-trips a serial value.
// Refs: R2666, R2669
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
// Refs: R2669, R2682
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
// the highest stamped ED serial.
// Refs: R2669
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

// --- Recall-level tests ---

// TestRecall_Propose_WritesRCAndRF runs a single --propose pass and
// verifies RC and RF records exist for the matched (target) chunk.
// The input chunk is excluded from results via self-chunk exclusion
// (R2623), so we use two chunks: cInput as the query, cTarget as the
// derivation subject.
// Refs: R2667, R2670, R2674, R2669
func TestRecall_Propose_WritesRCAndRF(t *testing.T) {
	l, db := setupFileBackedRecall(t)
	cInput, _ := indexLine(t, db, "input.txt", "apple banana cherry")
	cTarget, _ := indexLine(t, db, "target.txt", "apple banana grape")
	db.store.WriteChunkEmbedding(cInput, vecFrom(1, 0, 0, 0))
	db.store.WriteChunkEmbedding(cTarget, vecFrom(1, 0, 0, 0))
	db.store.WriteTagDefEmbedding("food", 10, vecFrom(1, 0, 0, 0)) // cos=1.0

	if _, err := l.Recall(
		[]ConnectionsInput{{ChunkID: cInput}},
		RecallOpts{K: 5, Propose: true, KeepTagless: true},
	); err != nil {
		t.Fatalf("Recall: %v", err)
	}

	reindexMirrors(t, db)
	props, err := db.store.DerivedProposals(cTarget)
	if err != nil {
		t.Fatalf("DerivedProposals: %v", err)
	}
	foundFood := false
	for _, p := range props {
		if p.Tagname == "food" {
			foundFood = true
			if p.Tally != 1 {
				t.Errorf("food tally: got %d want 1", p.Tally)
			}
		}
	}
	if !foundFood {
		t.Errorf("expected RC for (chunk %d, food); got %+v", cTarget, props)
	}

	var rf uint64
	if err := db.store.bolt.View(func(txn *bbolt.Tx) error {
		s, _, err := db.store.ReadDerivedFreshness(txn, cTarget)
		rf = s
		return err
	}); err != nil {
		t.Fatalf("ReadDerivedFreshness: %v", err)
	}
	if rf == 0 {
		t.Error("expected RF stamp > 0 after derivation")
	}
}

// TestRecall_Propose_EVLeg verifies R2911: a chunk resembling an existing tag
// *value* (EV) — with no definition (ED) for that tag — still gets the tag
// proposed. Without the EV leg there is no ED to derive against, so the pass
// would propose nothing.
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

	if _, err := l.Recall(
		[]ConnectionsInput{{ChunkID: cInput}},
		RecallOpts{K: 5, Propose: true, KeepTagless: true},
	); err != nil {
		t.Fatalf("Recall: %v", err)
	}

	reindexMirrors(t, db)
	props, err := db.store.DerivedProposals(cTarget)
	if err != nil {
		t.Fatalf("DerivedProposals: %v", err)
	}
	found := false
	for _, p := range props {
		if p.Tagname == "cuisine" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected cuisine proposed for cTarget via the EV leg; got %+v", props)
	}
}

// TestRecall_Propose_FreshnessSkipsRedundantWork verifies a second
// --propose call with no ED change leaves the tally unchanged
// (freshness skip).
// Refs: R2669
func TestRecall_Propose_FreshnessSkipsRedundantWork(t *testing.T) {
	l, db := setupFileBackedRecall(t)
	cInput, _ := indexLine(t, db, "input.txt", "apple banana cherry")
	cTarget, _ := indexLine(t, db, "target.txt", "apple banana grape")
	db.store.WriteChunkEmbedding(cInput, vecFrom(1, 0, 0, 0))
	db.store.WriteChunkEmbedding(cTarget, vecFrom(1, 0, 0, 0))
	db.store.WriteTagDefEmbedding("food", 10, vecFrom(1, 0, 0, 0))

	for range 2 {
		if _, err := l.Recall(
			[]ConnectionsInput{{ChunkID: cInput}},
			RecallOpts{K: 5, Propose: true, KeepTagless: true},
		); err != nil {
			t.Fatalf("Recall: %v", err)
		}
	}

	reindexMirrors(t, db)
	props, err := db.store.DerivedProposals(cTarget)
	if err != nil {
		t.Fatalf("DerivedProposals: %v", err)
	}
	for _, p := range props {
		if p.Tagname == "food" && p.Tally != 1 {
			t.Errorf("food tally after 2 passes: got %d want 1 (freshness should skip)", p.Tally)
		}
	}
}

// TestRecall_Propose_StaleEDRetriggersDerive verifies that writing a
// new ED record invalidates RF and a subsequent --propose advances
// the tally.
// Refs: R2669, R2674
func TestRecall_Propose_StaleEDRetriggersDerive(t *testing.T) {
	l, db := setupFileBackedRecall(t)
	cInput, _ := indexLine(t, db, "input.txt", "apple banana cherry")
	cTarget, _ := indexLine(t, db, "target.txt", "apple banana grape")
	db.store.WriteChunkEmbedding(cInput, vecFrom(1, 0, 0, 0))
	db.store.WriteChunkEmbedding(cTarget, vecFrom(1, 0, 0, 0))
	db.store.WriteTagDefEmbedding("food", 10, vecFrom(1, 0, 0, 0))

	if _, err := l.Recall(
		[]ConnectionsInput{{ChunkID: cInput}},
		RecallOpts{K: 5, Propose: true, KeepTagless: true},
	); err != nil {
		t.Fatalf("first Recall: %v", err)
	}

	// Advance the ED landscape — new ED write bumps the S serial.
	db.store.WriteTagDefEmbedding("style", 11, vecFrom(0, 1, 0, 0))

	if _, err := l.Recall(
		[]ConnectionsInput{{ChunkID: cInput}},
		RecallOpts{K: 5, Propose: true, KeepTagless: true},
	); err != nil {
		t.Fatalf("second Recall: %v", err)
	}

	reindexMirrors(t, db)
	props, _ := db.store.DerivedProposals(cTarget)
	for _, p := range props {
		if p.Tagname == "food" && p.Tally != 2 {
			t.Errorf("food tally after ED change: got %d want 2 (should re-derive)", p.Tally)
		}
	}
}

// TestRecall_Propose_FiltersAlreadyAttached verifies the alreadyOn
// filter — a chunk that already carries @food doesn't get a derived
// @food proposal.
// Refs: R2671
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

	if _, err := l.Recall(
		[]ConnectionsInput{{ChunkID: cInput}},
		RecallOpts{K: 5, Propose: true},
	); err != nil {
		t.Fatalf("Recall: %v", err)
	}

	reindexMirrors(t, db)
	props, _ := db.store.DerivedProposals(cTarget)
	for _, p := range props {
		if p.Tagname == "food" {
			t.Errorf("@food should be filtered (already attached); got %+v", p)
		}
	}
}

// TestRecall_Propose_SkipsRJRejected verifies the reject filter — a
// net-rejected (chunkID, tagname) in rejectByChunk is not re-proposed.
// Refs: R3070
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

	if _, err := l.Recall(
		[]ConnectionsInput{{ChunkID: cInput}},
		RecallOpts{K: 5, Propose: true, KeepTagless: true},
	); err != nil {
		t.Fatalf("Recall: %v", err)
	}

	// The reject filter suppressed the candidate — no @food authored.
	if m := externalMirrors(t); strings.Contains(m, "@food:") {
		t.Errorf("@food should be filtered (net-rejected); mirrors:\n%s", m)
	}
	reindexMirrors(t, db)
	props, _ := db.store.DerivedProposals(cTarget)
	for _, p := range props {
		if p.Tagname == "food" {
			t.Errorf("@food should be filtered (RJ); got %+v", p)
		}
	}
}

// TestRecall_Propose_StencilEmitsProposedTags verifies the surfaced
// RecalledChunk carries ProposedTags when --propose is set and the
// chunk has RC records.
// Refs: R2684, R2686
func TestRecall_Propose_StencilEmitsProposedTags(t *testing.T) {
	l, db := setupFileBackedRecall(t)
	cInput, _ := indexLine(t, db, "input.txt", "apple banana cherry")
	cTarget, _ := indexLine(t, db, "target.txt", "apple banana grape")
	db.store.WriteChunkEmbedding(cInput, vecFrom(1, 0, 0, 0))
	db.store.WriteChunkEmbedding(cTarget, vecFrom(1, 0, 0, 0))
	db.store.WriteTagDefEmbedding("food", 10, vecFrom(1, 0, 0, 0))
	db.store.WriteTagDefEmbedding("style", 11, vecFrom(0.7, 0.7, 0, 0))

	// First pass authors the @ext-candidate; the derivation is async (the
	// RC record lands on reindex), so proposals surface on the NEXT recall.
	if _, err := l.Recall(
		[]ConnectionsInput{{ChunkID: cInput}},
		RecallOpts{K: 5, Propose: true, KeepTagless: true},
	); err != nil {
		t.Fatalf("first Recall: %v", err)
	}
	reindexMirrors(t, db)

	res, err := l.Recall(
		[]ConnectionsInput{{ChunkID: cInput}},
		RecallOpts{K: 5, Propose: true, KeepTagless: true},
	)
	if err != nil {
		t.Fatalf("second Recall: %v", err)
	}
	var target *RecalledChunk
	for i := range res.Chunks {
		if res.Chunks[i].ChunkID == cTarget {
			target = &res.Chunks[i]
		}
	}
	if target == nil {
		t.Fatalf("target chunk %d not surfaced; got chunks: %+v", cTarget, res.Chunks)
	}
	if len(target.ProposedTags) == 0 {
		t.Fatalf("expected ProposedTags populated; got empty")
	}
	if target.ProposedTags[0] != "food" {
		t.Errorf("expected food first; got %v", target.ProposedTags)
	}
}

// TestRecall_Propose_ProposedTagsOmittedWithoutPropose verifies the
// field stays empty when --propose isn't set, even with existing RC
// records in the database.
// Refs: R2686
func TestRecall_Propose_ProposedTagsOmittedWithoutPropose(t *testing.T) {
	l, db := setupRecall(t)
	cInput, _ := indexLine(t, db, "input.txt", "apple banana cherry")
	cTarget, _ := indexLine(t, db, "target.txt", "apple banana grape")
	db.store.WriteChunkEmbedding(cInput, vecFrom(1, 0, 0, 0))
	db.store.WriteChunkEmbedding(cTarget, vecFrom(1, 0, 0, 0))

	// Pre-existing derived proposal on the target (map + RC record) from an
	// earlier pass.
	tvid := db.store.tvids.AllocOverlay(extCandidateTag, "x @leftover:")
	if err := db.store.bolt.Update(func(txn *bbolt.Tx) error {
		return db.store.WriteDerivedCandidate(txn, tvid, cTarget, 1)
	}); err != nil {
		t.Fatalf("WriteDerivedCandidate: %v", err)
	}
	db.extmap.mu.Lock()
	db.extmap.candidateSourcesByChunk[cTarget] = []uint64{tvid}
	db.extmap.mu.Unlock()

	// Recall WITHOUT --propose.
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
// prefix without touching the others.
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

// TestRecall_Propose_MinSimilarityFloor verifies the
// chunk-EC ↔ tag-ED cosine floor (`[recall].min_propose_similarity`)
// drops sub-threshold candidates before the top-K cut, never writes
// them as RC records, and surfaces scores via ProposedTagScores
// aligned to ProposedTags.
// Refs: R2742, R2743
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

	// First pass authors above-floor candidates; reindex derives; second
	// pass surfaces them via the stencil (derivation is async).
	if _, err := l.Recall(
		[]ConnectionsInput{{ChunkID: cInput}},
		RecallOpts{K: 5, Propose: true, KeepTagless: true},
	); err != nil {
		t.Fatalf("first Recall: %v", err)
	}
	reindexMirrors(t, db)
	res, err := l.Recall(
		[]ConnectionsInput{{ChunkID: cInput}},
		RecallOpts{K: 5, Propose: true, KeepTagless: true},
	)
	if err != nil {
		t.Fatalf("second Recall: %v", err)
	}
	var target *RecalledChunk
	for i := range res.Chunks {
		if res.Chunks[i].ChunkID == cTarget {
			target = &res.Chunks[i]
		}
	}
	if target == nil {
		t.Fatalf("target chunk %d not surfaced", cTarget)
	}
	if len(target.ProposedTags) != 1 || target.ProposedTags[0] != "food" {
		t.Fatalf("expected exactly [food]; got %v", target.ProposedTags)
	}
	if len(target.ProposedTagScores) != 1 {
		t.Fatalf("expected one score aligned to one tag; got %v", target.ProposedTagScores)
	}
	if target.ProposedTagScores[0] < 0.99 {
		t.Errorf("expected food cosine ≈ 1.0; got %v", target.ProposedTagScores[0])
	}

	// "noise" must not have produced an RC record (write-side floor).
	props, err := db.store.DerivedProposals(cTarget)
	if err != nil {
		t.Fatalf("DerivedProposals: %v", err)
	}
	for _, p := range props {
		if p.Tagname == "noise" {
			t.Errorf("noise should have been filtered by floor; found RC record: %+v", p)
		}
	}
}

// TestRecall_Propose_NoModelIsNoOp verifies --propose without
// EmbeddingAvailable is silent: no RC writes, recall result unaffected.
// Refs: R2676
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
	if _, err := l.Recall(
		[]ConnectionsInput{{ChunkID: cInput}},
		RecallOpts{K: 5, Propose: true, KeepTagless: true},
	); err != nil {
		t.Fatalf("Recall: %v", err)
	}

	props, _ := db.store.DerivedProposals(cTarget)
	if len(props) != 0 {
		t.Errorf("expected no RC writes without embedding; got %+v", props)
	}
	var rf uint64
	if err := db.store.bolt.View(func(txn *bbolt.Tx) error {
		s, _, err := db.store.ReadDerivedFreshness(txn, cTarget)
		rf = s
		return err
	}); err != nil {
		t.Fatalf("ReadDerivedFreshness: %v", err)
	}
	if rf != 0 {
		t.Errorf("expected no RF write without embedding; got %d", rf)
	}
}
