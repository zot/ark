package ark

// CRC: crc-Config.md | Test: test-Config.md, test-LinkTable.md

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestEnsureLuhmannSource verifies ark auto-indexes the orchestrator's own
// session by adding its Claude Code project directory as a chat-jsonl source,
// idempotently, with no user config (R3135).
func TestEnsureLuhmannSource(t *testing.T) {
	setTestHome(t, t.TempDir())
	want := ClaudeProjectDir(luhmannCwd())
	if want == "" {
		t.Fatal("ClaudeProjectDir(luhmannCwd()) returned empty")
	}
	c := &Config{}
	c.EnsureLuhmannSource()
	found := false
	for _, s := range c.Sources {
		if s.Dir == want {
			found = true
			if len(s.Include.Replace) != 1 || s.Include.Replace[0] != "*.jsonl" {
				t.Errorf("include = %v, want [*.jsonl]", s.Include.Replace)
			}
		}
	}
	if !found {
		t.Fatalf("EnsureLuhmannSource added no source for %s", want)
	}
	// Idempotent: a second call must not duplicate.
	n := len(c.Sources)
	c.EnsureLuhmannSource()
	if len(c.Sources) != n {
		t.Errorf("second EnsureLuhmannSource duplicated: %d → %d", n, len(c.Sources))
	}
}

func TestLoadValidConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ark.toml")
	content := `dotfiles = true

default_include = ["*.md", "*.txt"]
default_exclude = [".git/", ".env"]

[[source]]
dir = '` + dir + `'
strategies = {"*.txt" = "lines"}
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Dotfiles {
		t.Error("dotfiles should be true")
	}
	if len(cfg.Sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(cfg.Sources))
	}
	if cfg.Sources[0].Strategies["*.txt"] != "lines" {
		t.Errorf("expected per-source strategy for *.txt = 'lines', got %v", cfg.Sources[0].Strategies)
	}
	if cfg.HasErrors() {
		t.Errorf("should have no errors, got %v", cfg.Errors)
	}
}

// R2146: per-source include.add extends default_include
func TestPerSourceIncludeAddExtendsDefault(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ark.toml")
	content := `dotfiles = true
default_include = ["*.md", "*.go"]

[[source]]
dir = '` + dir + `'
include.add = ["*.lua"]
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	inc, _ := cfg.EffectivePatterns(cfg.Sources[0])
	if len(inc) != 3 {
		t.Fatalf("expected 3 include patterns (default + add), got %d: %v", len(inc), inc)
	}
	want := map[string]bool{"*.md": true, "*.go": true, "*.lua": true}
	for _, p := range inc {
		if !want[p] {
			t.Errorf("unexpected pattern %q in result", p)
		}
		delete(want, p)
	}
	if len(want) != 0 {
		t.Errorf("missing patterns: %v", want)
	}
}

// R2146: per-source exclude.add extends default_exclude
func TestPerSourceExcludeAddExtendsDefault(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ark.toml")
	content := `dotfiles = true
default_include = ["*.md"]
default_exclude = [".git/"]

[[source]]
dir = '` + dir + `'
exclude.add = ["drafts/"]
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	_, exc := cfg.EffectivePatterns(cfg.Sources[0])
	if len(exc) != 2 {
		t.Fatalf("expected 2 exclude patterns (default + add), got %d: %v", len(exc), exc)
	}
}

// R2143, R2144: per-source include replaces default_include
func TestPerSourceIncludeReplacesDefault(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ark.toml")
	content := `dotfiles = true
default_include = ["*.md", "*.go"]
default_exclude = []

[[source]]
dir = '` + dir + `'

include = ["*.txt"]
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	inc, _ := cfg.EffectivePatterns(cfg.Sources[0])
	if len(inc) != 1 || inc[0] != "*.txt" {
		t.Errorf("expected per-source include to replace default; got %v", inc)
	}
}

// R2144: when source omits include, default_include applies
func TestPerSourceOmittedInheritsDefault(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ark.toml")
	content := `dotfiles = true
default_include = ["*.md", "*.go"]

[[source]]
dir = '` + dir + `'
exclude = ["drafts/"]
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	inc, exc := cfg.EffectivePatterns(cfg.Sources[0])
	if len(inc) != 2 {
		t.Errorf("expected default_include to apply; got %v", inc)
	}
	if len(exc) != 1 || exc[0] != "drafts/" {
		t.Errorf("expected per-source exclude to replace default_exclude; got %v", exc)
	}
}

func TestIdenticalIncludeExcludeIsError(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ark.toml")
	content := `dotfiles = true
default_include = ["*.md"]
default_exclude = ["*.md"]
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HasErrors() {
		t.Error("should have validation error for identical include/exclude")
	}
}

func TestWriteDefaultConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ark.toml")
	if err := WriteDefaultConfig(configPath, nil); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Dotfiles {
		t.Error("default config should have dotfiles=true")
	}
	excSet := map[string]bool{}
	for _, e := range cfg.DefaultExclude {
		excSet[e] = true
	}
	if !excSet[".git/"] {
		t.Error("default excludes should contain .git/")
	}
	if !excSet[".env"] {
		t.Error("default excludes should contain .env")
	}
}

// Test: test-Config.md — add-include per-source round-trip
func TestAddIncludePerSourceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ark.toml")
	content := `dotfiles = true
default_include = ["*.md"]
default_exclude = [".git/"]

[[source]]
dir = '` + dir + `'

`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := cfg.AddInclude("*.org", dir); err != nil {
		t.Fatalf("AddInclude: %v", err)
	}
	if err := cfg.SaveConfig(configPath); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	cfg2, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(cfg2.Sources) == 0 {
		t.Fatal("no sources after round-trip")
	}
	found := false
	for _, p := range cfg2.Sources[0].Include.Replace {
		if p == "*.org" {
			found = true
		}
	}
	if !found {
		t.Errorf("per-source include missing *.org after round-trip, got %v", cfg2.Sources[0].Include.Replace)
	}
}

// Test: test-Config.md — default add-include round-trip
func TestAddIncludeDefaultRoundTrip(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ark.toml")
	content := `dotfiles = true
default_include = ["*.md"]
default_exclude = [".git/"]
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddInclude("*.txt", ""); err != nil {
		t.Fatalf("AddInclude: %v", err)
	}
	if err := cfg.SaveConfig(configPath); err != nil {
		t.Fatal(err)
	}

	cfg2, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range cfg2.DefaultInclude {
		if p == "*.txt" {
			found = true
		}
	}
	if !found {
		t.Errorf("global include missing *.txt after round-trip, got %v", cfg2.DefaultInclude)
	}
}

func TestMissingSourceDirNotError(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "ark.toml")
	content := `dotfiles = true
default_include = []
default_exclude = []

[[source]]
dir = "/nonexistent/path/that/will/never/exist"

`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HasErrors() {
		t.Errorf("should load without errors, got %v", cfg.Errors)
	}
}

// Test: test-Config.md — ** in [[source]].dir is rejected.
//
// A recursive source glob makes every subdirectory its own source root, so a
// single file change fires one watcher event per ancestor level — and the
// fault is latent, appearing only once a nested directory exists, which is
// exactly what validating the expansion at load time cannot catch. So the
// pattern itself is refused. R3200, R3201
func TestSourceDirRejectsDoubleStar(t *testing.T) {
	err := ValidateSourceDir("~/work/**")
	if err == nil {
		t.Fatal("** in a source dir must be rejected")
	}
	if !strings.Contains(err.Error(), "*") {
		t.Errorf("the error should name * as the single-level alternative, got %q", err)
	}
	if err := ValidateSourceDir("~/work/*"); err != nil {
		t.Errorf("a single-level source glob stays legal, got %v", err)
	}
	if err := ValidateSourceDir("/home/me/proj"); err != nil {
		t.Errorf("a concrete dir stays legal, got %v", err)
	}

	c := &Config{Sources: []Source{{Dir: "/tmp/a/**"}}}
	c.validate()
	if len(c.Errors) == 0 {
		t.Error("a recursive source glob in ark.toml should be reported by validate()")
	}
	if err := c.AddSource("/tmp/b/**"); err == nil {
		t.Error("AddSource must refuse a recursive glob outright")
	}
}

// Test: test-Config.md — strategies honor ** under the source-scoped context.
//
// StrategyForFile used filepath.Match on the source-relative path with a
// basename fallback, so `*.md` worked at any depth (via the fallback) but
// `**/*.md` matched one level only — ** had no effect at all. Matching through
// Matcher makes ** work; the no-slash case is unchanged, so existing configs
// keep resolving as they did. R3202, R3198
func TestStrategyForFileHonorsDoubleStar(t *testing.T) {
	c := &Config{Strategies: map[string]string{"**/*.md": "markdown"}}
	for _, p := range []string{"a.md", "docs/deep/b.md"} {
		if got := c.StrategyForFile(p, nil); got != "markdown" {
			t.Errorf("StrategyForFile(%q) = %q, want markdown", p, got)
		}
	}

	// The additive half: a no-slash pattern still reaches any depth, which is
	// what the retired basename fallback provided.
	c2 := &Config{Strategies: map[string]string{"*.md": "markdown"}}
	for _, p := range []string{"a.md", "docs/deep/b.md"} {
		if got := c2.StrategyForFile(p, nil); got != "markdown" {
			t.Errorf("StrategyForFile(%q) with bare *.md = %q, want markdown", p, got)
		}
	}
	if got := c2.StrategyForFile("a.go", nil); got != "lines" {
		t.Errorf("unmatched file should fall back to lines, got %q", got)
	}
}

