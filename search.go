package ark

// CRC: crc-Searcher.md

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zot/microfts2"

	"github.com/yuin/goldmark"
	"github.com/zot/microvec"
)

var tagPattern = regexp.MustCompile(`@([a-zA-Z][\w-]*):`)

// defaultSearchOpts returns FTS search options with dynamic trigram filtering.
// The filter uses a 50% ratio threshold — trigrams appearing in more than
// half of all chunks are skipped as non-discriminating.
// Seq: seq-search.md | R572, R574, R575
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
	return opts
}

// SearchOpts controls search behavior.
type SearchOpts struct {
	K               int                   // max results (default 20)
	Scores          bool                  // include scores in output
	After           time.Time             // only results newer than this (zero = no filter)
	Before          time.Time             // only results older than this (zero = no filter)
	About           string                // semantic query (microvec)
	Contains        string                // exact match query (microfts2)
	Regex           []string              // regex patterns (first drives SearchRegex, all are AND post-filters)
	ExceptRegex     []string              // regex subtract post-filters (any match rejects)
	LikeFile        string                // file path — use content as FTS density query
	Tags            bool                  // output extracted tags instead of content
	Filter          []string              // content-based positive filters (FTS queries, intersect)
	Except          []string              // content-based negative filters (FTS queries, subtract)
	FilterFiles     []string              // path-based positive filters (glob patterns, intersect)
	ExcludeFiles    []string              // path-based negative filters (glob patterns, subtract)
	FilterFileTags  []string              // tag-based positive filters (tag names, intersect)
	ExcludeFileTags []string              // tag-based negative filters (tag names, subtract)
	Score           string                // scoring strategy: "", "auto", "coverage", "density"
	Multi           bool                  // run all four strategies via SearchMulti
	Proximity       bool                  // enable proximity reranking
	Cache           *microfts2.ChunkCache // R652: session-provided cache (nil = per-query)
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
	Text     string // populated by FillChunks or FillFiles
	Strategy string // which scoring strategy produced this result (multi-search only)
}

// ChunkResult is the JSONL output for --chunks.
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

// FileResult is the JSONL output for --files.
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

// Searcher queries both engines and merges or intersects results.
type Searcher struct {
	fts    *microfts2.DB
	vec    *microvec.DB
	store  *Store
	config *Config
}

// SearchCombined sends the same query to both engines, merges by
// (fileid, chunknum), combines scores, sorts descending.
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

	vecResults, err := s.vec.Search(query, k*2) // over-fetch for merge
	if err != nil {
		// FTS-only mode: skip vec, return FTS results
		results := s.ftsOnly(ftsResults.Results)
		if len(results) > k {
			results = results[:k]
		}
		return s.filterAndResolve(results, opts)
	}

	merged := s.merge(ftsResults.Results, vecResults)
	if len(merged) > k {
		merged = merged[:k]
	}
	return s.filterAndResolve(merged, opts)
}

// SearchSplit dispatches --about, --contains, --regex to appropriate engines.
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

	var vecResults []microvec.SearchResult
	var ftsResults []microfts2.SearchResult

	if hasAbout {
		vr, err := s.vec.Search(opts.About, k*2)
		if err != nil {
			return nil, fmt.Errorf("vec search: %w", err)
		}
		vecResults = vr
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
		// Intersect
		results = s.intersect(ftsResults, vecResults)
	} else if hasAbout {
		// Vector only
		results = s.vecOnly(vecResults)
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
	return nil
}

