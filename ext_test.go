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
	if err := idx.fts.AddChunker("markdown", microfts2.MarkdownChunker{}, makeTagTransform("markdown")); err != nil {
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
	got, drop, matched := mutateExtLine(
		`@ext: /a/b.md:"foo" @topic: old`,
		`/a/b.md:"foo"`, "topic", "new", false)
	if !matched || drop {
		t.Fatalf("want matched && !drop, got matched=%v drop=%v", matched, drop)
	}
	if want := `@ext: /a/b.md:"foo" @topic: new`; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestMutateExtLineSingleTagRemoveDropsLine(t *testing.T) {
	got, drop, matched := mutateExtLine(
		`@ext: /a/b.md:"foo" @topic: x`,
		`/a/b.md:"foo"`, "topic", "", true)
	if !matched || !drop {
		t.Fatalf("want matched && drop, got matched=%v drop=%v", matched, drop)
	}
	if got != "" {
		t.Errorf("expected empty newLine for dropped line, got %q", got)
	}
}

func TestMutateExtLineMultiTagReplaceOnly(t *testing.T) {
	got, drop, matched := mutateExtLine(
		`@ext: %abc @t1: v1 @target: oldv @t3: v3`,
		`%abc`, "target", "newv", false)
	if !matched || drop {
		t.Fatalf("want matched && !drop")
	}
	if want := `@ext: %abc @t1: v1 @target: newv @t3: v3`; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestMutateExtLineMultiTagRemoveOnly(t *testing.T) {
	got, drop, matched := mutateExtLine(
		`@ext: %abc @t1: v1 @target: v2 @t3: v3`,
		`%abc`, "target", "", true)
	if !matched || drop {
		t.Fatalf("want matched && !drop")
	}
	if want := `@ext: %abc @t1: v1 @t3: v3`; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestMutateExtLineNoMatchOnTargetMismatch(t *testing.T) {
	in := `@ext: /a/b.md:"foo" @topic: v`
	got, _, matched := mutateExtLine(in, `/a/b.md:"bar"`, "topic", "new", false)
	if matched || got != in {
		t.Errorf("want unchanged, got matched=%v line=%q", matched, got)
	}
}

func TestMutateExtLineNoMatchOnTagMismatch(t *testing.T) {
	in := `@ext: /a/b.md:"foo" @topic: v`
	got, _, matched := mutateExtLine(in, `/a/b.md:"foo"`, "other", "new", false)
	if matched || got != in {
		t.Errorf("want unchanged, got matched=%v line=%q", matched, got)
	}
}

func TestMutateExtLineNonExtLineUntouched(t *testing.T) {
	in := `# heading`
	got, _, matched := mutateExtLine(in, `/x`, "topic", "new", false)
	if matched || got != in {
		t.Errorf("want unchanged, got matched=%v line=%q", matched, got)
	}
}

func TestApplyExtMirrorEditReplacesFirstMatch(t *testing.T) {
	data := []byte("@ext: /a:\"x\" @topic: old\n@ext: /b:\"y\" @topic: q\n")
	got, matched := applyExtMirrorEdit(data, `/a:"x"`, "topic", "new", false)
	if !matched {
		t.Fatal("expected match")
	}
	want := "@ext: /a:\"x\" @topic: new\n@ext: /b:\"y\" @topic: q\n"
	if string(got) != want {
		t.Errorf("got %q want %q", string(got), want)
	}
}

func TestApplyExtMirrorEditDropsLineOnRemove(t *testing.T) {
	data := []byte("@ext: /a:\"x\" @topic: old\n@ext: /b:\"y\" @topic: q\n")
	got, matched := applyExtMirrorEdit(data, `/a:"x"`, "topic", "", true)
	if !matched {
		t.Fatal("expected match")
	}
	want := "@ext: /b:\"y\" @topic: q\n"
	if string(got) != want {
		t.Errorf("got %q want %q", string(got), want)
	}
}

func TestApplyExtMirrorEditNoMatchReturnsOriginal(t *testing.T) {
	data := []byte("@ext: /a:\"x\" @topic: old\n")
	got, matched := applyExtMirrorEdit(data, `/missing`, "topic", "v", false)
	if matched {
		t.Fatal("expected no match")
	}
	if string(got) != string(data) {
		t.Errorf("data should be unchanged: got %q", string(got))
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
