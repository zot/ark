package ark

// CRC: crc-Searcher.md

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/zot/microfts2"
	"go.etcd.io/bbolt"

	"github.com/yuin/goldmark"
)

// defaultSearchOpts returns FTS search options with dynamic trigram filtering.
// The filter uses a 50% ratio threshold — trigrams appearing in more than
// half of all chunks are skipped as non-discriminating.
// Seq: seq-search.md | R572, R574, R575
// CRC: crc-Searcher.md | R572, R574, R575
func defaultSearchOpts(filterOpt microfts2.SearchOption, score string, sopts SearchOpts) []microfts2.SearchOption {
	opts := []microfts2.SearchOption{
		microfts2.WithTrigramFilter(microfts2.FilterByRatio(0.50)),
	}
	if score == "density" {
		opts = append(opts, microfts2.WithDensity())
	}
	if filterOpt != nil {
		opts = append(opts, filterOpt)
	}
	if !sopts.After.IsZero() {
		opts = append(opts, microfts2.WithAfter(sopts.After))
	}
	if !sopts.Before.IsZero() {
		opts = append(opts, microfts2.WithBefore(sopts.Before))
	}
	if sopts.NoTmp {
		opts = append(opts, microfts2.WithNoTmp())
	}
	// R1139: thread session cache into microfts2 search so post-filters
	// (verify, regex) share cached file reads instead of re-reading from disk
	if sopts.Cache != nil {
		opts = append(opts, microfts2.WithChunkCache(sopts.Cache))
	}
	opts = append(opts, sopts.extraOpts...)
	return opts
}

// SearchOpts controls search behavior.
// CRC: crc-SearchCmd.md, crc-Searcher.md | R649, R650, R651 — realizes the designed SearchCmd: captures all search params (incl. Session, Cache), constructed by CLI (runSearch), HTTP (handleSearch), and Lua, dispatched inline; "submit to session" is the Cache threading (R1139/R1140); converts to microfts2's searchConfig via the With* options in defaultSearchOpts
type SearchOpts struct {
	K               int                      // max results (default 20)
	Scores          bool                     // include scores in output
	After           time.Time                // only results newer than this (zero = no filter)
	Before          time.Time                // only results older than this (zero = no filter)
	About           string                   // semantic query — routed through Librarian + EC (R1916)
	Contains        string                   // exact match query (microfts2)
	Regex           []string                 // regex patterns (first drives SearchRegex, all are AND post-filters)
	ExceptRegex     []string                 // regex subtract post-filters (any match rejects)
	LikeFile        string                   // file path — use content as FTS density query
	Tags            bool                     // output extracted tags instead of content
	Filter          []string                 // content-based positive filters (FTS queries, intersect)
	Except          []string                 // content-based negative filters (FTS queries, subtract)
	FilterFiles     []string                 // path-based positive filters (glob patterns, intersect)
	ExcludeFiles    []string                 // path-based negative filters (glob patterns, subtract)
	FilterFileTags  []string                 // tag-based positive filters (tag names, intersect)
	ExcludeFileTags []string                 // tag-based negative filters (tag names, subtract)
	Score           string                   // scoring strategy: "", "auto", "coverage", "density"
	Multi           bool                     // run all four strategies via SearchMulti
	Fuzzy           bool                     // R744: typo-tolerant search via SearchFuzzy
	Proximity       bool                     // enable proximity reranking
	NoTmp           bool                     // R673: exclude tmp:// documents
	Cache           *microfts2.ChunkCache    // R652: session-provided cache (nil = per-query)
	ChunkFilters    []ChunkFilterRow         // R1402: stacked filter rows for chunk-level filtering
	extraOpts       []microfts2.SearchOption // built from ChunkFilters at search time
	// aboutResults, when set, is the precomputed top-k for opts.About
	// from a combined SearchChunksMulti call. SearchSplit/SearchCombined
	// consume it instead of running aboutSearch themselves. R1935
	aboutResults    []ChunkScore
	aboutResultsSet bool
	// aboutFilterSets, when set, are the raw chunkID memberships from
	// the same combined walk that produced aboutResults. SearchSplit
	// applies them to vec-only output. R1935
	aboutFilterSets []AboutFilterSet
}

// SetExtraOpts appends microfts2 search options built from chunk filters.
func (o *SearchOpts) SetExtraOpts(opts ...microfts2.SearchOption) {
	o.extraOpts = append(o.extraOpts, opts...)
}

// SetAboutResults plants the precomputed top-k for opts.About so that
// SearchSplit/SearchCombined skip a redundant SearchChunks call.
// CRC: crc-Searcher.md | R1935
func (o *SearchOpts) SetAboutResults(results []ChunkScore) {
	o.aboutResults = results
	o.aboutResultsSet = true
}

// SetAboutFilterSets plants the chunkID-set view of about-filter
// rows so vec-only paths can apply the same filter that
// WithChunkFilter applies on the FTS side.
// CRC: crc-Searcher.md | R1935
func (o *SearchOpts) SetAboutFilterSets(sets []AboutFilterSet) {
	o.aboutFilterSets = sets
}

// applyAboutFilterSets filters vecResults through every about-filter
// chunkID set, honouring polarity. Used by vec-only paths that
// bypass the FTS WithChunkFilter closures.
// CRC: crc-Searcher.md | R1935
func applyAboutFilterSets(vec []ChunkScore, sets []AboutFilterSet) []ChunkScore {
	if len(sets) == 0 {
		return vec
	}
	out := vec[:0]
	for _, cs := range vec {
		keep := true
		for _, s := range sets {
			_, in := s.ChunkIDs[cs.ChunkID]
			if s.Polarity == "without" {
				if in {
					keep = false
					break
				}
			} else if !in {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, cs)
		}
	}
	return out
}

// SearchResultEntry is a merged/intersected search result.
type SearchResultEntry struct {
	Path     string
	Range    string // opaque range from chunker (e.g. "1-10" for lines chunker)
	FTSScore float64
	VecScore float64
	Score    float64
	FileID   uint64
	ChunkNum uint64
	Text     string           // populated by FillChunks or FillFiles
	Attrs    []microfts2.Pair // populated by FillChunks for pdf strategy (R1705)
	Strategy string           // which scoring strategy produced this result (multi-search only)
}

// ChunkResult is the JSONL output for --chunks.
// CRC: crc-Searcher.md | R112
type ChunkResult struct {
	Path    string  `json:"path"`
	Range   string  `json:"range"`
	Score   float64 `json:"score"`
	Text    string  `json:"text"`
	Preview string  `json:"preview,omitempty"`
}

// ExtractPreview returns a window of n characters from text centered on the
// first case-insensitive occurrence of query. It adjusts to word boundaries
// and adds ellipsis when truncated. If query is not found, falls back to
// the first n characters (the FTS engine already verified the match against
// the raw file content).
func ExtractPreview(text, query string, n int) string {
	if n <= 0 || len(text) == 0 {
		return ""
	}
	// If text fits in the window, return it whole
	textRunes := []rune(text)
	if len(textRunes) <= n {
		return text
	}

	// Find match position (case-insensitive)
	matchPos := -1
	if query != "" {
		lower := strings.ToLower(text)
		idx := strings.Index(lower, strings.ToLower(query))
		if idx >= 0 {
			// Convert byte offset to rune offset
			matchPos = utf8.RuneCountInString(text[:idx])
		}
	}

	// No match: fall back to start of text
	if matchPos < 0 {
		matchPos = 0
	}

	// Center the window on the match
	queryRunes := utf8.RuneCountInString(query)
	half := (n - queryRunes) / 2
	start := matchPos - half
	end := start + n

	// Clamp to bounds
	if start < 0 {
		start = 0
		end = n
	}
	if end > len(textRunes) {
		end = len(textRunes)
		start = end - n
		if start < 0 {
			start = 0
		}
	}

	// Adjust to word boundaries (don't cut mid-word)
	if start > 0 {
		// Move start forward to next space
		for start < end && textRunes[start] != ' ' && textRunes[start] != '\n' {
			start++
		}
		if start < end && (textRunes[start] == ' ' || textRunes[start] == '\n') {
			start++ // skip the space
		}
	}
	if end < len(textRunes) {
		// Move end back to previous space
		for end > start && textRunes[end-1] != ' ' && textRunes[end-1] != '\n' {
			end--
		}
	}

	if start >= end {
		// Word boundary adjustment collapsed the window; fall back
		start = matchPos
		end = matchPos + n
		if end > len(textRunes) {
			end = len(textRunes)
		}
	}

	result := string(textRunes[start:end])

	// Add ellipsis
	if start > 0 {
		result = "..." + result
	}
	if end < len(textRunes) {
		result = result + "..."
	}
	return result
}

// FileResult is the JSONL output for --file-content.
// CRC: crc-Searcher.md | R113
type FileResult struct {
	Path  string  `json:"path"`
	Score float64 `json:"score"`
	Text  string  `json:"text"`
}

// chunkKey uniquely identifies a chunk across both engines.
type chunkKey struct {
	FileID   uint64
	ChunkNum uint64
}

// Searcher queries microfts2 and the Librarian-backed embedding pipeline,
// then merges or intersects results.
// CRC: crc-Searcher.md | R1909, R1916, R1923
type Searcher struct {
	fts       *microfts2.DB
	store     *Store
	config    *Config
	librarian *Librarian
}

// SearchCombined sends the same query to both engines, merges by
// (fileid, chunknum), combines scores, sorts descending.
// CRC: crc-Searcher.md | R228
func (s *Searcher) SearchCombined(query string, opts SearchOpts) ([]SearchResultEntry, error) {
	if err := validateSearchFlags(opts); err != nil {
		return nil, err
	}
	k := opts.K
	if k == 0 {
		k = 20
	}

	filterOpt, err := s.resolveFilters(opts)
	if err != nil {
		return nil, err
	}
	score := opts.Score
	ftsSearchOpts := defaultSearchOpts(filterOpt, score, opts)

	ftsResults, err := s.fts.Search(query, ftsSearchOpts...)
	if err != nil {
		return nil, fmt.Errorf("fts search: %w", err)
	}

	// R576: Fuzzy escalation: auto mode retries with density on zero results
	if len(ftsResults.Results) == 0 && (score == "" || score == "auto") {
		densityOpts := defaultSearchOpts(filterOpt, "density", opts)
		ftsResults, err = s.fts.Search(query, densityOpts...)
		if err != nil {
			return nil, fmt.Errorf("fts density search: %w", err)
		}
	}

	var vecResults []ChunkScore
	if opts.aboutResultsSet {
		vecResults = opts.aboutResults // precomputed by SearchGrouped's combined walk
	} else {
		vr, err := s.aboutSearch(query, k*2)
		if err != nil {
			// Embedding pipeline unavailable: fall back to FTS-only.
			results := s.ftsOnly(ftsResults.Results)
			if len(results) > k {
				results = results[:k]
			}
			return s.filterAndResolve(results, opts)
		}
		vecResults = vr
	}
	// Apply about-filter chunkID sets to vec results. R1935
	vecResults = applyAboutFilterSets(vecResults, opts.aboutFilterSets)

	merged := s.merge(ftsResults.Results, vecResults)
	if len(merged) > k {
		merged = merged[:k]
	}
	return s.filterAndResolve(merged, opts)
}

