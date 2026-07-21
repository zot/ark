package ark

// CRC: crc-Librarian.md, crc-RecallWatcher.md, crc-Searcher.md | Test: test-RecallScope.md

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAnchorGlobToDir covers the anchoring half of `-files` semantics: globs
// that already name an absolute location pass through, everything else joins
// to the caller's project directory.
// Refs: R3192, R3193
func TestAnchorGlobToDir(t *testing.T) {
	const dir = "/home/u/work/ark"
	cases := []struct{ glob, want string }{
		{"/etc/**", "/etc/**"},                             // absolute passes through
		{"~/.claude/projects/**", "~/.claude/projects/**"}, // home-relative passes through
		{"tmp://ARK-RECALL/x", "tmp://ARK-RECALL/x"},       // overlay passes through
		{"/**/*.go", "/**/*.go"},                           // the corpus-wide escape hatch
		{"specs/**", "/home/u/work/ark/specs/**"},          // unanchored joins
		{"**/*.jsonl", "/home/u/work/ark/**/*.jsonl"},      // ** is still project-relative
		{"design/", "/home/u/work/ark/design/"},            // trailing slash survives the join
		{"", ""},                                           // empty stays empty
	}
	for _, c := range cases {
		if got := AnchorGlobToDir(c.glob, dir); got != c.want {
			t.Errorf("AnchorGlobToDir(%q, dir) = %q, want %q", c.glob, got, c.want)
		}
	}
	// With no directory to anchor against, a glob must survive unchanged
	// rather than being silently mangled into something that matches nothing.
	if got := AnchorGlobToDir("specs/**", ""); got != "specs/**" {
		t.Errorf("AnchorGlobToDir with empty dir = %q, want passthrough", got)
	}
}

// TestAnchorGlobsToDir_DropsEmpty checks that an empty glob is dropped rather
// than carried through as a pattern matching nothing — a stray
// `filter-files=""` must not silently empty the corpus.
// Refs: R3185
func TestAnchorGlobsToDir_DropsEmpty(t *testing.T) {
	got := AnchorGlobsToDir([]string{"", "  ", "specs/**"}, "/p")
	if len(got) != 1 || got[0] != "/p/specs/**" {
		t.Fatalf("AnchorGlobsToDir = %v, want exactly [/p/specs/**]", got)
	}
	if AnchorGlobsToDir(nil, "/p") != nil {
		t.Error("AnchorGlobsToDir(nil) should stay nil")
	}
}

