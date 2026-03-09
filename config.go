package ark

// CRC: crc-Config.md | Seq: seq-config-mutate.md

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config represents the parsed ark.toml configuration.
type Config struct {
	Dotfiles        bool              `toml:"dotfiles"`
	CaseInsensitive bool              `toml:"case_insensitive,omitempty"`
	EmbedCmd        string            `toml:"embed_cmd,omitempty"`
	QueryCmd        string            `toml:"query_cmd,omitempty"`
	GlobalInclude   []string          `toml:"include"`
	GlobalExclude   []string          `toml:"exclude"`
	Strategies      map[string]string `toml:"strategies,omitempty"`
	Sources         []Source          `toml:"source"`
	Errors          []string          `toml:"-"`
	dbPath          string            `toml:"-"`
}

// Source is a directory entry in the configuration.
type Source struct {
	Dir        string            `toml:"dir"`
	Strategies map[string]string `toml:"strategies,omitempty"`
	Include    []string          `toml:"include"`
	Exclude    []string          `toml:"exclude"`
	FromGlob   string            `toml:"from_glob,omitempty"`
}

// LoadConfig reads and validates an ark.toml file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.validate()
	// Expand ~ in source directory paths
	for i := range cfg.Sources {
		cfg.Sources[i].Dir = expandHome(cfg.Sources[i].Dir)
	}
	return &cfg, nil
}

// CRC: crc-DB.md | R383
// WriteDefaultConfig writes an initial ark.toml with default excludes.
func WriteDefaultConfig(path string) error {
	const defaultConfig = `# Ark configuration
dotfiles = true
case_insensitive = true

# Global patterns — apply to all sources
include = []
exclude = [".git/", ".env", "node_modules/", "__pycache__/", ".DS_Store"]

# Strategy mapping — glob pattern to chunking strategy
[strategies]
"*.md" = "markdown"
"*.jsonl" = "chat-jsonl"

# Sources — directories to watch
# [[source]]
# dir = "~/notes"
# strategies = {"*.org" = "lines"}  # optional, amends global strategies
`
	return os.WriteFile(path, []byte(defaultConfig), 0644)
}

// EnsureArkSource adds the database directory as an in-memory source
// if not already present. This source is hardcoded — it does not appear
// in ark.toml and cannot be removed.
func (c *Config) EnsureArkSource() {
	if c.dbPath == "" {
		return
	}
	for _, src := range c.Sources {
		if src.Dir == c.dbPath {
			return
		}
	}
	c.Sources = append(c.Sources, Source{Dir: c.dbPath})
}

// EffectivePatterns returns the combined global + per-source patterns.
func (c *Config) EffectivePatterns(src Source) (includes, excludes []string) {
	includes = make([]string, 0, len(c.GlobalInclude)+len(src.Include))
	includes = append(includes, c.GlobalInclude...)
	includes = append(includes, src.Include...)

	excludes = make([]string, 0, len(c.GlobalExclude)+len(src.Exclude))
	excludes = append(excludes, c.GlobalExclude...)
	excludes = append(excludes, src.Exclude...)
	return
}

// HasErrors returns true if the config has validation errors.
func (c *Config) HasErrors() bool {
	return len(c.Errors) > 0
}

// validate checks for identical include/exclude strings.
func (c *Config) validate() {
	c.Errors = nil
	// Check global patterns
	c.checkDuplicates(c.GlobalInclude, c.GlobalExclude, "global")
	// Check per-source patterns against their effective set
	for _, src := range c.Sources {
		inc, exc := c.EffectivePatterns(src)
		c.checkDuplicates(inc, exc, src.Dir)
	}
}

func (c *Config) checkDuplicates(includes, excludes []string, context string) {
	excSet := make(map[string]bool, len(excludes))
	for _, e := range excludes {
		excSet[e] = true
	}
	for _, inc := range includes {
		if excSet[inc] {
			c.Errors = append(c.Errors, fmt.Sprintf(
				"pattern %q appears in both include and exclude (%s)", inc, context))
		}
	}
}

