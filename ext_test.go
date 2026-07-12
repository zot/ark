package ark

// CRC: crc-Indexer.md | Test: test-Tags.md

import (
	"testing"

	"github.com/zot/microfts2"
)

func TestParseExtTargetSingleTag(t *testing.T) {
	target, tags, ok := ParseExtTarget("~/notes/recipe.md @food: hamburger")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if target != "~/notes/recipe.md" {
		t.Errorf("target: got %q", target)
	}
	if len(tags) != 1 || tags[0].Tag != "food" || tags[0].Value != "hamburger" {
		t.Errorf("tags: %+v", tags)
	}
}

func TestParseExtTargetMultipleTags(t *testing.T) {
	target, tags, ok := ParseExtTarget("doc-uuid-42 @food: hamburger @origin: texas")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if target != "doc-uuid-42" {
		t.Errorf("target: got %q", target)
	}
	if len(tags) != 2 {
		t.Fatalf("want 2 tags, got %d (%+v)", len(tags), tags)
	}
	if tags[0].Tag != "food" || tags[0].Value != "hamburger" {
		t.Errorf("tag 0: %+v", tags[0])
	}
	if tags[1].Tag != "origin" || tags[1].Value != "texas" {
		t.Errorf("tag 1: %+v", tags[1])
	}
}

func TestParseExtTargetTrimsTarget(t *testing.T) {
	target, _, ok := ParseExtTarget("   spaced-target   @x: y")
	if !ok || target != "spaced-target" {
		t.Errorf("target: ok=%v got %q", ok, target)
	}
}

func TestParseExtTargetLowercasesTagNames(t *testing.T) {
	_, tags, _ := ParseExtTarget("t @Food: hamburger")
	if len(tags) != 1 || tags[0].Tag != "food" {
		t.Errorf("tag name lowering: %+v", tags)
	}
}

func TestParseExtTargetNoEmbeddedTags(t *testing.T) {
	_, _, ok := ParseExtTarget("just-a-target-no-tags")
	if ok {
		t.Errorf("expected ok=false when no tags follow")
	}
}

func TestParseExtTargetEmptyTarget(t *testing.T) {
	_, _, ok := ParseExtTarget("@food: hamburger")
	if ok {
		t.Errorf("expected ok=false when target is empty")
	}
}

// extTestDB indexes a markdown file with a known @id and returns a DB
// suitable for ResolveExtTarget tests.
func extTestDB(t *testing.T) (*DB, string, string) {
	t.Helper()
	idx, dir := testIndexer(t)
	if err := idx.fts.AddChunker("markdown", microfts2.MarkdownChunker{}); err != nil {
		t.Fatal(err)
	}
	const uuid = "ext-target-9911"
	fp := writeFile(t, dir, "target.md", "@id: "+uuid+"\n\nPreamble.\n\n## Heading\n\nbody.\n")
	if _, err := idx.AddFile(fp, "markdown"); err != nil {
		t.Fatal(err)
	}
	idx.store.LoadTvidMap()
	return newTestDB(idx, dir), fp, uuid
}

func TestResolveExtTargetUUID(t *testing.T) {
	db, _, uuid := extTestDB(t)
	chunks := db.ResolveExtTarget("%"+uuid, "")
	if len(chunks) == 0 {
		t.Fatalf("UUID target: no chunks resolved")
	}
}

func TestResolveExtTargetPath(t *testing.T) {
	db, fp, _ := extTestDB(t)
	chunks := db.ResolveExtTarget(fp, "")
	if len(chunks) != 1 {
		t.Fatalf("path target: want 1 chunk (first/preamble), got %d", len(chunks))
	}
}

