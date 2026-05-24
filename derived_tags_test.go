package ark

// CRC: crc-Store.md, crc-Librarian.md | Test: test-DerivedTags.md

import (
	"encoding/binary"
	"testing"

	"github.com/bmatsuo/lmdb-go/lmdb"
)

// --- Store-level tests ---

// TestStore_WriteDerivedProposal_TallyIncrements verifies tally goes
// from 1 → 2 across two WriteDerivedProposal calls for the same key.
// Refs: R2664, R2674
func TestStore_WriteDerivedProposal_TallyIncrements(t *testing.T) {
	_, db := setupRecall(t)
	const chunkID = uint64(42)

	// Two writes for the same (chunkID, tag).
	for range 2 {
		if err := db.store.env.Update(func(txn *lmdb.Txn) error {
			return db.store.WriteDerivedProposal(txn, chunkID, "priority")
		}); err != nil {
			t.Fatalf("WriteDerivedProposal: %v", err)
		}
	}

	props, err := db.store.DerivedProposals(chunkID)
	if err != nil {
		t.Fatalf("DerivedProposals: %v", err)
	}
	if len(props) != 1 {
		t.Fatalf("expected 1 proposal, got %d", len(props))
	}
	if props[0].Tagname != "priority" {
		t.Errorf("tagname: got %q want %q", props[0].Tagname, "priority")
	}
	if props[0].Tally != 2 {
		t.Errorf("tally: got %d want 2", props[0].Tally)
	}
}

