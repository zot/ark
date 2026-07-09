package ark

// CRC: crc-Store.md, crc-Librarian.md, crc-ExtMap.md, crc-DB.md | Test: test-DerivedTags.md
//
// Coverage for the tag-derived RC/RJ subsystem flip (#22 Pass B+C): the
// signed @count read-modify-write helpers, the ExtMap reverse-lookup
// accessors, the map-backed DerivedProposals read path, and the
// end-to-end file-backed derivation (author @ext-candidate → reindex →
// derive RC). Refs: R3058, R3065, R3066, R3067, R3068, R3070, R3074, R3075

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zot/microfts2"
	"go.etcd.io/bbolt"
)

// --- File-backed harness ---

// setupFileBackedRecall isolates arkHomeDir to a temp HOME (so mirror
// authoring lands in the sandbox, never live ~/.ark) and configures a
// source over the test index dir so resolveExtMirror resolves the target
// files the propose pass authors candidates for.
func setupFileBackedRecall(t *testing.T) (*Librarian, *DB) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home) // arkHomeDir() reads $HOME → mirror tree in the sandbox
	l, db := setupRecall(t)
	// Register "lines" (the default strategy .md mirrors resolve to) as the
	// line chunker, and configure two sources so both the target files (test
	// index dir) and the authored mirrors (~/.ark/external) resolve — what
	// resolveExtMirror and the propose pass's syncOnePath both need.
	_ = db.indexer.fts.AddStrategyFunc("lines", microfts2.LineChunkFunc)
	db.config.Sources = []Source{
		{Dir: db.dbPath},
		{Dir: filepath.Join(home, ".ark")},
	}
	return l, db
}

// externalMirrors concatenates every authored mirror file under the
// sandbox ~/.ark/external tree, for content assertions.
func externalMirrors(t *testing.T) string {
	t.Helper()
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, ".ark", "external")
	var sb strings.Builder
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, _ := os.ReadFile(p)
		sb.Write(b)
		sb.WriteByte('\n')
		return nil
	})
	return sb.String()
}

// reindexMirrors indexes every authored mirror file so its @ext-candidate
// / @ext-judgment tags derive their RC / RJ records and reverse-lookup
// maps, exactly as the live watcher would after authoring.
func reindexMirrors(t *testing.T, db *DB) {
	t.Helper()
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, ".ark", "external")
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		// The test indexer registers only the "line" chunker; each
		// @ext-candidate / @ext-judgment line is self-contained, so a
		// line chunk carries the whole tag for derivation. A new mirror
		// adds; a rewritten one (accept/reject transition) refreshes.
		if _, aerr := db.indexer.AddFile(p, "line"); aerr != nil {
			if strings.Contains(aerr.Error(), "already indexed") {
				if rerr := db.indexer.RefreshFile(p, "line"); rerr != nil {
					t.Fatalf("refresh mirror %s: %v", p, rerr)
				}
			} else {
				t.Fatalf("reindex mirror %s: %v", p, aerr)
			}
		}
		return nil
	})
}

// --- Pure @count RMW helpers ---

func TestExtractCountField(t *testing.T) {
	tags := []TagValue{{Tag: "topic", Value: "recall"}, {Tag: "count", Value: "-3"}}
	routed, count, has := extractCountField(tags)
	if !has || count != -3 {
		t.Errorf("count: got (%d, %v) want (-3, true)", count, has)
	}
	if len(routed) != 1 || routed[0].Tag != "topic" {
		t.Errorf("routed after count strip: %+v", routed)
	}

	// No @count → hasCount false, routed unchanged.
	if r, c, h := extractCountField([]TagValue{{Tag: "topic", Value: "recall"}}); h || c != 0 || len(r) != 1 {
		t.Errorf("no count: routed=%+v count=%d has=%v", r, c, h)
	}
	// Malformed @count → treated as absent (dropped, count 0).
	if r, c, h := extractCountField([]TagValue{{Tag: "count", Value: "abc"}}); h || c != 0 || len(r) != 0 {
		t.Errorf("malformed count: routed=%+v count=%d has=%v", r, c, h)
	}
}