// Test: test-Config.md — validatePattern accepts what the matcher accepts.
// Validator and matcher agree on legality: doublestar syntax, not
// filepath.Match syntax. R3203
func TestValidatePatternUsesDoublestar(t *testing.T) {
	for _, p := range []string{"**/*.{md,txt}", "docs/**/*.txt", "*.md"} {
		if err := validatePattern(p); err != nil {
			t.Errorf("validatePattern(%q) = %v, want nil", p, err)
		}
	}
	if err := validatePattern("["); err == nil {
		t.Error("a malformed pattern should be rejected")
	}
	if err := validatePattern(""); err == nil {
		t.Error("an empty pattern should be rejected")
	}
}

// Test: test-Config.md — rootless keys reach any depth.
// search_exclude and the [schedule] file keys have no contextual root, so a
// bare pattern means **/X and a slash-bearing relative pattern matches rather
// than silently matching nothing. R3199, R3195
func TestRootlessConfigKeysReachAnyDepth(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
	}{
		{"*.jsonl", "/home/me/.claude/projects/x/a.jsonl"},
		{"specs/**", "/home/me/proj/specs/x.md"},
		{"**/*.md", "/home/me/proj/deep/x.md"},
	}
	for _, c := range cases {
		if matchesFilterExclude(c.path, nil, []string{c.pattern}) {
			t.Errorf("rootless exclude %q should reject %q", c.pattern, c.path)
		}
		if !matchesFilterExclude(c.path, []string{c.pattern}, nil) {
			t.Errorf("rootless filter %q should admit %q", c.pattern, c.path)
		}
	}
}

// The `~/.ark` allowlist exists in three places that must agree: the
// arkSourceIncludePatterns constant, the commented [[source]] block in
// install/ark.toml, and the inventory in specs/source-monitoring.md. The spec
// states that obligation; nothing enforced it, and R961 drifted for months as a
// result — it still described four bare globs when the constant held fourteen
// per-extension ones. This is the sentry that stomps past the gate: it fails the
// moment someone adds an entry to one copy and not the other.
// CRC: crc-Config.md | R961, R962, R3278
func TestArkSourceIncludeMatchesInstallTemplate(t *testing.T) {
	want := map[string]bool{}
	for _, p := range strings.Fields(arkSourceIncludePatterns) {
		want[p] = true
	}
	if len(want) == 0 {
		t.Fatal("arkSourceIncludePatterns parsed empty")
	}

	data, err := os.ReadFile("install/ark.toml")
	if err != nil {
		t.Fatal(err)
	}
	// Pull the quoted patterns out of the commented `# dir = "~/.ark"` block,
	// which runs until the closing `# ]`.
	got := map[string]bool{}
	inBlock := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		switch {
		case strings.HasPrefix(trimmed, "include = ["):
			inBlock = true
			continue
		case inBlock && strings.HasPrefix(trimmed, "]"):
			inBlock = false
		}
		if !inBlock {
			continue
		}
		for _, field := range strings.Split(trimmed, ",") {
			if p := strings.Trim(strings.TrimSpace(field), `"`); p != "" {
				got[p] = true
			}
		}
	}
	if len(got) == 0 {
		t.Fatal("found no include patterns in install/ark.toml — the commented block moved or changed shape")
	}

	for p := range want {
		if !got[p] {
			t.Errorf("install/ark.toml is missing %q (present in arkSourceIncludePatterns)", p)
		}
	}
	for p := range got {
		if !want[p] {
			t.Errorf("install/ark.toml has %q, absent from arkSourceIncludePatterns", p)
		}
	}
}

