package ark

// CRC: crc-Config.md | Seq: seq-config-mutate.md

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// arkSourceIncludePatterns is the include list for the hardcoded ~/.ark
// source. Whitespace-separated so new patterns can be added one per line.
// CRC: crc-Config.md | R961, R962, R2393
const arkSourceIncludePatterns = `
	ark.toml
	schedule/**
	apps/**
	storage/**
	external/**
`

// Config represents the parsed ark.toml configuration.
// CRC: crc-Config.md | R624, R625
type Config struct {
	Dotfiles        bool              `toml:"dotfiles"`
	CaseInsensitive bool              `toml:"case_insensitive,omitempty"`
	EmbedCmd        string            `toml:"embed_cmd,omitempty"`
	QueryCmd        string            `toml:"query_cmd,omitempty"`
	DefaultInclude  []string          `toml:"default_include"`
	DefaultExclude  []string          `toml:"default_exclude"`
	Strategies      map[string]string `toml:"strategies,omitempty"`
	Sources         []Source          `toml:"source"`
	Chunkers        []ChunkerConfig   `toml:"chunker"`
	SessionTTL      string            `toml:"session_ttl,omitempty"`    // R646: duration string, default "30s"
	SearchExclude   []string          `toml:"search_exclude,omitempty"` // R938: default exclude patterns for search
	TagModel        string            `toml:"tag_model,omitempty"`      // R1274: GGUF embedding model filename
	EmbedTiers      []EmbedTier       `toml:"embed_tiers,omitempty"`    // R1588: ctx/parallel per tier
	// CRC: crc-Config.md | R1919, R1920, R1938
	AboutCentroidFilter    bool    `toml:"about_centroid_filter,omitempty"`
	AboutCentroidThreshold float64 `toml:"about_centroid_threshold,omitempty"`
	AboutFilterTopK        int     `toml:"about_filter_top_k,omitempty"`
	PdfPreviewZoom         float64 `toml:"pdf_preview_zoom,omitempty"`
	// CRC: crc-Config.md | R2125
	AutoCompact bool           `toml:"auto_compact,omitempty"`
	Schedule    ScheduleConfig `toml:"schedule"` // R853, R854
	Recall      RecallConfig   `toml:"recall"`   // R2659
	Errors      []string       `toml:"-"`
	dbPath      string         `toml:"-"`
}

// RecallConfig collects the [recall] section of ark.toml.
// CRC: crc-Config.md | R2659
type RecallConfig struct {
	// DiscussedTTL is the lifetime of an RD record before lazy
	// expiry takes effect. Empty/missing falls back to 24h;
	// `"0"` disables expiry (records never expire on read).
	// An unparseable value falls back to 24h with a warning at
	// server startup. R2659, R2663
	DiscussedTTL string `toml:"discussed_ttl,omitempty"`
}

// DiscussedTTLDuration parses the [recall].discussed_ttl field and
// returns the effective TTL. Empty/unparseable → 24h. `"0"` returns
// 0, signaling "never expire" to Store.ListDiscussed / PruneDiscussed.
// `wasParseError` is true when the value was set but couldn't be
// parsed — callers can log a warning at startup.
// CRC: crc-Config.md | R2659, R2663
func (rc RecallConfig) DiscussedTTLDuration() (ttl time.Duration, wasParseError bool) {
	const fallback = 24 * time.Hour
	if rc.DiscussedTTL == "" {
		return fallback, false
	}
	d, err := time.ParseDuration(rc.DiscussedTTL)
	if err != nil {
		return fallback, true
	}
	return d, false
}

// ScheduleConfig declares which tags carry date values and their defaults.
// CRC: crc-Config.md | R853, R854, R855, R953-R960
type ScheduleConfig struct {
	Tags             []string                     `toml:"tags"`
	Defaults         map[string]string            `toml:"defaults"`
	FilterFiles      []string                     `toml:"filter_files,omitempty"`      // R953: restrict schedule scanning to matching files
	ExcludeFiles     []string                     `toml:"exclude_files,omitempty"`     // R954: exclude files from schedule scanning
	LifecycleInclude []string                     `toml:"lifecycle_include,omitempty"` // R957: tags that get full lifecycle (default "*")
	LifecycleExclude []string                     `toml:"lifecycle_exclude,omitempty"` // R958: tags excluded from lifecycle
	TagConfig        map[string]TagScheduleConfig `toml:"tag"`                         // per-tag filter overrides
}

