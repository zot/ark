package ark

// CRC: crc-Patterns.md | Test: test-Patterns.md
//
// Every case runs against a t.TempDir() collection — no DB, no server, no
// fixtures. Patterns is filesystem-only by design, which is exactly what
// makes its decision logic cheap to pin.

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// patternFile writes one collection file and returns its path.
func patternFile(t *testing.T, dir, slug, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, slug+".md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func frontmattered(name, tags, summary string) string {
	return "---\nname: " + name + "\ntags: " + tags + "\nsummary: " + summary + "\n---\n\n# " + name + "\n\nbody text\n"
}

// R3267, R3269: slug is the key, and the order is stable.
func TestPatternsListOrdersBySlug(t *testing.T) {
	dir := t.TempDir()
	patternFile(t, dir, "straycat", frontmattered("Straycat", "performance, lua", "join once at the end"))
	patternFile(t, dir, "alibi-stamp", frontmattered("Alibi Stamp", "caching", "the cache's stamp is its alibi"))
	patternFile(t, dir, "baby-food", frontmattered("Baby Food", " agentic , format ", "markdown views of structured data"))

	pats, err := NewPatterns(dir).List()
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, p := range pats {
		got = append(got, p.Slug)
	}
	want := []string{"alibi-stamp", "baby-food", "straycat"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("slug order = %v, want %v", got, want)
	}
	// Tags split and trimmed, not passed through raw.
	if tags := pats[1].Tags; len(tags) != 2 || tags[0] != "agentic" || tags[1] != "format" {
		t.Errorf("tags = %#v, want [agentic format]", tags)
	}
	if pats[2].Name != "Straycat" || pats[2].Summary != "join once at the end" {
		t.Errorf("unexpected parse: %+v", pats[2])
	}
}

// R3271: a bad file is skipped, never emitted blank. This is the case that
// caught a real broken file in a live 54-pattern collection, where the prior
// shell implementation printed an entry with an empty name.
func TestPatternsListSkipsUnusableFiles(t *testing.T) {
	dir := t.TempDir()
	patternFile(t, dir, "good", frontmattered("Good", "x", "a real one"))
	patternFile(t, dir, "no-frontmatter", "# Heading\n\nTags: philosophy\n\nprose only\n")
	patternFile(t, dir, "no-name", "---\nsummary: nameless\n---\n\nbody\n")
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("---\nname: X\nsummary: Y\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pats, err := NewPatterns(dir).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(pats) != 1 || pats[0].Slug != "good" {
		t.Errorf("got %d patterns %v, want just [good]", len(pats), pats)
	}
}

// R3271: a new installation has no patterns yet — that is not an error.
func TestPatternsListToleratesMissingAndEmpty(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "never-created")
	pats, err := NewPatterns(missing).List()
	if err != nil || len(pats) != 0 {
		t.Errorf("missing dir: got (%v, %v), want (empty, nil)", pats, err)
	}
	pats, err = NewPatterns(t.TempDir()).List()
	if err != nil || len(pats) != 0 {
		t.Errorf("empty dir: got (%v, %v), want (empty, nil)", pats, err)
	}
	// And the block over an empty collection is empty, not a bare header.
	var buf bytes.Buffer
	if err := NewPatterns(missing).WriteBlock(&buf); err != nil || buf.Len() != 0 {
		t.Errorf("empty block: got %q, %v", buf.String(), err)
	}
}

// R3269: the block is pasted into system prompts, so its bytes are the
// contract — including the omitted Tags line when a pattern has no tags.
func TestPatternsWriteBlockShape(t *testing.T) {
	dir := t.TempDir()
	patternFile(t, dir, "aaa", frontmattered("Aaa", "one, two", "first summary"))
	patternFile(t, dir, "bbb", "---\nname: Bbb\nsummary: second summary\n---\n\nbody\n")

	var buf bytes.Buffer
	if err := NewPatterns(dir).WriteBlock(&buf); err != nil {
		t.Fatal(err)
	}
	want := "These patterns are in " + dir + "\n" +
		"When you notice a recurring technique worth naming, suggest adding it as a new pattern.\n\n" +
		"- **Aaa** (aaa.md)\n  Tags: one, two\n  first summary\n\n" +
		"- **Bbb** (bbb.md)\n  second summary\n\n"
	if buf.String() != want {
		t.Errorf("block mismatch\n--- got ---\n%s\n--- want ---\n%s", buf.String(), want)
	}
}