// R3278: the entry the personal-patterns feature depends on. Called out
// separately because its absence is silent — `ark patterns list` reads the
// directory, so priming keeps working while search and recall find nothing.
// CRC: crc-Config.md | R3278
func TestArkSourceIncludesPersonalPatterns(t *testing.T) {
	if !strings.Contains(arkSourceIncludePatterns, "personal-patterns/**/*.md") {
		t.Error("the default pattern collection would not be indexed; retrieval dies silently while priming still works")
	}
}

// TestLuhmannEffectiveChildArgv covers the three-level model chain and the
// wholesale command override — the whole resolution rule for what ark forks
// into the pty (R3329, R3330, R3331). Pure and collaborator-free: a zero-value
// config, no DB, no server, no pty.
func TestLuhmannEffectiveChildArgv(t *testing.T) {
	withModel := func(s string) LuhmannConfig { return LuhmannConfig{Model: s} }

	t.Run("bare default", func(t *testing.T) {
		// Neither parameter nor config: the compiled-in default (R3329).
		got, err := LuhmannConfig{}.EffectiveChildArgv("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"claude", "--model", "opus"}
		if !equalStrings(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("config supplies the model", func(t *testing.T) {
		got, err := withModel("sonnet").EffectiveChildArgv("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := []string{"claude", "--model", "sonnet"}; !equalStrings(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("parameter beats config", func(t *testing.T) {
		got, err := withModel("sonnet").EffectiveChildArgv("haiku")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := []string{"claude", "--model", "haiku"}; !equalStrings(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("parameter does not write config", func(t *testing.T) {
		// The point of a parameter: a one-off launch leaves the standing
		// default alone, which is what makes a UI chooser safe (R3329).
		lc := withModel("sonnet")
		if _, err := lc.EffectiveChildArgv("haiku"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if lc.Model != "sonnet" {
			t.Errorf("config model became %q; a parameter must not write config", lc.Model)
		}
	})

	t.Run("command override wins and bypasses the model chain", func(t *testing.T) {
		lc := LuhmannConfig{Model: "sonnet", Command: []string{"node", "spinner.js"}}
		got, err := lc.EffectiveChildArgv("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := []string{"node", "spinner.js"}; !equalStrings(got, want) {
			t.Errorf("got %v, want %v — an override supplies its own argv (R3330)", got, want)
		}
	})

	t.Run("override result is a copy", func(t *testing.T) {
		// A caller must not be able to reach back into loaded config.
		lc := LuhmannConfig{Command: []string{"node", "spinner.js"}}
		got, err := lc.EffectiveChildArgv("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got[0] = "clobbered"
		if lc.Command[0] != "node" {
			t.Errorf("mutating the result changed config to %v", lc.Command)
		}
	})

	t.Run("explicit model against an override is an error", func(t *testing.T) {
		// Neither can be honored, so it fails loudly instead of silently
		// discarding one (R3331).
		lc := LuhmannConfig{Command: []string{"node", "spinner.js"}}
		got, err := lc.EffectiveChildArgv("haiku")
		if err == nil {
			t.Fatalf("got %v, want an error: a model and a command override cannot both apply", got)
		}
		if !strings.Contains(err.Error(), "haiku") {
			t.Errorf("error %q should name the rejected model so the user can see what was dropped", err)
		}
	})

	t.Run("empty command slice is not an override", func(t *testing.T) {
		// command = [] in ark.toml must fall through, not produce an empty argv.
		got, err := LuhmannConfig{Command: []string{}}.EffectiveChildArgv("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := []string{"claude", "--model", "opus"}; !equalStrings(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

// Test: test-LinkTable.md — a symlinked source root is refused, not walked.
//
// R3354. The failure this prevents does not present as a zero to disbelieve:
// filepath.WalkDir lstats its root, so a symlinked one yields the link node as
// a single *file* entry and none of the contents. The scan reports one file
// having indexed nothing, and nothing about it looks anomalous.
func TestValidateSourceDirRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "notes")
	if err := os.Mkdir(real, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link-to-notes")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	if err := ValidateSourceDir(link); err == nil {
		t.Error("a symlinked source dir was accepted; ark records links, it does not follow them")
	} else if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("rejection does not say why: %v", err)
	}
	if err := ValidateSourceDir(real); err != nil {
		t.Errorf("a real directory was refused: %v", err)
	}
	// A glob names no file to lstat; its expansions are checked instead.
	if err := ValidateSourceDir(filepath.Join(dir, "*")); err != nil {
		t.Errorf("a glob pattern was refused: %v", err)
	}
	// A missing dir is the sources check's business (R198), not this one's.
	if err := ValidateSourceDir(filepath.Join(dir, "nonexistent")); err != nil {
		t.Errorf("a missing dir was refused here: %v", err)
	}
}

// Test: test-LinkTable.md — the glob door refuses one too.
//
// R3354. os.Stat was the wrong call and looked right: it follows the link, so
// a symlinked match passes IsDir() and gets auto-added as a concrete source,
// arriving at the same broken state through the other door.
func TestResolveGlobsSkipsSymlinkedMatch(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(dir, "alpha"), filepath.Join(dir, "gamma")); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{Sources: []Source{{Dir: filepath.Join(dir, "*")}}}
	if _, err := cfg.ResolveGlobs(); err != nil {
		t.Fatal(err)
	}

	var added []string
	for _, src := range cfg.Sources {
		if !IsGlob(src.Dir) {
			added = append(added, filepath.Base(src.Dir))
		}
	}
	sort.Strings(added)
	want := []string{"alpha", "beta"}
	if !reflect.DeepEqual(added, want) {
		t.Errorf("expanded sources = %v, want %v — the symlinked match must not become a source", added, want)
	}
	// Guard the guard: an expansion that found nothing would pass vacuously.
	if len(added) == 0 {
		t.Fatal("the glob matched no real directories, so this proves nothing")
	}
}

// loadTOML writes body as <dir>/ark.toml and loads it, so dbPath is dir.
func loadTOML(t *testing.T, dir, body string) *Config {
	t.Helper()
	p := filepath.Join(dir, "ark.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestExcludeDefaults: an absent key means the built-in default, for both
// settings, and the fence names the scheduler's own directory.
// Refs: R3581, R3582, R3583
func TestExcludeDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg := loadTOML(t, dir, "")
	if got, want := cfg.SearchExcludes(), []string{ExpandTilde(defaultSearchExclude)}; !reflect.DeepEqual(got, want) {
		t.Errorf("SearchExcludes() = %v, want the chat-log default %v", got, want)
	}
	if got, want := cfg.SystemExcludes(), []string{scheduleDirFor(dir) + "/**"}; !reflect.DeepEqual(got, want) {
		t.Errorf("SystemExcludes() = %v, want the scheduler's dir %v", got, want)
	}
}

// TestExcludeListsReplaceDefaults: a set list replaces the default and an
// explicit [] turns the setting off. Refs: R3583
func TestExcludeListsReplaceDefaults(t *testing.T) {
	cfg := loadTOML(t, t.TempDir(), `search_exclude = ["~/notes/**"]`)
	if got, want := cfg.SearchExcludes(), []string{ExpandTilde("~/notes/**")}; !reflect.DeepEqual(got, want) {
		t.Errorf("set list: got %v, want only %v (no default added)", got, want)
	}
	cfg = loadTOML(t, t.TempDir(), "search_exclude = []\nsystem_exclude = []\n")
	if got := cfg.SearchExcludes(); len(got) != 0 {
		t.Errorf("search_exclude = []: got %v, want none", got)
	}
	if got := cfg.SystemExcludes(); len(got) != 0 {
		t.Errorf("system_exclude = []: got %v, want none", got)
	}
}

// TestExplicitEmptyExcludeSurvivesSave: a config edit must not turn an
// explicitly disabled setting back on. Refs: R3583
func TestExplicitEmptyExcludeSurvivesSave(t *testing.T) {
	dir := t.TempDir()
	cfg := loadTOML(t, dir, "search_exclude = []\n")
	p := filepath.Join(dir, "ark.toml")
	if err := cfg.SaveConfig(p); err != nil {
		t.Fatal(err)
	}
	saved, _ := os.ReadFile(p)
	if !strings.Contains(string(saved), "search_exclude = []") {
		t.Errorf("saved ark.toml lost the explicit empty list:\n%s", saved)
	}
	if strings.Contains(string(saved), "system_exclude") {
		t.Errorf("saved ark.toml wrote an absent key's default:\n%s", saved)
	}
	again, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.SearchExcludes(); len(got) != 0 {
		t.Errorf("reloaded: got %v, want none", got)
	}
}
