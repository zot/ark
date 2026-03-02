package ark

// CRC: crc-Searcher.md

import (
	"fmt"
	"sort"

	"microfts2"

	"github.com/anthropics/microvec"
)

// SearchOpts controls search behavior.
type SearchOpts struct {
	K        int    // max results (default 20)
	Scores   bool   // include scores in output
	After    int64  // only results newer than this timestamp (unix nano, 0 = no filter)
	About    string // semantic query (microvec)
	Contains string // exact match query (microfts2)
	Regex    string // regex query (microfts2)
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
}

// chunkKey uniquely identifies a chunk across both engines.
type chunkKey struct {
	FileID   uint64
	ChunkNum uint64
}

// Searcher queries both engines and merges or intersects results.
type Searcher struct {
	fts *microfts2.DB
	vec *microvec.DB
}

// SearchCombined sends the same query to both engines, merges by
// (fileid, chunknum), combines scores, sorts descending.
func (s *Searcher) SearchCombined(query string, opts SearchOpts) ([]SearchResultEntry, error) {
	k := opts.K
	if k == 0 {
		k = 20
	}

	ftsResults, err := s.fts.Search(query)
	if err != nil {
		return nil, fmt.Errorf("fts search: %w", err)
	}

	vecResults, err := s.vec.Search(query, k*2) // over-fetch for merge
	if err != nil {
		return nil, fmt.Errorf("vec search: %w", err)
	}

	merged := s.merge(ftsResults.Results, vecResults)
	merged = s.applyFilters(merged, opts)
	if len(merged) > k {
		merged = merged[:k]
	}
	return s.resolveResults(merged)
}

// SearchSplit dispatches --about, --contains, --regex to appropriate engines.
func (s *Searcher) SearchSplit(opts SearchOpts) ([]SearchResultEntry, error) {
	if err := validateSplitFlags(opts); err != nil {
		return nil, err
	}

	k := opts.K
	if k == 0 {
		k = 20
	}

	hasAbout := opts.About != ""
	hasFTS := opts.Contains != "" || opts.Regex != ""

	var vecResults []microvec.SearchResult
	var ftsResults []microfts2.SearchResult

	if hasAbout {
		vr, err := s.vec.Search(opts.About, k*2)
		if err != nil {
			return nil, fmt.Errorf("vec search: %w", err)
		}
		vecResults = vr
	}

	if opts.Contains != "" {
		fr, err := s.fts.Search(opts.Contains)
		if err != nil {
			return nil, fmt.Errorf("fts search: %w", err)
		}
		ftsResults = fr.Results
	} else if opts.Regex != "" {
		fr, err := s.fts.SearchRegex(opts.Regex)
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

	results = s.applyFilters(results, opts)
	if len(results) > k {
		results = results[:k]
	}
	return s.resolveResults(results)
}

func validateSplitFlags(opts SearchOpts) error {
	if opts.Contains != "" && opts.Regex != "" {
		return fmt.Errorf("--contains and --regex are mutually exclusive")
	}
	return nil
}

// merge combines results from both engines by (fileid, chunknum).
func (s *Searcher) merge(ftsResults []microfts2.SearchResult, vecResults []microvec.SearchResult) []SearchResultEntry {
	m := make(map[chunkKey]*SearchResultEntry)

	for _, r := range ftsResults {
		key, ok := s.ftsChunkKey(r)
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

	var results []SearchResultEntry
	for _, r := range ftsResults {
		key, ok := s.ftsChunkKey(r)
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
	var results []SearchResultEntry
	for _, r := range ftsResults {
		key, ok := s.ftsChunkKey(r)
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

func (s *Searcher) applyFilters(results []SearchResultEntry, opts SearchOpts) []SearchResultEntry {
	if opts.After == 0 {
		return results
	}
	var filtered []SearchResultEntry
	for _, r := range results {
		info, err := s.fts.FileInfoByID(r.FileID)
		if err != nil {
			continue
		}
		if info.ModTime >= opts.After {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func (s *Searcher) resolveResults(results []SearchResultEntry) ([]SearchResultEntry, error) {
	var resolved []SearchResultEntry
	for _, r := range results {
		info, err := s.fts.FileInfoByID(r.FileID)
		if err != nil {
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

// ftsChunkKey resolves an FTS search result to a chunkKey.
func (s *Searcher) ftsChunkKey(r microfts2.SearchResult) (chunkKey, bool) {
	status, err := s.fts.CheckFile(r.Path)
	if err != nil {
		return chunkKey{}, false
	}
	info, err := s.fts.FileInfoByID(status.FileID)
	if err != nil {
		return chunkKey{}, false
	}
	cn := chunkNumForLines(info, r.StartLine)
	return chunkKey{FileID: status.FileID, ChunkNum: cn}, true
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