func TestUpsertCountLine(t *testing.T) {
	cand := `@ext-candidate: %abc @topic:`

	// New candidate line appends at @count: 1.
	data := upsertCountLine(nil, cand, 1)
	if got, want := string(data), cand+" @count: 1\n"; got != want {
		t.Fatalf("append: got %q want %q", got, want)
	}
	// Exact-identity repeat bumps @count to 2.
	data = upsertCountLine(data, cand, 1)
	if got, want := string(data), cand+" @count: 2\n"; got != want {
		t.Fatalf("bump: got %q want %q", got, want)
	}

	// Judgment line: first reject creates @count: -1, second → -2.
	jid := judgmentIdentity("%abc", "topic")
	j := upsertCountLine(nil, jid, -1)
	if got, want := string(j), jid+" @count: -1\n"; got != want {
		t.Fatalf("judgment create: got %q want %q", got, want)
	}
	j = upsertCountLine(j, jid, -1)
	if got, want := string(j), jid+" @count: -2\n"; got != want {
		t.Fatalf("judgment decrement: got %q want %q", got, want)
	}

	// A count returning to 0 removes the line (absent ≡ neutral).
	one := []byte(jid + " @count: -1\n")
	if got := string(upsertCountLine(one, jid, 1)); got != "" {
		t.Fatalf("zero removes: got %q want empty", got)
	}

	// A bare identity line (no @count) counts as 0, so +1 materializes 1.
	bare := []byte(cand + "\n")
	if got, want := string(upsertCountLine(bare, cand, 1)), cand+" @count: 1\n"; got != want {
		t.Fatalf("bare implicit 0: got %q want %q", got, want)
	}
}

func TestJudgmentIdentity(t *testing.T) {
	if got, want := judgmentIdentity("%abc", "Topic"), "@ext-judgment: %abc @topic:"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

// --- ExtMap reverse-lookup accessors (R3065, R3066) ---

func TestExtMapDerivedAccessors(t *testing.T) {
	m := NewExtMap()
	// CandidateSourcesForChunk returns a copy, empty when absent.
	if got := m.CandidateSourcesForChunk(7); got != nil {
		t.Errorf("absent chunk: got %v want nil", got)
	}
	m.candidateSourcesByChunk[7] = []uint64{11, 12}
	got := m.CandidateSourcesForChunk(7)
	if len(got) != 2 || got[0] != 11 || got[1] != 12 {
		t.Errorf("sources: got %v", got)
	}
	got[0] = 99 // mutating the copy must not corrupt the map
	if m.candidateSourcesByChunk[7][0] != 11 {
		t.Error("CandidateSourcesForChunk must return a copy")
	}

	// RejectScore: 0 when neutral, negative when net-rejected.
	if s := m.RejectScore(7, "food"); s != 0 {
		t.Errorf("neutral: got %d want 0", s)
	}
	m.rejectByChunk[7] = map[string]int64{"food": -2}
	if s := m.RejectScore(7, "food"); s != -2 {
		t.Errorf("rejected: got %d want -2", s)
	}
}

// --- Map-backed DerivedProposals read path (R3067) ---

func TestStore_DerivedProposals_MapBacked(t *testing.T) {
	_, db := setupRecall(t)
	const chunkID = uint64(42)

	// Seed two candidate sources (resolvable tvids) + their RC tallies +
	// the reverse-lookup map, as a reindex would.
	seed := func(tag string, tally uint64) uint64 {
		tvid := db.store.tvids.AllocOverlay(extCandidateTag, "x @"+tag+": @count: "+itoa(int(tally)))
		if err := db.store.bolt.Update(func(txn *bbolt.Tx) error {
			return db.store.WriteDerivedCandidate(txn, tvid, chunkID, tally)
		}); err != nil {
			t.Fatalf("WriteDerivedCandidate(%s): %v", tag, err)
		}
		return tvid
	}
	tFood := seed("food", 3)
	tStyle := seed("style", 1)
	db.extmap.mu.Lock()
	db.extmap.candidateSourcesByChunk[chunkID] = []uint64{tFood, tStyle}
	db.extmap.mu.Unlock()

	props, err := db.store.DerivedProposals(chunkID)
	if err != nil {
		t.Fatalf("DerivedProposals: %v", err)
	}
	// Sorted by tally desc: food(3) before style(1).
	if len(props) != 2 || props[0].Tagname != "food" || props[0].Tally != 3 || props[1].Tagname != "style" {
		t.Fatalf("props: %+v", props)
	}

	// A net-rejected tagname is filtered (defense-in-depth).
	db.extmap.mu.Lock()
	db.extmap.rejectByChunk[chunkID] = map[string]int64{"style": -1}
	db.extmap.mu.Unlock()
	props, _ = db.store.DerivedProposals(chunkID)
	for _, p := range props {
		if p.Tagname == "style" {
			t.Errorf("net-rejected style should be filtered; got %+v", props)
		}
	}
}

// --- End-to-end: propose authors an @ext-candidate, reindex derives RC ---

func TestRecall_Propose_AuthorsAndDerives(t *testing.T) {
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
		t.Fatalf("Recall: %v", err)
	}

	// The propose pass authors an @ext-candidate mirror line (R3068).
	if m := externalMirrors(t); !strings.Contains(m, "@food: @count: 1") {
		t.Fatalf("expected @ext-candidate @food authored; mirrors:\n%s", m)
	}

	// Reindexing that mirror derives the RC record + reverse map (R3064).
	reindexMirrors(t, db)
	props, err := db.store.DerivedProposals(cTarget)
	if err != nil {
		t.Fatalf("DerivedProposals: %v", err)
	}
	found := false
	for _, p := range props {
		if p.Tagname == "food" {
			found = true
			if p.Tally != 1 {
				t.Errorf("food tally: got %d want 1", p.Tally)
			}
		}
	}
	if !found {
		t.Errorf("expected food proposal after reindex; got %+v", props)
	}
}