// TestScopeAdmitsPath covers the positive-then-negative glob rule.
// Refs: R3185, R3192
func TestScopeAdmitsPath(t *testing.T) {
	const p = "/home/u/work/ark/specs/bloodhound.md"
	cases := []struct {
		name            string
		filter, exclude []string
		want            bool
	}{
		{"no globs admits", nil, nil, true},
		{"positive match", []string{"/home/u/work/ark/**"}, nil, true},
		{"positive miss", []string{"/home/u/other/**"}, nil, false},
		{"positives union", []string{"/nope/**", "/home/u/work/ark/**"}, nil, true},
		{"exclude wins over match", []string{"/home/u/work/ark/**"}, []string{"**/*.md"}, false},
		{"exclude alone", nil, []string{"**/*.md"}, false},
		{"exclude misses", nil, []string{"**/*.go"}, true},
	}
	// The three depth shapes, which are easy to confuse. A bare `*.go` is
	// joined to the project *before* matching, so it never becomes the
	// match-any-basename pattern it resembles — only `**/` gives depth, and
	// only a leading `/` escapes the project.
	depth := []struct {
		glob string
		want bool
	}{
		{"/**/*.go", true},                    // anywhere in the corpus
		{"/home/u/work/ark/**/*.go", true},    // this project, any depth
		{"/home/u/work/other/**/*.go", false}, // a different project
		{"/home/u/work/ark/*.go", false},      // top level only; this path is nested
	}
	const goPath = "/home/u/work/ark/cmd/ark/main.go"
	for _, d := range depth {
		if got := scopeAdmitsPath(goPath, []string{d.glob}, nil); got != d.want {
			t.Errorf("scopeAdmitsPath(%s, %q) = %v, want %v", goPath, d.glob, got, d.want)
		}
	}
	for _, c := range cases {
		if got := scopeAdmitsPath(p, c.filter, c.exclude); got != c.want {
			t.Errorf("%s: scopeAdmitsPath = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestRecall_PathScopeFiltersAtAdmission is the load-bearing test for the
// push-down: the corpus is arranged so the *strongest* matches are out of
// scope, so a post-filter (apply K, then drop) would return nothing while the
// admission filter returns the in-scope match. The two designs are
// indistinguishable unless the out-of-scope chunks outrank the in-scope one,
// which is exactly what this arranges. Content differs per file because
// identical content deduplicates to a single chunk.
// Refs: R3185, R3186, R3192
func TestRecall_PathScopeFiltersAtAdmission(t *testing.T) {
	l, db := setupRecall(t)

	keepDir := filepath.Join(db.dbPath, "keep")
	dropDir := filepath.Join(db.dbPath, "drop")
	for _, d := range []string{keepDir, dropDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Out-of-scope files carry the whole query; the in-scope file carries less
	// of it, so it ranks below all three.
	indexLine(t, db, filepath.Join("drop", "a.txt"), "apple banana cherry alpha")
	indexLine(t, db, filepath.Join("drop", "b.txt"), "apple banana cherry beta")
	indexLine(t, db, filepath.Join("drop", "c.txt"), "apple banana cherry gamma")
	indexLine(t, db, filepath.Join("keep", "d.txt"), "apple banana delta")

	const query = "apple banana cherry"

	// Sanity: unscoped, the drop/ files must genuinely outrank keep/ — without
	// that the scoped assertion below proves nothing about *when* the filter runs.
	unscoped, err := l.Recall([]ConnectionsInput{{Text: query}}, RecallOpts{K: 3, KeepTagless: true})
	if err != nil {
		t.Fatalf("Recall (unscoped): %v", err)
	}
	if len(unscoped.Chunks) == 0 {
		t.Fatal("unscoped recall found nothing; the fixture is not exercising the substrate")
	}
	for _, c := range unscoped.Chunks {
		if strings.HasPrefix(c.Path, keepDir) {
			t.Fatalf("fixture invalid: keep/ ranked inside the unscoped top-3 (%s), so a post-filter would pass this test too", c.Path)
		}
	}

	scoped, err := l.Recall(
		[]ConnectionsInput{{Text: query}},
		RecallOpts{K: 3, KeepTagless: true, FilterFiles: []string{keepDir + "/**"}},
	)
	if err != nil {
		t.Fatalf("Recall (scoped): %v", err)
	}
	if len(scoped.Chunks) != 1 {
		t.Fatalf("scoped recall returned %d chunks, want 1 — a post-filter would return 0 here", len(scoped.Chunks))
	}
	if !strings.HasPrefix(scoped.Chunks[0].Path, keepDir) {
		t.Errorf("scoped recall surfaced %q, want a path under %q", scoped.Chunks[0].Path, keepDir)
	}
	if scoped.ScopeEmpty {
		t.Error("ScopeEmpty set even though the include glob matched files")
	}

	// The negative direction.
	excluded, err := l.Recall(
		[]ConnectionsInput{{Text: query}},
		RecallOpts{K: 10, KeepTagless: true, ExcludeFiles: []string{dropDir + "/**"}},
	)
	if err != nil {
		t.Fatalf("Recall (exclude): %v", err)
	}
	if len(excluded.Chunks) == 0 {
		t.Fatal("exclude glob removed everything, including the in-scope file")
	}
	for _, c := range excluded.Chunks {
		if strings.HasPrefix(c.Path, dropDir) {
			t.Errorf("excluded path %q survived the exclude glob", c.Path)
		}
	}
}

// TestRecall_ScopeEmptyReportsWrongGlob distinguishes "your scope matched no
// indexed file" from "the corpus had nothing" — the failure a reader cannot
// otherwise see, since both return zero chunks.
// Refs: R3188
func TestRecall_ScopeEmptyReportsWrongGlob(t *testing.T) {
	l, db := setupRecall(t)
	indexLine(t, db, "a.txt", "apple banana cherry")

	wrongGlob, err := l.Recall(
		[]ConnectionsInput{{Text: "apple banana cherry"}},
		RecallOpts{K: 10, KeepTagless: true, FilterFiles: []string{"/no/such/place/**"}},
	)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(wrongGlob.Chunks) != 0 {
		t.Fatalf("a glob matching nothing returned %d chunks", len(wrongGlob.Chunks))
	}
	if !wrongGlob.ScopeEmpty {
		t.Error("ScopeEmpty not set for an include glob matching no indexed file")
	}

	// A real negative — corpus searched within a valid scope, nothing matched —
	// must NOT claim the scope was wrong, or the signal is worthless.
	realMiss, err := l.Recall(
		[]ConnectionsInput{{Text: "zzzz nonexistent phrase"}},
		RecallOpts{K: 10, KeepTagless: true, FilterFiles: []string{db.dbPath + "/**"}},
	)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if realMiss.ScopeEmpty {
		t.Error("ScopeEmpty set for a glob that does match indexed files")
	}

	// No scope at all is never a scope failure.
	unscoped, err := l.Recall(
		[]ConnectionsInput{{Text: "apple"}},
		RecallOpts{K: 10, KeepTagless: true},
	)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if unscoped.ScopeEmpty {
		t.Error("ScopeEmpty set on an unscoped recall")
	}
}

// TestRecall_ScopeSparesConversationChunks: injected conversation context is the
// caller's own material, not a search hit, so a file scope must not drop it —
// otherwise scoping a hunt would silently stop the conversation from earning
// proposals. Mirrors TestRecall_Propose_InjectsConversationChunks, adding an
// exclude glob that covers the conversation file.
// Refs: R3187
func TestRecall_ScopeSparesConversationChunks(t *testing.T) {
	l, db := setupFileBackedRecall(t)

	cConv, convPath := indexLine(t, db, "conv.txt", "zebra xylophone quartz")
	cOther, _ := indexLine(t, db, "other.txt", "apple banana cherry")
	db.store.WriteChunkEmbedding(cConv, vecFrom(0, 1, 0, 0))
	db.store.WriteChunkEmbedding(cOther, vecFrom(1, 0, 0, 0))
	db.store.WriteTagDefEmbedding("food", 10, vecFrom(0, 1, 0, 0)) // aligns cConv

	res, err := l.Recall(
		[]ConnectionsInput{{ChunkID: cConv}},
		RecallOpts{
			K:                  5,
			Propose:            true,
			KeepTagless:        true,
			ConversationChunks: []uint64{cConv},
			// Excludes the conversation file itself. A scope that reached the
			// injection would drop it here.
			ExcludeFiles: []string{convPath},
		},
	)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	conv := findSurfaced(res, cConv)
	if conv == nil {
		t.Fatal("the file scope suppressed an injected conversation chunk; injection must bypass the scope")
	}
	if len(conv.ProposedTags) == 0 || conv.ProposedTags[0] != "food" {
		t.Errorf("expected food proposed on the conversation chunk; got %v", conv.ProposedTags)
	}
}

// TestScopeOf parses the CLI request doc's repeatable scope metadata lines.
// Refs: R3191
func TestScopeOf(t *testing.T) {
	payload := strings.Join([]string{
		"scope: all",
		"depth: lookup",
		"want: passages",
		"filter-files: /home/u/work/ark/**",
		"filter-files: /home/u/work/microfts2/**",
		"exclude-files: /home/u/.claude/projects/**",
		"filter-files:",
		"",
		"where did we settle the chunker interface?",
	}, "\n")

	filter, exclude := scopeOf(payload)
	if len(filter) != 2 || filter[0] != "/home/u/work/ark/**" || filter[1] != "/home/u/work/microfts2/**" {
		t.Errorf("filter = %v, want the two globs in order (empty value dropped)", filter)
	}
	if len(exclude) != 1 || exclude[0] != "/home/u/.claude/projects/**" {
		t.Errorf("exclude = %v, want one glob", exclude)
	}

	// The scope keys must not leak into the clue the seed searches.
	if clue := clueOf(payload); strings.Contains(clue, "filter-files") {
		t.Errorf("clueOf leaked scope metadata into the search clue: %q", clue)
	}
}

// TestParseBloodhoundAttrs covers the opening tag's attribute run: the bare
// notags opt-out, repeatable globs in both directions, order independence,
// anchoring against the emitting session's cwd, and forward compatibility with
// an attribute this build does not know.
// Refs: R3110, R3184, R3185, R3193
func TestParseBloodhoundAttrs(t *testing.T) {
	const cwd = "/home/u/work/ark"

	t.Run("empty run", func(t *testing.T) {
		req := parseBloodhoundAttrs("", cwd)
		if req.notags || req.scoped() {
			t.Errorf("empty attribute run produced %+v", req)
		}
	})

	t.Run("repeats and both directions", func(t *testing.T) {
		req := parseBloodhoundAttrs(
			` filter-files="~/work/ark/**" exclude-files="**/*.jsonl" filter-files="specs/**"`, cwd)
		want := []string{"~/work/ark/**", "/home/u/work/ark/specs/**"}
		if len(req.filterFiles) != 2 || req.filterFiles[0] != want[0] || req.filterFiles[1] != want[1] {
			t.Errorf("filterFiles = %v, want %v (tilde passes through, unanchored joins cwd)", req.filterFiles, want)
		}
		if len(req.excludeFiles) != 1 || req.excludeFiles[0] != "/home/u/work/ark/**/*.jsonl" {
			t.Errorf("excludeFiles = %v, want the cwd-anchored jsonl glob", req.excludeFiles)
		}
		if req.notags {
			t.Error("notags set without the bare attribute")
		}
	})

	t.Run("notags mixed with globs, any order", func(t *testing.T) {
		for _, run := range []string{
			` notags filter-files="specs/**"`,
			` filter-files="specs/**" notags`,
		} {
			req := parseBloodhoundAttrs(run, cwd)
			if !req.notags {
				t.Errorf("%q: notags not recognized", run)
			}
			if len(req.filterFiles) != 1 {
				t.Errorf("%q: filterFiles = %v", run, req.filterFiles)
			}
		}
	})

	t.Run("unrecognized attribute ignored", func(t *testing.T) {
		req := parseBloodhoundAttrs(` depth="investigate" filter-files="specs/**"`, cwd)
		if len(req.filterFiles) != 1 || req.filterFiles[0] != "/home/u/work/ark/specs/**" {
			t.Errorf("an unknown attribute broke parsing: %v", req.filterFiles)
		}
	})

	t.Run("empty value dropped", func(t *testing.T) {
		req := parseBloodhoundAttrs(` filter-files=""`, cwd)
		if req.scoped() {
			t.Errorf(`filter-files="" became a scope: %+v — it must not empty the corpus`, req)
		}
	})

	t.Run("notags not matched as a substring", func(t *testing.T) {
		req := parseBloodhoundAttrs(` filter-files="notags/**"`, cwd)
		if req.notags {
			t.Error("notags matched inside a glob value")
		}
	})
}

// TestScanBloodhounds_AttributesAndCwd checks recognition end-to-end from JSONL
// bytes: the attribute run parses, the globs anchor against the line's own cwd,
// and `<BLOODHOUNDER>` is not a watermark (the regex's leading \s).
// Refs: R3184, R3193
func TestScanBloodhounds_AttributesAndCwd(t *testing.T) {
	line := func(cwd, text string) string {
		b, err := json.Marshal(map[string]any{
			"type": "assistant",
			"cwd":  cwd,
			"message": map[string]any{
				"content": []map[string]string{{"type": "text", "text": text}},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b) + "\n"
	}

	newBytes := []byte(
		line("/home/u/work/ark", `<BLOODHOUND filter-files="specs/**">where is the chunker interface?</BLOODHOUND>`) +
			line("/home/u/work/other", `<BLOODHOUND exclude-files="**/*.jsonl" notags>second hunt</BLOODHOUND>`) +
			line("/home/u/work/ark", `<BLOODHOUNDER>not a watermark</BLOODHOUNDER>`) +
			line("/home/u/work/ark", `<BLOODHOUND>plain hunt</BLOODHOUND>`))

	reqs := scanBloodhounds(newBytes)
	if len(reqs) != 3 {
		t.Fatalf("got %d requests, want 3 (BLOODHOUNDER must not match)", len(reqs))
	}

	if got := reqs[0].filterFiles; len(got) != 1 || got[0] != "/home/u/work/ark/specs/**" {
		t.Errorf("first hunt filterFiles = %v, want anchored to its own line's cwd", got)
	}
	// The second line reports a different cwd — each watermark anchors against
	// the session that emitted it, not against a watcher-wide notion of "here".
	if got := reqs[1].excludeFiles; len(got) != 1 || got[0] != "/home/u/work/other/**/*.jsonl" {
		t.Errorf("second hunt excludeFiles = %v, want anchored to /home/u/work/other", got)
	}
	if !reqs[1].notags {
		t.Error("second hunt lost its notags")
	}
	if reqs[2].scoped() || reqs[2].payload != "plain hunt" {
		t.Errorf("plain watermark should carry no scope: %+v", reqs[2])
	}
}

// TestScopeDirective renders the ready-made filter string the secretary copies.
// The secretary must never compose a glob, so the directive has to arrive
// complete and quoted.
// Refs: R3189, R3194
func TestScopeDirective(t *testing.T) {
	if got := scopeDirective(bloodhoundReq{payload: "x"}); got != "" {
		t.Errorf("unscoped hunt produced a directive: %q", got)
	}
	got := scopeDirective(bloodhoundReq{
		filterFiles:  []string{"/w/ark/**", "/w/microfts2/**"},
		excludeFiles: []string{"/h/.claude/projects/**"},
	})
	for _, want := range []string{
		`-with -files '/w/ark/**'`,
		`-with -files '/w/microfts2/**'`,
		`-without -files '/h/.claude/projects/**'`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("directive missing %s\ngot: %s", want, got)
		}
	}
	if !strings.Contains(got, "EVERY") {
		t.Errorf("directive does not tell the secretary to apply it to every search: %s", got)
	}
}

// TestRenderBloodhoundSeed_ScopeEmptyNote checks that a wrong scope reads
// differently from an empty corpus. Collapsing the two would hand back a
// confident "nothing found" for a mistyped glob.
// Refs: R3188
func TestRenderBloodhoundSeed_ScopeEmptyNote(t *testing.T) {
	req := bloodhoundReq{payload: "clue", filterFiles: []string{"/typo/**"}}
	scopeNote := renderBloodhoundSeed(&RecallResult{ScopeEmpty: true}, req)
	if !strings.Contains(scopeNote, "/typo/**") {
		t.Errorf("scope-empty note does not name the offending glob:\n%s", scopeNote)
	}
	if !strings.Contains(scopeNote, "scope matched no indexed file") {
		t.Errorf("scope-empty note does not say the scope was the problem:\n%s", scopeNote)
	}

	emptyCorpus := renderBloodhoundSeed(&RecallResult{}, bloodhoundReq{payload: "clue"})
	if strings.Contains(emptyCorpus, "scope matched no indexed file") {
		t.Errorf("an ordinary empty seed claimed a scope failure:\n%s", emptyCorpus)
	}
	if !strings.Contains(emptyCorpus, "no corpus matches") {
		t.Errorf("ordinary empty seed lost its note:\n%s", emptyCorpus)
	}
}

// TestBuildSearchTask_CarriesScope pins the scope directive into the task doc
// the secretary actually reads.
// Refs: R3189
func TestBuildSearchTask_CarriesScope(t *testing.T) {
	body := buildSearchTask("sess", "sess-1", "## Recall seed\n\n_(none)_\n", bloodhoundReq{
		payload:     "where is the chunker interface?",
		filterFiles: []string{"/w/ark/**"},
	})
	if !strings.Contains(body, `-with -files '/w/ark/**'`) {
		t.Errorf("task doc lost the scope directive:\n%s", body)
	}
	// Directives must precede the seed, which precedes the crank handle.
	scopeAt := strings.Index(body, "-with -files")
	seedAt := strings.Index(body, "## Recall seed")
	handleAt := strings.Index(body, "You are the bloodhound")
	if !(scopeAt < seedAt && seedAt < handleAt) {
		t.Errorf("task doc order wrong: scope=%d seed=%d handle=%d", scopeAt, seedAt, handleAt)
	}
}