// aboutSearch embeds query and ranks chunks via the EC pipeline.
// Returns ([]ChunkScore, nil) on success, an error when embedding is
// unavailable so callers can fall back to FTS-only.
// CRC: crc-Searcher.md | R1916, R1935
func (s *Searcher) aboutSearch(query string, k int) ([]ChunkScore, error) {
	if s.librarian == nil || !s.librarian.EmbeddingAvailable() {
		return nil, fmt.Errorf("embedding pipeline unavailable")
	}
	qvec, err := s.librarian.EmbedQuery(query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	return s.librarian.SearchChunks(qvec, k)
}

// SearchSplit dispatches --about, --contains, --regex to appropriate engines.
// CRC: crc-Searcher.md | R228
func (s *Searcher) SearchSplit(opts SearchOpts) ([]SearchResultEntry, error) {
	if err := validateSearchFlags(opts); err != nil {
		return nil, err
	}

	k := opts.K
	if k == 0 {
		k = 20
	}

	filterOpt, err := s.resolveFilters(opts)
	if err != nil {
		return nil, err
	}
	score := opts.Score
	ftsSearchOpts := defaultSearchOpts(filterOpt, score, opts)

	hasAbout := opts.About != ""
	hasFTS := opts.Contains != "" || len(opts.Regex) > 0 || opts.LikeFile != ""

	var vecResults []ChunkScore
	var ftsResults []microfts2.SearchResult

	if hasAbout {
		if opts.aboutResultsSet {
			vecResults = opts.aboutResults // precomputed by SearchGrouped's combined walk
		} else {
			vr, err := s.aboutSearch(opts.About, k*2)
			if err != nil {
				return nil, fmt.Errorf("about search: %w", err)
			}
			vecResults = vr
		}
		// Apply about-filter chunkID sets — vec-only paths bypass the
		// FTS WithChunkFilter closures that ResolveAboutFilters built. R1935
		vecResults = applyAboutFilterSets(vecResults, opts.aboutFilterSets)
	}

	// Apply --except-regex as post-filter to any search mode
	if len(opts.ExceptRegex) > 0 {
		ftsSearchOpts = append(ftsSearchOpts, microfts2.WithExceptRegex(opts.ExceptRegex...))
	}

	if opts.LikeFile != "" {
		// --like-file always uses density regardless of --score
		content, err := os.ReadFile(opts.LikeFile)
		if err != nil {
			return nil, fmt.Errorf("read like-file: %w", err)
		}
		fr, err := s.fts.Search(string(content), append(ftsSearchOpts, microfts2.WithDensity())...)
		if err != nil {
			return nil, fmt.Errorf("fts like-file search: %w", err)
		}
		ftsResults = fr.Results
	} else if opts.Contains != "" {
		containsOpts := append(ftsSearchOpts, microfts2.WithVerify())
		if len(opts.Regex) > 0 {
			containsOpts = append(containsOpts, microfts2.WithRegexFilter(opts.Regex...))
		}
		fr, err := s.fts.Search(opts.Contains, containsOpts...)
		if err != nil {
			return nil, fmt.Errorf("fts search: %w", err)
		}
		ftsResults = fr.Results
		// R576: Fuzzy escalation: auto mode retries with density on zero results
		if len(ftsResults) == 0 && (score == "" || score == "auto") {
			densityOpts := defaultSearchOpts(filterOpt, "density", opts)
			densityOpts = append(densityOpts, microfts2.WithVerify())
			if len(opts.Regex) > 0 {
				densityOpts = append(densityOpts, microfts2.WithRegexFilter(opts.Regex...))
			}
			fr, err = s.fts.Search(opts.Contains, densityOpts...)
			if err != nil {
				return nil, fmt.Errorf("fts density search: %w", err)
			}
			ftsResults = fr.Results
		}
	} else if len(opts.Regex) > 0 {
		// First regex drives the search; all regexes are AND post-filters
		regexOpts := append(ftsSearchOpts, microfts2.WithRegexFilter(opts.Regex...))
		fr, err := s.fts.SearchRegex(opts.Regex[0], regexOpts...)
		if err != nil {
			return nil, fmt.Errorf("fts regex search: %w", err)
		}
		ftsResults = fr.Results
	}

	var results []SearchResultEntry

	if hasAbout && hasFTS {
		// Intersect (the FTS side already carries the inline post-filter stack)
		results = s.intersect(ftsResults, vecResults)
	} else if hasAbout {
		// Vector only: the candidate set skipped the FTS scan, so apply the
		// post-filter stack + default scope post-hoc before resolving. R2951
		if def := s.effectiveExcludeFiles(opts); def != nil {
			opts.ExcludeFiles = def
		}
		keptIDs, ferr := s.postFilterChunkIDs(chunkScoreIDs(vecResults), opts, opts.Cache)
		if ferr != nil {
			return nil, ferr
		}
		resolved, ferr := s.filterAndResolve(s.vecOnly(filterChunkScores(vecResults, keptIDs)), opts)
		if ferr != nil {
			return nil, ferr
		}
		resolved = filterByPathGlobs(resolved, opts)
		if len(resolved) > k {
			resolved = resolved[:k]
		}
		return resolved, nil
	} else {
		// FTS only
		results = s.ftsOnly(ftsResults)
	}

	if len(results) > k {
		results = results[:k]
	}
	return s.filterAndResolve(results, opts)
}

func validateSearchFlags(opts SearchOpts) error {
	// --contains and --regex can combine: contains drives FTS, regex post-filters
	if opts.LikeFile != "" && (opts.Contains != "" || len(opts.Regex) > 0) {
		return fmt.Errorf("--like-file is mutually exclusive with --contains and --regex")
	}
	// R590: --multi is mutually exclusive with --score
	if opts.Multi && opts.Score != "" {
		return fmt.Errorf("--multi and --score are mutually exclusive")
	}
	// R740: --fuzzy is mutually exclusive with --multi, --score, split flags
	if opts.Fuzzy {
		if opts.Multi {
			return fmt.Errorf("--fuzzy and --multi are mutually exclusive")
		}
		if opts.Score != "" {
			return fmt.Errorf("--fuzzy and --score are mutually exclusive")
		}
		if opts.About != "" || opts.Contains != "" || len(opts.Regex) > 0 || opts.LikeFile != "" {
			return fmt.Errorf("--fuzzy is mutually exclusive with --about, --contains, --regex, --like-file")
		}
	}
	return nil
}

// --- Chunk-level filter closures ---
// CRC: crc-Searcher.md | R1395-R1401

// ChunkFilterRow describes a single stacked filter row from the UI.
// CRC: crc-Searcher.md | R1939
type ChunkFilterRow struct {
	Polarity string `json:"polarity"` // "with" or "without"
	Mode     string `json:"mode"`     // "contains", "fuzzy", "tag", ...
	Query    string `json:"query"`
	K        int    `json:"k,omitempty"` // about-mode only: top-k override (0 = use cfg.AboutFilterTopK)
}

// resolveChunkLocation resolves a CRecord to (path, range) using the fileIDPaths map.
// CRC: crc-Searcher.md | R1395, R1867, R2959
func resolveChunkLocation(crec microfts2.CRecord, paths map[uint64]string) (string, string, bool) {
	if len(crec.FileIDs) == 0 {
		return "", "", false
	}
	fileid := crec.FileIDs[0].FileID
	path, ok := paths[fileid]
	if !ok {
		return "", "", false
	}
	// An overlay (tmp://) CRecord is never attach'd to a DB, so its db is nil;
	// calling FileRecord would deref it and panic the search actor (crashing the
	// server). Treat a DB-less record as unresolved — chunkText then returns nil
	// and the chunk filter keeps it, the existing "can't verify" degradation
	// (R1401, R2959). See TestResolveChunkLocationNilDB.
	if crec.DB() == nil {
		return "", "", false
	}
	frec, err := crec.FileRecord(fileid)
	if err != nil {
		return "", "", false
	}
	for _, entry := range frec.Chunks {
		if entry.ChunkID == crec.ChunkID {
			return path, entry.Location, true
		}
	}
	return "", "", false
}

// chunkText reads chunk text using the cache, returning nil on miss. R1401
func chunkText(crec microfts2.CRecord, cache *microfts2.ChunkCache, paths map[uint64]string) []byte {
	path, loc, ok := resolveChunkLocation(crec, paths)
	if !ok {
		return nil
	}
	text, ok := cache.ChunkText(path, loc)
	if !ok {
		return nil
	}
	return text
}

// ContainsChunkFilter returns a ChunkFilter that substring-matches chunk text.
// CRC: crc-Searcher.md | R1397
func ContainsChunkFilter(term string, cache *microfts2.ChunkCache, paths map[uint64]string) microfts2.ChunkFilter {
	lower := strings.ToLower(term)
	return func(crec microfts2.CRecord) bool {
		text := chunkText(crec, cache, paths)
		if text == nil {
			return true // R1401: can't verify → keep
		}
		return strings.Contains(strings.ToLower(string(text)), lower)
	}
}

// FuzzyChunkFilter returns a ChunkFilter that fuzzy-matches chunk text.
// CRC: crc-Searcher.md | R1398
func FuzzyChunkFilter(term string, cache *microfts2.ChunkCache, paths map[uint64]string) microfts2.ChunkFilter {
	return func(crec microfts2.CRecord) bool {
		text := chunkText(crec, cache, paths)
		if text == nil {
			return true // R1401: can't verify → keep
		}
		results := fuzzyMatch(term, []string{string(text)}, 0.01)
		return len(results) > 0 && results[0].score > 0
	}
}

// fileIDChunkFilter returns a ChunkFilter from a pre-built file ID set.
// CRC: crc-Searcher.md | R1867
func fileIDChunkFilter(fileIDs map[uint64]bool) microfts2.ChunkFilter {
	if len(fileIDs) == 0 {
		return func(crec microfts2.CRecord) bool { return false }
	}
	return func(crec microfts2.CRecord) bool {
		for _, fid := range crec.FileIDs {
			if fileIDs[fid.FileID] {
				return true
			}
		}
		return false
	}
}

// chunkIDChunkFilter returns a ChunkFilter from a pre-built chunkID set.
// Use when V/F records pin specific chunks (chunk-precise tag filters).
// CRC: crc-Searcher.md | R1399
func chunkIDChunkFilter(chunkIDs map[uint64]bool) microfts2.ChunkFilter {
	if len(chunkIDs) == 0 {
		return func(crec microfts2.CRecord) bool { return false }
	}
	return func(crec microfts2.CRecord) bool {
		return chunkIDs[crec.ChunkID]
	}
}

// TagChunkFilter returns a ChunkFilter that matches by predicate
// against the indexed tag set (T/F/V records + ExtMap + tmp overlay).
// Chunk-precise — no chunk text reads.
// CRC: crc-Searcher.md | R1399, R2442, R2451
func TagChunkFilter(p MatchPredicate, store *Store) microfts2.ChunkFilter {
	if store == nil {
		return func(microfts2.CRecord) bool { return false }
	}
	chunkIDs, _ := resolvePredicateLocations(p, store)
	return chunkIDChunkFilter(chunkIDs)
}

// FileTagChunkFilter returns a ChunkFilter that accepts any chunk
// whose primary file has at least one tag accepted by p. Resolves the
// fileID approval set once at construction; the closure performs an
// O(N) membership check against the chunk's FileIDs list.
// CRC: crc-Searcher.md | R2453, R2454, R2455, R2456
func FileTagChunkFilter(p MatchPredicate, store *Store) microfts2.ChunkFilter {
	if store == nil {
		return func(microfts2.CRecord) bool { return false }
	}
	_, fileIDs := resolvePredicateLocations(p, store)
	return fileIDChunkFilter(fileIDs)
}

// resolvePredicateLocations composes existing Store primitives to
// produce the (chunkID, fileID) sets satisfying p. Spans inline +
// ExtMap + tmp via the parity already baked into TagFiles and
// MatchTagValues. Used by both TagChunkFilter and FileTagChunkFilter.
// CRC: crc-Searcher.md | R2442, R2453, R2456
func resolvePredicateLocations(p MatchPredicate, store *Store) (map[uint64]bool, map[uint64]bool) {
	chunkIDs := make(map[uint64]bool)
	fileIDs := make(map[uint64]bool)
	names := matchingTagNames(p, store)
	if len(names) == 0 {
		return chunkIDs, fileIDs
	}
	for _, name := range names {
		if p.ValueMode == ValueAny {
			// TagFiles fans chunkIDs out to fileIDs already via the
			// chunk resolver.
			recs, _ := store.TagFiles([]string{name})
			for _, r := range recs {
				chunkIDs[r.ChunkID] = true
				if r.FileID != 0 {
					fileIDs[r.FileID] = true
				}
			}
			continue
		}
		// Value-bearing modes: iterate every value for the tag and
		// apply MatchValue. MatchTagValues(name, nil) returns every
		// (value, chunkIDs) pair across inline + ExtMap + tmp; we
		// filter here for one unified path.
		allMatches, _ := store.MatchTagValues(name, nil)
		for _, m := range allMatches {
			if !p.MatchValue(m.Value) {
				continue
			}
			for _, cid := range m.ChunkIDs {
				chunkIDs[cid] = true
			}
		}
	}
	if p.ValueMode != ValueAny && len(chunkIDs) > 0 {
		for fid := range store.FilesForChunks(chunkIDs) {
			fileIDs[fid] = true
		}
	}
	return chunkIDs, fileIDs
}

// matchingTagNames enumerates tag names accepted by p.NameMode.
// Exact mode short-circuits to a single lowercased candidate without
// touching the index — downstream lookups yield nothing if the name
// has no records.
// CRC: crc-Searcher.md | R2443
func matchingTagNames(p MatchPredicate, store *Store) []string {
	switch p.NameMode {
	case NameContains:
		names, _ := store.MatchTagNames(p.NameTokens)
		return names
	case NameRegex:
		return store.MatchNamesRegex(p.NameRE)
	default:
		return []string{strings.ToLower(p.NameStr)}
	}
}

// rowChunkFilter builds the polarity-applied ChunkFilter predicate for one
// filter row, or (nil, false) when the row has no predicate form (empty
// query, or about-mode, which resolves via the embedding pipeline). Single
// source of truth for filter-row semantics: BuildChunkFilters wraps each as
// a microfts2 option for the inline FTS scan, and postFilterChunkIDs applies
// them post-hoc for index-lookup primaries (tag/file-tag/about), so the two
// paths cannot drift apart.
// CRC: crc-Searcher.md | R1403, R1471, R2452, R2951
func rowChunkFilter(row ChunkFilterRow, cache *microfts2.ChunkCache, paths map[uint64]string, store *Store) (microfts2.ChunkFilter, bool) {
	if row.Query == "" {
		return nil, false
	}
	var filter microfts2.ChunkFilter
	switch row.Mode {
	case "contains":
		filter = ContainsChunkFilter(row.Query, cache, paths)
	case "fuzzy":
		filter = FuzzyChunkFilter(row.Query, cache, paths)
	case "regex":
		// On the post-hoc path regex is a chunk-text predicate; the inline
		// FTS path keeps the dedicated microfts2 regex option (more efficient
		// over the candidate posting lists). Same RE2 semantics either way.
		re, err := regexp.Compile(row.Query)
		if err != nil {
			return nil, false
		}
		filter = func(crec microfts2.CRecord) bool {
			text := chunkText(crec, cache, paths)
			if text == nil {
				return true // R1401: can't verify -> keep
			}
			return re.Match(text)
		}
	case "tag":
		// R2442, R2451, R2452: sigil match syntax [~|:]NAME [(=|:|~) VALUE].
		if store == nil {
			return nil, false
		}
		p, err := ParseMatchSyntax(row.Query)
		if err != nil {
			return nil, false
		}
		filter = TagChunkFilter(p, store)
	case "file-tag":
		// R2453: file-level predicate -- every chunk in a file that has the
		// tag is accepted, regardless of the chunk's own content.
		if store == nil {
			return nil, false
		}
		p, err := ParseMatchSyntax(row.Query)
		if err != nil {
			return nil, false
		}
		filter = FileTagChunkFilter(p, store)
	case "files":
		// R1770/R950: glob match against file paths -> file ID set.
		filter = fileIDChunkFilter(matchFilesGlob(row.Query, paths))
	default:
		return nil, false
	}
	if row.Polarity == "without" { // R1400
		orig := filter
		filter = func(crec microfts2.CRecord) bool { return !orig(crec) }
	}
	return filter, true
}

// BuildChunkFilters converts UI filter rows into microfts2 search options
// for the inline FTS scan. Regex keeps the dedicated microfts2 option; every
// other mode is built from rowChunkFilter so the inline semantics match the
// post-hoc funnel exactly (R2951).
// CRC: crc-Searcher.md | R1403, R1471, R2452, R2951
func BuildChunkFilters(rows []ChunkFilterRow, cache *microfts2.ChunkCache, paths map[uint64]string, store *Store) []microfts2.SearchOption {
	var opts []microfts2.SearchOption
	for _, row := range rows {
		if row.Query == "" {
			continue
		}
		// Regex mode uses dedicated microfts2 options (more efficient).
		// CRC: crc-Server.md | R1404
		if row.Mode == "regex" {
			if row.Polarity == "without" {
				opts = append(opts, microfts2.WithExceptRegex(row.Query))
			} else {
				opts = append(opts, microfts2.WithRegexFilter(row.Query))
			}
			continue
		}
		filter, ok := rowChunkFilter(row, cache, paths, store)
		if !ok {
			continue
		}
		opts = append(opts, microfts2.WithChunkFilter(filter))
	}
	return opts
}

// effectiveExcludeFiles returns the default search_exclude scope to inject
// when the caller supplied no explicit file filter. A positive `-files`
// chunk-filter row (the user asking for a specific path set) or any explicit
// --filter-files/--exclude-files disables it. Shared by resolveFilters (FTS
// path) and the index-lookup funnel so every primary mode applies the same
// default. R939, R940.
// CRC: crc-Searcher.md | R939, R940, R2951
func (s *Searcher) effectiveExcludeFiles(opts SearchOpts) []string {
	if s.config == nil || len(s.config.SearchExclude) == 0 {
		return nil
	}
	if len(opts.FilterFiles) > 0 || len(opts.ExcludeFiles) > 0 {
		return nil
	}
	for _, cf := range opts.ChunkFilters {
		if cf.Polarity == "with" && cf.Mode == "files" {
			return nil
		}
	}
	return s.config.SearchExclude
}

// postFilterChunkIDs applies the user post-filter stack to a non-FTS
// candidate set. Index-lookup primaries (tag/file-tag, and the about
// vec-only path) skip the inline FTS WithChunkFilter scan, so they route
// their resolved chunkIDs through here: every opts.ChunkFilters row except
// about-mode (resolved via the embedding pipeline, applied separately)
// becomes a rowChunkFilter predicate, evaluated against each candidate's
// CRecord. Returns the chunkIDs passing every predicate, order preserved.
// CRC: crc-Searcher.md | Seq: seq-search.md | R2951
func (s *Searcher) postFilterChunkIDs(chunkIDs []uint64, opts SearchOpts, cache *microfts2.ChunkCache) ([]uint64, error) {
	if len(chunkIDs) == 0 {
		return chunkIDs, nil
	}
	paths, err := s.fts.FileIDPaths()
	if err != nil {
		return nil, err
	}
	if cache == nil {
		cache = s.fts.NewChunkCache()
	}
	var preds []microfts2.ChunkFilter
	for _, row := range opts.ChunkFilters {
		// about: resolved via the embedding pipeline, applied separately.
		// files: a path predicate, applied at the resolved-path level by
		// filterByPathGlobs (a deduplicated chunk belongs to several files,
		// so the path it is reported under is what `-files` must match).
		if row.Mode == "about" || row.Mode == "files" {
			continue
		}
		if f, ok := rowChunkFilter(row, cache, paths, s.store); ok {
			preds = append(preds, f)
		}
	}
	if len(preds) == 0 {
		return chunkIDs, nil
	}
	kept := make([]uint64, 0, len(chunkIDs))
	err = s.fts.DB().View(func(txn *bbolt.Tx) error {
		for _, cid := range chunkIDs {
			crec, rerr := s.fts.ReadCRecord(txn, cid)
			if rerr != nil {
				continue // stale chunkID -- ChunksByID would skip it anyway
			}
			// ReadCRecord decodes the value; the chunkID is the key, so set
			// it here -- chunkText/tag predicates resolve via crec.ChunkID.
			crec.ChunkID = cid
			keep := true
			for _, p := range preds {
				if !p(crec) {
					keep = false
					break
				}
			}
			if keep {
				kept = append(kept, cid)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return kept, nil
}

// filterByPathGlobs applies the structural FilterFiles (with) and
// ExcludeFiles (without, including the injected default search_exclude
// scope) path globs to a resolved candidate set via the shared Matcher.
// Extracted from SearchTagChunks so the about vec-only path reuses it.
// CRC: crc-Searcher.md | R2951
func filterByPathGlobs(results []SearchResultEntry, opts SearchOpts) []SearchResultEntry {
	// R2951: -files post-filter rows are path predicates, applied here against
	// the reported path (with => must match its glob; without => must not).
	type filesRow struct {
		glob   string
		negate bool
	}
	var fileRows []filesRow
	for _, row := range opts.ChunkFilters {
		if row.Mode == "files" && row.Query != "" {
			fileRows = append(fileRows, filesRow{ExpandTilde(row.Query), row.Polarity == "without"})
		}
	}
	if len(opts.FilterFiles) == 0 && len(opts.ExcludeFiles) == 0 && len(fileRows) == 0 {
		return results
	}
	m := &Matcher{Dotfiles: true}
	filtered := results[:0]
	for _, r := range results {
		if len(opts.FilterFiles) > 0 {
			include := false
			for _, pat := range opts.FilterFiles {
				if m.Match(pat, r.Path, "", false) {
					include = true
					break
				}
			}
			if !include {
				continue
			}
		}
		excluded := false
		for _, pat := range opts.ExcludeFiles {
			if m.Match(pat, r.Path, "", false) {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		// -files rows AND together at their polarity against the reported path.
		passes := true
		for _, fr := range fileRows {
			if pathMatchesGlob(fr.glob, r.Path) == fr.negate {
				passes = false
				break
			}
		}
		if !passes {
			continue
		}
		filtered = append(filtered, r)
	}
	return filtered
}

// chunkScoreIDs extracts the chunkID slice from a ChunkScore list (order
// preserved) so the vec-only candidate set can run through the funnel.
// CRC: crc-Searcher.md | R2951
func chunkScoreIDs(scores []ChunkScore) []uint64 {
	ids := make([]uint64, len(scores))
	for i, cs := range scores {
		ids[i] = cs.ChunkID
	}
	return ids
}

// filterChunkScores keeps only the ChunkScores whose chunkID survived the
// post-filter funnel, preserving order.
// CRC: crc-Searcher.md | R2951
func filterChunkScores(scores []ChunkScore, keep []uint64) []ChunkScore {
	if len(keep) == len(scores) {
		return scores // funnel dropped nothing
	}
	set := make(map[uint64]struct{}, len(keep))
	for _, id := range keep {
		set[id] = struct{}{}
	}
	out := scores[:0]
	for _, cs := range scores {
		if _, ok := set[cs.ChunkID]; ok {
			out = append(out, cs)
		}
	}
	return out
}

// matchFilesGlob returns the set of fileIDs whose path matches glob —
// either by basename (`filepath.Match`) or full-path doublestar. A leading
// `~/` (or `~user/`) is expanded first, so `-files '~/.claude/projects/**'`
// works like the `search_exclude` config: shell single-quotes keep the
// tilde literal, so ark expands it (indexed paths are absolute). R1770, R950
// CRC: crc-Searcher.md | R1770, R950
func matchFilesGlob(glob string, paths map[uint64]string) map[uint64]bool {
	glob = ExpandTilde(glob)
	fileIDs := make(map[uint64]bool)
	for fid, p := range paths {
		if pathMatchesGlob(glob, p) {
			fileIDs[fid] = true
		}
	}
	return fileIDs
}

// pathMatchesGlob reports whether one path matches an (already tilde-expanded)
// glob, by basename (filepath.Match) or full-path doublestar — the same test
// matchFilesGlob applies per file. Used by the path-level `-files` post-filter
// (filterByPathGlobs) so a `-files` row matches the file each result is
// reported under, not any file a content-deduplicated chunk happens to share.
// CRC: crc-Searcher.md | R1770, R950, R2951
func pathMatchesGlob(glob, path string) bool {
	if matched, _ := filepath.Match(glob, filepath.Base(path)); matched {
		return true
	}
	matched, _ := doublestar.Match(glob, path)
	return matched
}

// AboutResolution bundles everything ResolveAboutFilters returns.
// CRC: crc-Searcher.md | R1932, R1935
type AboutResolution struct {
	Remaining       []ChunkFilterRow         // filter rows with about rows stripped
	AboutResults    []ChunkScore             // top-k for primary --about (nil when none requested)
	HasAboutResults bool                     // primary --about was processed (even if empty)
	ChunkFilterOpts []microfts2.SearchOption // WithChunkFilter closures for each about filter row
	AboutFilterSets []AboutFilterSet         // raw chunkID sets so vec-only paths can filter too
	Early           []microfts2.SearchOption // centroid pre-filter (file-level WithOnly)
	Late            []microfts2.SearchOption // centroid pre-filter (file-level WithExcept)
}

// AboutFilterSet pairs the chunkID membership of an about-filter row
// with its polarity, so callers (vec-only paths) can apply the same
// filter the FTS WithChunkFilter closures would.
// CRC: crc-Searcher.md | R1935
type AboutFilterSet struct {
	ChunkIDs map[uint64]struct{}
	Polarity string // "with" or "without"
}

// ResolveAboutFilters orchestrates every about query in one search:
// the primary `--about` (if primaryAbout != "") and every Mode ==
// "about" chunk-filter row are submitted as a single
// `Librarian.SearchChunksMulti` call, sharing one EC walk.
//
// Outputs:
//   - Remaining: filter rows with about rows stripped (caller passes
//     these to BuildChunkFilters).
//   - AboutResults: top-k chunks for the primary query (HasAboutResults
//     reports whether the slot is meaningful).
//   - ChunkFilterOpts: WithChunkFilter closures gating chunks on the
//     per-row chunkID set (`with` polarity = membership, `without`
//     polarity = inverted).
//   - Early/Late: centroid pre-filter `WithOnly`/`WithExcept` options
//     emitted only when cfg.AboutCentroidFilter is true.
//
// When the embedding pipeline is unavailable, primary --about is
// fatal (returns err) so the caller can decide on FTS fallback.
// About-mode filter rows are dropped with a logged warning instead.
//
// CRC: crc-Searcher.md | Seq: seq-search.md | R1787, R1921, R1934, R1935
func ResolveAboutFilters(rows []ChunkFilterRow, primaryAbout string, primaryK int, lib *Librarian, store *Store, cfg *Config) (AboutResolution, error) {
	var res AboutResolution
	var aboutRows []ChunkFilterRow
	for _, row := range rows {
		if row.Mode == "about" && row.Query != "" {
			aboutRows = append(aboutRows, row)
		} else {
			res.Remaining = append(res.Remaining, row)
		}
	}

	hasPrimary := primaryAbout != ""
	if !hasPrimary && len(aboutRows) == 0 {
		return res, nil
	}

	if lib == nil || !lib.EmbeddingAvailable() {
		if hasPrimary {
			return res, fmt.Errorf("embedding pipeline unavailable")
		}
		log.Printf("about filter: embedding pipeline unavailable, dropping %d filter row(s)", len(aboutRows))
		return res, nil
	}

	// CRC: crc-Config.md | R1920 — centroid pre-filter cosine gate.
	threshold := 0.3
	if cfg != nil {
		threshold = cfg.AboutCentroidThreshold
	}

	// Combined chunk-level walk: primary --about and every about
	// filter row are top-K requests sharing one EC cursor scan.
	// Filter rows convert their TopK into a chunkID membership set.
	// CRC: crc-Searcher.md | R1932, R1935
	filterTopK := 200
	if cfg != nil && cfg.AboutFilterTopK > 0 {
		filterTopK = cfg.AboutFilterTopK
	}
	var reqs []AboutRequest
	primaryReqIdx := -1
	if hasPrimary {
		qvec, err := lib.EmbedQuery(primaryAbout)
		if err != nil {
			return res, fmt.Errorf("embed primary about: %w", err)
		}
		primaryReqIdx = len(reqs)
		reqs = append(reqs, AboutRequest{QueryVec: qvec, K: primaryK})
	}
	var metas []aboutFilterMeta
	for _, row := range aboutRows {
		qvec, err := lib.EmbedQuery(row.Query)
		if err != nil {
			log.Printf("about filter: embed %q: %v — dropping", row.Query, err)
			continue
		}
		k := row.K
		if k <= 0 {
			k = filterTopK
		}
		metas = append(metas, aboutFilterMeta{row: row, reqIdx: len(reqs), queryVec: qvec})
		reqs = append(reqs, AboutRequest{QueryVec: qvec, K: k})
	}

	multi, err := lib.SearchChunksMulti(reqs)
	if err != nil {
		return res, fmt.Errorf("about multi-search: %w", err)
	}

	if hasPrimary && primaryReqIdx >= 0 && primaryReqIdx < len(multi) {
		res.AboutResults = multi[primaryReqIdx].TopK
		res.HasAboutResults = true
	}
	for _, m := range metas {
		topK := multi[m.reqIdx].TopK
		if len(topK) == 0 {
			continue
		}
		set := make(map[uint64]struct{}, len(topK))
		for _, cs := range topK {
			set[cs.ChunkID] = struct{}{}
		}
		res.AboutFilterSets = append(res.AboutFilterSets, AboutFilterSet{
			ChunkIDs: set,
			Polarity: m.row.Polarity,
		})
		filter := func(crec microfts2.CRecord) bool {
			_, ok := set[crec.ChunkID]
			return ok
		}
		if m.row.Polarity == "without" { // R1400
			orig := filter
			filter = func(crec microfts2.CRecord) bool { return !orig(crec) }
		}
		res.ChunkFilterOpts = append(res.ChunkFilterOpts, microfts2.WithChunkFilter(filter))
	}

	// Optional centroid pre-filter — file-level WithOnly/WithExcept.
	// Reuses the embeddings we already computed above.
	if cfg != nil && cfg.AboutCentroidFilter && store != nil {
		centroids, cerr := store.ScanFileCentroids()
		if cerr == nil && len(centroids) > 0 {
			res.Early, res.Late = computeCentroidFilters(metas, centroids, threshold, primaryK)
		}
	}

	return res, nil
}

// aboutFilterMeta carries per-row state across the chunk-level walk
// and the optional file-level centroid filter.
type aboutFilterMeta struct {
	row      ChunkFilterRow
	reqIdx   int
	queryVec []float32
}

// computeCentroidFilters scores each about filter's query vector
// against every file centroid, keeping files above the threshold,
// and emits per-row WithOnly (with) or WithExcept (without).
// CRC: crc-Searcher.md | R1921
func computeCentroidFilters(metas []aboutFilterMeta, centroids map[uint64][]float32, threshold float64, k int) (early, late []microfts2.SearchOption) {
	for _, m := range metas {
		type scored struct {
			fileID uint64
			score  float64
		}
		var matches []scored
		for fid, centroid := range centroids {
			s := cosineSimilarity(m.queryVec, centroid)
			if s > threshold {
				matches = append(matches, scored{fid, s})
			}
		}
		if len(matches) == 0 {
			continue
		}
		sort.Slice(matches, func(i, j int) bool { return matches[i].score > matches[j].score })
		limit := k * 2
		if limit > 0 && len(matches) > limit {
			matches = matches[:limit]
		}
		fileIDs := make(map[uint64]struct{}, len(matches))
		for _, mm := range matches {
			fileIDs[mm.fileID] = struct{}{}
		}
		if m.row.Polarity == "without" {
			late = append(late, microfts2.WithExcept(fileIDs))
		} else {
			early = append(early, microfts2.WithOnly(fileIDs))
		}
	}
	return
}

// CRC: crc-Searcher.md | R215-R227, R512-R516
// resolveFilters builds a microfts2 search option from all filter flags.
// Path filters first (cheap), then content filters. Positives intersect,
// negatives subtract. Returns nil if no filtering is requested.
func (s *Searcher) resolveFilters(opts SearchOpts) (microfts2.SearchOption, error) {
	// R939, R940: inject the default search_exclude scope when the caller
	// supplied no explicit file filter. Shared with the index-lookup funnel
	// via effectiveExcludeFiles so every primary mode applies the same
	// default; a positive `-files` row or explicit flags disable it.
	if def := s.effectiveExcludeFiles(opts); def != nil {
		opts.ExcludeFiles = def
	}
	// R950: expand a leading `~/` on the explicit file-glob flags so they
	// behave like the (already tilde-expanded) search_exclude config. No-op
	// for globs without a leading tilde. SearchExclude is pre-expanded at
	// config load, so re-expanding here is idempotent.
	opts.FilterFiles = ExpandTildeSlice(opts.FilterFiles)
	opts.ExcludeFiles = ExpandTildeSlice(opts.ExcludeFiles)
	hasFilters := len(opts.Filter) > 0 || len(opts.Except) > 0 ||
		len(opts.FilterFiles) > 0 || len(opts.ExcludeFiles) > 0 ||
		len(opts.FilterFileTags) > 0 || len(opts.ExcludeFileTags) > 0
	if !hasFilters {
		return nil, nil
	}

	// Get all indexed files for path matching and ID resolution.
	// Uses FileIDPaths (N records, ~318 KB) instead of StaleFiles
	// (F records, ~26 MB) — we only need fileid↔path mapping here.
	pathIndex, err := s.fts.FileIDPaths()
	if err != nil {
		return nil, fmt.Errorf("resolve filters: %w", err)
	}

	// Start with all file IDs (will narrow down)
	allIDs := make(map[uint64]struct{}, len(pathIndex))
	for id := range pathIndex {
		allIDs[id] = struct{}{}
	}

	// Track whether we have any positive filters (which means "only these")
	hasPositive := len(opts.FilterFiles) > 0 || len(opts.Filter) > 0 || len(opts.FilterFileTags) > 0
	included := allIDs // start with all; narrow if positive filters exist
	if hasPositive {
		included = nil // will be built by intersection
	}

	// Path-based positive filters (--filter-files): intersect
	if len(opts.FilterFiles) > 0 {
		m := &Matcher{Dotfiles: true}
		matched := make(map[uint64]struct{})
		for id, path := range pathIndex {
			for _, pat := range opts.FilterFiles {
				if m.Match(pat, path, "", false) {
					matched[id] = struct{}{}
					break
				}
			}
		}
		included = matched
	}

	// Tag-based positive filters (--filter-file-tags): intersect
	if len(opts.FilterFileTags) > 0 {
		records, err := s.store.TagFiles(opts.FilterFileTags)
		if err != nil {
			return nil, fmt.Errorf("filter-file-tags: %w", err)
		}
		matched := make(map[uint64]struct{})
		for _, rec := range records {
			matched[rec.FileID] = struct{}{}
		}
		if included == nil {
			included = matched
		} else {
			for id := range included {
				if _, ok := matched[id]; !ok {
					delete(included, id)
				}
			}
		}
	}

	// Content-based positive filters (--filter): intersect
	for _, query := range opts.Filter {
		fr, err := s.fts.Search(query)
		if err != nil {
			return nil, fmt.Errorf("filter query %q: %w", query, err)
		}
		matched := make(map[uint64]struct{})
		for _, r := range fr.Results {
			status, err := s.fts.CheckFile(r.Path)
			if err != nil {
				continue
			}
			matched[status.FileID] = struct{}{}
		}
		if included == nil {
			included = matched
		} else {
			// Intersect
			for id := range included {
				if _, ok := matched[id]; !ok {
					delete(included, id)
				}
			}
		}
	}

	if included == nil {
		included = allIDs
	}

	// Path-based negative filters (--exclude-files): subtract
	if len(opts.ExcludeFiles) > 0 {
		m := &Matcher{Dotfiles: true}
		for id, path := range pathIndex {
			for _, pat := range opts.ExcludeFiles {
				if m.Match(pat, path, "", false) {
					delete(included, id)
					break
				}
			}
		}
	}

	// Content-based negative filters (--except): subtract
	for _, query := range opts.Except {
		fr, err := s.fts.Search(query)
		if err != nil {
			return nil, fmt.Errorf("except query %q: %w", query, err)
		}
		for _, r := range fr.Results {
			status, err := s.fts.CheckFile(r.Path)
			if err != nil {
				continue
			}
			delete(included, status.FileID)
		}
	}

	// Tag-based negative filters (--exclude-file-tags): subtract
	if len(opts.ExcludeFileTags) > 0 {
		records, err := s.store.TagFiles(opts.ExcludeFileTags)
		if err != nil {
			return nil, fmt.Errorf("exclude-file-tags: %w", err)
		}
		for _, rec := range records {
			delete(included, rec.FileID)
		}
	}

	return microfts2.WithOnly(included), nil
}

// merge combines results from both engines by (FileID, ChunkID).
// CRC: crc-Searcher.md | R1918
func (s *Searcher) merge(ftsResults []microfts2.SearchResult, vecResults []ChunkScore) []SearchResultEntry {
	m := make(map[chunkKey]*SearchResultEntry)
	cache := s.newFTSKeyCache()

	var tmpResults []SearchResultEntry
	for _, r := range ftsResults {
		// Overlay results (tmp://) can't merge with vec — collect separately.
		if strings.HasPrefix(r.Path, "tmp://") {
			tmpResults = append(tmpResults, SearchResultEntry{
				Path:     r.Path,
				Range:    r.Range,
				FTSScore: r.Score,
				Score:    r.Score,
			})
			continue
		}
		key, ok := cache.resolve(r)
		if !ok {
			continue
		}
		entry, exists := m[key]
		if !exists {
			entry = &SearchResultEntry{
				FileID:   key.FileID,
				ChunkNum: key.ChunkNum,
			}
			m[key] = entry
		}
		entry.FTSScore = r.Score
	}

	for _, r := range vecResults {
		key, ok := cache.resolveChunkScore(r)
		if !ok {
			continue
		}
		entry, exists := m[key]
		if !exists {
			entry = &SearchResultEntry{
				FileID:   key.FileID,
				ChunkNum: key.ChunkNum,
			}
			m[key] = entry
		}
		entry.VecScore = r.Score
	}

	results := make([]SearchResultEntry, 0, len(m)+len(tmpResults))
	for _, entry := range m {
		entry.Score = entry.FTSScore + entry.VecScore
		results = append(results, *entry)
	}
	results = append(results, tmpResults...)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	return results
}

// intersect keeps only chunks present in both result sets.
// CRC: crc-Searcher.md | R1918
func (s *Searcher) intersect(ftsResults []microfts2.SearchResult, vecResults []ChunkScore) []SearchResultEntry {
	cache := s.newFTSKeyCache()
	vecMap := make(map[chunkKey]float64)
	for _, r := range vecResults {
		key, ok := cache.resolveChunkScore(r)
		if !ok {
			continue
		}
		vecMap[key] = r.Score
	}

	var results []SearchResultEntry
	for _, r := range ftsResults {
		key, ok := cache.resolve(r)
		if !ok {
			continue
		}
		if vecScore, found := vecMap[key]; found {
			results = append(results, SearchResultEntry{
				FileID:   key.FileID,
				ChunkNum: key.ChunkNum,
				FTSScore: r.Score,
				VecScore: vecScore,
				Score:    r.Score + vecScore,
			})
		}
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	return results
}

// vecOnly converts ChunkScore results into SearchResultEntry rows.
// CRC: crc-Searcher.md | R1918
func (s *Searcher) vecOnly(vecResults []ChunkScore) []SearchResultEntry {
	cache := s.newFTSKeyCache()
	results := make([]SearchResultEntry, 0, len(vecResults))
	for _, r := range vecResults {
		key, ok := cache.resolveChunkScore(r)
		if !ok {
			continue
		}
		results = append(results, SearchResultEntry{
			FileID:   key.FileID,
			ChunkNum: key.ChunkNum,
			VecScore: r.Score,
			Score:    r.Score,
		})
	}
	return results
}

func (s *Searcher) ftsOnly(ftsResults []microfts2.SearchResult) []SearchResultEntry {
	cache := s.newFTSKeyCache()
	var results []SearchResultEntry
	for _, r := range ftsResults {
		// Overlay results (tmp://) carry Path and Range directly.
		if strings.HasPrefix(r.Path, "tmp://") {
			results = append(results, SearchResultEntry{
				Path:     r.Path,
				Range:    r.Range,
				FTSScore: r.Score,
				Score:    r.Score,
			})
			continue
		}
		key, ok := cache.resolve(r)
		if !ok {
			continue
		}
		results = append(results, SearchResultEntry{
			FileID:   key.FileID,
			ChunkNum: key.ChunkNum,
			FTSScore: r.Score,
			Score:    r.Score,
		})
	}
	return results
}

// filterAndResolve resolves file paths and chunk ranges for search results.
// Date filtering is now handled by microfts2 WithAfter/WithBefore at search time.
func (s *Searcher) filterAndResolve(results []SearchResultEntry, opts SearchOpts) ([]SearchResultEntry, error) {
	var resolved []SearchResultEntry
	for _, r := range results {
		// Overlay results already have Path and Range resolved.
		if r.Path != "" {
			resolved = append(resolved, r)
			continue
		}
		info, err := s.fts.FileInfoByID(r.FileID)
		if err != nil {
			continue
		}
		r.Path = info.Names[0]
		cn := int(r.ChunkNum)
		if cn < len(info.Chunks) {
			r.Range = info.Chunks[cn].Location
		}
		resolved = append(resolved, r)
	}
	return resolved, nil
}

// FillChunks reads chunk text for each result using a fresh per-query ChunkCache.
// R115: content comes from the index, not a fresh permission-gated file read.
// R116: chunk text is stored at index time, so retrieval needs no embedding model.
// CRC: crc-Searcher.md | R108, R115, R116, R605, R653
func (s *Searcher) FillChunks(results []SearchResultEntry) ([]SearchResultEntry, error) {
	return s.FillChunksUsing(results, nil)
}

// SearchTagChunks resolves a tag-derived chunkID set to a flat
// SearchResultEntry list -- bypasses FTS. R2951: routes the candidate set
// through the post-filter funnel (default search_exclude scope + the user
// post-filter stack + path globs) so post-filtering is
// primary-mode-independent. Caps to opts.K. Caller fills chunks/files.
// CRC: crc-Searcher.md | Seq: seq-search.md | R2442, R2951
func (s *Searcher) SearchTagChunks(chunkIDs []uint64, opts SearchOpts) ([]SearchResultEntry, error) {
	if def := s.effectiveExcludeFiles(opts); def != nil {
		opts.ExcludeFiles = def
	}
	chunkIDs, err := s.postFilterChunkIDs(chunkIDs, opts, opts.Cache)
	if err != nil {
		return nil, err
	}
	results, err := s.ChunksByID(chunkIDs)
	if err != nil {
		return nil, err
	}
	results = filterByPathGlobs(results, opts)
	k := opts.K
	if k == 0 {
		k = 20
	}
	if len(results) > k {
		results = results[:k]
	}
	return results, nil
}

// GroupTagChunks resolves a tag-derived chunkID set straight to GroupedResult
// — bypasses FTS. Wraps SearchTagChunks with chunk-text fill and the shared
// grouping/preview pipeline used by SearchGrouped.
// CRC: crc-Searcher.md | R2442
func (s *Searcher) GroupTagChunks(chunkIDs []uint64, opts SearchOpts) ([]GroupedResult, error) {
	results, err := s.SearchTagChunks(chunkIDs, opts)
	if err != nil {
		return nil, err
	}
	results, err = s.FillChunksUsing(results, opts.Cache)
	if err != nil {
		return nil, err
	}
	return s.groupResults(results, nil), nil
}

// ChunksByID resolves a chunkID set to SearchResultEntry list directly —
// no FTS pass. Reads each C record for FileID, skips stale chunkIDs whose
// C records no longer exist, and looks up Range via the F record. Used by
// tag queries: V records pin exact chunkIDs, so FTS is redundant.
// CRC: crc-Searcher.md | R1399
func (s *Searcher) ChunksByID(chunkIDs []uint64) ([]SearchResultEntry, error) {
	if len(chunkIDs) == 0 {
		return nil, nil
	}
	paths, err := s.fts.FileIDPaths()
	if err != nil {
		return nil, err
	}
	rangeCache := make(map[uint64]map[uint64]string) // fileID → chunkID → range
	var entries []SearchResultEntry
	err = s.fts.DB().View(func(txn *bbolt.Tx) error {
		for _, cid := range chunkIDs {
			crec, rerr := s.fts.ReadCRecord(txn, cid)
			if rerr != nil || len(crec.FileIDs) == 0 {
				continue
			}
			fid := crec.FileIDs[0].FileID
			path, ok := paths[fid]
			if !ok {
				continue
			}
			ranges, cached := rangeCache[fid]
			if !cached {
				frec, ferr := s.fts.FileInfoByID(fid)
				if ferr != nil {
					rangeCache[fid] = nil
					continue
				}
				ranges = make(map[uint64]string, len(frec.Chunks))
				for _, fce := range frec.Chunks {
					ranges[fce.ChunkID] = fce.Location
				}
				rangeCache[fid] = ranges
			}
			rng, ok := ranges[cid]
			if !ok {
				continue
			}
			entries = append(entries, SearchResultEntry{
				Path:     path,
				Range:    rng,
				FileID:   fid,
				ChunkNum: cid,
				Score:    1,
			})
		}
		return nil
	})
	return entries, err
}

// FillChunksUsing reads chunk text using the provided cache, or creates a
// fresh per-query cache if cache is nil. The session path provides a non-nil
// cache so that successive searches reuse cached file reads.
// CRC: crc-Searcher.md | R652
func (s *Searcher) FillChunksUsing(results []SearchResultEntry, cache *microfts2.ChunkCache) ([]SearchResultEntry, error) {
	if cache == nil {
		cache = s.fts.NewChunkCache()
	}
	for i := range results {
		r := &results[i]
		content, ok := cache.ChunkText(r.Path, r.Range)
		if !ok {
			continue
		}
		r.Text = string(content)
		// R1705: for PDF chunks, also populate Attrs so RenderPreview can
		// emit <pdf-chunk> elements with tag_rects-derived overlays. We
		// gate on strategy to avoid the extra cost for non-PDF files.
		if info, err := s.fts.FileInfoByID(r.FileID); err == nil && info.Strategy == "pdf" {
			if chunks, err := cache.GetChunks(r.Path, r.Range, 0, 0); err == nil && len(chunks) == 1 {
				r.Attrs = chunks[0].Attrs
			}
		}
	}
	return results, nil
}

// FillFiles deduplicates results by file and reads full content.
// Multiple chunk hits from one file → one entry with best score.
// CRC: crc-Searcher.md | R109, R111
func (s *Searcher) FillFiles(results []SearchResultEntry) ([]SearchResultEntry, error) {
	type fileEntry struct {
		idx   int
		score float64
	}
	seen := make(map[uint64]*fileEntry)
	var deduped []SearchResultEntry

	for _, r := range results {
		if fe, ok := seen[r.FileID]; ok {
			if r.Score > fe.score {
				deduped[fe.idx].Score = r.Score
				fe.score = r.Score
			}
			continue
		}
		seen[r.FileID] = &fileEntry{idx: len(deduped), score: r.Score}
		deduped = append(deduped, r)
	}

	for i := range deduped {
		r := &deduped[i]
		info, err := s.fts.FileInfoByID(r.FileID)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(info.Names[0])
		if err != nil {
			continue
		}
		r.Text = string(data)
		r.Range = ""
	}
	return deduped, nil
}

// parseRange parses a "start-end" range string into 1-based line numbers.
// Returns (0, 0) if the range cannot be parsed.
func parseRange(r string) (startLine, endLine int) {
	parts := strings.SplitN(r, "-", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	s, err1 := strconv.Atoi(parts[0])
	e, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0
	}
	return s, e
}

// extractByRange extracts text from lines using a "start-end" range string.
// Lines are 1-based. Returns the joined lines including trailing newline.
func extractByRange(lines []string, rangeStr string) string {
	start, end := parseRange(rangeStr)
	if start == 0 && end == 0 {
		return ""
	}
	// Convert to 0-based index
	si := start - 1
	ei := end
	if si < 0 {
		si = 0
	}
	if ei > len(lines) {
		ei = len(lines)
	}
	if si >= ei {
		return ""
	}
	return strings.Join(lines[si:ei], "\n") + "\n"
}

// ftsKeyCache caches LMDB lookups for FTS result resolution.
type ftsKeyCache struct {
	s        *Searcher
	fileIDs  map[string]uint64
	fileInfo map[uint64]microfts2.FRecord
}

func (s *Searcher) newFTSKeyCache() *ftsKeyCache {
	return &ftsKeyCache{
		s:        s,
		fileIDs:  make(map[string]uint64),
		fileInfo: make(map[uint64]microfts2.FRecord),
	}
}

func (c *ftsKeyCache) resolve(r microfts2.SearchResult) (chunkKey, bool) {
	fileID, ok := c.fileIDs[r.Path]
	if !ok {
		status, err := c.s.fts.CheckFile(r.Path)
		if err != nil {
			return chunkKey{}, false
		}
		fileID = status.FileID
		c.fileIDs[r.Path] = fileID
	}
	info, ok := c.fileInfo[fileID]
	if !ok {
		var err error
		info, err = c.s.fts.FileInfoByID(fileID)
		if err != nil {
			return chunkKey{}, false
		}
		c.fileInfo[fileID] = info
	}
	cn := chunkNumForRange(info, r.Range)
	return chunkKey{FileID: fileID, ChunkNum: cn}, true
}

// resolveChunkScore converts a ChunkScore (FileID + ChunkID) to a
// chunkKey (FileID + position-in-file) for merge/intersect keying.
// CRC: crc-Searcher.md | R1918
func (c *ftsKeyCache) resolveChunkScore(cs ChunkScore) (chunkKey, bool) {
	info, ok := c.fileInfo[cs.FileID]
	if !ok {
		var err error
		info, err = c.s.fts.FileInfoByID(cs.FileID)
		if err != nil {
			return chunkKey{}, false
		}
		c.fileInfo[cs.FileID] = info
	}
	for i, cr := range info.Chunks {
		if cr.ChunkID == cs.ChunkID {
			return chunkKey{FileID: cs.FileID, ChunkNum: uint64(i)}, true
		}
	}
	return chunkKey{}, false
}

// TagLocation is one chunk where a (tag, value) pair appears.
// CRC: crc-Searcher.md | R2433
type TagLocation struct {
	Path  string `json:"path"`
	Range string `json:"range"`
}

// TagValueGroup is the chunks containing a single (tag, value) pair.
// Count is the chunk count; Locations is the per-chunk path:range list.
// Value may be empty when the @tag: appeared with no value text.
// CRC: crc-Searcher.md | R2433, R2434
type TagValueGroup struct {
	Value     string        `json:"value"`
	Count     int           `json:"count"`
	Locations []TagLocation `json:"locations,omitempty"`
}

// TagResult is a tag found in search results, with its value/location
// breakdown. Count is the chunk count across all values; FileCount is the
// number of distinct files containing the tag.
// CRC: crc-Searcher.md | R2433, R2434
type TagResult struct {
	Tag       string          `json:"tag"`
	Count     int             `json:"count"`
	BestScore float64         `json:"bestScore,omitempty"`
	FileCount int             `json:"fileCount,omitempty"`
	Values    []TagValueGroup `json:"values,omitempty"`
}

// ExtractResultTags scans result chunk texts for @tag: value patterns,
// skipping mentions inside markdown fenced/indented code blocks. Each
// chunk contributes at most one location per (tag, value) pair — repeated
// inline occurrences within the same chunk don't inflate counts. Results
// must have Text populated (via FillChunks).
// CRC: crc-Searcher.md | R2433, R2434
func ExtractResultTags(results []SearchResultEntry) []TagResult {
	type groupKey struct{ tag, value string }
	groups := make(map[string]map[string][]TagLocation)
	bestScores := make(map[string]float64)
	files := make(map[string]map[string]struct{})

	for _, r := range results {
		seen := make(map[groupKey]bool)
		for _, tv := range ExtractTagValues([]byte(r.Text), "markdown") {
			key := groupKey{tag: tv.Tag, value: tv.Value}
			if seen[key] {
				continue
			}
			seen[key] = true
			if groups[tv.Tag] == nil {
				groups[tv.Tag] = make(map[string][]TagLocation)
				files[tv.Tag] = make(map[string]struct{})
			}
			groups[tv.Tag][tv.Value] = append(groups[tv.Tag][tv.Value], TagLocation{Path: r.Path, Range: r.Range})
			files[tv.Tag][r.Path] = struct{}{}
			if r.Score > bestScores[tv.Tag] {
				bestScores[tv.Tag] = r.Score
			}
		}
	}

	tags := make([]TagResult, 0, len(groups))
	for tag, valMap := range groups {
		tr := TagResult{Tag: tag, BestScore: bestScores[tag], FileCount: len(files[tag])}
		for value, locs := range valMap {
			tr.Values = append(tr.Values, TagValueGroup{Value: value, Count: len(locs), Locations: locs})
			tr.Count += len(locs)
		}
		sort.Slice(tr.Values, func(i, j int) bool {
			if tr.Values[i].Count != tr.Values[j].Count {
				return tr.Values[i].Count > tr.Values[j].Count
			}
			return tr.Values[i].Value < tr.Values[j].Value
		})
		tags = append(tags, tr)
	}
	sort.Slice(tags, func(i, j int) bool {
		if tags[i].Count != tags[j].Count {
			return tags[i].Count > tags[j].Count
		}
		return tags[i].Tag < tags[j].Tag
	})
	return tags
}

// GroupedResult is a file with its matching chunks, for the app UI.
// Tuple array in JSON: [filepath, strategy, [chunk, ...]]
type GroupedResult struct {
	Path     string         `json:"path"`
	Strategy string         `json:"strategy"`
	Chunks   []GroupedChunk `json:"chunks"`
}

// GroupedChunk is a single chunk in a grouped search result.
// CRC: crc-Searcher.md | Seq: seq-editor-endpoints.md
type GroupedChunk struct {
	Range       string  `json:"range"`
	Score       float64 `json:"score"`
	Content     string  `json:"content"`
	ContentType string  `json:"contentType"`
	Preview     string  `json:"preview"`
}

// StrategyToContentType maps an indexing strategy to a content type string.
// CRC: crc-Searcher.md | R1072, R1094
func StrategyToContentType(strategy string) string {
	switch strategy {
	case "markdown":
		return "markdown"
	case "chat-jsonl":
		return "json"
	case "bracket", "indent":
		return "code"
	default:
		return "text"
	}
}

// SearchMulti runs a query through all four scoring strategies (coverage, density,
// overlap, bm25) in a single microfts2 SearchMulti call. Results are deduplicated
// by (fileid, chunknum), keeping the best score per chunk across strategies.
// CRC: crc-Searcher.md | Seq: seq-search.md | R587, R588, R589, R593, R594, R595, R596, R598, R601, R602
func (s *Searcher) SearchMulti(query string, opts SearchOpts) ([]SearchResultEntry, error) {
	if err := validateSearchFlags(opts); err != nil {
		return nil, err
	}
	k := opts.K
	if k == 0 {
		k = 20
	}

	filterOpt, err := s.resolveFilters(opts)
	if err != nil {
		return nil, err
	}
	ftsSearchOpts := defaultSearchOpts(filterOpt, "", opts)

	// Proximity reranking is handled inside microfts2.SearchMulti
	if opts.Proximity {
		ftsSearchOpts = append(ftsSearchOpts, microfts2.WithProximityRerank(k*2))
	}

	// Build strategy map
	strategies, err := s.buildStrategies(query)
	if err != nil {
		return nil, fmt.Errorf("build strategies: %w", err)
	}

	multiResults, err := s.fts.SearchMulti(query, strategies, k, ftsSearchOpts...)
	if err != nil {
		return nil, fmt.Errorf("multi search: %w", err)
	}

	// Deduplicate by (fileid, chunknum), keeping best score and tracking strategy
	type dedup struct {
		entry SearchResultEntry
		multi bool // seen from multiple strategies
	}
	cache := s.newFTSKeyCache()
	seen := make(map[chunkKey]*dedup)

	for _, mr := range multiResults {
		for _, r := range mr.Results {
			key, ok := cache.resolve(r)
			if !ok {
				continue
			}
			if d, exists := seen[key]; exists {
				if r.Score > d.entry.Score {
					d.entry.FTSScore = r.Score
					d.entry.Score = r.Score
				}
				d.multi = true
			} else {
				seen[key] = &dedup{
					entry: SearchResultEntry{
						FileID:   key.FileID,
						ChunkNum: key.ChunkNum,
						FTSScore: r.Score,
						Score:    r.Score,
						Strategy: mr.Strategy,
					},
				}
			}
		}
	}

	results := make([]SearchResultEntry, 0, len(seen))
	for _, d := range seen {
		if d.multi {
			d.entry.Strategy = "multi"
		}
		results = append(results, d.entry)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if len(results) > k {
		results = results[:k]
	}
	return s.filterAndResolve(results, opts)
}

// buildStrategies creates the scorer map for SearchMulti.
// CRC: crc-Searcher.md | R586, R604, R697, R698, R699, R700
func (s *Searcher) buildStrategies(query string) (map[string]microfts2.ScoreFunc, error) {
	strategies := map[string]microfts2.ScoreFunc{
		"coverage": microfts2.ScoreCoverage,
		"density":  microfts2.ScoreDensityFunc,
		"overlap":  microfts2.ScoreOverlap,
	}

	// BM25 needs query trigrams for IDF computation
	trigramCounts, err := s.fts.QueryTrigramCounts(query)
	if err != nil {
		return nil, fmt.Errorf("query trigram counts: %w", err)
	}
	queryTrigrams := make([]uint32, len(trigramCounts))
	for i, tc := range trigramCounts {
		queryTrigrams[i] = tc.Trigram
	}
	bm25, err := s.fts.BM25Func(queryTrigrams)
	if err != nil {
		return nil, fmt.Errorf("bm25 init: %w", err)
	}
	strategies["bm25"] = bm25

	return strategies, nil
}

// SearchFuzzy runs a typo-tolerant search via microfts2.SearchFuzzy.
// Uses OR-union of trigrams with posting-list tally, then C-record re-scoring.
// CRC: crc-Searcher.md | Seq: seq-search.md
// R745, R746
func (s *Searcher) SearchFuzzy(query string, opts SearchOpts) ([]SearchResultEntry, error) {
	if err := validateSearchFlags(opts); err != nil {
		return nil, err
	}
	k := opts.K
	if k == 0 {
		k = 20
	}

	filterOpt, err := s.resolveFilters(opts)
	if err != nil {
		return nil, err
	}
	ftsSearchOpts := defaultSearchOpts(filterOpt, "", opts)

	if opts.Proximity {
		ftsSearchOpts = append(ftsSearchOpts, microfts2.WithProximityRerank(k*2))
	}

	fuzzyResults, err := s.fts.SearchFuzzy(query, k, ftsSearchOpts...)
	if err != nil {
		return nil, fmt.Errorf("fuzzy search: %w", err)
	}

	cache := s.newFTSKeyCache()
	results := make([]SearchResultEntry, 0, len(fuzzyResults.Results))
	for _, r := range fuzzyResults.Results {
		key, ok := cache.resolve(r)
		if !ok {
			continue
		}
		results = append(results, SearchResultEntry{
			FileID:   key.FileID,
			ChunkNum: key.ChunkNum,
			FTSScore: r.Score,
			Score:    r.Score,
			Strategy: "fuzzy",
		})
	}

	if len(results) > k {
		results = results[:k]
	}
	return s.filterAndResolve(results, opts)
}

// SearchGrouped runs a search and groups results by file.
// Files sorted by best chunk score (descending), chunks within each file
// sorted by score (descending). Each chunk includes a pre-rendered HTML preview.
// CRC: crc-Searcher.md | R1935
func (s *Searcher) SearchGrouped(query string, opts SearchOpts) ([]GroupedResult, error) {
	// R1402-R1403, R1787, R1935: combined about coordination.
	// One ResolveAboutFilters call covers the primary --about and
	// every Mode == "about" filter row via a single SearchChunksMulti
	// EC walk. BuildChunkFilters handles the remaining (non-about)
	// rows.
	if len(opts.ChunkFilters) > 0 || opts.About != "" {
		k := opts.K
		if k == 0 {
			k = 20
		}
		ar, err := ResolveAboutFilters(opts.ChunkFilters, opts.About, k*2, s.librarian, s.store, s.config)
		if err != nil {
			return nil, err
		}
		if ar.HasAboutResults {
			opts.aboutResults = ar.AboutResults
			opts.aboutResultsSet = true
		}
		opts.aboutFilterSets = ar.AboutFilterSets
		opts.extraOpts = append(opts.extraOpts, ar.Early...)
		opts.extraOpts = append(opts.extraOpts, ar.ChunkFilterOpts...)
		if opts.Cache != nil {
			if paths, pathErr := s.fts.FileIDPaths(); pathErr == nil {
				opts.extraOpts = append(opts.extraOpts, BuildChunkFilters(ar.Remaining, opts.Cache, paths, s.store)...)
			}
		}
		opts.extraOpts = append(opts.extraOpts, ar.Late...)
	}

	var results []SearchResultEntry
	var err error
	// CRC: crc-Searcher.md | R603 — multi-strategy search for the UI
	if opts.Multi {
		results, err = s.SearchMulti(query, opts)
	} else if opts.Fuzzy {
		// CRC: crc-Searcher.md | R747
		results, err = s.SearchFuzzy(query, opts)
	} else if opts.About != "" || opts.Contains != "" || len(opts.Regex) > 0 || opts.LikeFile != "" {
		results, err = s.SearchSplit(opts)
	} else {
		results, err = s.SearchCombined(query, opts)
	}
	if err != nil {
		return nil, err
	}

	results, err = s.FillChunksUsing(results, opts.Cache)
	if err != nil {
		return nil, err
	}

	// Compile highlight patterns from whichever field carries the user's query text
	highlightQuery := query
	if highlightQuery == "" {
		switch {
		case opts.Contains != "":
			highlightQuery = opts.Contains
		case opts.About != "":
			highlightQuery = opts.About
		case len(opts.Regex) > 0:
			highlightQuery = opts.Regex[0]
		}
	}
	var tokenPatterns []*regexp.Regexp
	if len(opts.Regex) > 0 {
		// R1230: Regex mode: use the pattern directly for highlighting
		if re, err := regexp.Compile("(?i)(" + highlightQuery + ")"); err == nil {
			tokenPatterns = []*regexp.Regexp{re}
		}
	} else {
		tokenPatterns = compileTokenPatterns(highlightQuery)
	}

	return s.groupResults(results, tokenPatterns), nil
}

// groupResults groups SearchResultEntry by file, renders previews, and sorts
// files / chunks by score (descending). Shared by SearchGrouped and the
// tag-direct path (GroupTagChunks). tokenPatterns may be nil (no highlighting).
func (s *Searcher) groupResults(results []SearchResultEntry, tokenPatterns []*regexp.Regexp) []GroupedResult {
	type fileGroup struct {
		path     string
		strategy string
		chunks   []GroupedChunk
		best     float64
	}
	groups := make(map[uint64]*fileGroup)
	var order []uint64
	for _, r := range results {
		g, ok := groups[r.FileID]
		if !ok {
			strategy := ""
			if info, err := s.fts.FileInfoByID(r.FileID); err == nil {
				strategy = info.Strategy
			}
			g = &fileGroup{path: r.Path, strategy: strategy}
			groups[r.FileID] = g
			order = append(order, r.FileID)
		}
		pdfZoom := s.config.PdfPreviewZoom
		if pdfZoom <= 0 {
			pdfZoom = 1.5
		}
		preview := RenderPreview(r.Text, g.strategy, tokenPatterns, r.Attrs, r.Path, pdfZoom)
		g.chunks = append(g.chunks, GroupedChunk{
			Range:       r.Range,
			Score:       r.Score,
			Content:     r.Text,
			ContentType: StrategyToContentType(g.strategy),
			Preview:     preview,
		})
		if r.Score > g.best {
			g.best = r.Score
		}
	}
	sort.Slice(order, func(i, j int) bool {
		return groups[order[i]].best > groups[order[j]].best
	})
	grouped := make([]GroupedResult, 0, len(order))
	for _, fid := range order {
		g := groups[fid]
		sort.Slice(g.chunks, func(i, j int) bool {
			return g.chunks[i].Score > g.chunks[j].Score
		})
		grouped = append(grouped, GroupedResult{
			Path:     g.path,
			Strategy: g.strategy,
			Chunks:   g.chunks,
		})
	}
	return grouped
}

// RenderPreview renders chunk text as HTML for app display.
// Strategy determines the renderer: goldmark for markdown,
// JSON pretty-print for JSON (under a length threshold),
// <pdf-chunk> element for PDF chunks with a rect attribute,
// plain text with HTML escaping otherwise.
// Query tokens are highlighted with <mark> tags in text formats.
// CRC: crc-Searcher.md | R1703-R1708
func RenderPreview(text, strategy string, patterns []*regexp.Regexp, attrs []microfts2.Pair, path string, pdfZoom float64) string {
	if strategy == "pdf" {
		if html, ok := renderPdfPreview(attrs, path, pdfZoom); ok {
			return html
		}
		// Salvage chunks (no rect) fall through to the plain-text path. R1708
	}
	var rendered string
	switch strategy {
	case "markdown":
		var buf bytes.Buffer
		if err := goldmark.Convert([]byte(text), &buf); err == nil {
			rendered = buf.String()
		} else {
			rendered = preEscaped(text)
		}
	default:
		if len(text) < 4096 && looksLikeJSON(text) {
			var out bytes.Buffer
			if err := json.Indent(&out, []byte(text), "", "  "); err == nil {
				rendered = preEscaped(out.String())
			} else {
				rendered = preEscaped(text)
			}
		} else {
			rendered = preEscaped(text)
		}
	}
	return highlightTokens(rendered, patterns)
}

// renderPdfPreview emits a <pdf-chunk> element with <ark-tag> children
// for any tags recorded in the chunk's tag_rects attribute. Returns
// ("", false) when the chunk lacks a rect attribute (salvage chunks
// per R1708) so the caller can fall through to the text preview path.
// R1703-R1707.
func renderPdfPreview(attrs []microfts2.Pair, path string, pdfZoom float64) (string, bool) {
	rect, hasRect := microfts2.PairGet(attrs, "rect")
	if !hasRect || len(rect) == 0 {
		return "", false
	}
	page, _ := microfts2.PairGet(attrs, "page")
	if len(page) == 0 {
		page = []byte("1")
	}
	tagRects, _ := microfts2.PairGet(attrs, "tag_rects")
	tagSegments, _ := microfts2.PairGet(attrs, "tag_segments")
	pageSize, _ := microfts2.PairGet(attrs, "page_size")

	var b strings.Builder
	b.WriteString(`<pdf-chunk src="`)
	b.WriteString(template.HTMLEscapeString(rawURLFor(path)))
	b.WriteString(`" page="`)
	b.WriteString(template.HTMLEscapeString(string(page)))
	b.WriteString(`" rect="`)
	b.WriteString(template.HTMLEscapeString(string(rect)))
	b.WriteString(`"`)
	if len(pageSize) > 0 {
		b.WriteString(` page-size="`)
		b.WriteString(template.HTMLEscapeString(string(pageSize)))
		b.WriteString(`"`)
	}
	if pdfZoom > 0 {
		fmt.Fprintf(&b, ` zoom="%g"`, pdfZoom)
	}
	b.WriteString(`>`)
	writePdfTagChildren(&b, string(tagRects), string(tagSegments))
	b.WriteString(`</pdf-chunk>`)
	return b.String(), true
}

// renderPdfChunksByPage groups PDF chunks by their `page` attribute
// and emits one `<pdf-chunk>` per page covering the full page area.
// All `tag_rects` from chunks on the same page are concatenated
// (semicolon-separated) so every tag overlays the rendered page.
// Pages without a `page_size` attribute fall through to an HTML-
// escaped pre-wrapped block. R1739, R1740
// fileID stamps every emitted <ark-curate-region> child so the
// content-view curate-pin button has both identifiers it needs (R2421).
// CRC: crc-Server.md | R1739, R1740, R1982, R2421
func renderPdfChunksByPage(chunks []microfts2.ChunkResult, path string, fileID uint64, db *DB) string {
	type pageAgg struct {
		page          string
		pageSize      string
		tagRects      []string
		tagSegments   []string
		headingRects  []string                // R2076: per-heading rect for <ark-heading> overlays
		extTagsBlocks []string                // R2065, R2073, R2082: per-chunk <ark-ext-tags> with rect
		curateRegions []string                // R2421: per-chunk <ark-curate-region rect chunkid fileid>
		salvage       []microfts2.ChunkResult // only used when pageSize stays empty
	}
	// R2065, R2079: chunkIDs in chunk order so we can look up ext routings
	// per chunk via ChunkResult.Index.
	chunkIDs := db.ChunkIDsForPath(path)
	var order []string
	pages := make(map[string]*pageAgg)
	for _, ch := range chunks {
		pageVal, _ := microfts2.PairGet(ch.Attrs, "page")
		pageStr := string(pageVal)
		if pageStr == "" {
			pageStr = "1"
		}
		agg := pages[pageStr]
		if agg == nil {
			agg = &pageAgg{page: pageStr}
			pages[pageStr] = agg
			order = append(order, pageStr)
		}
		if ps, ok := microfts2.PairGet(ch.Attrs, "page_size"); ok && len(ps) > 0 && agg.pageSize == "" {
			agg.pageSize = string(ps)
		}
		if tr, ok := microfts2.PairGet(ch.Attrs, "tag_rects"); ok && len(tr) > 0 {
			agg.tagRects = append(agg.tagRects, string(tr))
			ts, _ := microfts2.PairGet(ch.Attrs, "tag_segments")
			agg.tagSegments = append(agg.tagSegments, string(ts))
		}
		rect, hasRect := microfts2.PairGet(ch.Attrs, "rect")
		if !hasRect {
			agg.salvage = append(agg.salvage, ch)
		}
		// R2076: PDFChunker emits font_size only for Heading-kind blocks.
		if _, isHeading := microfts2.PairGet(ch.Attrs, "font_size"); isHeading && hasRect {
			agg.headingRects = append(agg.headingRects, string(rect))
		}
		// R2065, R2073, R2079, R2082: per-chunk ext-tags overlay.
		if hasRect && ch.Index < len(chunkIDs) {
			routings := db.extmap.ExtRoutingsForTargetChunk(chunkIDs[ch.Index], db)
			if block := renderExtTagsBlock(routings, string(rect)); block != "" {
				agg.extTagsBlocks = append(agg.extTagsBlocks, block)
			}
		}
		// R2421: per-chunk <ark-curate-region> for the curate-pin button.
		// Salvage chunks (no rect) get the standard pin via their
		// <div class="ark-chunk"> wrapper below — no region here.
		if hasRect && ch.Index < len(chunkIDs) {
			agg.curateRegions = append(agg.curateRegions, fmt.Sprintf(
				`<ark-curate-region chunkid="%d" fileid="%d" rect="%s"></ark-curate-region>`,
				chunkIDs[ch.Index], fileID, template.HTMLEscapeString(string(rect)),
			))
		}
	}

	var buf strings.Builder
	for _, pageStr := range order {
		agg := pages[pageStr]
		if agg.pageSize == "" {
			// R1740: no page_size anywhere on the page → render salvage
			// chunks as HTML-escaped pre-wrapped text.
			for _, ch := range agg.salvage {
				buf.WriteString(`<div class="ark-chunk">`)
				buf.WriteString(wrapTagElements(template.HTMLEscapeString(ch.Content), db))
				buf.WriteString("</div>\n")
			}
			continue
		}
		pw, ph := parsePageSize(agg.pageSize)
		buf.WriteString(`<pdf-chunk src="`)
		buf.WriteString(template.HTMLEscapeString(rawURLFor(path)))
		buf.WriteString(`" page="`)
		buf.WriteString(template.HTMLEscapeString(agg.page))
		buf.WriteString(`" rect="0,0,`)
		buf.WriteString(pw)
		buf.WriteString(`,`)
		buf.WriteString(ph)
		buf.WriteString(`" page-size="`)
		buf.WriteString(template.HTMLEscapeString(agg.pageSize))
		buf.WriteString(`">`)
		writePdfTagChildren(&buf, strings.Join(agg.tagRects, ";"), strings.Join(agg.tagSegments, ";"))
		// R2076: <ark-heading> overlays for Heading-kind blocks.
		for _, hr := range agg.headingRects {
			buf.WriteString(`<ark-heading rect="`)
			buf.WriteString(template.HTMLEscapeString(hr))
			buf.WriteString(`"></ark-heading>`)
		}
		// R2065, R2073, R2082: <ark-ext-tags> overlays per chunk that
		// has incoming ext routings.
		for _, etb := range agg.extTagsBlocks {
			buf.WriteString(etb)
		}
		// R2421: per-chunk <ark-curate-region> overlays for pin buttons
		// + hover outlines. Positioned by pdf-chunk-element's
		// positionRegions pass.
		for _, cr := range agg.curateRegions {
			buf.WriteString(cr)
		}
		buf.WriteString(`</pdf-chunk>` + "\n")
	}
	return buf.String()
}

// parsePageSize splits a "W,H" string into its two components,
// returning them verbatim (already in PDF point units).
func parsePageSize(ps string) (string, string) {
	parts := strings.SplitN(ps, ",", 2)
	if len(parts) != 2 {
		return "0", "0"
	}
	return parts[0], parts[1]
}

// rawURLFor builds the `/raw/PATH` URL for a file path. Paths are
// URL-path-encoded so spaces and other special characters survive.
func rawURLFor(path string) string {
	if path == "" {
		return ""
	}
	// Preserve the leading `/` so we produce "/raw/home/..." rather than
	// "/raw%2Fhome/...".
	u := &url.URL{Path: "/raw" + path}
	return u.String()
}

// writePdfTagChildren parses the chunk's tag_rects attribute and emits
// one `<ark-tag rect="…" segments="…"><name>…</name> <value>…</value></ark-tag>`
// child per entry. tag_rects format: `name=value@x,y,w,h;…` (R1672).
// tag_segments is index-aligned with tag_rects — per-tag
// `@|name|colon|valRect1|valRect2…`. When tagSegments
// is empty or a tag's entry is empty, the `segments` attribute is
// omitted and the element falls back to approximate mapping.
// CRC: crc-PDFChunker.md | R1671
// CRC: crc-PdfChunkElement.md | R1758-R1761
func writePdfTagChildren(b *strings.Builder, tagRects, tagSegments string) {
	if tagRects == "" {
		return
	}
	rectEntries := strings.Split(tagRects, ";")
	var segEntries []string
	if tagSegments != "" {
		segEntries = strings.Split(tagSegments, ";")
	}
	for i, entry := range rectEntries {
		if entry == "" {
			continue
		}
		at := strings.LastIndex(entry, "@")
		if at < 0 {
			continue
		}
		nameValue := entry[:at]
		rect := entry[at+1:]
		eq := strings.Index(nameValue, "=")
		if eq < 0 {
			continue
		}
		name := decodeTagRectField(nameValue[:eq])
		value := decodeTagRectField(nameValue[eq+1:])
		if rect == "" || name == "" {
			continue
		}
		var segs string
		if i < len(segEntries) {
			segs = segEntries[i]
		}
		b.WriteString(`<ark-tag rect="`)
		b.WriteString(template.HTMLEscapeString(rect))
		if segs != "" {
			b.WriteString(`" segments="`)
			b.WriteString(template.HTMLEscapeString(segs))
		}
		b.WriteString(`"><name>`)
		b.WriteString(template.HTMLEscapeString(name))
		b.WriteString(`</name> <value>`)
		b.WriteString(template.HTMLEscapeString(value))
		b.WriteString(`</value></ark-tag>`)
	}
}

// decodeTagRectField reverses the percent-encoding that the chunker
// applies to the four structural delimiters (`=`, `@`, `;`, `,`) and
// `%`. R1672.
func decodeTagRectField(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	decoded, err := url.QueryUnescape(strings.ReplaceAll(s, "+", "%2B"))
	if err != nil {
		return s
	}
	return decoded
}

func preEscaped(text string) string {
	return "<pre>" + html.EscapeString(text) + "</pre>"
}

// looksLikeJSON checks if text starts with { or [ after trimming whitespace.
func looksLikeJSON(text string) bool {
	trimmed := strings.TrimSpace(text)
	return len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[')
}

// compileTokenPatterns splits a query into tokens and compiles case-insensitive
// regexes for each. Compiled once per search, reused across all chunk previews.
func compileTokenPatterns(query string) []*regexp.Regexp {
	var patterns []*regexp.Regexp
	for _, token := range strings.Fields(query) {
		re, err := regexp.Compile("(?i)(" + regexp.QuoteMeta(token) + ")")
		if err == nil {
			patterns = append(patterns, re)
		}
	}
	return patterns
}

// highlightTokens wraps occurrences of query tokens in <mark> tags.
// Case-insensitive matching. Operates on HTML — avoids matching inside tags.
func highlightTokens(htmlStr string, patterns []*regexp.Regexp) string {
	for _, re := range patterns {
		htmlStr = replaceOutsideTags(htmlStr, re)
	}
	return htmlStr
}

// replaceOutsideTags applies regex replacement only to text outside HTML tags.
func replaceOutsideTags(s string, re *regexp.Regexp) string {
	var buf strings.Builder
	for len(s) > 0 {
		// Find next tag
		tagStart := strings.IndexByte(s, '<')
		if tagStart < 0 {
			// No more tags — highlight remaining text
			buf.WriteString(re.ReplaceAllString(s, "<mark>$1</mark>"))
			break
		}
		// Highlight text before the tag
		if tagStart > 0 {
			buf.WriteString(re.ReplaceAllString(s[:tagStart], "<mark>$1</mark>"))
		}
		// Copy tag verbatim
		tagEnd := strings.IndexByte(s[tagStart:], '>')
		if tagEnd < 0 {
			// Unclosed tag — copy rest verbatim
			buf.WriteString(s[tagStart:])
			break
		}
		buf.WriteString(s[tagStart : tagStart+tagEnd+1])
		s = s[tagStart+tagEnd+1:]
	}
	return buf.String()
}

// chunkNumForRange finds which chunk matches the given range string.
func chunkNumForRange(info microfts2.FRecord, rangeStr string) uint64 {
	for i, cr := range info.Chunks {
		if cr.Location == rangeStr {
			return uint64(i)
		}
	}
	// Fallback: find the chunk whose range contains the start line
	startLine, _ := parseRange(rangeStr)
	for i, cr := range info.Chunks {
		s, e := parseRange(cr.Location)
		if startLine >= s && startLine <= e {
			return uint64(i)
		}
	}
	return 0
}