// TestStore_ReadDerivedJudgment_AbsentPresentMalformed covers the v3
// judgment codec on the live helper (the coverage the retired ReadJudgment
// held): absent → neutral, present → the signed score, and a value that
// isn't signed-varint + 8 bytes reads conservatively as rejected. (R3059)
func TestStore_ReadDerivedJudgment_AbsentPresentMalformed(t *testing.T) {
	_, db := setupRecall(t)
	const src, tgt = uint64(50), uint64(7)

	// (a) absent → (0, false)
	if err := db.store.bolt.View(func(txn *bbolt.Tx) error {
		score, present, err := db.store.ReadDerivedJudgment(txn, src, tgt)
		if err != nil {
			return err
		}
		if present || score != 0 {
			t.Errorf("absent: got (%d, %v) want (0, false)", score, present)
		}
		return nil
	}); err != nil {
		t.Fatalf("absent read: %v", err)
	}

	// (b) present → the signed score
	if err := db.store.bolt.Update(func(txn *bbolt.Tx) error {
		return db.store.WriteDerivedJudgment(txn, src, tgt, -3, 999)
	}); err != nil {
		t.Fatalf("WriteDerivedJudgment: %v", err)
	}
	if err := db.store.bolt.View(func(txn *bbolt.Tx) error {
		score, present, err := db.store.ReadDerivedJudgment(txn, src, tgt)
		if err != nil {
			return err
		}
		if !present || score != -3 {
			t.Errorf("present: got (%d, %v) want (-3, true)", score, present)
		}
		return nil
	}); err != nil {
		t.Fatalf("present read: %v", err)
	}

	// (c) malformed value → conservative rejected (negative, present)
	if err := db.store.bolt.Update(func(txn *bbolt.Tx) error {
		return bPut(txn, derivedRoutedKey(prefixDerivedRejection, 51, 8), []byte{0x01, 0x02, 0x03})
	}); err != nil {
		t.Fatalf("write malformed: %v", err)
	}
	if err := db.store.bolt.View(func(txn *bbolt.Tx) error {
		score, present, err := db.store.ReadDerivedJudgment(txn, 51, 8)
		if err != nil {
			return err
		}
		if !present || score >= 0 {
			t.Errorf("malformed: got (%d, %v) want (negative, true)", score, present)
		}
		return nil
	}); err != nil {
		t.Fatalf("malformed read: %v", err)
	}
}

// targetSpec resolves a chunk to its path:range locator (what the
// re-homed accept/reject methods author against).
func targetSpec(t *testing.T, db *DB, chunkID uint64) string {
	t.Helper()
	info, err := db.ChunkInfo(chunkID)
	if err != nil {
		t.Fatalf("ChunkInfo(%d): %v", chunkID, err)
	}
	return info.Path + ":" + info.Range
}

// TestStore_RejectDerived_FileBacked drives the re-homed reject: author a
// candidate (→ RC), then RejectDerived authors an @ext-judgment whose
// reindex derives a negative RJ (rejectByChunk) and drops the candidate.
// Refs: R3069, R3075
func TestStore_RejectDerived_FileBacked(t *testing.T) {
	_, db := setupFileBackedRecall(t)
	cTarget, _ := indexLine(t, db, "target.txt", "apple banana grape")
	target := targetSpec(t, db, cTarget)

	if err := db.CandidateExtTag(target, "food", "", ""); err != nil {
		t.Fatalf("CandidateExtTag: %v", err)
	}
	reindexMirrors(t, db)
	if props, _ := db.store.DerivedProposals(cTarget); len(props) != 1 || props[0].Tagname != "food" {
		t.Fatalf("pre-reject proposals: %+v", props)
	}

	if _, err := db.store.RejectDerived(db, cTarget, "food"); err != nil {
		t.Fatalf("RejectDerived: %v", err)
	}
	reindexMirrors(t, db)

	if score := db.extmap.RejectScore(cTarget, "food"); score >= 0 {
		t.Errorf("expected negative reject score after reindex; got %d", score)
	}
	// The candidate transitioned to a judgment — it is no longer proposed.
	for _, p := range func() []DerivedProposal { ps, _ := db.store.DerivedProposals(cTarget); return ps }() {
		if p.Tagname == "food" {
			t.Errorf("food should no longer be proposed after reject; got %+v", p)
		}
	}
}