// TestStore_WriteAndReadDerivedFreshness round-trips a serial value.
// Refs: R2666, R2669
func TestStore_WriteAndReadDerivedFreshness(t *testing.T) {
	_, db := setupRecall(t)
	const chunkID = uint64(42)
	const serial = uint64(12345)

	if err := db.store.env.Update(func(txn *lmdb.Txn) error {
		return db.store.WriteDerivedFreshness(txn, chunkID, serial)
	}); err != nil {
		t.Fatalf("WriteDerivedFreshness: %v", err)
	}

	var got uint64
	var found bool
	if err := db.store.env.View(func(txn *lmdb.Txn) error {
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
	if err := db.store.env.View(func(txn *lmdb.Txn) error {
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

// TestStore_HasDerivedRejection_PresentAndAbsent verifies RJ probe.
// Refs: R2665, R2673
func TestStore_HasDerivedRejection_PresentAndAbsent(t *testing.T) {
	_, db := setupRecall(t)
	if err := db.store.RejectDerived(42, "bogus"); err != nil {
		t.Fatalf("RejectDerived: %v", err)
	}

	var present, absent bool
	if err := db.store.env.View(func(txn *lmdb.Txn) error {
		p, err := db.store.HasDerivedRejection(txn, 42, "bogus")
		if err != nil {
			return err
		}
		a, err := db.store.HasDerivedRejection(txn, 42, "missing")
		if err != nil {
			return err
		}
		present, absent = p, a
		return nil
	}); err != nil {
		t.Fatalf("HasDerivedRejection: %v", err)
	}
	if !present {
		t.Error("expected present=true for bogus")
	}
	if absent {
		t.Error("expected absent=false for missing")
	}
}

// TestStore_DerivedProposals_SortByTallyDesc verifies the result order.
// Refs: R2678
func TestStore_DerivedProposals_SortByTallyDesc(t *testing.T) {
	_, db := setupRecall(t)
	const chunkID = uint64(42)

	if err := db.store.env.Update(func(txn *lmdb.Txn) error {
		// priority gets tally=3
		for range 3 {
			if err := db.store.WriteDerivedProposal(txn, chunkID, "priority"); err != nil {
				return err
			}
		}
		// status gets tally=1
		if err := db.store.WriteDerivedProposal(txn, chunkID, "status"); err != nil {
			return err
		}
		// axis gets tally=5
		for range 5 {
			if err := db.store.WriteDerivedProposal(txn, chunkID, "axis"); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("WriteDerivedProposal setup: %v", err)
	}

	props, err := db.store.DerivedProposals(chunkID)
	if err != nil {
		t.Fatalf("DerivedProposals: %v", err)
	}
	want := []DerivedProposal{
		{ChunkID: chunkID, Tagname: "axis", Tally: 5},
		{ChunkID: chunkID, Tagname: "priority", Tally: 3},
		{ChunkID: chunkID, Tagname: "status", Tally: 1},
	}
	if len(props) != len(want) {
		t.Fatalf("expected %d props, got %d", len(want), len(props))
	}
	for i := range want {
		if props[i] != want[i] {
			t.Errorf("position %d: got %+v want %+v", i, props[i], want[i])
		}
	}
}

// TestStore_DerivedProposals_FiltersRJ verifies that an RC entry
// shadowed by an RJ record is excluded from DerivedProposals.
// Refs: R2678, R2673
func TestStore_DerivedProposals_FiltersRJ(t *testing.T) {
	_, db := setupRecall(t)
	const chunkID = uint64(42)

	// Write two RC entries.
	if err := db.store.env.Update(func(txn *lmdb.Txn) error {
		if err := db.store.WriteDerivedProposal(txn, chunkID, "priority"); err != nil {
			return err
		}
		return db.store.WriteDerivedProposal(txn, chunkID, "status")
	}); err != nil {
		t.Fatalf("WriteDerivedProposal: %v", err)
	}

	// Reject one of them (this also drops its RC, but we want to test
	// the read-side shadow filter — so re-add the RC for status to
	// simulate a pre-rejection-leftover scenario).
	if err := db.store.RejectDerived(chunkID, "status"); err != nil {
		t.Fatalf("RejectDerived: %v", err)
	}
	if err := db.store.env.Update(func(txn *lmdb.Txn) error {
		return db.store.WriteDerivedProposal(txn, chunkID, "status")
	}); err != nil {
		t.Fatalf("re-write RC after reject: %v", err)
	}

	props, err := db.store.DerivedProposals(chunkID)
	if err != nil {
		t.Fatalf("DerivedProposals: %v", err)
	}
	if len(props) != 1 {
		t.Fatalf("expected 1 prop (status filtered by RJ shadow); got %d: %+v", len(props), props)
	}
	if props[0].Tagname != "priority" {
		t.Errorf("survivor: got %q want priority", props[0].Tagname)
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

// TestStore_AcceptDerived_DropsRCAndAttaches verifies atomic
// RC delete + F/V attach via the existing tag-attach path.
// Refs: R2679
func TestStore_AcceptDerived_DropsRCAndAttaches(t *testing.T) {
	_, db := setupRecall(t)
	chunkID, _ := indexLine(t, db, "1.txt", "apple banana")

	// Seed an RC record.
	if err := db.store.env.Update(func(txn *lmdb.Txn) error {
		return db.store.WriteDerivedProposal(txn, chunkID, "priority")
	}); err != nil {
		t.Fatalf("WriteDerivedProposal: %v", err)
	}

	tvid, err := db.store.AcceptDerived(chunkID, "priority", "high")
	if err != nil {
		t.Fatalf("AcceptDerived: %v", err)
	}
	if tvid == 0 {
		t.Error("expected non-zero resolved tvid")
	}

	// RC should be gone.
	props, _ := db.store.DerivedProposals(chunkID)
	if len(props) != 0 {
		t.Errorf("expected RC dropped after accept; got %+v", props)
	}

	// Tag should be attached — TagsForChunk picks up the F/V write.
	tags, err := db.store.AllTagsForChunk(chunkID)
	if err != nil {
		t.Fatalf("AllTagsForChunk: %v", err)
	}
	found := false
	for _, tv := range tags {
		if tv.Tag == "priority" && tv.Value == "high" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected priority:high attached to chunk; got %+v", tags)
	}
}

// TestStore_RejectDerived_DropsRCAndWritesRJ verifies atomic RC
// delete + RJ write with NOW timestamp.
// Refs: R2665, R2680
func TestStore_RejectDerived_DropsRCAndWritesRJ(t *testing.T) {
	_, db := setupRecall(t)
	const chunkID = uint64(42)

	if err := db.store.env.Update(func(txn *lmdb.Txn) error {
		return db.store.WriteDerivedProposal(txn, chunkID, "fluff")
	}); err != nil {
		t.Fatalf("WriteDerivedProposal: %v", err)
	}

	if err := db.store.RejectDerived(chunkID, "fluff"); err != nil {
		t.Fatalf("RejectDerived: %v", err)
	}

	// RC dropped.
	props, _ := db.store.DerivedProposals(chunkID)
	if len(props) != 0 {
		t.Errorf("expected RC dropped after reject; got %+v", props)
	}

	// RJ present.
	rjKey := derivedKey(prefixDerivedRejection, chunkID, "fluff")
	if err := db.store.env.View(func(txn *lmdb.Txn) error {
		v, err := txn.Get(db.store.dbi, rjKey)
		if err != nil {
			return err
		}
		if len(v) != 8 {
			t.Errorf("RJ value: got %d bytes want 8", len(v))
		}
		ts := int64(binary.BigEndian.Uint64(v))
		if ts == 0 {
			t.Error("RJ timestamp is zero")
		}
		return nil
	}); err != nil {
		t.Fatalf("read RJ: %v", err)
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
	l, db := setupRecall(t)
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
	if err := db.store.env.View(func(txn *lmdb.Txn) error {
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

// TestRecall_Propose_FreshnessSkipsRedundantWork verifies a second
// --propose call with no ED change leaves the tally unchanged
// (freshness skip).
// Refs: R2669
func TestRecall_Propose_FreshnessSkipsRedundantWork(t *testing.T) {
	l, db := setupRecall(t)
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
	l, db := setupRecall(t)
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
	l, db := setupRecall(t)
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

	props, _ := db.store.DerivedProposals(cTarget)
	for _, p := range props {
		if p.Tagname == "food" {
			t.Errorf("@food should be filtered (already attached); got %+v", p)
		}
	}
}

// TestRecall_Propose_SkipsRJRejected verifies the RJ filter — a
// rejected (chunkID, tagname) is not re-proposed.
// Refs: R2673
func TestRecall_Propose_SkipsRJRejected(t *testing.T) {
	l, db := setupRecall(t)
	cInput, _ := indexLine(t, db, "input.txt", "apple banana cherry")
	cTarget, _ := indexLine(t, db, "target.txt", "apple banana grape")
	db.store.WriteChunkEmbedding(cInput, vecFrom(1, 0, 0, 0))
	db.store.WriteChunkEmbedding(cTarget, vecFrom(1, 0, 0, 0))
	db.store.WriteTagDefEmbedding("food", 10, vecFrom(1, 0, 0, 0))

	// Pre-reject @food on the target chunk.
	if err := db.store.RejectDerived(cTarget, "food"); err != nil {
		t.Fatalf("RejectDerived: %v", err)
	}

	if _, err := l.Recall(
		[]ConnectionsInput{{ChunkID: cInput}},
		RecallOpts{K: 5, Propose: true, KeepTagless: true},
	); err != nil {
		t.Fatalf("Recall: %v", err)
	}

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
	l, db := setupRecall(t)
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

	// Pre-existing RC record on the target from an earlier (simulated) pass.
	if err := db.store.env.Update(func(txn *lmdb.Txn) error {
		return db.store.WriteDerivedProposal(txn, cTarget, "leftover")
	}); err != nil {
		t.Fatalf("WriteDerivedProposal: %v", err)
	}

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
	if err := db.store.env.View(func(txn *lmdb.Txn) error {
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