func TestMutateExtLineSingleTagReplace(t *testing.T) {
	placed := false
	got, drop, matched := mutateExtLine(
		`@ext: /a/b.md:"foo" @topic: old`,
		`/a/b.md:"foo"`, "topic", "new", extOpSet, extClassCommitted, &placed, nil)
	if !matched || drop {
		t.Fatalf("want matched && !drop, got matched=%v drop=%v", matched, drop)
	}
	if want := `@ext: /a/b.md:"foo" @topic: new`; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestMutateExtLineSingleTagRemoveDropsLine(t *testing.T) {
	placed := false
	got, drop, matched := mutateExtLine(
		`@ext: /a/b.md:"foo" @topic: x`,
		`/a/b.md:"foo"`, "topic", "", extOpRemove, extClassCommitted, &placed, nil)
	if !matched || !drop {
		t.Fatalf("want matched && drop, got matched=%v drop=%v", matched, drop)
	}
	if got != "" {
		t.Errorf("expected empty newLine for dropped line, got %q", got)
	}
}

func TestMutateExtLineMultiTagReplaceOnly(t *testing.T) {
	placed := false
	got, drop, matched := mutateExtLine(
		`@ext: %abc @t1: v1 @target: oldv @t3: v3`,
		`%abc`, "target", "newv", extOpSet, extClassCommitted, &placed, nil)
	if !matched || drop {
		t.Fatalf("want matched && !drop")
	}
	if want := `@ext: %abc @t1: v1 @target: newv @t3: v3`; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestMutateExtLineMultiTagRemoveOnly(t *testing.T) {
	placed := false
	got, drop, matched := mutateExtLine(
		`@ext: %abc @t1: v1 @target: v2 @t3: v3`,
		`%abc`, "target", "", extOpRemove, extClassCommitted, &placed, nil)
	if !matched || drop {
		t.Fatalf("want matched && !drop")
	}
	if want := `@ext: %abc @t1: v1 @t3: v3`; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

// extOpRemove with a non-empty value spares spans whose value differs.
func TestMutateExtLineRemoveByValueSparesOthers(t *testing.T) {
	placed := false
	got, drop, matched := mutateExtLine(
		`@ext: %abc @topic: keep @topic: drop`,
		`%abc`, "topic", "drop", extOpRemove, extClassCommitted, &placed, nil)
	if !matched || drop {
		t.Fatalf("want matched && !drop, got matched=%v drop=%v", matched, drop)
	}
	if want := `@ext: %abc @topic: keep`; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

// extOpAdd never rewrites; it only reports an exact (target,tag,value) dup.
func TestMutateExtLineAddReportsDup(t *testing.T) {
	placed := false
	in := `@ext: %abc @topic: recall`
	got, drop, matched := mutateExtLine(in, `%abc`, "topic", "recall", extOpAdd, extClassCommitted, &placed, nil)
	if !matched || drop || got != in {
		t.Fatalf("dup: want matched && !drop && unchanged, got matched=%v drop=%v line=%q", matched, drop, got)
	}
	got, _, matched = mutateExtLine(in, `%abc`, "topic", "bloodhound", extOpAdd, extClassCommitted, &placed, nil)
	if matched || got != in {
		t.Errorf("no dup: want !matched && unchanged, got matched=%v line=%q", matched, got)
	}
}

func TestMutateExtLineNoMatchOnTargetMismatch(t *testing.T) {
	placed := false
	in := `@ext: /a/b.md:"foo" @topic: v`
	got, _, matched := mutateExtLine(in, `/a/b.md:"bar"`, "topic", "new", extOpSet, extClassCommitted, &placed, nil)
	if matched || got != in {
		t.Errorf("want unchanged, got matched=%v line=%q", matched, got)
	}
}

func TestMutateExtLineNoMatchOnTagMismatch(t *testing.T) {
	placed := false
	in := `@ext: /a/b.md:"foo" @topic: v`
	got, _, matched := mutateExtLine(in, `/a/b.md:"foo"`, "other", "new", extOpSet, extClassCommitted, &placed, nil)
	if matched || got != in {
		t.Errorf("want unchanged, got matched=%v line=%q", matched, got)
	}
}

func TestMutateExtLineNonExtLineUntouched(t *testing.T) {
	placed := false
	in := `# heading`
	got, _, matched := mutateExtLine(in, `/x`, "topic", "new", extOpSet, extClassCommitted, &placed, nil)
	if matched || got != in {
		t.Errorf("want unchanged, got matched=%v line=%q", matched, got)
	}
}

func TestApplyExtMirrorEditReplacesFirstMatch(t *testing.T) {
	data := []byte("@ext: /a:\"x\" @topic: old\n@ext: /b:\"y\" @topic: q\n")
	got, matched, _ := applyExtMirrorEdit(data, `/a:"x"`, "topic", "new", extOpSet, extClassCommitted)
	if !matched {
		t.Fatal("expected match")
	}
	want := "@ext: /a:\"x\" @topic: new\n@ext: /b:\"y\" @topic: q\n"
	if string(got) != want {
		t.Errorf("got %q want %q", string(got), want)
	}
}

// set collapses every (TARGET,tag) value across lines to one.
func TestApplyExtMirrorEditSetCollapsesAcrossLines(t *testing.T) {
	data := []byte("@ext: /a:\"x\" @topic: one\n@ext: /a:\"x\" @topic: two\n@ext: /b:\"y\" @topic: q\n")
	got, matched, _ := applyExtMirrorEdit(data, `/a:"x"`, "topic", "z", extOpSet, extClassCommitted)
	if !matched {
		t.Fatal("expected match")
	}
	want := "@ext: /a:\"x\" @topic: z\n@ext: /b:\"y\" @topic: q\n"
	if string(got) != want {
		t.Errorf("collapse: got %q want %q", string(got), want)
	}
}

func TestApplyExtMirrorEditDropsLineOnRemove(t *testing.T) {
	data := []byte("@ext: /a:\"x\" @topic: old\n@ext: /b:\"y\" @topic: q\n")
	got, matched, _ := applyExtMirrorEdit(data, `/a:"x"`, "topic", "", extOpRemove, extClassCommitted)
	if !matched {
		t.Fatal("expected match")
	}
	want := "@ext: /b:\"y\" @topic: q\n"
	if string(got) != want {
		t.Errorf("got %q want %q", string(got), want)
	}
}

// remove with no value drops every (TARGET,tag) line.
func TestApplyExtMirrorEditRemoveAllValues(t *testing.T) {
	data := []byte("@ext: /a:\"x\" @topic: one\n@ext: /a:\"x\" @topic: two\n")
	got, matched, _ := applyExtMirrorEdit(data, `/a:"x"`, "topic", "", extOpRemove, extClassCommitted)
	if !matched {
		t.Fatal("expected match")
	}
	if string(got) != "" {
		t.Errorf("remove-all: want empty, got %q", string(got))
	}
}

// remove with a value drops only the matching-value line.
func TestApplyExtMirrorEditRemoveByValue(t *testing.T) {
	data := []byte("@ext: /a:\"x\" @topic: one\n@ext: /a:\"x\" @topic: two\n")
	got, matched, _ := applyExtMirrorEdit(data, `/a:"x"`, "topic", "one", extOpRemove, extClassCommitted)
	if !matched {
		t.Fatal("expected match")
	}
	want := "@ext: /a:\"x\" @topic: two\n"
	if string(got) != want {
		t.Errorf("remove-by-value: got %q want %q", string(got), want)
	}
}

func TestApplyExtMirrorEditNoMatchReturnsOriginal(t *testing.T) {
	data := []byte("@ext: /a:\"x\" @topic: old\n")
	got, matched, _ := applyExtMirrorEdit(data, `/missing`, "topic", "v", extOpSet, extClassCommitted)
	if matched {
		t.Fatal("expected no match")
	}
	if string(got) != string(data) {
		t.Errorf("data should be unchanged: got %q", string(got))
	}
}

// add appends a new value; an exact dup leaves the file untouched.
func TestApplyExtMirrorEditAdd(t *testing.T) {
	data := []byte("@ext: /a:\"x\" @topic: recall\n")
	got, matched, _ := applyExtMirrorEdit(data, `/a:"x"`, "topic", "recall", extOpAdd, extClassCommitted)
	if !matched || string(got) != string(data) {
		t.Errorf("dup: want matched && unchanged, got matched=%v data=%q", matched, string(got))
	}
	got, matched, _ = applyExtMirrorEdit(data, `/a:"x"`, "topic", "bloodhound", extOpAdd, extClassCommitted)
	if matched {
		t.Errorf("new value: want !matched (caller appends), got matched=%v data=%q", matched, string(got))
	}
}

func TestResolveExtTargetUnknown(t *testing.T) {
	db, _, _ := extTestDB(t)
	if chunks := db.ResolveExtTarget("/nope-not-real", ""); len(chunks) != 0 {
		t.Errorf("unknown target should resolve empty, got %v", chunks)
	}
}

func TestResolveExtTargetEmpty(t *testing.T) {
	db, _, _ := extTestDB(t)
	if chunks := db.ResolveExtTarget("   ", ""); chunks != nil {
		t.Errorf("blank target should be nil, got %v", chunks)
	}
}

// TestOverlayExtRoutingToPersistentTarget reproduces the recall-curation
// server crash: an overlay (tmp://) @ext source routing to a *persistent*
// target. Before the fix, runOverlayExtRouting passed a nil txn to
// applyIndexExt → chunkFileID → fts.ReadCRecord(nil) → nil-pointer panic
// in lmdb.Txn.Get. The read-only env.View lets the persistent target's
// fileid resolve and the routed tag reach it.
// CRC: crc-Indexer.md | R2915
func TestOverlayExtRoutingToPersistentTarget(t *testing.T) {
	idx, dir := testIndexer(t)
	if err := idx.fts.AddChunker("markdown", microfts2.MarkdownChunker{}); err != nil {
		t.Fatal(err)
	}
	const uuid = "ext-tgt-77"
	fp := writeFile(t, dir, "target.md", "@id: "+uuid+"\n\nPreamble.\n")
	if _, err := idx.AddFile(fp, "markdown"); err != nil {
		t.Fatal(err)
	}
	if err := idx.store.LoadTvidMap(); err != nil {
		t.Fatal(err)
	}

	// Overlay machinery + ExtMap, mirroring DB.Open's wiring.
	tmpStore := NewTmpTagStore(idx.store.TvidMap())
	idx.store.SetTmpTagStore(tmpStore)
	db := newTestDB(idx, dir)
	db.extmap = NewExtMap()
	if err := db.extmap.Rebuild(db); err != nil {
		t.Fatal(err)
	}
	idx.extmap = db.extmap
	idx.db = db
	idx.store.SetExtMap(db.extmap)
	tmpStore.SetExtMap(db.extmap, db)

	// Overlay (tmp://) @ext source routing to the persistent target.
	src := "@ext: %" + uuid + " @topic: routed\n\nsource body.\n"
	if _, err := db.AddTmpFile("tmp://ext-src.md", "markdown", []byte(src)); err != nil {
		t.Fatalf("AddTmpFile (overlay @ext to persistent target): %v", err)
	}

	// The routed tag must have reached the persistent target chunk.
	if chunks := db.extmap.ExtTagValueChunks("topic", "routed"); len(chunks) == 0 {
		t.Fatal("overlay @ext routing did not reach the persistent target")
	}
}

// --- Pass A: @ext-candidate / @ext-judgment authoring ledger ---

// Spacey paths parse correctly: the TARGET is bounded at the first
// routed @tag:, so a space inside a bare path stays part of the TARGET.
// The automated producer proposes tags on such files, so this must hold
// — and insight-first keeps it holding. (R3050)
func TestParseExtTargetSpaceInPath(t *testing.T) {
	target, tags, ok := ParseExtTarget(`/home/my notes/file with space.md @topic: recall`)
	if !ok || target != "/home/my notes/file with space.md" {
		t.Fatalf("spacey path: ok=%v target=%q", ok, target)
	}
	if len(tags) != 1 || tags[0].Tag != "topic" || tags[0].Value != "recall" {
		t.Errorf("tags: %+v", tags)
	}
	target, tags, ok = ParseExtTarget(`insight: "why" /home/my notes/x y.md @topic: recall`)
	if !ok || target != "/home/my notes/x y.md" || len(tags) != 1 || tags[0].Value != "recall" {
		t.Errorf("insight+spacey: target=%q tags=%+v ok=%v", target, tags, ok)
	}
}

// ParseExtTarget peels a leading reserved insight; it is not a routed tag. (R3050)
func TestParseExtTargetSkipsInsight(t *testing.T) {
	target, tags, ok := ParseExtTarget(`insight: "resembles the streaming note" %abc @topic: recall`)
	if !ok || target != "%abc" {
		t.Fatalf("ok=%v target=%q", ok, target)
	}
	if len(tags) != 1 || tags[0].Tag != "topic" || tags[0].Value != "recall" {
		t.Errorf("routed tags: %+v (insight should be excluded)", tags)
	}
}

// A quoted @insight containing `@` or `:` must not be mis-split. (R3050)
func TestParseExtTargetInsightWithEmbeddedAtColon(t *testing.T) {
	_, tags, ok := ParseExtTarget(`insight: "see @foo: bar, ratio 3:1" %abc @topic: recall`)
	if !ok {
		t.Fatal("ok=false")
	}
	if len(tags) != 1 || tags[0].Tag != "topic" || tags[0].Value != "recall" {
		t.Errorf("embedded @/: leaked into routed tags: %+v", tags)
	}
}

// An @insight with no routed tag is nothing to apply. (R3050)
func TestParseExtTargetInsightOnlyIsNoop(t *testing.T) {
	if _, _, ok := ParseExtTarget(`insight: "just a thought" %abc`); ok {
		t.Error("expected ok=false for insight-only @ext line")
	}
}

// extClass markers name the three mirror classes. (R3052)
func TestExtClassMarker(t *testing.T) {
	cases := map[extClass]string{
		extClassCommitted: "@ext",
		extClassCandidate: "@ext-candidate",
		extClassJudgment:  "@ext-judgment",
	}
	for c, want := range cases {
		if got := c.marker(); got != want {
			t.Errorf("marker(%d): got %q want %q", c, got, want)
		}
	}
}

// mutateExtLine only touches lines of the requested class. (R3052)
func TestMutateExtLineClassAware(t *testing.T) {
	placed := false
	line := `@ext-candidate: %abc @topic: recall`
	if _, _, matched := mutateExtLine(line, "%abc", "topic", "", extOpRemove, extClassCommitted, &placed, nil); matched {
		t.Errorf("committed class should not match an @ext-candidate line")
	}
	placed = false
	got, drop, matched := mutateExtLine(line, "%abc", "topic", "", extOpRemove, extClassCandidate, &placed, nil)
	if !matched || !drop || got != "" {
		t.Errorf("candidate class: matched=%v drop=%v got=%q", matched, drop, got)
	}
}

// candidateLine builds the canonical proposal line: disposition right
// after the marker, then insight quoted before the routed tag, escaped.
// (R3051, R3053, R3092)
func TestCandidateLine(t *testing.T) {
	if got := candidateLine("%abc", "topic", "recall", "it fits", "external"); got != `@ext-candidate: external insight: "it fits" %abc @topic: recall` {
		t.Errorf("with insight: %q", got)
	}
	if got := candidateLine("%abc", "topic", "recall", "", "external"); got != `@ext-candidate: external %abc @topic: recall` {
		t.Errorf("no insight: %q", got)
	}
	// Disposition is part of the identity — internal makes a distinct line.
	if got := candidateLine("%abc", "topic", "", "", "internal"); got != `@ext-candidate: internal %abc @topic:` {
		t.Errorf("bare tag, internal: %q", got)
	}
	if got := candidateLine("%abc", "topic", "recall", `say "hi"`, "external"); got != `@ext-candidate: external insight: "say \"hi\"" %abc @topic: recall` {
		t.Errorf("quoted insight escaping: %q", got)
	}
	// Empty disposition is omitted (a dateless, dispositionless legacy line).
	if got := candidateLine("%abc", "topic", "recall", "", ""); got != `@ext-candidate: %abc @topic: recall` {
		t.Errorf("empty disposition omitted: %q", got)
	}
}

// stripLeadingDateDisposition peels a leading first-seen date and — only
// after a date — an internal/external disposition, mirroring the on-disk
// @ext-candidate / @ext-judgment line order. Committed @ext and
// date-shaped TARGETs pass through. (R3090, R3092, R3093)
func TestStripLeadingDateDisposition(t *testing.T) {
	cases := []struct{ in, want string }{
		// candidate: date + disposition peeled, insight + TARGET remain
		{`2026-07-12 external insight: "why" notes/f.md @topic: x`, `insight: "why" notes/f.md @topic: x`},
		{`2026-07-12 internal notes/f.md @topic: x`, `notes/f.md @topic: x`},
		// judgment: date only, no disposition token → just the date peels
		{`2026-07-12 notes/f.md @topic:`, `notes/f.md @topic:`},
		// committed @ext: no leading date → unchanged
		{`notes/f.md @topic: recall`, `notes/f.md @topic: recall`},
		{`%abc @topic: recall`, `%abc @topic: recall`},
		// date-shape guard: "2026-07-12.md" has no space at position 10 → not a date
		{`2026-07-12.md @topic: recall`, `2026-07-12.md @topic: recall`},
		// disposition peel fires only after a date: a bare "external…" target stays
		{`external-notes.md @topic: recall`, `external-notes.md @topic: recall`},
		// a non-disposition word after the date is left in place (only the date peels)
		{`2026-07-12 someword notes/f.md @topic: x`, `someword notes/f.md @topic: x`},
	}
	for _, c := range cases {
		if got := stripLeadingDateDisposition(c.in); got != c.want {
			t.Errorf("stripLeadingDateDisposition(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ParseExtTarget peels the leading date + disposition before resolving the
// TARGET, so a dated candidate/judgment value parses to the same (TARGET,
// tags) as its bare committed form. (R3090, R3092)
func TestParseExtTargetDateDisposition(t *testing.T) {
	cases := []struct {
		name, in, wantTarget, wantTag, wantVal string
	}{
		{"candidate date+disp+insight", `2026-07-12 external insight: "why" notes/f.md @topic: recall`, "notes/f.md", "topic", "recall"},
		{"candidate internal", `2026-07-12 internal notes/f.md @topic: recall`, "notes/f.md", "topic", "recall"},
		{"judgment date only", `2026-07-12 notes/f.md @topic:`, "notes/f.md", "topic", ""},
		{"committed no date", `notes/f.md @topic: recall`, "notes/f.md", "topic", "recall"},
		{"date-shape target not peeled", `2026-07-12.md @topic: recall`, "2026-07-12.md", "topic", "recall"},
	}
	for _, c := range cases {
		target, tags, ok := ParseExtTarget(c.in)
		if !ok {
			t.Errorf("%s: ok=false", c.name)
			continue
		}
		if target != c.wantTarget {
			t.Errorf("%s: target=%q want %q", c.name, target, c.wantTarget)
		}
		if len(tags) != 1 || tags[0].Tag != c.wantTag || tags[0].Value != c.wantVal {
			t.Errorf("%s: tags=%+v want {%s:%s}", c.name, tags, c.wantTag, c.wantVal)
		}
	}
}

// appendExtLine emits a tag-name-only routed tag for an empty value
// (the @ext-judgment form). (R3055)
func TestAppendExtLineJudgmentTagNameOnly(t *testing.T) {
	if got := appendExtLine(nil, "%abc", "topic", "", extClassJudgment); string(got) != "@ext-judgment: %abc @topic:\n" {
		t.Errorf("judgment tag-name-only: %q", string(got))
	}
	if got := appendExtLine(nil, "%abc", "topic", "recall", extClassCommitted); string(got) != "@ext: %abc @topic: recall\n" {
		t.Errorf("committed: %q", string(got))
	}
}

// mirrorHasLine matches whole lines exactly, so a differing insight is
// a distinct proposal. (R3053)
func TestMirrorHasLine(t *testing.T) {
	data := []byte("@ext-candidate: %abc @topic: recall\n@ext: %x @y: z\n")
	if !mirrorHasLine(data, "@ext-candidate: %abc @topic: recall") {
		t.Error("should find the exact candidate line")
	}
	if mirrorHasLine(data, `@ext-candidate: insight: "x" %abc @topic: recall`) {
		t.Error("differing insight is a distinct line, must not match")
	}
}

// Accept transition (the pure composition DB.transitionExtCandidate
// runs): remove the @ext-candidate span, re-emit committed, drop the
// @insight. (R3054)
func TestExtTransitionAcceptComposition(t *testing.T) {
	data := []byte(`@ext-candidate: insight: "resembles streaming" %abc @topic: recall` + "\n")
	newData, matched, removed := applyExtMirrorEdit(data, "%abc", "topic", "", extOpRemove, extClassCandidate)
	if !matched || len(removed) != 1 || removed[0].Tag != "topic" || removed[0].Value != "recall" {
		t.Fatalf("remove: matched=%v removed=%+v", matched, removed)
	}
	for _, tv := range removed {
		newData = upsertExtLine(newData, "%abc", tv.Tag, tv.Value, extOpAdd, extClassCommitted)
	}
	if want := "@ext: %abc @topic: recall\n"; string(newData) != want {
		t.Errorf("accept: got %q want %q", string(newData), want)
	}
}

// Regression (live Pass A drive): accept/reject with a *specific* value
// must still match an @ext-candidate that carries an @insight — the
// insight must not throw off routed-tag value matching. (R3053, R3054)
func TestExtCandidateInsightValueMatch(t *testing.T) {
	line := `insight: "why here" /home/deck/work/ark/principles.md @itag: ival`
	target, tags, ok := ParseExtTarget(line)
	t.Logf("ParseExtTarget → target=%q tags=%+v ok=%v", target, tags, ok)

	data := []byte(`@ext-candidate: insight: "why here" /home/deck/work/ark/principles.md @itag: ival` + "\n")
	newData, matched, removed := applyExtMirrorEdit(data, "/home/deck/work/ark/principles.md", "itag", "ival", extOpRemove, extClassCandidate)
	if !matched {
		t.Fatalf("expected match on insight-bearing candidate; removed=%+v newData=%q", removed, string(newData))
	}
	if len(removed) != 1 || removed[0].Tag != "itag" || removed[0].Value != "ival" {
		t.Errorf("removed=%+v", removed)
	}
}

// Reject transition: remove candidate span(s), re-emit a single
// tag-name-only @ext-judgment, deduped across values. (R3055)
func TestExtTransitionRejectComposition(t *testing.T) {
	data := []byte(
		`@ext-candidate: %abc @topic: recall` + "\n" +
			`@ext-candidate: %abc @topic: bloodhound` + "\n")
	newData, matched, removed := applyExtMirrorEdit(data, "%abc", "topic", "", extOpRemove, extClassCandidate)
	if !matched || len(removed) != 2 {
		t.Fatalf("remove: matched=%v removed=%+v", matched, removed)
	}
	seen := map[string]bool{}
	for _, tv := range removed {
		if seen[tv.Tag] {
			continue
		}
		seen[tv.Tag] = true
		newData = upsertExtLine(newData, "%abc", tv.Tag, "", extOpAdd, extClassJudgment)
	}
	if want := "@ext-judgment: %abc @topic:\n"; string(newData) != want {
		t.Errorf("reject: got %q want %q", string(newData), want)
	}
}
