package ark

// CRC: crc-Store.md, crc-Librarian.md, crc-ExtMap.md, crc-DB.md | Test: test-DerivedTags.md
//
// Coverage for the tag-derived RC/RJ subsystem flip (#22 Pass B+C): the
// signed @count read-modify-write helpers, the ExtMap reverse-lookup
// accessors, the map-backed DerivedProposals read path, and the
// end-to-end file-backed derivation (author @ext-candidate → reindex →
// derive RC). Refs: R3058, R3065, R3066, R3067, R3068, R3070, R3074, R3075

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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
	// resolveExtMirror and the propose pass's per-file scan both need.
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
	cand := `@ext-candidate: external %abc @topic:`
	const d1, d2 = "2026-07-12", "2026-07-13"

	// New candidate line appends at @count: 1, stamped with the first-seen
	// date immediately after the marker.
	data := upsertCountLine(nil, cand, d1, 1)
	if got, want := string(data), `@ext-candidate: 2026-07-12 external %abc @topic: @count: 1`+"\n"; got != want {
		t.Fatalf("append: got %q want %q", got, want)
	}
	// Exact-identity repeat bumps @count to 2 and FREEZES the first-seen date:
	// a later date passed in is ignored on a bump (first-seen wins).
	data = upsertCountLine(data, cand, d2, 1)
	if got, want := string(data), `@ext-candidate: 2026-07-12 external %abc @topic: @count: 2`+"\n"; got != want {
		t.Fatalf("bump freezes date: got %q want %q", got, want)
	}

	// Judgment line: first reject creates @count: -1 (dated), second → -2,
	// preserving the original date.
	jid := judgmentIdentity("%abc", "topic")
	j := upsertCountLine(nil, jid, d1, -1)
	if got, want := string(j), `@ext-judgment: 2026-07-12 %abc @topic: @count: -1`+"\n"; got != want {
		t.Fatalf("judgment create: got %q want %q", got, want)
	}
	j = upsertCountLine(j, jid, d2, -1)
	if got, want := string(j), `@ext-judgment: 2026-07-12 %abc @topic: @count: -2`+"\n"; got != want {
		t.Fatalf("judgment decrement: got %q want %q", got, want)
	}

	// A count returning to 0 removes the line (absent ≡ neutral).
	one := []byte(`@ext-judgment: 2026-07-12 %abc @topic: @count: -1` + "\n")
	if got := string(upsertCountLine(one, jid, d1, 1)); got != "" {
		t.Fatalf("zero removes: got %q want empty", got)
	}

	// A bare (dateless, countless) legacy line counts as 0; +1 materializes 1
	// and stays dateless — no own date to reinsert (backward compat).
	bare := []byte(cand + "\n")
	if got, want := string(upsertCountLine(bare, cand, d1, 1)), cand+" @count: 1\n"; got != want {
		t.Fatalf("bare implicit 0 stays dateless: got %q want %q", got, want)
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

	if err := db.CandidateExtTag(target, "food", "", "", extDispositionExternal, false); err != nil {
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

	if err := db.CandidateExtTag(target, "priority", "high", "", extDispositionExternal, false); err != nil {
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

// TestCandidateExtTag_DispositionDistinctLines drives the file-authoring
// path: internal and external are distinct proposals (disposition is part
// of the line identity) with independent @count tallies, and each line
// carries a leading first-seen date after the marker. (R3090, R3092)
func TestCandidateExtTag_DispositionDistinctLines(t *testing.T) {
	_, db := setupFileBackedRecall(t)
	cTarget, _ := indexLine(t, db, "target.txt", "apple banana grape")
	target := targetSpec(t, db, cTarget)

	// external once, internal twice → two distinct lines, independent tallies.
	for _, d := range []string{extDispositionExternal, extDispositionInternal, extDispositionInternal} {
		if err := db.CandidateExtTag(target, "food", "fruit", "", d, false); err != nil {
			t.Fatalf("CandidateExtTag(%s): %v", d, err)
		}
	}

	mirror, err := db.resolveExtMirror(target)
	if err != nil {
		t.Fatalf("resolveExtMirror: %v", err)
	}
	data, err := os.ReadFile(mirror)
	if err != nil {
		t.Fatalf("read mirror: %v", err)
	}

	var candLines []string
	for _, ln := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(ln, "@ext-candidate:") {
			candLines = append(candLines, ln)
		}
	}
	if len(candLines) != 2 {
		t.Fatalf("want 2 distinct candidate lines, got %d: %q", len(candLines), candLines)
	}

	var sawExt1, sawInt2 bool
	for _, ln := range candLines {
		body := strings.TrimPrefix(ln, "@ext-candidate: ")
		if len(body) < 11 || !isExtDate(body[:10]) || body[10] != ' ' {
			t.Errorf("candidate line missing leading first-seen date: %q", ln)
		}
		if strings.Contains(ln, " external ") && strings.HasSuffix(ln, "@count: 1") {
			sawExt1 = true
		}
		if strings.Contains(ln, " internal ") && strings.HasSuffix(ln, "@count: 2") {
			sawInt2 = true
		}
	}
	if !sawExt1 {
		t.Errorf("external proposal should carry @count: 1; lines=%q", candLines)
	}
	if !sawInt2 {
		t.Errorf("internal proposal (authored twice) should carry @count: 2; lines=%q", candLines)
	}
}

// peelExtCandidateDisposition surfaces the disposition; collectAcceptedCandidates
// returns one entry per matching candidate line, tagged with its disposition —
// so an internal and an external proposal of the same (tag,value) both surface.
// (R3100)
func TestPeelAndCollectDisposition(t *testing.T) {
	if d := peelExtCandidateDisposition("2026-07-12 internal notes/f.md @topic: recall"); d != "internal" {
		t.Errorf("internal peel: %q", d)
	}
	if d := peelExtCandidateDisposition("2026-07-12 external notes/f.md @topic: recall"); d != "external" {
		t.Errorf("external peel: %q", d)
	}
	if d := peelExtCandidateDisposition("notes/f.md @topic: recall"); d != "" {
		t.Errorf("no leading date → no disposition: %q", d)
	}
	data := []byte(
		"@ext-candidate: 2026-07-12 internal notes/f.md @topic: recall @count: 1\n" +
			"@ext-candidate: 2026-07-12 external notes/f.md @topic: recall @count: 1\n")
	got := collectAcceptedCandidates(data, "notes/f.md", "topic", "recall")
	if len(got) != 2 {
		t.Fatalf("want 2 accepted candidates, got %d: %+v", len(got), got)
	}
	seen := map[string]bool{got[0].disposition: true, got[1].disposition: true}
	if !seen["internal"] || !seen["external"] {
		t.Errorf("expected both dispositions surfaced; got %+v", got)
	}
}

// TestAcceptExtTag_InternalWritesSourceBody drives an internal accept end to
// end: author an internal candidate on a markdown file, accept it, and confirm
// the inline `@tag` is written into the file's own body (not routed to the
// mirror), the candidate is consumed, and a positive `@ext-judgment` lands.
// (R3100, R3101, R3103)
func TestAcceptExtTag_InternalWritesSourceBody(t *testing.T) {
	_, db := setupFileBackedRecall(t)
	// Register the wrapped markdown chunker so internal insertion is available.
	db.chunkerByName = make(map[string]any)
	if err := db.addChunker("markdown", wrapInternalTagChunker(microfts2.MarkdownChunker{}, stencilMarkdown)); err != nil {
		t.Fatalf("register markdown: %v", err)
	}
	src := filepath.Join(db.dbPath, "note.md")
	if err := os.WriteFile(src, []byte("## A\nbody a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.indexer.AddFile(src, "markdown"); err != nil {
		t.Fatalf("index source: %v", err)
	}
	if err := db.CandidateExtTag(src, "topic", "recall", "", extDispositionInternal, false); err != nil {
		t.Fatalf("candidate: %v", err)
	}
	applied, err := db.AcceptExtTag(src, "topic", "recall")
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if applied != extDispositionInternal { // R3248: internal write landed → reported
		t.Errorf("applied = %q, want internal", applied)
	}
	body, _ := os.ReadFile(src)
	if !strings.Contains(string(body), "@topic: recall") {
		t.Errorf("source file body missing the inline tag:\n%s", body)
	}
	mirror := externalMirrors(t)
	if strings.Contains(mirror, "@ext: ") {
		t.Errorf("internal accept must not route an @ext edge; mirror:\n%s", mirror)
	}
	if !strings.Contains(mirror, "@ext-judgment:") || !strings.Contains(mirror, "@count: 1") {
		t.Errorf("expected a positive @ext-judgment @count: 1; mirror:\n%s", mirror)
	}
	if strings.Contains(mirror, "@ext-candidate:") {
		t.Errorf("candidate should be consumed; mirror:\n%s", mirror)
	}
}

// TestAcceptExtTag_InternalFallsBackToExternal: an internal disposition against
// a target whose chunker can't host an inline tag (the line chunker) degrades
// to the external mirror route, and still records the positive judgment. (R3102, R3103)
func TestAcceptExtTag_InternalFallsBackToExternal(t *testing.T) {
	_, db := setupFileBackedRecall(t)
	cTarget, _ := indexLine(t, db, "target.txt", "apple banana grape")
	target := targetSpec(t, db, cTarget)
	if err := db.CandidateExtTag(target, "food", "fruit", "", extDispositionInternal, false); err != nil {
		t.Fatalf("candidate: %v", err)
	}
	applied, err := db.AcceptExtTag(target, "food", "fruit")
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if applied != extDispositionExternal { // R3248: degrade is reported honestly
		t.Errorf("applied = %q, want external (degraded)", applied)
	}
	mirror := externalMirrors(t)
	if !strings.Contains(mirror, "@ext: ") || !strings.Contains(mirror, "@food: fruit") {
		t.Errorf("internal on a line-chunked target should fall back to external @ext; mirror:\n%s", mirror)
	}
	if !strings.Contains(mirror, "@ext-judgment:") {
		t.Errorf("expected a positive @ext-judgment; mirror:\n%s", mirror)
	}
}

// collectAcceptedCandidates surfaces the replace flag per candidate, so accept
// can pick the add vs replace cell (a replace and an add proposal of the same
// routing are independent). (R3104, R3106)
func TestCollectAcceptedCandidates_Replace(t *testing.T) {
	data := []byte(
		"@ext-candidate: 2026-07-12 internal replace notes/f.md @cuisine: italian @count: 1\n" +
			"@ext-candidate: 2026-07-12 external notes/f.md @cuisine: french @count: 1\n")
	got := collectAcceptedCandidates(data, "notes/f.md", "cuisine", "")
	if len(got) != 2 {
		t.Fatalf("want 2 candidates, got %d: %+v", len(got), got)
	}
	byVal := map[string]acceptedCandidate{got[0].value: got[0], got[1].value: got[1]}
	if !byVal["italian"].replace || byVal["italian"].disposition != extDispositionInternal {
		t.Errorf("italian should be internal+replace: %+v", byVal["italian"])
	}
	if byVal["french"].replace {
		t.Errorf("french is an add-proposal, replace must be false: %+v", byVal["french"])
	}
}

// TestAcceptExtTag_InternalReplaceRewritesInline drives the internal-replace
// accept cell end to end: a replace candidate rewrites an existing inline tag
// in the source body in place, rather than adding a second line. (R3106, R3107)
func TestAcceptExtTag_InternalReplaceRewritesInline(t *testing.T) {
	_, db := setupFileBackedRecall(t)
	db.chunkerByName = make(map[string]any)
	if err := db.addChunker("markdown", wrapInternalTagChunker(microfts2.MarkdownChunker{}, stencilMarkdown)); err != nil {
		t.Fatalf("register markdown: %v", err)
	}
	src := filepath.Join(db.dbPath, "note.md")
	if err := os.WriteFile(src, []byte("@cuisine: french\n\n## A\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.indexer.AddFile(src, "markdown"); err != nil {
		t.Fatalf("index source: %v", err)
	}
	// internal + replace: collapse the existing @cuisine value in place.
	if err := db.CandidateExtTag(src, "cuisine", "italian", "", extDispositionInternal, true); err != nil {
		t.Fatalf("candidate: %v", err)
	}
	applied, err := db.AcceptExtTag(src, "cuisine", "italian")
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if applied != extDispositionInternal { // R3248
		t.Errorf("applied = %q, want internal", applied)
	}
	body, _ := os.ReadFile(src)
	if strings.Contains(string(body), "@cuisine: french") {
		t.Errorf("replace should have collapsed the old value; body:\n%s", body)
	}
	if !strings.Contains(string(body), "@cuisine: italian") {
		t.Errorf("replace should have written the new value; body:\n%s", body)
	}
	if n := strings.Count(string(body), "@cuisine:"); n != 1 {
		t.Errorf("replace must not add a second @cuisine line (got %d); body:\n%s", n, body)
	}
	if mirror := externalMirrors(t); strings.Contains(mirror, "@ext: ") {
		t.Errorf("internal replace must not route an @ext edge; mirror:\n%s", mirror)
	}
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

// TestRemoveInternalTag_EndToEnd drives the internal-remove cell against the
// same fixture the internal accept uses: insert an inline tag via accept,
// remove it with RemoveInternalTag, and confirm the body is restored; a
// second remove errors (no matching line — never a silent no-op, never a
// mirror fallback). (R3244, R3245)
func TestRemoveInternalTag_EndToEnd(t *testing.T) {
	_, db := setupFileBackedRecall(t)
	db.chunkerByName = make(map[string]any)
	if err := db.addChunker("markdown", wrapInternalTagChunker(microfts2.MarkdownChunker{}, stencilMarkdown)); err != nil {
		t.Fatalf("register markdown: %v", err)
	}
	src := filepath.Join(db.dbPath, "note.md")
	orig := "## A\nbody a\n"
	if err := os.WriteFile(src, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.indexer.AddFile(src, "markdown"); err != nil {
		t.Fatalf("index source: %v", err)
	}
	if err := db.CandidateExtTag(src, "topic", "recall", "", extDispositionInternal, false); err != nil {
		t.Fatalf("candidate: %v", err)
	}
	if _, err := db.AcceptExtTag(src, "topic", "recall"); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if err := db.RemoveInternalTag(src, "topic"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	body, _ := os.ReadFile(src)
	if string(body) != orig {
		t.Errorf("remove should restore the original body:\n got %q\nwant %q", body, orig)
	}
	if err := db.RemoveInternalTag(src, "topic"); err == nil {
		t.Error("second remove should error — no matching inline line (R3245)")
	}
}

// --- The exposed ledger: PendingCandidates, ranking, sentinel, amend (#67) ---

// pendingRow returns the single expected ledger row, failing when the
// ledger does not hold exactly one.
func pendingRow(t *testing.T, db *DB) CandidateRow {
	t.Helper()
	rows, err := db.store.PendingCandidates(nil, 0)
	if err != nil {
		t.Fatalf("PendingCandidates: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want exactly 1 pending row, got %d: %+v", len(rows), rows)
	}
	return rows[0]
}

// TestRankCandidateRows drives the ranking contract in isolation — a slice
// in, a slice out, no DB. The load-bearing part is that truncation happens
// AFTER ranking, so a --limit can never cut off an unattached row (the one
// most likely to need a human). (R3253)
func TestRankCandidateRows(t *testing.T) {
	rows := []CandidateRow{
		{SourceTvid: 4, Tagname: "zeta", Tally: 9, Date: "2026-07-01", Chunks: []uint64{7}},
		{SourceTvid: 1, Tagname: "alpha", Tally: 1, Date: "2026-07-01"}, // unattached
		{SourceTvid: 3, Tagname: "beta", Tally: 9, Date: "2026-07-02", Chunks: []uint64{8}},
		{SourceTvid: 2, Tagname: "beta", Tally: 9, Date: "2026-07-02", Chunks: []uint64{9}},
	}
	// Unattached first despite the lowest tally; then tally desc, date desc,
	// tagname asc, tvid asc.
	got := rankCandidateRows(append([]CandidateRow(nil), rows...), 0)
	want := []uint64{1, 2, 3, 4}
	for i, w := range want {
		if got[i].SourceTvid != w {
			t.Fatalf("rank order: got %d at %d, want %d (full: %+v)", got[i].SourceTvid, i, w, got)
		}
	}
	// Total order: a different input permutation ranks identically.
	shuffled := []CandidateRow{rows[3], rows[0], rows[2], rows[1]}
	for i, r := range rankCandidateRows(shuffled, 0) {
		if r.SourceTvid != want[i] {
			t.Errorf("order is not total: %+v", rankCandidateRows(shuffled, 0))
			break
		}
	}
	// limit <= 0 is unlimited; a limit truncates the RANKED slice, so
	// limit=1 yields the unattached row rather than the top-tally one.
	for _, lim := range []int{0, -1, 99} {
		if n := len(rankCandidateRows(append([]CandidateRow(nil), rows...), lim)); n != 4 {
			t.Errorf("limit %d: got %d rows, want 4", lim, n)
		}
	}
	one := rankCandidateRows(append([]CandidateRow(nil), rows...), 1)
	if len(one) != 1 || one[0].SourceTvid != 1 {
		t.Errorf("limit 1 must keep the unattached row: %+v", one)
	}
}

// TestPendingCandidates_RowCarriesWholeLine confirms every field the review
// console judges survives the round trip. Nothing but the tally is stored,
// so each field is a recovery from the source tvid that can regress
// silently — including the insight and the @count the human is judging on.
// (R3252)
func TestPendingCandidates_RowCarriesWholeLine(t *testing.T) {
	_, db := setupFileBackedRecall(t)
	cTarget, _ := indexLine(t, db, "target.txt", "apple banana grape")
	target := targetSpec(t, db, cTarget)

	const insight = `both name a "fruit" list`
	if err := db.CandidateExtTag(target, "topic", "fruit", insight, extDispositionInternal, true); err != nil {
		t.Fatalf("CandidateExtTag: %v", err)
	}
	reindexMirrors(t, db)

	row := pendingRow(t, db)
	if row.Tagname != "topic" || row.Value != "fruit" {
		t.Errorf("routed tag/value: got @%s: %q", row.Tagname, row.Value)
	}
	if row.Insight != insight {
		t.Errorf("insight: got %q want %q", row.Insight, insight)
	}
	if row.Disposition != extDispositionInternal || !row.Replace {
		t.Errorf("disposition/replace: got %q/%v want internal/true", row.Disposition, row.Replace)
	}
	if row.Target != target {
		t.Errorf("target: got %q want %q", row.Target, target)
	}
	if row.Tally != 1 {
		t.Errorf("tally: got %d want 1", row.Tally)
	}
	if row.Date == "" {
		t.Error("date: empty — the first-seen stamp did not survive the recovery")
	}
	if len(row.Chunks) != 1 || row.Chunks[0] != cTarget {
		t.Errorf("chunks: got %v want [%d]", row.Chunks, cTarget)
	}
}

// TestPendingCandidates_CorpusWideVsPerChunk pins the default that makes
// the ledger worth having over a per-card view: agents author across
// sessions, so a proposal on a chunk the human never pinned must still be
// visible. (R3252)
func TestPendingCandidates_CorpusWideVsPerChunk(t *testing.T) {
	_, db := setupFileBackedRecall(t)
	cA, _ := indexLine(t, db, "a.txt", "apple banana grape")
	cB, _ := indexLine(t, db, "b.txt", "cherry damson elder")
	if err := db.CandidateExtTag(targetSpec(t, db, cA), "topic", "fruit", "", extDispositionExternal, false); err != nil {
		t.Fatalf("candidate A: %v", err)
	}
	if err := db.CandidateExtTag(targetSpec(t, db, cB), "topic", "berry", "", extDispositionExternal, false); err != nil {
		t.Fatalf("candidate B: %v", err)
	}
	reindexMirrors(t, db)

	all, err := db.store.PendingCandidates(nil, 0)
	if err != nil {
		t.Fatalf("corpus-wide: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("corpus-wide: got %d rows want 2: %+v", len(all), all)
	}
	for _, tc := range []struct {
		chunk uint64
		want  string
	}{{cA, "fruit"}, {cB, "berry"}} {
		rows, err := db.store.PendingCandidates([]uint64{tc.chunk}, 0)
		if err != nil {
			t.Fatalf("per-chunk %d: %v", tc.chunk, err)
		}
		if len(rows) != 1 || rows[0].Value != tc.want {
			t.Errorf("per-chunk %d: got %+v want one row valued %q", tc.chunk, rows, tc.want)
		}
	}
}

// TestPendingCandidates_UnattachedSentinel is the completeness invariant:
// a candidate whose TARGET resolves to nothing still holds an RC record
// (keyed at chunk 0) and so still rides the prefix scan. Before the
// sentinel it had no record at all — invisible to any review console,
// which is precisely the row a human most needs. (R3254)
func TestPendingCandidates_UnattachedSentinel(t *testing.T) {
	_, db := setupFileBackedRecall(t)
	cReal, _ := indexLine(t, db, "target.txt", "apple banana grape")

	if err := db.CandidateExtTag(filepath.Join(db.dbPath, "typo.txt"), "topic", "ghost", "", extDispositionExternal, false); err != nil {
		t.Fatalf("unresolvable candidate: %v", err)
	}
	if err := db.CandidateExtTag(targetSpec(t, db, cReal), "topic", "fruit", "", extDispositionExternal, false); err != nil {
		t.Fatalf("resolvable candidate: %v", err)
	}
	reindexMirrors(t, db)

	rows, err := db.store.PendingCandidates(nil, 0)
	if err != nil {
		t.Fatalf("PendingCandidates: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows (one unattached), got %d: %+v", len(rows), rows)
	}
	if len(rows[0].Chunks) != 0 || rows[0].Value != "ghost" {
		t.Errorf("the unattached row must sort first: %+v", rows)
	}
	ghost := rows[0].SourceTvid

	// RC[ghost][0] exists and is the ONLY record for that tvid.
	if err := db.store.bolt.View(func(txn *bbolt.Tx) error {
		if tally, ok, err := db.store.ReadDerivedCandidate(txn, ghost, 0); err != nil || !ok || tally == 0 {
			t.Errorf("RC[%d][0]: tally=%d ok=%v err=%v — sentinel missing", ghost, tally, ok, err)
		}
		return db.store.ScanAllDerivedCandidates(txn, func(src, chunk, _ uint64) error {
			if src == ghost && chunk != 0 {
				t.Errorf("unattached tvid %d has an RC record at chunk %d", src, chunk)
			}
			return nil
		})
	}); err != nil {
		t.Fatalf("view: %v", err)
	}
	// The sentinel is a record, not an edge: it must not key the reverse
	// lookup, and it is not a proposal *on* the resolvable chunk.
	if src := db.extmap.CandidateSourcesForChunk(0); len(src) != 0 {
		t.Errorf("candidateSourcesByChunk must have no entry keyed 0; got %v", src)
	}
	props, _ := db.store.DerivedProposals(cReal)
	if len(props) != 1 {
		t.Errorf("DerivedProposals(%d) should hold only the attached proposal; got %+v", cReal, props)
	}
}

// TestSentinelClearsWhenTargetResolves is the invariant's other half. A
// stale RC[tvid][0] would leave a perfectly resolved proposal sorted into
// the alarm band at the top of the ledger forever. (R3254)
func TestSentinelClearsWhenTargetResolves(t *testing.T) {
	_, db := setupFileBackedRecall(t)
	// A file that exists in a source but is not indexed yet: the TARGET
	// resolves to nothing at authoring time.
	later := filepath.Join(db.dbPath, "later.txt")
	if err := os.WriteFile(later, []byte("apple banana grape\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := db.CandidateExtTag(later, "topic", "fruit", "", extDispositionExternal, false); err != nil {
		t.Fatalf("CandidateExtTag: %v", err)
	}
	reindexMirrors(t, db)
	if row := pendingRow(t, db); len(row.Chunks) != 0 {
		t.Fatalf("precondition: candidate should start unattached, got chunks %v", row.Chunks)
	}

	// Now the target lands, and the mirror's reindex re-resolves it.
	if _, err := db.indexer.AddFile(later, "line"); err != nil {
		t.Fatalf("index target: %v", err)
	}
	reindexMirrors(t, db)

	row := pendingRow(t, db)
	if len(row.Chunks) == 0 {
		t.Fatalf("candidate should have attached once the target indexed: %+v", row)
	}
	if err := db.store.bolt.View(func(txn *bbolt.Tx) error {
		if _, ok, err := db.store.ReadDerivedCandidate(txn, row.SourceTvid, 0); err != nil || ok {
			t.Errorf("RC[%d][0] should be gone once a real target resolved (ok=%v err=%v)", row.SourceTvid, ok, err)
		}
		return nil
	}); err != nil {
		t.Fatalf("view: %v", err)
	}
}

// TestAmendExtCandidate_RevisesInPlace is the amend contract: the proposal
// stays pending rather than orphaning, nil opts inherit the old line's
// metadata, and the revision starts a fresh tally — the old @count measured
// agreement on a proposal the human just changed. (R3255)
func TestAmendExtCandidate_RevisesInPlace(t *testing.T) {
	_, db := setupFileBackedRecall(t)
	cTarget, _ := indexLine(t, db, "target.txt", "apple banana grape")
	target := targetSpec(t, db, cTarget)

	const insight = "shares the fruit list"
	for i := 0; i < 2; i++ { // author twice → tally 2
		if err := db.CandidateExtTag(target, "topic", "fruit", insight, extDispositionInternal, false); err != nil {
			t.Fatalf("CandidateExtTag: %v", err)
		}
	}
	reindexMirrors(t, db)
	before := pendingRow(t, db)
	if before.Tally != 2 {
		t.Fatalf("precondition: want tally 2, got %d", before.Tally)
	}

	if err := db.AmendExtCandidate(before.SourceTvid, "topic", "produce", ExtAmendOpts{}); err != nil {
		t.Fatalf("AmendExtCandidate: %v", err)
	}
	reindexMirrors(t, db)

	if n := strings.Count(externalMirrors(t), "@ext-candidate:"); n != 1 {
		t.Errorf("want exactly 1 candidate line after amend, got %d:\n%s", n, externalMirrors(t))
	}
	after := pendingRow(t, db)
	if after.Value != "produce" {
		t.Errorf("value: got %q want %q", after.Value, "produce")
	}
	if after.Tally != 1 {
		t.Errorf("tally: got %d want 1 — a changed proposal must not inherit the old votes", after.Tally)
	}
	if after.Insight != insight || after.Disposition != extDispositionInternal {
		t.Errorf("nil opts must carry forward: insight=%q disposition=%q", after.Insight, after.Disposition)
	}
}

// TestAmendExtCandidate_MergesIntoCollision pins the behavior that falls
// out of reusing CandidateExtTag's author rather than writing amend's own:
// a revision landing on an existing identity merges into that line's tally
// instead of duplicating it. (R3255)
func TestAmendExtCandidate_MergesIntoCollision(t *testing.T) {
	_, db := setupFileBackedRecall(t)
	cTarget, _ := indexLine(t, db, "target.txt", "apple banana grape")
	target := targetSpec(t, db, cTarget)

	if err := db.CandidateExtTag(target, "topic", "alpha", "", extDispositionExternal, false); err != nil {
		t.Fatalf("candidate A: %v", err)
	}
	for i := 0; i < 2; i++ { // B starts at tally 2
		if err := db.CandidateExtTag(target, "topic", "beta", "", extDispositionExternal, false); err != nil {
			t.Fatalf("candidate B: %v", err)
		}
	}
	reindexMirrors(t, db)

	rows, err := db.store.PendingCandidates(nil, 0)
	if err != nil || len(rows) != 2 {
		t.Fatalf("precondition: want 2 rows, got %d (%v)", len(rows), err)
	}
	var aTvid uint64
	for _, r := range rows {
		if r.Value == "alpha" {
			aTvid = r.SourceTvid
		}
	}
	// Amend A into B's exact identity.
	if err := db.AmendExtCandidate(aTvid, "topic", "beta", ExtAmendOpts{}); err != nil {
		t.Fatalf("AmendExtCandidate: %v", err)
	}
	reindexMirrors(t, db)

	if n := strings.Count(externalMirrors(t), "@ext-candidate:"); n != 1 {
		t.Errorf("collision must merge, not duplicate: %d lines\n%s", n, externalMirrors(t))
	}
	if row := pendingRow(t, db); row.Value != "beta" || row.Tally != 3 {
		t.Errorf("merged row: got value=%q tally=%d want beta/3", row.Value, row.Tally)
	}
}

// TestAmendExtCandidate_RejectsBadTvid pins the one place amend departs
// from its accept/reject siblings: their no-match is a legitimate no-op,
// but amend was handed one specific line, so reporting success for a
// revision that never happened is the failure worth catching. (R3255)
func TestAmendExtCandidate_RejectsBadTvid(t *testing.T) {
	_, db := setupFileBackedRecall(t)
	cTarget, _ := indexLine(t, db, "target.txt", "apple banana grape")
	if err := db.CandidateExtTag(targetSpec(t, db, cTarget), "topic", "fruit", "", extDispositionExternal, false); err != nil {
		t.Fatalf("CandidateExtTag: %v", err)
	}
	reindexMirrors(t, db)
	before := externalMirrors(t)

	if err := db.AmendExtCandidate(999999, "topic", "x", ExtAmendOpts{}); err == nil {
		t.Error("an unallocated tvid must error, not silently no-op")
	}
	// The tvid of an ordinary, non-candidate tag is the subtler case.
	plain, _ := indexLine(t, db, "plain.txt", "@mood: cheerful")
	_ = plain
	reindexMirrors(t, db)
	if tvid, ok := db.store.tvids.Lookup("mood", "cheerful"); ok {
		if err := db.AmendExtCandidate(tvid, "topic", "x", ExtAmendOpts{}); err == nil {
			t.Errorf("tvid %d is not an @ext-candidate — amend must error", tvid)
		}
	}
	if after := externalMirrors(t); after != before {
		t.Errorf("a failed amend must not touch the mirror:\n got %q\nwant %q", after, before)
	}
}

// --- The exposed ledger: HTTP surface (#67) ---

// TestExtAmendRequestPointerDecode is the cheap, pure half of the amend
// endpoint's contract and the one most likely to regress into a plain
// string field: a JSON key the caller never sent must decode to nil
// (carry the old line's value forward), while an explicitly-sent empty
// string must decode to a non-nil pointer at "" (clear the field). A
// non-pointer struct cannot tell those apart, and the failure is silent —
// every amend would quietly wipe the insight it was told to keep. (R3257)
func TestExtAmendRequestPointerDecode(t *testing.T) {
	var absent extAmendRequest
	if err := json.Unmarshal([]byte(`{"tvid":7,"tag":"topic"}`), &absent); err != nil {
		t.Fatalf("decode absent: %v", err)
	}
	if absent.Tvid != 7 || absent.Tag != "topic" {
		t.Errorf("scalars: got tvid=%d tag=%q", absent.Tvid, absent.Tag)
	}
	for name, p := range map[string]any{
		"target": absent.Target, "insight": absent.Insight,
		"disposition": absent.Disposition,
	} {
		if p != (any)((*string)(nil)) {
			t.Errorf("%s: absent key must decode to nil, got %v", name, p)
		}
	}
	if absent.Replace != nil {
		t.Errorf("replace: absent key must decode to nil, got %v", *absent.Replace)
	}

	var explicit extAmendRequest
	if err := json.Unmarshal([]byte(`{"tvid":7,"tag":"topic","insight":"","replace":false}`), &explicit); err != nil {
		t.Fatalf("decode explicit: %v", err)
	}
	if explicit.Insight == nil || *explicit.Insight != "" {
		t.Errorf(`an explicit "" must clear, not inherit: %v`, explicit.Insight)
	}
	if explicit.Replace == nil || *explicit.Replace {
		t.Errorf("an explicit false must clear, not inherit: %v", explicit.Replace)
	}
}

// TestHandleExtPending drives GET /ext/pending over httptest: the JSON
// shape the CLI and any other client decode, the server-side TARGET
// resolution (the CLI cannot resolve one while the server holds the index
// lock), and the empty-ledger case, which must be `[]` and never JSON
// null. (R3259)
func TestHandleExtPending(t *testing.T) {
	_, db := setupFileBackedRecall(t)
	srv := &Server{db: db}

	get := func(query string) []CandidateRow {
		t.Helper()
		rec := httptest.NewRecorder()
		srv.handleExtPending(rec, httptest.NewRequest("GET", "/ext/pending"+query, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status %d — %s", query, rec.Code, rec.Body.String())
		}
		if strings.TrimSpace(rec.Body.String()) == "null" {
			t.Fatalf("GET %s: an empty ledger must serialize as [], not null", query)
		}
		var rows []CandidateRow
		if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
			t.Fatalf("GET %s: decode %q: %v", query, rec.Body.String(), err)
		}
		return rows
	}

	if rows := get(""); len(rows) != 0 {
		t.Fatalf("empty ledger: got %+v", rows)
	}

	cA, _ := indexLine(t, db, "a.txt", "apple banana grape")
	cB, _ := indexLine(t, db, "b.txt", "cherry damson elder")
	targetA := targetSpec(t, db, cA)
	if err := db.CandidateExtTag(targetA, "topic", "fruit", "why not", extDispositionExternal, false); err != nil {
		t.Fatalf("candidate A: %v", err)
	}
	if err := db.CandidateExtTag(targetSpec(t, db, cB), "topic", "berry", "", extDispositionExternal, false); err != nil {
		t.Fatalf("candidate B: %v", err)
	}
	reindexMirrors(t, db)

	if rows := get(""); len(rows) != 2 {
		t.Errorf("corpus-wide: got %d rows want 2: %+v", len(rows), rows)
	}
	// ?target= is resolved server-side — the point of the parameter.
	rows := get("?target=" + url.QueryEscape(targetA))
	if len(rows) != 1 || rows[0].Value != "fruit" || rows[0].Insight != "why not" {
		t.Errorf("target filter: got %+v", rows)
	}
	// ?chunk= takes a raw chunkid, the shape a caller already holding one uses.
	if rows := get("?chunk=" + strconv.FormatUint(cB, 10)); len(rows) != 1 || rows[0].Value != "berry" {
		t.Errorf("chunk filter: got %+v", rows)
	}
	if rows := get("?limit=1"); len(rows) != 1 {
		t.Errorf("limit: got %d rows want 1", len(rows))
	}
	// A malformed filter is a 400, not a silent corpus-wide scan.
	for _, bad := range []string{"?chunk=abc", "?limit=xyz"} {
		rec := httptest.NewRecorder()
		srv.handleExtPending(rec, httptest.NewRequest("GET", "/ext/pending"+bad, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s: status %d, want 400", bad, rec.Code)
		}
	}
}

// --- Authoring authority + the copier that dropped the gate (#67 fallout) ---

// TestWithFTSCarriesExtRoutingFields is the regression test for a bug that
// lived from 2026-04-30 to 2026-07-26 with a fully green suite. Indexer.withFTS
// rebuilds the struct field-by-field, and when extmap/db were added to Indexer a
// month after the copier was written, nobody updated it. Every caller of withFTS
// is inside an enqueueWrite closure, so the write actor got an Indexer whose
// extmap and db were nil, runExtRouting's guard returned immediately, and NO
// @ext or @ext-candidate ever routed on the watcher path.
//
// What hid it is the reason this test is written against the copier rather than
// against routing: every existing test wires idx.extmap and idx.db BY HAND
// (ext_test.go, recall_test.go), so they exercise an Indexer production never
// builds. A field-by-field copier is a silent-omission machine — adding a field
// to the struct does not add it here, and the compiler says nothing.
// CRC: crc-Indexer.md | R3264
func TestWithFTSCarriesExtRoutingFields(t *testing.T) {
	_, db := setupFileBackedRecall(t)
	if db.indexer.extmap == nil || db.indexer.db == nil {
		t.Fatal("precondition: the live indexer should have extmap and db wired")
	}
	cp := db.indexer.withFTS(db.indexer.fts)
	if cp.extmap == nil {
		t.Error("withFTS dropped extmap — runExtRouting's guard will return early and no @ext routes on the watcher path")
	}
	if cp.db == nil {
		t.Error("withFTS dropped db — same guard, same silent dead routing")
	}
}

// TestExtHonoredOnlyFromMirrorTree pins the authority rule: an @ext-family tag
// is an instruction, so honoring one from any indexed file means discussing a
// routing authors it. Found live when a transcript-quoted @ext-candidate — its
// path elided to "…" for readability — filed itself as a real proposal.
// (R3264)
func TestExtHonoredOnlyFromMirrorTree(t *testing.T) {
	_, db := setupFileBackedRecall(t)
	home, _ := os.UserHomeDir()

	// The global mirror tree is authoritative; a plain source file is not.
	if got := db.IsExtMirrorPath(filepath.Join(home, ".ark", "external", "slug", "note.md.md")); !got {
		t.Error("the global ~/.ark/external tree must be authoritative")
	}
	if got := db.IsExtMirrorPath(filepath.Join(db.dbPath, "note.md")); got {
		t.Error("an ordinary indexed file must NOT be able to author @ext")
	}
	// A chat transcript is the motivating case.
	if got := db.IsExtMirrorPath(filepath.Join(home, ".claude", "projects", "-x", "s.jsonl")); got {
		t.Error("a conversation transcript must never author @ext — quoting a routing would file it")
	}

	// Source-local mirror trees (R3171) are authoritative too.
	db.config.Sources = append(db.config.Sources, Source{Dir: filepath.Join(home, "bibles"), ExtMirror: "mirrors"})
	if got := db.IsExtMirrorPath(filepath.Join(home, "bibles", "mirrors", "john.text.xhtml.md")); !got {
		t.Error("a source-local ext_mirror tree must be authoritative (R3171)")
	}
	if got := db.IsExtMirrorPath(filepath.Join(home, "bibles", "john.text.xhtml")); got {
		t.Error("the source's own content is not its mirror tree")
	}
}

// TestExtFromNonMirrorFileDoesNotRoute is the end-to-end half: an @ext-candidate
// authored into an ordinary indexed file must index as text and derive nothing.
// (R3264)
func TestExtFromNonMirrorFileDoesNotRoute(t *testing.T) {
	_, db := setupFileBackedRecall(t)
	cTarget, _ := indexLine(t, db, "target.txt", "apple banana grape")
	target := targetSpec(t, db, cTarget)

	// Same line the CLI would author, but written into a plain source file
	// rather than the mirror tree — the shape a transcript or a note has.
	impostor := filepath.Join(db.dbPath, "notes.md")
	line := "@ext-candidate: 2026-07-26 external " + target + " @topic: smuggled\n"
	if err := os.WriteFile(impostor, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.indexer.AddFile(impostor, "line"); err != nil {
		t.Fatalf("index impostor: %v", err)
	}

	rows, err := db.store.PendingCandidates(nil, 0)
	if err != nil {
		t.Fatalf("PendingCandidates: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("a non-mirror file must not file proposals; ledger holds %+v", rows)
	}
	if props, _ := db.store.DerivedProposals(cTarget); len(props) != 0 {
		t.Errorf("no proposal should attach to the target; got %+v", props)
	}
	// It is inert, not invisible: the tag still indexes as ordinary text.
	if tvid, ok := db.store.tvids.Lookup(extCandidateTag, strings.TrimSpace(line[len("@ext-candidate:"):])); !ok {
		t.Errorf("the tag should still index as text (tvid lookup failed, tvid=%d)", tvid)
	}
}

// extMirrorSource returns a path inside a mirror tree, for tests that need to
// AUTHOR an @ext directive rather than test who may author one. It configures a
// source-local ext_mirror (R3171) over the test index dir, so authority (R3264)
// is satisfied without depending on $HOME — the same arrangement
// specs/bible-chunker.md recommends for a scripture checkout.
// CRC: crc-Indexer.md | R3171, R3264
func extMirrorSource(t *testing.T, db *DB, name string) string {
	t.Helper()
	const mirror = "mirrors"
	var configured bool
	for i := range db.config.Sources {
		if db.config.Sources[i].Dir == db.dbPath {
			db.config.Sources[i].ExtMirror = mirror
			configured = true
		}
	}
	if !configured {
		db.config.Sources = append(db.config.Sources, Source{Dir: db.dbPath, ExtMirror: mirror})
	}
	dir := filepath.Join(db.dbPath, mirror)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, name)
}
