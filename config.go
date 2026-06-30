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
// Standard top-level files (ark.toml, chimes.md, tags.md) are listed
// explicitly so ark-managed content is indexed regardless of the user's
// [[source]] configuration in ark.toml.
// CRC: crc-Config.md | R961, R962, R2393, R2811, R2856
const arkSourceIncludePatterns = `
	ark.toml
	chimes.md
	tags.md
	schedule/**/*.md
	apps/**/*.lua
	apps/**/*.js
	apps/**/*.html
	apps/**/*.css
	apps/**/*.md
	storage/**/*.md
	storage/**/*.pdf
	external/**/*.md
	skills/**/*.md
`

// Config represents the parsed ark.toml configuration.
// CRC: crc-Config.md | R624, R625
// CRC: crc-Config.md | R8
type Config struct {
	Dotfiles        bool              `toml:"dotfiles"` // R25
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
	Embedding       EmbeddingConfig   `toml:"embedding"`                // R2964-R2968: model, tiers, lib provisioning
	// CRC: crc-Config.md | R1919, R1920, R1938
	AboutCentroidFilter    bool    `toml:"about_centroid_filter,omitempty"`
	AboutCentroidThreshold float64 `toml:"about_centroid_threshold,omitempty"`
	AboutFilterTopK        int     `toml:"about_filter_top_k,omitempty"`
	PdfPreviewZoom         float64 `toml:"pdf_preview_zoom,omitempty"`
	// CRC: crc-Config.md | R2125
	AutoCompact bool           `toml:"auto_compact,omitempty"`
	Schedule    ScheduleConfig `toml:"schedule"` // R853, R854
	Recall      RecallConfig   `toml:"recall"`   // R2659
	Luhmann     LuhmannConfig  `toml:"luhmann"`  // R2797
	Errors      []string       `toml:"-"`
	dbPath      string         `toml:"-"`
}

// LuhmannConfig collects the [luhmann] section of ark.toml — restart-
// policy knobs read by the Luhmann orchestrator session (a Claude Code
// session, not Go code) when it acts on managed-subagent completions.
// CRC: crc-Config.md | R2797, R2798, R2799, R2800, R2801, R2862
type LuhmannConfig struct {
	// ContextLimit is the token ceiling the orchestrator passes to
	// each spawned subagent. Used by the subagent's self-recycle
	// check via `ark connections recall context --limit`. R2797
	ContextLimit *int `toml:"context_limit,omitempty"`

	// CrashPauseAfter is the consecutive-crash count at which the
	// supervisor stops respawning and writes a `pause` record to
	// luhmann.jsonl. R2798
	CrashPauseAfter *int `toml:"crash_pause_after,omitempty"`

	// QuitEarlyPauseAfter is the consecutive-quit-early count at
	// which the supervisor stops respawning and writes a storm
	// `pause` record (reason quit-early-storm). Parallel to
	// CrashPauseAfter but on the independent quit_early counter. R2862
	QuitEarlyPauseAfter *int `toml:"quit_early_pause_after,omitempty"`

	// BackoffSeconds is the schedule of seconds to wait between
	// successive crash respawns. Final value reused for further
	// attempts up to CrashPauseAfter. R2799
	BackoffSeconds []int `toml:"backoff_seconds,omitempty"`

	// Classes is the per-class enable map; key is the class name
	// (e.g. "recall"), value carries the enable flag and any
	// future per-class knobs. R2800
	Classes map[string]LuhmannClass `toml:"class,omitempty"`
}

// LuhmannClass is per-managed-subagent-class configuration.
// CRC: crc-Config.md | R2800
type LuhmannClass struct {
	// Enabled declares whether the orchestrator should host this
	// class. Setting to false disables it without removing
	// supervisor state from luhmann.jsonl. Default true.
	Enabled *bool `toml:"enabled,omitempty"`
}

// EffectiveContextLimit returns ContextLimit with the default applied.
// CRC: crc-Config.md | R2797
func (lc LuhmannConfig) EffectiveContextLimit() int {
	if lc.ContextLimit == nil {
		return 150000
	}
	return *lc.ContextLimit
}

// EffectiveCrashPauseAfter returns CrashPauseAfter with default.
// CRC: crc-Config.md | R2798
func (lc LuhmannConfig) EffectiveCrashPauseAfter() int {
	if lc.CrashPauseAfter == nil {
		return 3
	}
	return *lc.CrashPauseAfter
}