// TagScheduleConfig holds per-tag schedule filtering overrides.
type TagScheduleConfig struct {
	FilterFiles  []string `toml:"filter_files,omitempty"`
	ExcludeFiles []string `toml:"exclude_files,omitempty"`
}

// EmbedTier defines a context/parallel pair for chunk embedding.
// CRC: crc-Config.md | R1588, R1589
type EmbedTier struct {
	Ctx      int `toml:"ctx"`
	Parallel int `toml:"parallel"`
}

// ByteLimit returns the max chunk byte size this tier can handle.
// ~3 bytes/token for BERT WordPiece. R1589
func (t EmbedTier) ByteLimit() int {
	return (t.Ctx / t.Parallel) * 3
}

// DefaultEmbedTiers returns the default tiers tuned for Steam Deck Vulkan GPU. R1591
func DefaultEmbedTiers() []EmbedTier {
	return []EmbedTier{
		{Ctx: 1024, Parallel: 32},
		{Ctx: 2048, Parallel: 16},
		{Ctx: 2048, Parallel: 8},
		{Ctx: 16384, Parallel: 12},
		{Ctx: 16384, Parallel: 8},
	}
}

// ChunkerConfig defines a language chunker from [[chunker]] in ark.toml.
// Easy form (bracket/indent): flat string pairs for strings/brackets.
// Full form (bracket-full/indent-full): inline table structs.
// CRC: crc-Config.md | R624, R625
type ChunkerConfig struct {
	Name          string     `toml:"name"`
	Type          string     `toml:"type"` // bracket, bracket-full, indent, indent-full
	TabWidth      int        `toml:"tab_width,omitempty"`
	LineComments  []string   `toml:"line_comments"`
	BlockComments [][]string `toml:"block_comments"`

	// Easy form fields (bracket/indent)
	Strings  [][]string `toml:"strings"`
	Brackets [][]string `toml:"brackets"`

	// Full form fields (bracket-full/indent-full)
	StringDefs  []StringDefConfig  `toml:"string_defs"`
	BracketDefs []BracketDefConfig `toml:"bracket_defs"`
}

// StringDefConfig is the full-form string delimiter config.
type StringDefConfig struct {
	Open   string `toml:"open"`
	Close  string `toml:"close"`
	Escape string `toml:"escape"`
}

// BracketDefConfig is the full-form bracket group config.
// CRC: crc-Config.md | R2148, R2149, R2150
type BracketDefConfig struct {
	Open       []string `toml:"open"`
	Separators []string `toml:"separators"`
	Close      []string `toml:"close"`
	Escape     string   `toml:"escape,omitempty"`
	// AllowedInner: nil = code mode (default); non-nil (even empty)
	// = scan-restricted, mirroring microfts2.BracketGroup semantics.
	AllowedInner  *[]string `toml:"allowed_inner,omitempty"`
	AllowedParent []string  `toml:"allowed_parent,omitempty"`
}

// Source is a directory entry in the configuration.
type Source struct {
	Dir        string            `toml:"dir"`
	Strategies map[string]string `toml:"strategies,omitempty"`
	Include    PatternSpec       `toml:"include,omitempty"`
	Exclude    PatternSpec       `toml:"exclude,omitempty"`
	FromGlob   string            `toml:"from_glob,omitempty"`
}

// PatternSpec is a per-source include or exclude pattern list.
// Two TOML forms are accepted (mutually exclusive within one source):
//
//	include = ["*.md"]              -- replace form (Replace)
//	include.add = ["*.lua"]         -- extend form (Add)
//
// Replace substitutes the default entirely; extend appends to the
// default. Both empty means inherit the default.
//
// CRC: crc-Config.md | R2143, R2144, R2146
type PatternSpec struct {
	Replace []string
	Add     []string
}

// IsZero reports whether the PatternSpec carries no patterns
// (used for omitempty).
func (p PatternSpec) IsZero() bool {
	return len(p.Replace) == 0 && len(p.Add) == 0
}