func expandHome(path string) string {
	if len(path) >= 2 && path[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}

// SaveConfig writes the current config state to an ark.toml file.
func (c *Config) SaveConfig(path string) error {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(c); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return os.WriteFile(path, buf.Bytes(), 0644)
}

// IsGlob returns true if dir contains glob characters (*, ?, [).
func IsGlob(dir string) bool {
	return strings.ContainsAny(dir, "*?[")
}

// AddSource adds a new source directory. Glob patterns (containing *, ?, [)
// are stored as-is without validation. Concrete paths are validated to exist.
func (c *Config) AddSource(dir string) error {
	dir = expandHome(dir)
	for _, src := range c.Sources {
		if src.Dir == dir {
			return fmt.Errorf("source %q already configured", dir)
		}
	}
	if !IsGlob(dir) {
		info, err := os.Stat(dir)
		if err != nil {
			return fmt.Errorf("directory %q: %w", dir, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("%q is not a directory", dir)
		}
	}
	c.Sources = append(c.Sources, Source{Dir: dir})
	c.validate()
	return nil
}

// RemoveSource removes a source directory by path. Returns an error
// if the source is a concrete dir managed by a glob pattern or if
// the directory is the ark database directory (hardcoded source).
func (c *Config) RemoveSource(dir string) error {
	dir = expandHome(dir)
	if c.dbPath != "" && dir == c.dbPath {
		return fmt.Errorf("cannot remove %s — hardcoded source", dir)
	}
	// Check if this concrete source is managed by a glob (via from_glob field)
	if !IsGlob(dir) {
		for _, src := range c.Sources {
			if src.Dir == dir && src.FromGlob != "" {
				return fmt.Errorf("source %q is managed by glob %q — change the glob instead", dir, src.FromGlob)
			}
		}
	}
	for i, src := range c.Sources {
		if src.Dir == dir {
			c.Sources = append(c.Sources[:i], c.Sources[i+1:]...)
			c.validate()
			return nil
		}
	}
	return fmt.Errorf("source %q not found", dir)
}

// SourcesCheckResult holds the result of glob source reconciliation.
type SourcesCheckResult struct {
	Added    []string `json:"added,omitempty"`
	MIA      []string `json:"mia,omitempty"`
	Orphaned []string `json:"orphaned,omitempty"`
}

// ResolveGlobs expands all glob source patterns, diffs against concrete sources,
// and returns what needs to be added, what's missing, and what's orphaned.
func (c *Config) ResolveGlobs() (*SourcesCheckResult, error) {
	result := &SourcesCheckResult{}

	// Collect existing concrete sources and which glob owns them
	concreteSet := make(map[string]bool)
	globOwned := make(map[string]string) // concrete dir → glob pattern

	for _, src := range c.Sources {
		if !IsGlob(src.Dir) {
			concreteSet[src.Dir] = true
		}
	}

	// Expand each glob and reconcile
	for _, src := range c.Sources {
		if !IsGlob(src.Dir) {
			continue
		}
		matches, err := filepath.Glob(src.Dir)
		if err != nil {
			return nil, fmt.Errorf("glob %q: %w", src.Dir, err)
		}
		for _, m := range matches {
			info, err := os.Stat(m)
			if err != nil || !info.IsDir() {
				continue
			}
			globOwned[m] = src.Dir
			if !concreteSet[m] {
				// New directory — add as concrete source
				c.Sources = append(c.Sources, Source{
					Dir:        m,
					Strategies: src.Strategies,
					Include:    src.Include,
					Exclude:    src.Exclude,
					FromGlob:   src.Dir,
				})
				concreteSet[m] = true
				result.Added = append(result.Added, m)
			}
		}
	}

	// Check for MIA and orphans
	globDirs := make(map[string]bool)
	for _, src := range c.Sources {
		if IsGlob(src.Dir) {
			globDirs[src.Dir] = true
		}
	}
	for _, src := range c.Sources {
		if IsGlob(src.Dir) {
			continue
		}
		if _, err := os.Stat(src.Dir); os.IsNotExist(err) {
			result.MIA = append(result.MIA, src.Dir)
		}
		// Orphan: has a from_glob but that glob is no longer in config
		if src.FromGlob != "" && !globDirs[src.FromGlob] {
			result.Orphaned = append(result.Orphaned, src.Dir)
		}
	}

	c.validate()
	return result, nil
}

// StrategyForFile merges per-source strategies over global strategies,
// then finds the longest matching pattern. Returns the matched strategy
// name, or "lines" if no pattern matches.
func (c *Config) StrategyForFile(relPath string, sourceStrategies map[string]string) string {
	if len(c.Strategies) == 0 && len(sourceStrategies) == 0 {
		return "lines"
	}
	// Merge: start with global, overlay per-source (same key = per-source wins)
	merged := make(map[string]string, len(c.Strategies)+len(sourceStrategies))
	for k, v := range c.Strategies {
		merged[k] = v
	}
	for k, v := range sourceStrategies {
		merged[k] = v
	}
	bestStrategy := ""
	bestLen := 0
	base := filepath.Base(relPath)
	for pattern, strategy := range merged {
		matched, err := filepath.Match(pattern, relPath)
		if err != nil {
			continue
		}
		// Also try matching just the filename for simple patterns like "*.md"
		if !matched {
			matched, err = filepath.Match(pattern, base)
			if err != nil {
				continue
			}
		}
		if matched && len(pattern) > bestLen {
			bestStrategy = strategy
			bestLen = len(pattern)
		}
	}
	if bestStrategy != "" {
		return bestStrategy
	}
	return "lines"
}

// AddStrategy adds a global strategy mapping (e.g. "*.md" -> "markdown").
func (c *Config) AddStrategy(pattern, strategy string) error {
	if err := validatePattern(pattern); err != nil {
		return err
	}
	if strategy == "" {
		return fmt.Errorf("strategy name required")
	}
	if c.Strategies == nil {
		c.Strategies = make(map[string]string)
	}
	c.Strategies[pattern] = strategy
	return nil
}

// AddInclude adds an include pattern. If sourceDir is empty, adds to
// global patterns; otherwise adds to the specified source's patterns.
func (c *Config) AddInclude(pattern, sourceDir string) error {
	if err := validatePattern(pattern); err != nil {
		return err
	}
	if sourceDir == "" {
		c.GlobalInclude = append(c.GlobalInclude, pattern)
	} else {
		src, err := c.findSource(sourceDir)
		if err != nil {
			return err
		}
		src.Include = append(src.Include, pattern)
	}
	c.validate()
	return nil
}

// AddExclude adds an exclude pattern. If sourceDir is empty, adds to
// global patterns; otherwise adds to the specified source's patterns.
func (c *Config) AddExclude(pattern, sourceDir string) error {
	if err := validatePattern(pattern); err != nil {
		return err
	}
	if sourceDir == "" {
		c.GlobalExclude = append(c.GlobalExclude, pattern)
	} else {
		src, err := c.findSource(sourceDir)
		if err != nil {
			return err
		}
		src.Exclude = append(src.Exclude, pattern)
	}
	c.validate()
	return nil
}

// RemovePattern removes a pattern from include or exclude lists. If
// sourceDir is empty, removes from global; otherwise from the specified
// source. Returns an error if the pattern wasn't found.
func (c *Config) RemovePattern(pattern, sourceDir string) error {
	if sourceDir == "" {
		if removeFromSlice(&c.GlobalInclude, pattern) || removeFromSlice(&c.GlobalExclude, pattern) {
			c.validate()
			return nil
		}
		return fmt.Errorf("pattern %q not found in global patterns", pattern)
	}
	src, err := c.findSource(sourceDir)
	if err != nil {
		return err
	}
	if removeFromSlice(&src.Include, pattern) || removeFromSlice(&src.Exclude, pattern) {
		c.validate()
		return nil
	}
	return fmt.Errorf("pattern %q not found in source %q", pattern, sourceDir)
}

// WhyResult explains why a file has its current classification.
type WhyResult struct {
	Path     string   `json:"path"`
	Status   string   `json:"status"` // "included", "excluded", "unresolved"
	Patterns []string `json:"patterns,omitempty"`
	Sources  []string `json:"sources,omitempty"`
	Conflict bool     `json:"conflict,omitempty"` // include-wins-conflicts applied
}

// ShowWhy explains why a file is included, excluded, or unresolved.
// It checks config patterns and ignore files (.gitignore, .arkignore).
func (c *Config) ShowWhy(filePath string) (*WhyResult, error) {
	filePath = expandHome(filePath)
	m := &Matcher{Dotfiles: c.Dotfiles}

	info, statErr := os.Stat(filePath)
	isDir := statErr == nil && info.IsDir()

	result := &WhyResult{Path: filePath}

	// Find which source this file belongs to and get relative path
	var matchedSource *Source
	var relPath string
	for i := range c.Sources {
		rel, err := filepath.Rel(c.Sources[i].Dir, filePath)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		matchedSource = &c.Sources[i]
		relPath = rel
		break
	}

	if matchedSource == nil {
		result.Status = "unresolved"
		result.Sources = append(result.Sources, "file is not under any configured source directory")
		return result, nil
	}

	filePath = relPath // Use relative path for pattern matching from here on
	// Check ignore files
	ignoreExcludes, ignoreSources := c.loadIgnoreFiles(matchedSource.Dir, filePath)

	// Find all matching patterns with their sources
	var matchingIncludes, matchingExcludes []string
	var includeSources, excludeSources []string

	collect := func(patterns []string, label string, pats *[]string, srcs *[]string) {
		for _, p := range patterns {
			if m.Match(p, filePath, isDir) {
				*pats = append(*pats, p)
				*srcs = append(*srcs, label)
			}
		}
	}
	collect(c.GlobalInclude, "global include", &matchingIncludes, &includeSources)
	collect(matchedSource.Include, fmt.Sprintf("source %s include", matchedSource.Dir), &matchingIncludes, &includeSources)
	collect(c.GlobalExclude, "global exclude", &matchingExcludes, &excludeSources)
	collect(matchedSource.Exclude, fmt.Sprintf("source %s exclude", matchedSource.Dir), &matchingExcludes, &excludeSources)
	// Ignore file patterns have per-pattern source labels
	for i, p := range ignoreExcludes {
		if m.Match(p, filePath, isDir) {
			matchingExcludes = append(matchingExcludes, p)
			excludeSources = append(excludeSources, ignoreSources[i])
		}
	}

	// Determine result
	if len(matchingIncludes) > 0 {
		result.Status = "included"
		result.Patterns = matchingIncludes
		result.Sources = includeSources
		if len(matchingExcludes) > 0 {
			result.Conflict = true
			// Also report the excluded patterns that were overridden
			result.Patterns = append(result.Patterns, matchingExcludes...)
			result.Sources = append(result.Sources, excludeSources...)
		}
	} else if len(matchingExcludes) > 0 {
		result.Status = "excluded"
		result.Patterns = matchingExcludes
		result.Sources = excludeSources
	} else {
		result.Status = "unresolved"
		result.Sources = append(result.Sources, "no matching pattern")
	}

	return result, nil
}

// loadIgnoreFiles reads .gitignore and .arkignore from the source
// directory and returns patterns and their source labels.
func (c *Config) loadIgnoreFiles(sourceDir, _ string) (patterns, sources []string) {
	for _, name := range []string{".gitignore", ".arkignore"} {
		// Check in source dir and in parent dirs of the file
		ignorePath := filepath.Join(sourceDir, name)
		pats, err := parseIgnoreFile(ignorePath)
		if err != nil {
			continue
		}
		for _, p := range pats {
			patterns = append(patterns, p)
			sources = append(sources, name)
		}
	}
	return
}

// parseIgnoreFile reads a .gitignore-style file and returns patterns.
func parseIgnoreFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var patterns []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Negation patterns (!) are not supported — skip them
		if strings.HasPrefix(line, "!") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns, scanner.Err()
}

func (c *Config) findSource(dir string) (*Source, error) {
	dir = expandHome(dir)
	for i := range c.Sources {
		if c.Sources[i].Dir == dir {
			return &c.Sources[i], nil
		}
	}
	return nil, fmt.Errorf("source %q not found", dir)
}

func removeFromSlice(s *[]string, val string) bool {
	for i, v := range *s {
		if v == val {
			*s = append((*s)[:i], (*s)[i+1:]...)
			return true
		}
	}
	return false
}

// validatePattern checks that a pattern is syntactically valid.
func validatePattern(pattern string) error {
	if pattern == "" {
		return fmt.Errorf("empty pattern")
	}
	// Try to parse with filepath.Match to catch syntax errors
	_, err := filepath.Match(pattern, "test")
	if err != nil {
		return fmt.Errorf("invalid pattern %q: %w", pattern, err)
	}
	return nil
}