// R3272: machine consumers never parse the markdown; tags is [] not null.
func TestPatternsWriteJSON(t *testing.T) {
	dir := t.TempDir()
	patternFile(t, dir, "aaa", frontmattered("Aaa", "one, two", "first"))
	patternFile(t, dir, "bbb", "---\nname: Bbb\nsummary: second\n---\n\nbody\n")

	var buf bytes.Buffer
	if err := NewPatterns(dir).WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	var got []Pattern
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Slug != "aaa" || got[1].Slug != "bbb" {
		t.Fatalf("unexpected rows: %+v", got)
	}
	if len(got[0].Tags) != 2 {
		t.Errorf("tags = %#v, want 2", got[0].Tags)
	}
	if !strings.Contains(buf.String(), `"tags": []`) {
		t.Errorf("untagged pattern must serialize tags as [], not null:\n%s", buf.String())
	}
	if got[0].Path != filepath.Join(dir, "aaa.md") {
		t.Errorf("path = %q", got[0].Path)
	}
}

// R3273: the CLI owns the file shape, and creates the collection if absent.
func TestPatternsAddWritesShapeAndCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-yet")
	body := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(body, []byte("## How it works\n\nsome prose"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := NewPatterns(dir)
	path, err := p.Add("Try-The-Handle", "Try the Handle", "probe before routing around a stated limit", " verification , empirical ", mustRead(t, body))
	if err != nil {
		t.Fatal(err)
	}
	got := string(mustRead(t, path))
	want := "---\nname: Try the Handle\ntags: verification, empirical\n" +
		"summary: probe before routing around a stated limit\n---\n\n" +
		"# Try the Handle\n\n## How it works\n\nsome prose\n"
	if got != want {
		t.Errorf("file mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	// Slug is lowercased from the argument, so the file round-trips through List.
	pats, err := p.List()
	if err != nil || len(pats) != 1 || pats[0].Slug != "try-the-handle" {
		t.Errorf("round-trip: %+v, %v", pats, err)
	}
}

// R3273: never clobber a body nobody asked to delete.
func TestPatternsAddRefusesExistingSlug(t *testing.T) {
	dir := t.TempDir()
	p := NewPatterns(dir)
	path, err := p.Add("dup", "First", "the original summary", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	before := mustRead(t, path)

	if _, err := p.Add("dup", "Second", "a replacement summary", "", []byte("new body")); err == nil {
		t.Fatal("second Add with the same slug must fail")
	}
	if after := mustRead(t, path); !bytes.Equal(before, after) {
		t.Errorf("refused Add still modified the file:\n%s", after)
	}
}

// R3274: the definition is computed from frontmatter, never written into the file.
func TestDerivedDefFromFrontmatter(t *testing.T) {
	dir := t.TempDir()
	content := frontmattered("Straycat", "performance", "join once at the end")
	path := patternFile(t, dir, "straycat", content)
	before := mustRead(t, path)

	name, desc, ok := NewPatterns(dir).DerivedDef(path, []byte(content))
	if !ok || name != "straycat" || desc != "join once at the end" {
		t.Fatalf("DerivedDef = (%q, %q, %v)", name, desc, ok)
	}
	after := mustRead(t, path)
	if !bytes.Equal(before, after) {
		t.Error("DerivedDef must not modify the file")
	}
	if bytes.Contains(after, []byte("@tag:")) {
		t.Error("no @tag: line may be written into a pattern file")
	}
}

// R3275: derivation is scoped to *.md directly inside the collection.
func TestDerivedDefRejectsOutOfScopePaths(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "collection")
	content := frontmattered("Straycat", "performance", "join once")
	p := NewPatterns(dir)

	sibling := patternFile(t, filepath.Join(root, "elsewhere"), "straycat", content)
	nested := patternFile(t, filepath.Join(dir, "nested"), "straycat", content)
	wrongExt := patternFile(t, dir, "notes", content)
	wrongExt = strings.TrimSuffix(wrongExt, ".md") + ".txt"
	if err := os.WriteFile(wrongExt, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{sibling, nested, wrongExt} {
		if _, _, ok := p.DerivedDef(path, []byte(content)); ok {
			t.Errorf("%s must not derive a definition", path)
		}
	}
}

// R3275: the append path derives nothing — and does so without knowing it is
// the append path. executeAppend passes only the appended bytes, which cannot
// begin with the frontmatter fence, so the content test alone carries it.
func TestDerivedDefRejectsAppendedBytes(t *testing.T) {
	dir := t.TempDir()
	full := frontmattered("Straycat", "performance", "join once at the end")
	path := patternFile(t, dir, "straycat", full)

	// What executeAppend would hand us: a mid-file fragment.
	appended := []byte("\n## A section added later\n\nmore prose\n")
	if _, _, ok := NewPatterns(dir).DerivedDef(path, appended); ok {
		t.Error("appended bytes must not derive a definition — the full-index one survives")
	}
	// Sanity: the same path with full content still derives, so the rejection
	// above is the content test firing and not a path mismatch.
	if _, _, ok := NewPatterns(dir).DerivedDef(path, []byte(full)); !ok {
		t.Error("full content on the same path must still derive")
	}
}

// R3277: one reader, two schemas — the personal collection keys on summary,
// the Frictionless UI collection on description.
func TestParseFrontmatterServesBothCollections(t *testing.T) {
	personal := ParseFrontmatter([]byte(frontmattered("Straycat", "perf", "join once")))
	if personal["name"] != "Straycat" || personal["summary"] != "join once" {
		t.Errorf("personal schema: %#v", personal)
	}
	ui := ParseFrontmatter([]byte("---\nname: Custom ViewLists\ndescription: styling default ViewLists\n---\n\n# body\n"))
	if ui["name"] != "Custom ViewLists" || ui["description"] != "styling default ViewLists" {
		t.Errorf("ui schema: %#v", ui)
	}
	// A body-only file has no frontmatter, whichever schema you wanted.
	if got := ParseFrontmatter([]byte("# Heading\n\nprose\n")); got != nil {
		t.Errorf("no frontmatter should parse to nil, got %#v", got)
	}
	// An unterminated fence is not frontmatter either.
	if got := ParseFrontmatter([]byte("---\nname: X\n")); got != nil {
		t.Errorf("unterminated fence should parse to nil, got %#v", got)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestStripFrontmatterKeepsBodyTags is the load-bearing boundary for R3297:
// only the YAML block goes. The body's `@tag:` lines look like metadata and are
// content — they carry origin and neighbouring-pattern connections that the
// listing never showed, which is what a reader opens the file for. A
// "strip the metadata" implementation would remove exactly the payload.
func TestStripFrontmatterKeepsBodyTags(t *testing.T) {
	content := []byte("---\nname: X\ntags: a, b\nsummary: s\n---\n\n# X\n\n@learned: why it exists\n@connection: [[y]]\n\nBody.\n")
	body, had := StripFrontmatter(content)
	if !had {
		t.Fatal("frontmatter should have been detected")
	}
	got := string(body)
	for _, want := range []string{"@learned: why it exists", "@connection: [[y]]", "Body."} {
		if !strings.Contains(got, want) {
			t.Errorf("body must keep %q; got:\n%s", want, got)
		}
	}
	for _, gone := range []string{"summary: s", "tags: a, b"} {
		if strings.Contains(got, gone) {
			t.Errorf("frontmatter field %q should be stripped; got:\n%s", gone, got)
		}
	}
}

// TestStripFrontmatterLeavesUnfencedContentAlone guards the other direction: a
// file with no frontmatter, or an unterminated fence, must pass through whole
// rather than losing its head to a half-matched block.
func TestStripFrontmatterLeavesUnfencedContentAlone(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"no frontmatter", "# Title\n\nBody.\n"},
		{"unterminated fence", "---\nname: X\n\n# Title\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, had := StripFrontmatter([]byte(tc.in))
			if had {
				t.Error("should not report frontmatter")
			}
			if string(body) != tc.in {
				t.Errorf("content must pass through unchanged; got:\n%s", body)
			}
		})
	}
}

// TestShowPrintsOneNamingLine pins R3297's dedup: `patterns add` writes a
// `# NAME` heading, so the header would otherwise state the name twice — in the
// verb whose whole purpose is spending fewer tokens.
func TestShowPrintsOneNamingLine(t *testing.T) {
	dir := t.TempDir()
	write := func(slug, body string) {
		if err := os.WriteFile(filepath.Join(dir, slug+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("dup", "---\nname: Dup Pattern\nsummary: s\n---\n\n# Dup Pattern\n\nBody here.\n")
	// A leading H1 that is NOT the name is content, and must survive.
	write("other", "---\nname: Other Pattern\nsummary: s\n---\n\n# Some Section\n\nBody here.\n")

	p := NewPatterns(dir)

	var buf bytes.Buffer
	if err := p.Show(&buf, "dup"); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if n := strings.Count(got, "Dup Pattern"); n != 1 {
		t.Errorf("name should appear exactly once, got %d:\n%s", n, got)
	}
	if !strings.HasPrefix(got, "# Dup Pattern (dup.md)\n") {
		t.Errorf("header must name pattern and file; got:\n%s", got)
	}
	if !strings.Contains(got, "Body here.") {
		t.Errorf("body missing:\n%s", got)
	}

	buf.Reset()
	if err := p.Show(&buf, "other"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "# Some Section") {
		t.Errorf("an H1 that is not the name is content and must survive; got:\n%s", buf.String())
	}
}

// TestShowUnknownSlugNamesTheDirectory — the likeliest cause of a miss is a
// collection that is not where the reader assumed, and the slug alone won't say.
func TestShowUnknownSlugNamesTheDirectory(t *testing.T) {
	dir := t.TempDir()
	err := NewPatterns(dir).Show(io.Discard, "nosuch")
	if err == nil {
		t.Fatal("unknown slug must error")
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("error should name the collection dir %q; got %v", dir, err)
	}
}