// CRC: crc-Searcher.md
// resolveFilters builds a microfts2 search option from all filter flags.
// Path filters first (cheap), then content filters. Positives intersect,
// negatives subtract. Returns nil if no filtering is requested.
func (s *Searcher) resolveFilters(opts SearchOpts) (microfts2.SearchOption, error) {
	hasFilters := len(opts.Filter) > 0 || len(opts.Except) > 0 ||
		len(opts.FilterFiles) > 0 || len(opts.ExcludeFiles) > 0 ||
		len(opts.FilterFileTags) > 0 || len(opts.ExcludeFileTags) > 0
	if !hasFilters {
		return nil, nil
	}

	// Get all indexed files for path matching and ID resolution
	statuses, err := s.fts.StaleFiles()
	if err != nil {
		return nil, fmt.Errorf("resolve filters: %w", err)
	}

	// Start with all file IDs (will narrow down)
	allIDs := make(map[uint64]struct{}, len(statuses))
	pathIndex := make(map[uint64]string, len(statuses)) // fileID → path
	for _, fs := range statuses {
		allIDs[fs.FileID] = struct{}{}
		pathIndex[fs.FileID] = fs.Path
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
				if m.Match(pat, path, false) {
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
				if m.Match(pat, path, false) {
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

// merge combines results from both engines by (fileid, chunknum).
func (s *Searcher) merge(ftsResults []microfts2.SearchResult, vecResults []microvec.SearchResult) []SearchResultEntry {
	m := make(map[chunkKey]*SearchResultEntry)
	cache := s.newFTSKeyCache()

	for _, r := range ftsResults {
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
		key := chunkKey{FileID: r.FileID, ChunkNum: r.ChunkNum}
		entry, ok := m[key]
		if !ok {
			entry = &SearchResultEntry{
				FileID:   r.FileID,
				ChunkNum: r.ChunkNum,
			}
			m[key] = entry
		}
		entry.VecScore = r.Score
	}

	results := make([]SearchResultEntry, 0, len(m))
	for _, entry := range m {
		entry.Score = entry.FTSScore + entry.VecScore
		results = append(results, *entry)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	return results
}

// intersect keeps only chunks present in both result sets.
func (s *Searcher) intersect(ftsResults []microfts2.SearchResult, vecResults []microvec.SearchResult) []SearchResultEntry {
	vecMap := make(map[chunkKey]float64)
	for _, r := range vecResults {
		key := chunkKey{FileID: r.FileID, ChunkNum: r.ChunkNum}
		vecMap[key] = r.Score
	}

	cache := s.newFTSKeyCache()
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

func (s *Searcher) vecOnly(vecResults []microvec.SearchResult) []SearchResultEntry {
	results := make([]SearchResultEntry, len(vecResults))
	for i, r := range vecResults {
		results[i] = SearchResultEntry{
			FileID:   r.FileID,
			ChunkNum: r.ChunkNum,
			VecScore: r.Score,
			Score:    r.Score,
		}
	}
	return results
}

func (s *Searcher) ftsOnly(ftsResults []microfts2.SearchResult) []SearchResultEntry {
	cache := s.newFTSKeyCache()
	var results []SearchResultEntry
	for _, r := range ftsResults {
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
// CRC: crc-Searcher.md | R605, R653
func (s *Searcher) FillChunks(results []SearchResultEntry) ([]SearchResultEntry, error) {
	return s.FillChunksUsing(results, nil)
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
	}
	return results, nil
}

// FillFiles deduplicates results by file and reads full content.
// Multiple chunk hits from one file → one entry with best score.
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

// TagResult is a tag found in search results.
type TagResult struct {
	Tag       string  `json:"tag"`
	Count     int     `json:"count"`
	BestScore float64 `json:"bestScore,omitempty"`
}

// ExtractResultTags scans result chunk texts for @tag: patterns and returns
// tag names with counts and best scores. Results must have Text populated.
func ExtractResultTags(results []SearchResultEntry) []TagResult {
	re := tagPattern
	counts := make(map[string]int)
	bestScores := make(map[string]float64)

	for _, r := range results {
		matches := re.FindAllStringSubmatch(r.Text, -1)
		seen := make(map[string]bool)
		for _, m := range matches {
			tag := m[1]
			if !seen[tag] {
				counts[tag]++
				seen[tag] = true
			}
			if r.Score > bestScores[tag] {
				bestScores[tag] = r.Score
			}
		}
	}

	tags := make([]TagResult, 0, len(counts))
	for tag, count := range counts {
		tags = append(tags, TagResult{Tag: tag, Count: count, BestScore: bestScores[tag]})
	}
	sort.Slice(tags, func(i, j int) bool {
		return tags[i].Count > tags[j].Count
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
type GroupedChunk struct {
	Range   string  `json:"range"`
	Score   float64 `json:"score"`
	Preview string  `json:"preview"`
}

// SearchMulti runs a query through all four scoring strategies (coverage, density,
// overlap, bm25) in a single microfts2 SearchMulti call. Results are deduplicated
// by (fileid, chunknum), keeping the best score per chunk across strategies.
// CRC: crc-Searcher.md | Seq: seq-search.md
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

// buildStrategies creates the strategy map for SearchMulti.
// CRC: crc-Searcher.md
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

// SearchGrouped runs a search and groups results by file.
// Files sorted by best chunk score (descending), chunks within each file
// sorted by score (descending). Each chunk includes a pre-rendered HTML preview.
// CRC: crc-Searcher.md
func (s *Searcher) SearchGrouped(query string, opts SearchOpts) ([]GroupedResult, error) {
	var results []SearchResultEntry
	var err error
	if opts.Multi {
		results, err = s.SearchMulti(query, opts)
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
	tokenPatterns := compileTokenPatterns(highlightQuery)

	// Group by file
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
			// Look up strategy for this file
			strategy := ""
			if info, err := s.fts.FileInfoByID(r.FileID); err == nil {
				strategy = info.Strategy
			}
			g = &fileGroup{path: r.Path, strategy: strategy}
			groups[r.FileID] = g
			order = append(order, r.FileID)
		}
		preview := RenderPreview(r.Text, g.strategy, tokenPatterns)
		g.chunks = append(g.chunks, GroupedChunk{
			Range:   r.Range,
			Score:   r.Score,
			Preview: preview,
		})
		if r.Score > g.best {
			g.best = r.Score
		}
	}

	// Sort files by best chunk score (descending)
	sort.Slice(order, func(i, j int) bool {
		return groups[order[i]].best > groups[order[j]].best
	})

	// Build result, sort chunks within each file by score (descending)
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
	return grouped, nil
}

// RenderPreview renders chunk text as HTML for app display.
// Strategy determines the renderer: goldmark for markdown,
// JSON pretty-print for JSON (under a length threshold),
// plain text with HTML escaping otherwise.
// Query tokens are highlighted with <mark> tags in all formats.
// CRC: crc-Searcher.md
func RenderPreview(text, strategy string, patterns []*regexp.Regexp) string {
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
