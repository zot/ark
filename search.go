package ark

// CRC: crc-Searcher.md

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"microfts2"

	"github.com/anthropics/microvec"
)

var tagPattern = regexp.MustCompile(`@([a-zA-Z][\w-]*):`)

// SearchOpts controls search behavior.
type SearchOpts struct {
	K         int      // max results (default 20)
	Scores    bool     // include scores in output
	After     int64    // only results newer than this timestamp (unix nano, 0 = no filter)
	About     string   // semantic query (microvec)
	Contains  string   // exact match query (microfts2)
	Regex     string   // regex query (microfts2)
	LikeFile  string   // file path — use content as FTS density query
	Tags      bool     // output extracted tags instead of content
	Source    []string // only search files from matching source dirs (substring)
	NotSource []string // exclude files from matching source dirs (substring)
}

// SearchResultEntry is a merged/intersected search result.
type SearchResultEntry struct {
	Path      string
	StartLine int
	EndLine   int
	FTSScore  float64
	VecScore  float64
	Score     float64
	FileID    uint64
	ChunkNum  uint64
	Text      string // populated by FillChunks or FillFiles
}

// ChunkResult is the JSONL output for --chunks.
type ChunkResult struct {
	Path      string  `json:"path"`
	StartLine int     `json:"startLine"`
	EndLine   int     `json:"endLine"`
	Score     float64 `json:"score"`
	Text      string  `json:"text"`
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

	sourceFilter, err := s.resolveSourceFilter(opts)
	if err != nil {
		return nil, err
	}
	var ftsSearchOpts []microfts2.SearchOption
	if sourceFilter != nil {
		ftsSearchOpts = append(ftsSearchOpts, sourceFilter)
	}

	ftsResults, err := s.fts.Search(query, ftsSearchOpts...)
	if err != nil {
		return nil, fmt.Errorf("fts search: %w", err)
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

	sourceFilter, err := s.resolveSourceFilter(opts)
	if err != nil {
		return nil, err
	}
	var ftsSearchOpts []microfts2.SearchOption
	if sourceFilter != nil {
		ftsSearchOpts = append(ftsSearchOpts, sourceFilter)
	}

	hasAbout := opts.About != ""
	hasFTS := opts.Contains != "" || opts.Regex != "" || opts.LikeFile != ""

	var vecResults []microvec.SearchResult
	var ftsResults []microfts2.SearchResult

	if hasAbout {
		vr, err := s.vec.Search(opts.About, k*2)
		if err != nil {
			return nil, fmt.Errorf("vec search: %w", err)
		}
		vecResults = vr
	}

	if opts.LikeFile != "" {
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
		fr, err := s.fts.Search(opts.Contains, ftsSearchOpts...)
		if err != nil {
			return nil, fmt.Errorf("fts search: %w", err)
		}
		ftsResults = fr.Results
	} else if opts.Regex != "" {
		fr, err := s.fts.SearchRegex(opts.Regex, ftsSearchOpts...)
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
	if opts.Contains != "" && opts.Regex != "" {
		return fmt.Errorf("--contains and --regex are mutually exclusive")
	}
	if opts.LikeFile != "" && (opts.Contains != "" || opts.Regex != "") {
		return fmt.Errorf("--like-file is mutually exclusive with --contains and --regex")
	}
	if len(opts.Source) > 0 && len(opts.NotSource) > 0 {
		return fmt.Errorf("--source and --not-source are mutually exclusive")
	}
	return nil
}

// resolveSourceFilter builds a microfts2 search option that filters by source directory.
// Returns nil if no source filtering is requested.
func (s *Searcher) resolveSourceFilter(opts SearchOpts) (microfts2.SearchOption, error) {
	if len(opts.Source) == 0 && len(opts.NotSource) == 0 {
		return nil, nil
	}

	// Find matching source dirs
	var matchedDirs []string
	patterns := opts.Source
	if len(patterns) == 0 {
		patterns = opts.NotSource
	}
	for _, src := range s.config.Sources {
		for _, pat := range patterns {
			if strings.Contains(src.Dir, pat) {
				matchedDirs = append(matchedDirs, src.Dir)
				break
			}
		}
	}

	// Collect file IDs for files in matched source dirs
	statuses, err := s.fts.StaleFiles()
	if err != nil {
		return nil, fmt.Errorf("resolve source filter: %w", err)
	}
	ids := make(map[uint64]struct{})
	for _, fs := range statuses {
		for _, dir := range matchedDirs {
			if strings.HasPrefix(fs.Path, dir+"/") || strings.HasPrefix(fs.Path, dir) {
				ids[fs.FileID] = struct{}{}
				break
			}
		}
	}

	if len(opts.Source) > 0 {
		return microfts2.WithOnly(ids), nil
	}
	return microfts2.WithExcept(ids), nil
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

// filterAndResolve applies time filtering and resolves file paths/lines
// in a single pass, avoiding redundant FileInfoByID lookups.
func (s *Searcher) filterAndResolve(results []SearchResultEntry, opts SearchOpts) ([]SearchResultEntry, error) {
	var resolved []SearchResultEntry
	for _, r := range results {
		info, err := s.fts.FileInfoByID(r.FileID)
		if err != nil {
			continue
		}
		if opts.After != 0 && info.ModTime < opts.After {
			continue
		}
		r.Path = info.Filename
		cn := int(r.ChunkNum)
		if cn < len(info.ChunkStartLines) {
			r.StartLine = info.ChunkStartLines[cn]
		}
		if cn < len(info.ChunkEndLines) {
			r.EndLine = info.ChunkEndLines[cn]
		}
		resolved = append(resolved, r)
	}
	return resolved, nil
}

// FillChunks reads chunk text for each result from disk.
// Groups by FileID to avoid re-reading the same file.
func (s *Searcher) FillChunks(results []SearchResultEntry) ([]SearchResultEntry, error) {
	type fileCache struct {
		data    []byte
		offsets []int64
	}
	cache := make(map[uint64]*fileCache)

	for i := range results {
		r := &results[i]
		fc, ok := cache[r.FileID]
		if !ok {
			info, err := s.fts.FileInfoByID(r.FileID)
			if err != nil {
				continue
			}
			data, err := os.ReadFile(info.Filename)
			if err != nil {
				continue
			}
			fc = &fileCache{data: data, offsets: info.ChunkOffsets}
			cache[r.FileID] = fc
		}
		r.Text = string(sliceChunk(fc.data, fc.offsets, int(r.ChunkNum)))
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
		data, err := os.ReadFile(info.Filename)
		if err != nil {
			continue
		}
		r.Text = string(data)
		r.StartLine = 0
		r.EndLine = 0
	}
	return deduped, nil
}

// sliceChunk extracts a single chunk from data using byte offsets.
func sliceChunk(data []byte, offsets []int64, chunkNum int) []byte {
	if chunkNum >= len(offsets) {
		return nil
	}
	start := offsets[chunkNum]
	var end int64
	if chunkNum+1 < len(offsets) {
		end = offsets[chunkNum+1]
	} else {
		end = int64(len(data))
	}
	if start > int64(len(data)) {
		start = int64(len(data))
	}
	if end > int64(len(data)) {
		end = int64(len(data))
	}
	return data[start:end]
}

// ftsKeyCache caches LMDB lookups for FTS result resolution.
type ftsKeyCache struct {
	s        *Searcher
	fileIDs  map[string]uint64
	fileInfo map[uint64]microfts2.FileInfo
}

func (s *Searcher) newFTSKeyCache() *ftsKeyCache {
	return &ftsKeyCache{
		s:        s,
		fileIDs:  make(map[string]uint64),
		fileInfo: make(map[uint64]microfts2.FileInfo),
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
	cn := chunkNumForLines(info, r.StartLine)
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

// chunkNumForLines finds which chunk contains the given start line.
func chunkNumForLines(info microfts2.FileInfo, startLine int) uint64 {
	for i, sl := range info.ChunkStartLines {
		if sl == startLine {
			return uint64(i)
		}
	}
	// Fallback: find the chunk whose range contains this line
	for i, sl := range info.ChunkStartLines {
		el := 0
		if i < len(info.ChunkEndLines) {
			el = info.ChunkEndLines[i]
		}
		if startLine >= sl && startLine <= el {
			return uint64(i)
		}
	}
	return 0
}