// UnmarshalTOML decodes either the array form (`= [...]`) into
// Replace or the table form (`{ add = [...] }`) into Add. R2146.
func (p *PatternSpec) UnmarshalTOML(data interface{}) error {
	switch v := data.(type) {
	case []interface{}:
		p.Replace = stringSliceFromTOML(v)
		return nil
	case map[string]interface{}:
		addRaw, ok := v["add"]
		if !ok {
			return fmt.Errorf("pattern table must have key `add`")
		}
		addList, ok := addRaw.([]interface{})
		if !ok {
			return fmt.Errorf("`add` must be an array of strings")
		}
		p.Add = stringSliceFromTOML(addList)
		return nil
	case nil:
		return nil
	default:
		return fmt.Errorf("unsupported pattern form: %T", data)
	}
}

// MarshalTOML emits the array form when Replace is set, the inline-
// table form `{ add = [...] }` when Add is set, otherwise an empty
// representation that is omitted by omitempty.
func (p PatternSpec) MarshalTOML() ([]byte, error) {
	if len(p.Replace) > 0 {
		return tomlEncodeStrings(p.Replace), nil
	}
	if len(p.Add) > 0 {
		buf := []byte("{add = ")
		buf = append(buf, tomlEncodeStrings(p.Add)...)
		buf = append(buf, '}')
		return buf, nil
	}
	return []byte("[]"), nil
}