// EffectiveQuitEarlyPauseAfter returns QuitEarlyPauseAfter with default.
// CRC: crc-Config.md | R2862
func (lc LuhmannConfig) EffectiveQuitEarlyPauseAfter() int {
	if lc.QuitEarlyPauseAfter == nil {
		return 3
	}
	return *lc.QuitEarlyPauseAfter
}

// EffectiveBackoffSeconds returns BackoffSeconds with default applied.
// CRC: crc-Config.md | R2799
func (lc LuhmannConfig) EffectiveBackoffSeconds() []int {
	if len(lc.BackoffSeconds) == 0 {
		return []int{1, 5, 30}
	}
	return lc.BackoffSeconds
}

// ClassEnabled returns true when the named class is enabled (default
// true when no entry exists).
// CRC: crc-Config.md | R2800
func (lc LuhmannConfig) ClassEnabled(name string) bool {
	c, ok := lc.Classes[name]
	if !ok || c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// RecallConfig collects the [recall] section of ark.toml.
// CRC: crc-Config.md | R2659, R2687, R2688, R2689, R2690, R2692, R2693, R2767, R2768
type RecallConfig struct {
	// DiscussedTTL is the lifetime of an RD record before lazy
	// expiry takes effect. Empty/missing falls back to 24h;
	// `"0"` disables expiry (records never expire on read).
	// An unparseable value falls back to 24h with a warning at
	// server startup. R2659, R2663
	DiscussedTTL string `toml:"discussed_ttl,omitempty"`

	// Enabled is the master switch for the simple-recall watcher.
	// Default false (opt-in for v1). R2688
	Enabled bool `toml:"enabled,omitempty"`

	// Propose passes `propose = true` through to the recall
	// substrate on each watcher fire. Default true. R2689
	Propose *bool `toml:"propose,omitempty"`

	// MinSimilarity is the per-section similarity gate: sections
	// whose top recalled chunk scores below this are dropped from
	// the DM. Default 0.65. R2690, R2739
	MinSimilarity *float64 `toml:"min_similarity,omitempty"`

	// MinProposeSimilarity is the chunk-EC ↔ tag-ED cosine floor
	// for derived-tag proposals. Candidates scoring below this are
	// dropped before the top-K cut in selectCandidates and never
	// written as RC records. Default 0.70. R2742
	MinProposeSimilarity *float64 `toml:"min_propose_similarity,omitempty"`

	// ActivationDelay is the seconds the watcher waits after a
	// `turn_duration` record before firing the recall pass. A
	// user record arriving inside this window cancels the firing
	// entirely. Default 15. R2728
	ActivationDelay *int `toml:"activation_delay,omitempty"`

	// ChunksPerDM caps the recalled chunks per section in the DM
	// body. Default 5. R2692
	ChunksPerDM *int `toml:"chunks_per_dm,omitempty"`

	// Sources is an optional whitelist of source root directories
	// (matching Source.Dir in ark.toml). When non-empty, only
	// sources whose root is in this list (and whose strategy is
	// chat-jsonl) qualify. Empty = all chat-jsonl sources qualify.
	// R2693
	Sources []string `toml:"sources,omitempty"`

	// RejectProposeCeiling is the RJ-counter threshold above which
	// the propose pass suppresses a (chunk, tag) pair. `0` (unset)
	// means infinite — any RJ existence suppresses, preserving v1
	// behavior. R2767
	RejectProposeCeiling *int `toml:"reject_propose_ceiling,omitempty"`

	// RejectMentionCeiling is the RJ-counter threshold above which
	// the assistant suppresses the (chunk, tag) pair from any
	// user-facing mention. `0` (unset) means infinite. R2768
	RejectMentionCeiling *int `toml:"reject_mention_ceiling,omitempty"`

	// SurfaceCooldown is the window within which a previously-surfaced
	// (session, chunk) is suppressed by the secretary's deterministic
	// floor; it doubles as the RM record's lazy-expiry TTL. Go duration
	// string; default "24h". R2886
	SurfaceCooldown string `toml:"surface_cooldown,omitempty"`

	// ContextTurns is how many trailing conversation turns
	// `recall next --session` injects into the curation doc so the
	// secretary judges with the live conversation. Default 3. R2892
	ContextTurns *int `toml:"context_turns,omitempty"`

	// PerCellCount is the number of chunks allocated per cell in the
	// recall 2×2 (source × axis) grid; the per-call target is 4×this.
	// Default 3. R2907, R2912
	PerCellCount *int `toml:"per_cell_count,omitempty"`

	// ChatFunnelGate caps how many conversation sub-chunks survive the
	// trigram pre-filter and get embedded per recall fire — the chat
	// funnel's cost bound (SIGNAL Q2.3). Default 8. R2910, R2912
	ChatFunnelGate *int `toml:"chat_funnel_gate,omitempty"`
}

// EffectivePropose returns Propose with the default applied.
// CRC: crc-Config.md | R2689
func (rc RecallConfig) EffectivePropose() bool {
	if rc.Propose == nil {
		return true
	}
	return *rc.Propose
}

// EffectiveMinSimilarity returns MinSimilarity with the default applied.
// CRC: crc-Config.md | R2690
func (rc RecallConfig) EffectiveMinSimilarity() float64 {
	if rc.MinSimilarity == nil {
		return 0.65
	}
	return *rc.MinSimilarity
}

// EffectiveMinProposeSimilarity returns MinProposeSimilarity with the
// default applied.
// CRC: crc-Config.md | R2742
func (rc RecallConfig) EffectiveMinProposeSimilarity() float64 {
	if rc.MinProposeSimilarity == nil {
		return 0.70
	}
	return *rc.MinProposeSimilarity
}

// EffectivePerCellCount returns PerCellCount with the default applied.
// CRC: crc-Config.md | R2907, R2912
func (rc RecallConfig) EffectivePerCellCount() int {
	if rc.PerCellCount == nil {
		return 3
	}
	return *rc.PerCellCount
}

// EffectiveChatFunnelGate returns ChatFunnelGate with the default applied.
// CRC: crc-Config.md | R2910, R2912
func (rc RecallConfig) EffectiveChatFunnelGate() int {
	if rc.ChatFunnelGate == nil {
		return 8
	}
	return *rc.ChatFunnelGate
}

// EffectiveActivationDelay returns ActivationDelay with the default applied.
// CRC: crc-Config.md | R2728
func (rc RecallConfig) EffectiveActivationDelay() int {
	if rc.ActivationDelay == nil {
		return 15
	}
	return *rc.ActivationDelay
}

// EffectiveChunksPerDM returns ChunksPerDM with the default applied.
// CRC: crc-Config.md | R2692
func (rc RecallConfig) EffectiveChunksPerDM() int {
	if rc.ChunksPerDM == nil {
		return 5
	}
	return *rc.ChunksPerDM
}

// EffectiveRejectProposeCeiling returns RejectProposeCeiling with
// the default applied. `0` (unset) means infinite — suppression is
// existence-driven, preserving v1 behavior.
// CRC: crc-Config.md | R2767
func (rc RecallConfig) EffectiveRejectProposeCeiling() int {
	if rc.RejectProposeCeiling == nil {
		return 0
	}
	return *rc.RejectProposeCeiling
}

// EffectiveRejectMentionCeiling returns RejectMentionCeiling with
// the default applied. `0` (unset) means infinite.
// CRC: crc-Config.md | R2768
func (rc RecallConfig) EffectiveRejectMentionCeiling() int {
	if rc.RejectMentionCeiling == nil {
		return 0
	}
	return *rc.RejectMentionCeiling
}

// SurfaceCooldownDuration parses [recall].surface_cooldown and returns
// the effective window. Empty/unparseable -> 24h. wasParseError is true
// when the value was set but couldn't be parsed.
// CRC: crc-Config.md | R2886
func (rc RecallConfig) SurfaceCooldownDuration() (window time.Duration, wasParseError bool) {
	const fallback = 24 * time.Hour
	if rc.SurfaceCooldown == "" {
		return fallback, false
	}
	d, err := time.ParseDuration(rc.SurfaceCooldown)
	if err != nil {
		return fallback, true
	}
	return d, false
}

// EffectiveContextTurns returns ContextTurns with the default (3)
// applied.
// CRC: crc-Config.md | R2892
func (rc RecallConfig) EffectiveContextTurns() int {
	if rc.ContextTurns == nil {
		return 3
	}
	if *rc.ContextTurns < 0 {
		return 0
	}
	return *rc.ContextTurns
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

// ScheduleConfig is the top-level [schedule] section of ark.toml. Tag
// declarations live in per-tag [schedule.tag.X] blocks; the mere
// presence of a block declares X as a schedule tag (R2830, R2833).
// Legacy `tags = [...]` / `[schedule.defaults]` / `lifecycle_include` /
// `lifecycle_exclude` are not parsed (R2832 — T108-T125 retired).
// CRC: crc-Config.md | R2830, R2832
type ScheduleConfig struct {
	FilterFiles  []string                     `toml:"filter_files,omitempty"`  // R953
	ExcludeFiles []string                     `toml:"exclude_files,omitempty"` // R954
	Tag          map[string]ScheduleTagConfig `toml:"tag,omitempty"`           // per-tag blocks (R2830)
}

// ScheduleTagConfig is one [schedule.tag.X] block. The block's
// presence declares X as a schedule tag; all fields are optional and
// take per-tag defaults when unset.
// CRC: crc-Config.md | R2830, R2831
type ScheduleTagConfig struct {
	Lifecycle       string   `toml:"lifecycle,omitempty"`        // R2822: "disk" (default), "tmp", or "none"
	LogCap          *int     `toml:"log_cap,omitempty"`          // R2827: fired-entry cap; default 1000
	DefaultDuration string   `toml:"default_duration,omitempty"` // R2831: replaces [schedule.defaults]
	FilterFiles     []string `toml:"filter_files,omitempty"`     // R2831: per-tag filter override
	ExcludeFiles    []string `toml:"exclude_files,omitempty"`    // R2831: per-tag exclude override
	Suppress        bool     `toml:"suppress,omitempty"`         // R2835: stop arming without dropping declaration
}

// Lifecycle constants.
// CRC: crc-Config.md | R2822
const (
	LifecycleDisk = "disk"
	LifecycleTmp  = "tmp"
	LifecycleNone = "none"
)

// defaultLogCap is the per-tag fired-entry cap applied when a
// [schedule.tag.X] block omits log_cap.
// CRC: crc-Config.md | R2827
const defaultLogCap = 1000

// defaultChimeTags is the canonical list of standard chime cadences
// (R2778, R2779). Mirrors the cadences declared in chimes.md.
// CRC: crc-Config.md | R2834
var defaultChimeTags = []string{
	"chime-1m", "chime-5m", "chime-15m", "chime-30m", "chime-45m", "chime-60m",
}

// EmbeddingConfig collects the [embedding] section of ark.toml — the GGUF
// model, the adaptive tiers, and the runtime-provisioned llama.cpp shared
// libraries the yzma engine dlopens.
// CRC: crc-Config.md, crc-LlamaLibs.md | R2964, R2965, R2966, R2967, R2968
type EmbeddingConfig struct {
	// Model is the GGUF embedding model filename under the ark dir, used
	// for chunk, tag, and query embeddings. Empty disables vector-EC.
	// Renamed from the top-level tag_model. R2964
	Model string `toml:"model,omitempty"`

	// Tiers configures the adaptive embedding tiers; each entry's ctx
	// maps to the context NCtx and parallel to NSeqMax. Renamed from the
	// top-level embed_tiers. R2965
	Tiers []EmbedTier `toml:"tiers,omitempty"`

	// LibDir holds the llama.cpp shared libs the engine dlopens at
	// startup. Empty resolves to <ark-dir>/lib, beside the LMDB env. R2966
	LibDir string `toml:"lib_dir,omitempty"`

	// Backend selects the llama.cpp build: auto|cpu|vulkan|cuda|metal|
	// rocm. auto detects the platform GPU. R2967
	Backend string `toml:"backend,omitempty"`

	// LlamaVersion pins the llama.cpp release build to provision, within
	// yzma's tested range, keeping release builds reproducible. R2968
	LlamaVersion string `toml:"llama_version,omitempty"`
}

// ResolveLibDir returns the configured lib_dir or the default <dbPath>/lib,
// with leading ~ expanded. R2966
func (e EmbeddingConfig) ResolveLibDir(dbPath string) string {
	if e.LibDir != "" {
		return ExpandTilde(e.LibDir)
	}
	return filepath.Join(dbPath, "lib")
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
// CRC: crc-Config.md | R27
type Source struct {
	Dir        string            `toml:"dir"`
	Strategies map[string]string `toml:"strategies,omitempty"` // R14
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
// CRC: crc-Config.md | R23 — config is TOML, named ark.toml
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
	cfg.SearchExclude = ExpandTildeSlice(cfg.SearchExclude) // R943: search_exclude carried through LoadConfig (startup + reload)
	cfg.Schedule.FilterFiles = ExpandTildeSlice(cfg.Schedule.FilterFiles)
	cfg.Schedule.ExcludeFiles = ExpandTildeSlice(cfg.Schedule.ExcludeFiles)
	for k, tc := range cfg.Schedule.Tag {
		tc.FilterFiles = ExpandTildeSlice(tc.FilterFiles)
		tc.ExcludeFiles = ExpandTildeSlice(tc.ExcludeFiles)
		cfg.Schedule.Tag[k] = tc
	}
	for i := range cfg.Sources {
		cfg.Sources[i].Dir = ExpandTilde(cfg.Sources[i].Dir)
		cfg.Sources[i].Include.Replace = ExpandTildeSlice(cfg.Sources[i].Include.Replace)
		cfg.Sources[i].Include.Add = ExpandTildeSlice(cfg.Sources[i].Include.Add)
		cfg.Sources[i].Exclude.Replace = ExpandTildeSlice(cfg.Sources[i].Exclude.Replace)
		cfg.Sources[i].Exclude.Add = ExpandTildeSlice(cfg.Sources[i].Exclude.Add)
	}
	cfg.EnsureDefaultScheduleTags()
	return &cfg, nil
}

// EnsureDefaultScheduleTags injects synthetic [schedule.tag.X] blocks
// for the six standard chime cadences (defaultChimeTags) if the user
// hasn't already declared them in ark.toml. The synthetic blocks
// default to lifecycle="none" (no audit) and no default_duration —
// chimes are heartbeats, not events; ack tracking and audit history
// would just be noise. User can override by adding an explicit block
// to ark.toml (e.g. `[schedule.tag.chime-1m] lifecycle = "disk"` to
// turn on audit). Mirrors EnsureArkSource — ark adds defaults that
// the user can override.
// CRC: crc-Config.md | R2822, R2825, R2834
func (c *Config) EnsureDefaultScheduleTags() {
	if c.Schedule.Tag == nil {
		c.Schedule.Tag = make(map[string]ScheduleTagConfig)
	}
	for _, name := range defaultChimeTags {
		if _, ok := c.Schedule.Tag[name]; ok {
			continue
		}
		c.Schedule.Tag[name] = ScheduleTagConfig{Lifecycle: LifecycleNone}
	}
}

// CRC: crc-DB.md | R383
// WriteDefaultConfig writes an initial ark.toml with default excludes.
// WriteDefaultConfig writes the initial ark.toml.
// If configSeed is non-nil, uses that (from install/ark.toml bundle).
// Otherwise falls back to a minimal built-in default.
// CRC: crc-Config.md | R631, R632, R633, R2781, R2834
// CRC: crc-Config.md | R22
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

# Schedule tags — declared via [schedule.tag.X] blocks; presence
# of a block declares X as a schedule tag (R2830). The six standard
# chime cadences (chime-1m through chime-60m) are auto-declared by
# EnsureDefaultScheduleTags with lifecycle="none" (no audit); add an
# explicit block here to override — e.g. lifecycle="disk" to enable
# fire-history audit, or suppress=true to stop a chime from firing.
[schedule]
`
	return os.WriteFile(path, []byte(defaultConfig), 0644)
}

// EnsureArkSource adds the database directory as an in-memory source
// if not already present. This source is hardcoded — it does not appear
// in ark.toml and cannot be removed. Scoped to content directories only.
// CRC: crc-Config.md | R338, R339, R341, R961, R962
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

// IsScheduleTag returns true when a [schedule.tag.X] block exists for
// the given tag. Block presence is the declaration mechanism. (R2833)
// CRC: crc-Config.md | R2833
func (c *Config) IsScheduleTag(tag string) bool {
	_, ok := c.Schedule.Tag[tag]
	return ok
}

// ScheduleTags returns the full per-tag config map. (R2833)
// CRC: crc-Config.md | R2833
func (c *Config) ScheduleTags() map[string]ScheduleTagConfig {
	return c.Schedule.Tag
}

// Lifecycle returns the audit destination for the given tag: one of
// LifecycleDisk, LifecycleTmp, LifecycleNone. Empty / missing field
// defaults to LifecycleDisk. (R2822)
// CRC: crc-Config.md | R2822
func (c *Config) Lifecycle(tag string) string {
	tc, ok := c.Schedule.Tag[tag]
	if !ok {
		return LifecycleNone // tag isn't declared — no audit
	}
	switch tc.Lifecycle {
	case LifecycleTmp:
		return LifecycleTmp
	case LifecycleNone:
		return LifecycleNone
	default:
		return LifecycleDisk
	}
}

// LogCap returns the per-tag fired-entry cap. Default 1000. (R2827)
// CRC: crc-Config.md | R2827
func (c *Config) LogCap(tag string) int {
	tc, ok := c.Schedule.Tag[tag]
	if !ok || tc.LogCap == nil {
		return defaultLogCap
	}
	if *tc.LogCap <= 0 {
		return 1 // treat 0/negative as "always keep one entry"
	}
	return *tc.LogCap
}

// DefaultDuration returns the per-tag default duration (replaces R854
// [schedule.defaults]).
// CRC: crc-Config.md | R2831
func (c *Config) DefaultDuration(tag string) string {
	return c.Schedule.Tag[tag].DefaultDuration
}

// IsSuppressed returns true when [schedule.tag.X] suppress = true.
// CRC: crc-Config.md | R2835
func (c *Config) IsSuppressed(tag string) bool {
	return c.Schedule.Tag[tag].Suppress
}

// MatchesScheduleFilter returns true if a file path passes the
// top-level [schedule] filter_files/exclude_files. When both are
// absent, all files pass.
// CRC: crc-Config.md | R953, R954, R955, R956
func (c *Config) MatchesScheduleFilter(path string) bool {
	return matchesFilterExclude(path, c.Schedule.FilterFiles, c.Schedule.ExcludeFiles)
}

// MatchesScheduleFilterForTag returns true if a file path passes the
// schedule filter for a specific tag. Checks per-tag overrides first,
// falls back to global. (R2831)
// CRC: crc-Config.md | R1012 — global excludes always apply; per-tag filters narrow further.
func (c *Config) MatchesScheduleFilterForTag(path, tag string) bool {
	if tc, ok := c.Schedule.Tag[tag]; ok {
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
// CRC: crc-Config.md | R947, R948, R949, R952
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
// CRC: crc-Config.md | R11
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

// initEmbedTiers applies defaults and sorts tiers by byte limit.
// CRC: crc-Config.md | R1590, R1591, R1592
func (c *Config) initEmbedTiers() {
	if len(c.Embedding.Tiers) == 0 && c.Embedding.Model != "" {
		c.Embedding.Tiers = DefaultEmbedTiers()
	}
	valid := c.Embedding.Tiers[:0]
	for _, t := range c.Embedding.Tiers {
		if t.Ctx > 0 && t.Parallel > 0 && t.Parallel <= t.Ctx {
			valid = append(valid, t)
		} else {
			c.Errors = append(c.Errors, fmt.Sprintf(
				"embedding.tiers: invalid tier ctx=%d parallel=%d (need ctx>0, 0<parallel<=ctx)", t.Ctx, t.Parallel))
		}
	}
	c.Embedding.Tiers = valid
	// Sort by byte limit ascending
	sort.Slice(c.Embedding.Tiers, func(i, j int) bool {
		return c.Embedding.Tiers[i].ByteLimit() < c.Embedding.Tiers[j].ByteLimit()
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
// CRC: crc-Config.md | R194
func IsGlob(dir string) bool {
	return strings.ContainsAny(dir, "*?[")
}

// AddSource adds a new source directory. Glob patterns (containing *, ?, [)
// are stored as-is without validation. Concrete paths are validated to exist.
// CRC: crc-Config.md | R148, R195, R201
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
// CRC: crc-Config.md | R200, R340
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
// CRC: crc-Config.md | R196, R197, R198, R199, R203
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
// CRC: crc-Config.md | R205, R206, R207, R208
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
// CRC: crc-Config.md | R149, R151
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
// CRC: crc-Config.md | R149
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
// CRC: crc-Config.md | R150
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
// CRC: crc-Config.md | R157, R158
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