// TestStore_AcceptDerived_FileBacked drives the re-homed accept: author a
// candidate (→ RC), then AcceptDerived rewrites it to @ext whose reindex
// lands the live X+V edge (the tag attaches) and drops the candidate.
// Refs: R3071
func TestStore_AcceptDerived_FileBacked(t *testing.T) {
	_, db := setupFileBackedRecall(t)
	cTarget, _ := indexLine(t, db, "target.txt", "apple banana grape")
	target := targetSpec(t, db, cTarget)

	if err := db.CandidateExtTag(target, "priority", "high", ""); err != nil {
		t.Fatalf("CandidateExtTag: %v", err)
	}
	reindexMirrors(t, db)

	if err := db.store.AcceptDerived(db, cTarget, "priority", "high"); err != nil {
		t.Fatalf("AcceptDerived: %v", err)
	}
	reindexMirrors(t, db)

	// The committed routing surfaces as a live tag on the target chunk.
	tags, err := db.store.AllTagsForChunk(cTarget)
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
		t.Errorf("expected priority:high attached via @ext after accept; got %+v", tags)
	}
}

// TestRecall_Propose_SameCallProposals verifies the synchronous
// materialization (R3076): a single --propose call authors AND reindexes,
// so the proposal is visible in the same call — both to DerivedProposals
// and in the surfaced chunk's ProposedTags — with no manual reindex.
func TestRecall_Propose_SameCallProposals(t *testing.T) {
	l, db := setupFileBackedRecall(t)
	cInput, _ := indexLine(t, db, "input.txt", "apple banana cherry")
	cTarget, _ := indexLine(t, db, "target.txt", "apple banana grape")
	db.store.WriteChunkEmbedding(cInput, vecFrom(1, 0, 0, 0))
	db.store.WriteChunkEmbedding(cTarget, vecFrom(1, 0, 0, 0))
	db.store.WriteTagDefEmbedding("food", 10, vecFrom(1, 0, 0, 0))

	res, err := l.Recall(
		[]ConnectionsInput{{ChunkID: cInput}},
		RecallOpts{K: 5, Propose: true, KeepTagless: true},
	)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}

	props, _ := db.store.DerivedProposals(cTarget)
	found := false
	for _, p := range props {
		if p.Tagname == "food" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected food proposal in the SAME call; got %+v", props)
	}

	var target *RecalledChunk
	for i := range res.Chunks {
		if res.Chunks[i].ChunkID == cTarget {
			target = &res.Chunks[i]
		}
	}
	if target == nil || len(target.ProposedTags) == 0 || target.ProposedTags[0] != "food" {
		t.Fatalf("expected food in ProposedTags same-call; got %+v", target)
	}
}

// TestRecall_Propose_SyncReindexCost measures the batched sync-reindex
// cost — one reindex per distinct touched mirror. Not a pass/fail gate;
// run with -v to read the number. (Line chunker + async embedding, so
// this is the FTS + tag + derive cost, a representative lower bound.)
func TestRecall_Propose_SyncReindexCost(t *testing.T) {
	_, db := setupFileBackedRecall(t)
	const M = 12
	var mirrors []string
	for i := 0; i < M; i++ {
		cT, _ := indexLine(t, db, fmt.Sprintf("t%d.txt", i), "apple banana grape")
		target := targetSpec(t, db, cT)
		if err := db.CandidateExtTag(target, "food", "", ""); err != nil {
			t.Fatalf("CandidateExtTag: %v", err)
		}
		mp, err := db.resolveExtMirror(target)
		if err != nil {
			t.Fatalf("resolveExtMirror: %v", err)
		}
		mirrors = append(mirrors, mp)
	}

	start := time.Now()
	if err := SyncVoid(db, func(_ *DB) error {
		for _, mp := range mirrors {
			db.syncOnePath(db.indexer, mp)
		}
		return nil
	}); err != nil {
		t.Fatalf("sync-reindex: %v", err)
	}
	elapsed := time.Since(start)
	t.Logf("batched sync-reindex of %d mirrors: %v total, %.3f ms/mirror",
		M, elapsed, float64(elapsed.Microseconds())/float64(M)/1000)
}

// itoa avoids importing strconv into this file for the one call site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