// stringSliceFromTOML asserts each element of a TOML-decoded
// []interface{} as a string.
func stringSliceFromTOML(v []interface{}) []string {
	out := make([]string, 0, len(v))
	for _, x := range v {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// tomlEncodeStrings emits a TOML inline string array.
func tomlEncodeStrings(s []string) []byte {
	buf := []byte{'['}
	for i, x := range s {
		if i > 0 {
			buf = append(buf, ',', ' ')
		}
		buf = append(buf, '"')
		// Minimal TOML escape — backslash and double-quote.
		for j := 0; j < len(x); j++ {
			c := x[j]
			if c == '\\' || c == '"' {
				buf = append(buf, '\\')
			}
			buf = append(buf, c)
		}
		buf = append(buf, '"')
	}
	buf = append(buf, ']')
	return buf
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
	cfg.initEmbedTiers() // R1590, R1591
	if cfg.AboutCentroidThreshold == 0 {
		cfg.AboutCentroidThreshold = 0.3 // R1920
	}
	if cfg.AboutFilterTopK == 0 {
		cfg.AboutFilterTopK = 200 // R1938
	}
	// R950, R951: expand tilde in all path fields at load time
	cfg.DefaultInclude = ExpandTildeSlice(cfg.DefaultInclude)
	cfg.DefaultExclude = ExpandTildeSlice(cfg.DefaultExclude)
	cfg.SearchExclude = ExpandTildeSlice(cfg.SearchExclude)
	cfg.Schedule.FilterFiles = ExpandTildeSlice(cfg.Schedule.FilterFiles)
	cfg.Schedule.ExcludeFiles = ExpandTildeSlice(cfg.Schedule.ExcludeFiles)
	for k, tc := range cfg.Schedule.TagConfig {
		tc.FilterFiles = ExpandTildeSlice(tc.FilterFiles)
		tc.ExcludeFiles = ExpandTildeSlice(tc.ExcludeFiles)
		cfg.Schedule.TagConfig[k] = tc
	}
	for i := range cfg.Sources {
		cfg.Sources[i].Dir = ExpandTilde(cfg.Sources[i].Dir)
		cfg.Sources[i].Include.Replace = ExpandTildeSlice(cfg.Sources[i].Include.Replace)
		cfg.Sources[i].Include.Add = ExpandTildeSlice(cfg.Sources[i].Include.Add)
		cfg.Sources[i].Exclude.Replace = ExpandTildeSlice(cfg.Sources[i].Exclude.Replace)
		cfg.Sources[i].Exclude.Add = ExpandTildeSlice(cfg.Sources[i].Exclude.Add)
	}
	return &cfg, nil
}

// CRC: crc-DB.md | R383
// WriteDefaultConfig writes an initial ark.toml with default excludes.
// WriteDefaultConfig writes the initial ark.toml.
// If configSeed is non-nil, uses that (from install/ark.toml bundle).
// Otherwise falls back to a minimal built-in default.
// CRC: crc-Config.md | R631, R632, R633
func WriteDefaultConfig(path string, configSeed []byte) error {
	if len(configSeed) > 0 {
		return os.WriteFile(path, configSeed, 0644)
	}
	const defaultConfig = `# Ark configuration
dotfiles = true
case_insensitive = true

# Default patterns — apply to any source that doesn't override
default_include = []
default_exclude = [".git/", ".env", "node_modules/", "__pycache__/", ".DS_Store"]

# Strategy mapping — glob pattern to chunking strategy
[strategies]
"*.md" = "markdown"
"*.jsonl" = "chat-jsonl"
`
	return os.WriteFile(path, []byte(defaultConfig), 0644)
}

// EnsureArkSource adds the database directory as an in-memory source
// if not already present. This source is hardcoded — it does not appear
// in ark.toml and cannot be removed. Scoped to content directories only.
// CRC: crc-Config.md | R961, R962
func (c *Config) EnsureArkSource() {
	if c.dbPath == "" {
		return
	}
	for _, src := range c.Sources {
		if src.Dir == c.dbPath {
			return
		}
	}
	c.Sources = append(c.Sources, Source{
		Dir:     c.dbPath,
		Include: PatternSpec{Replace: strings.Fields(arkSourceIncludePatterns)},
	})
}

// IsInSource returns true if path falls under any configured source directory.
// CRC: crc-Config.md | R1154
func (c *Config) IsInSource(path string) bool {
	for _, src := range c.Sources {
		if strings.HasPrefix(path, src.Dir+string(filepath.Separator)) || path == src.Dir {
			return true
		}
	}
	return false
}

// SourceRootForPath returns the absolute source-root directory that
// contains path, or "" with ok=false when no concrete source claims
// it. Glob-pattern sources (`src.Dir` containing `*?[`) are skipped —
// the caller works with concrete roots only.
// CRC: crc-Config.md | R2392
func (c *Config) SourceRootForPath(path string) (string, bool) {
	for _, src := range c.Sources {
		if IsGlob(src.Dir) {
			continue
		}
		if path == src.Dir || strings.HasPrefix(path, src.Dir+string(filepath.Separator)) {
			return src.Dir, true
		}
	}
	return "", false
}

// EffectivePatterns returns the include/exclude patterns in effect
// for a source. Per-source patterns either replace or extend the
// defaults: replace form (`include = [...]`) substitutes; extend form
// (`include.add = [...]`) appends. A source that uses neither form
// inherits the default unchanged.
// CRC: crc-Config.md | R2143, R2144, R2146
func (c *Config) EffectivePatterns(src Source) (includes, excludes []string) {
	includes = resolvePatterns(src.Include, c.DefaultInclude)
	excludes = resolvePatterns(src.Exclude, c.DefaultExclude)
	return
}

// collectPatternSpec drives the ShowWhy collector against the
// effective patterns for one category, labeling matches by their
// origin (the source's `include`/`exclude` form, the source's
// extend list, or the default). R2146.
func collectPatternSpec(spec PatternSpec, defaults []string,
	sourceLabel, defaultLabel string,
	matches, sources *[]string,
	collect func(patterns []string, label string, pats *[]string, srcs *[]string)) {
	if len(spec.Replace) > 0 {
		collect(spec.Replace, sourceLabel, matches, sources)
		return
	}
	collect(defaults, defaultLabel, matches, sources)
	if len(spec.Add) > 0 {
		collect(spec.Add, sourceLabel+" (add)", matches, sources)
	}
}

// resolvePatterns applies replace/extend semantics for one
// (PatternSpec, defaults) pair.
// CRC: crc-Config.md | R2143, R2144, R2146
func resolvePatterns(spec PatternSpec, defaults []string) []string {
	if len(spec.Replace) > 0 {
		return spec.Replace
	}
	if len(spec.Add) > 0 {
		out := make([]string, 0, len(defaults)+len(spec.Add))
		out = append(out, defaults...)
		out = append(out, spec.Add...)
		return out
	}
	return defaults
}

// HasErrors returns true if the config has validation errors.
func (c *Config) HasErrors() bool {
	return len(c.Errors) > 0
}

// ParseSessionTTL returns the session TTL as a duration.
// Returns defaultSessionTTL if the field is empty or unparseable.
// R646
func (c *Config) ParseSessionTTL() time.Duration {
	if c.SessionTTL == "" {
		return defaultSessionTTL
	}
	d, err := time.ParseDuration(c.SessionTTL)
	if err != nil || d <= 0 {
		return defaultSessionTTL
	}
	return d
}

// IsScheduleTag checks if a tag is declared as a schedule tag.
// Returns the default duration and true if found. R853, R855
// CRC: crc-Config.md
func (c *Config) IsScheduleTag(tag string) (defaultDur string, ok bool) {
	for _, t := range c.Schedule.Tags {
		if t == tag {
			dur := c.Schedule.Defaults[tag]
			return dur, true
		}
	}
	return "", false
}

// ScheduleTags returns the full schedule tag map (tag → default duration).
// CRC: crc-Config.md | R853
func (c *Config) ScheduleTags() map[string]string {
	m := make(map[string]string, len(c.Schedule.Tags))
	for _, t := range c.Schedule.Tags {
		m[t] = c.Schedule.Defaults[t]
	}
	return m
}

// IsLifecycleTag returns true if a schedule tag participates in the full
// lifecycle (log writing, check-gap, gap detection).
// CRC: crc-Config.md | R957, R958, R960
func (c *Config) IsLifecycleTag(tag string) bool {
	inc := c.Schedule.LifecycleInclude
	if len(inc) == 0 {
		inc = []string{"*"} // default: all tags
	}
	matched := false
	for _, pat := range inc {
		if ok, _ := filepath.Match(pat, tag); ok {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}
	for _, pat := range c.Schedule.LifecycleExclude {
		if ok, _ := filepath.Match(pat, tag); ok {
			return false
		}
	}
	return true
}

// MatchesScheduleFilter returns true if a file path passes the schedule
// filter_files/exclude_files. When both are absent, all files pass.
// CRC: crc-Config.md | R953, R954, R955, R956
func (c *Config) MatchesScheduleFilter(path string) bool {
	return matchesFilterExclude(path, c.Schedule.FilterFiles, c.Schedule.ExcludeFiles)
}

// MatchesScheduleFilterForTag returns true if a file path passes the schedule
// filter for a specific tag. Checks per-tag overrides first, falls back to global.
func (c *Config) MatchesScheduleFilterForTag(path, tag string) bool {
	if tc, ok := c.Schedule.TagConfig[tag]; ok {
		hasOverride := len(tc.FilterFiles) > 0 || len(tc.ExcludeFiles) > 0
		if hasOverride {
			// Per-tag filter: still apply global excludes, then tag-specific filters
			if !matchesFilterExclude(path, nil, c.Schedule.ExcludeFiles) {
				return false
			}
			return matchesFilterExclude(path, tc.FilterFiles, tc.ExcludeFiles)
		}
	}
	// No per-tag override — use global
	return c.MatchesScheduleFilter(path)
}

// matchesFilterExclude checks a path against filter/exclude glob lists.
func matchesFilterExclude(path string, filterFiles, excludeFiles []string) bool {
	if len(filterFiles) == 0 && len(excludeFiles) == 0 {
		return true
	}
	m := &Matcher{Dotfiles: true}
	if len(filterFiles) > 0 {
		matched := false
		for _, pat := range filterFiles {
			if m.Match(pat, path, "", false) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	for _, pat := range excludeFiles {
		if m.Match(pat, path, "", false) {
			return false
		}
	}
	return true
}

// ExpandTilde expands ~ and ~user at the start of a path.
// ~ → os.UserHomeDir(). ~user → os/user.Lookup first, ~/../user fallback.
// CRC: crc-Config.md | R947, R948, R949
func ExpandTilde(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	// ~ alone or ~/...
	if path == "~" || strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[1:])
	}
	// ~user or ~user/...
	var username, rest string
	if idx := strings.IndexByte(path, '/'); idx >= 0 {
		username = path[1:idx]
		rest = path[idx:]
	} else {
		username = path[1:]
	}
	// Try OS user database first
	if u, err := user.Lookup(username); err == nil {
		return filepath.Join(u.HomeDir, rest)
	}
	// Fallback: ~/../user
	return filepath.Join(filepath.Dir(home), username, rest)
}

// ExpandTildeSlice expands tilde in each element of a string slice.
// CRC: crc-Config.md | R950
func ExpandTildeSlice(paths []string) []string {
	if len(paths) == 0 {
		return paths
	}
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = ExpandTilde(p)
	}
	return out
}

// validate checks for identical include/exclude strings.
func (c *Config) validate() {
	c.Errors = nil
	// Check global patterns
	c.checkDuplicates(c.DefaultInclude, c.DefaultExclude, "global")
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

// initEmbedTiers applies defaults and sorts tiers by byte limit. R1590, R1591, R1592
func (c *Config) initEmbedTiers() {
	if len(c.EmbedTiers) == 0 && c.TagModel != "" {
		c.EmbedTiers = DefaultEmbedTiers()
	}
	valid := c.EmbedTiers[:0]
	for _, t := range c.EmbedTiers {
		if t.Ctx > 0 && t.Parallel > 0 && t.Parallel <= t.Ctx {
			valid = append(valid, t)
		} else {
			c.Errors = append(c.Errors, fmt.Sprintf(
				"embed_tiers: invalid tier ctx=%d parallel=%d (need ctx>0, 0<parallel<=ctx)", t.Ctx, t.Parallel))
		}
	}
	c.EmbedTiers = valid
	// Sort by byte limit ascending
	sort.Slice(c.EmbedTiers, func(i, j int) bool {
		return c.EmbedTiers[i].ByteLimit() < c.EmbedTiers[j].ByteLimit()
	})
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
	dir = ExpandTilde(dir)
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
	dir = ExpandTilde(dir)
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
		c.DefaultInclude = append(c.DefaultInclude, pattern)
	} else {
		src, err := c.findSource(sourceDir)
		if err != nil {
			return err
		}
		appendToPatternSpec(&src.Include, pattern)
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
		c.DefaultExclude = append(c.DefaultExclude, pattern)
	} else {
		src, err := c.findSource(sourceDir)
		if err != nil {
			return err
		}
		appendToPatternSpec(&src.Exclude, pattern)
	}
	c.validate()
	return nil
}

// appendToPatternSpec appends a pattern to whichever form is already
// in use (Replace or Add), so add-include preserves the user's
// chosen form. When both are empty, Replace is the default.
func appendToPatternSpec(spec *PatternSpec, pattern string) {
	if len(spec.Add) > 0 {
		spec.Add = append(spec.Add, pattern)
		return
	}
	spec.Replace = append(spec.Replace, pattern)
}

// RemovePattern removes a pattern from include or exclude lists. If
// sourceDir is empty, removes from global; otherwise from the specified
// source. Returns an error if the pattern wasn't found.
func (c *Config) RemovePattern(pattern, sourceDir string) error {
	if sourceDir == "" {
		if removeFromSlice(&c.DefaultInclude, pattern) || removeFromSlice(&c.DefaultExclude, pattern) {
			c.validate()
			return nil
		}
		return fmt.Errorf("pattern %q not found in global patterns", pattern)
	}
	src, err := c.findSource(sourceDir)
	if err != nil {
		return err
	}
	if removeFromSlice(&src.Include.Replace, pattern) || removeFromSlice(&src.Include.Add, pattern) ||
		removeFromSlice(&src.Exclude.Replace, pattern) || removeFromSlice(&src.Exclude.Add, pattern) {
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
	filePath = ExpandTilde(filePath)
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

	// Check ignore files (uses source-relative form)
	ignoreExcludes, ignoreSources := c.loadIgnoreFiles(matchedSource.Dir, relPath)

	// Find all matching patterns with their sources. R2133: pass abs
	// path + source dir so the matcher can dispatch on `/`, `./`, or
	// bare patterns correctly.
	var matchingIncludes, matchingExcludes []string
	var includeSources, excludeSources []string

	collect := func(patterns []string, label string, pats *[]string, srcs *[]string) {
		for _, p := range patterns {
			if m.Match(p, filePath, matchedSource.Dir, isDir) {
				*pats = append(*pats, p)
				*srcs = append(*srcs, label)
			}
		}
	}
	// R2143, R2144, R2146: replace/extend resolution per category.
	collectPatternSpec(matchedSource.Include, c.DefaultInclude,
		fmt.Sprintf("source %s include", matchedSource.Dir),
		"default_include",
		&matchingIncludes, &includeSources, collect)
	collectPatternSpec(matchedSource.Exclude, c.DefaultExclude,
		fmt.Sprintf("source %s exclude", matchedSource.Dir),
		"default_exclude",
		&matchingExcludes, &excludeSources, collect)
	// Ignore file patterns have per-pattern source labels
	for i, p := range ignoreExcludes {
		if m.Match(p, filePath, matchedSource.Dir, isDir) {
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
	dir = ExpandTilde(dir)
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
