package ark

// CRC: crc-Indexer.md

import (
	"fmt"
	"regexp"
	"strings"

	"microfts2"

	"github.com/anthropics/microvec"
)

var tagRegex = regexp.MustCompile(`@([a-zA-Z][\w-]*):`)

// Indexer coordinates adding, removing, and refreshing files across
// both microfts2 and microvec. Extracts tags from file content.
type Indexer struct {
	fts   *microfts2.DB
	vec   *microvec.DB
	store *Store
}

// AddFile adds a file to both engines and extracts tags. microfts2
// first (gets fileid and chunk offsets), then reads chunks and adds
// to microvec, then extracts and stores tags.
func (idx *Indexer) AddFile(path, strategy string) (uint64, error) {
	fileid, content, err := idx.fts.AddFileWithContent(path, strategy)
	if err != nil {
		return 0, fmt.Errorf("fts add %s: %w", path, err)
	}

	data, chunks, err := splitChunks(content, fileid, idx.fts)
	if err != nil {
		return fileid, fmt.Errorf("read chunks %s: %w", path, err)
	}

	if err := idx.vec.AddFile(fileid, chunks); err != nil {
		return fileid, fmt.Errorf("vec add %s: %w", path, err)
	}

	if idx.store != nil {
		tags := ExtractTags(data)
		if err := idx.store.UpdateTags(fileid, tags); err != nil {
			return fileid, fmt.Errorf("update tags %s: %w", path, err)
		}
	}

	return fileid, nil
}

// RemoveFile removes a file from both engines and tags by path.
func (idx *Indexer) RemoveFile(path string) error {
	status, err := idx.fts.CheckFile(path)
	if err != nil {
		return fmt.Errorf("check file %s: %w", path, err)
	}
	fileid := status.FileID

	if err := idx.fts.RemoveFile(path); err != nil {
		return fmt.Errorf("fts remove %s: %w", path, err)
	}
	if err := idx.vec.RemoveFile(fileid); err != nil {
		return fmt.Errorf("vec remove %s: %w", path, err)
	}
	if idx.store != nil {
		if err := idx.store.RemoveTags(fileid); err != nil {
			return fmt.Errorf("remove tags %s: %w", path, err)
		}
	}
	return nil
}

// RemoveByID removes a file from both engines and tags by fileid.
func (idx *Indexer) RemoveByID(fileid uint64) error {
	info, err := idx.fts.FileInfoByID(fileid)
	if err != nil {
		return fmt.Errorf("file info %d: %w", fileid, err)
	}
	if err := idx.fts.RemoveFile(info.Filename); err != nil {
		return fmt.Errorf("fts remove %d: %w", fileid, err)
	}
	if err := idx.vec.RemoveFile(fileid); err != nil {
		return fmt.Errorf("vec remove %d: %w", fileid, err)
	}
	if idx.store != nil {
		if err := idx.store.RemoveTags(fileid); err != nil {
			return fmt.Errorf("remove tags %d: %w", fileid, err)
		}
	}
	return nil
}

// RefreshFile re-indexes a single file: reindex in microfts2 first,
// then swap vectors and tags. FTS-first ordering ensures old state
// is intact if the reindex fails.
func (idx *Indexer) RefreshFile(path, strategy string) error {
	// Get old fileid to remove old vectors later
	status, err := idx.fts.CheckFile(path)
	if err != nil {
		return fmt.Errorf("check file %s: %w", path, err)
	}
	oldID := status.FileID

	// Re-index in microfts2 first (safe: failure leaves old state)
	fileid, content, err := idx.fts.ReindexWithContent(path, strategy)
	if err != nil {
		return fmt.Errorf("fts reindex %s: %w", path, err)
	}

	// Remove old vectors
	if err := idx.vec.RemoveFile(oldID); err != nil {
		return fmt.Errorf("vec remove old %s: %w", path, err)
	}

	// Split content into chunks using offsets from microfts2
	data, chunks, err := splitChunks(content, fileid, idx.fts)
	if err != nil {
		return fmt.Errorf("read chunks %s: %w", path, err)
	}
	if err := idx.vec.AddFile(fileid, chunks); err != nil {
		return fmt.Errorf("vec add %s: %w", path, err)
	}

	// Re-extract tags (replaces old counts for this file)
	if idx.store != nil {
		tags := ExtractTags(data)
		if err := idx.store.UpdateTags(fileid, tags); err != nil {
			return fmt.Errorf("update tags %s: %w", path, err)
		}
	}

	return nil
}

// RefreshStale re-indexes all stale files, optionally filtered by patterns.
// Returns the list of missing files found during the check.
func (idx *Indexer) RefreshStale(patterns []string, matcher *Matcher) ([]microfts2.FileStatus, error) {
	statuses, err := idx.fts.StaleFiles()
	if err != nil {
		return nil, fmt.Errorf("stale files: %w", err)
	}

	var missing []microfts2.FileStatus
	for _, s := range statuses {
		if s.Status == "missing" {
			missing = append(missing, s)
			continue
		}
		if s.Status != "stale" {
			continue
		}
		// If patterns given, filter
		if len(patterns) > 0 {
			matched := false
			for _, p := range patterns {
				if matcher.Match(p, s.Path, false) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		if err := idx.RefreshFile(s.Path, s.Strategy); err != nil {
			return missing, fmt.Errorf("refresh %s: %w", s.Path, err)
		}
	}
	return missing, nil
}

// ExtractTags scans content for @tag: patterns and returns tag counts.
// Tag names are stored lowercase. The colon is required (disambiguates
// from emails and mentions).
func ExtractTags(content []byte) map[string]uint32 {
	matches := tagRegex.FindAllSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	tags := make(map[string]uint32)
	for _, m := range matches {
		name := strings.ToLower(string(m[1]))
		tags[name]++
	}
	return tags
}

// splitChunks reads chunk text from a file using ranges from microfts2.
// Returns the raw file data alongside sliced chunks (avoids bytes.Join
// when callers need the full content for tag extraction).
func splitChunks(data []byte, fileid uint64, fts *microfts2.DB) ([]byte, [][]byte, error) {
	info, err := fts.FileInfoByID(fileid)
	if err != nil {
		return nil, nil, err
	}

	lines := strings.Split(string(data), "\n")
	chunks := make([][]byte, len(info.ChunkRanges))
	for i, r := range info.ChunkRanges {
		chunks[i] = []byte(extractByRange(lines, r))
	}
	return data, chunks, nil
}
