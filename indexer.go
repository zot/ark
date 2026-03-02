package ark

// CRC: crc-Indexer.md

import (
	"fmt"
	"os"

	"microfts2"

	"github.com/anthropics/microvec"
)

// Indexer coordinates adding, removing, and refreshing files across
// both microfts2 and microvec.
type Indexer struct {
	fts *microfts2.DB
	vec *microvec.DB
}

// AddFile adds a file to both engines. microfts2 first (gets fileid
// and chunk offsets), then reads chunks and adds to microvec.
func (idx *Indexer) AddFile(path, strategy string) (uint64, error) {
	fileid, err := idx.fts.AddFile(path, strategy)
	if err != nil {
		return 0, fmt.Errorf("fts add %s: %w", path, err)
	}

	chunks, err := readChunks(path, fileid, idx.fts)
	if err != nil {
		return fileid, fmt.Errorf("read chunks %s: %w", path, err)
	}

	if err := idx.vec.AddFile(fileid, chunks); err != nil {
		return fileid, fmt.Errorf("vec add %s: %w", path, err)
	}

	return fileid, nil
}

// RemoveFile removes a file from both engines by path.
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
	return nil
}

// RemoveByID removes a file from both engines by fileid.
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
	return nil
}

// RefreshFile re-indexes a single file: reindex in microfts2 first,
// then swap vectors. FTS-first ordering ensures old state is intact
// if the reindex fails.
func (idx *Indexer) RefreshFile(path, strategy string) error {
	// Get old fileid to remove old vectors later
	status, err := idx.fts.CheckFile(path)
	if err != nil {
		return fmt.Errorf("check file %s: %w", path, err)
	}
	oldID := status.FileID

	// Re-index in microfts2 first (safe: failure leaves old state)
	fileid, err := idx.fts.Reindex(path, strategy)
	if err != nil {
		return fmt.Errorf("fts reindex %s: %w", path, err)
	}

	// Remove old vectors
	if err := idx.vec.RemoveFile(oldID); err != nil {
		return fmt.Errorf("vec remove old %s: %w", path, err)
	}

	// Read new chunks and add to microvec
	chunks, err := readChunks(path, fileid, idx.fts)
	if err != nil {
		return fmt.Errorf("read chunks %s: %w", path, err)
	}
	if err := idx.vec.AddFile(fileid, chunks); err != nil {
		return fmt.Errorf("vec add %s: %w", path, err)
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

// readChunks reads chunk text from a file using offsets from microfts2.
func readChunks(path string, fileid uint64, fts *microfts2.DB) ([][]byte, error) {
	info, err := fts.FileInfoByID(fileid)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	offsets := info.ChunkOffsets
	chunks := make([][]byte, len(offsets))
	for i, off := range offsets {
		start := off
		var end int64
		if i+1 < len(offsets) {
			end = offsets[i+1]
		} else {
			end = int64(len(data))
		}
		if start > int64(len(data)) {
			start = int64(len(data))
		}
		if end > int64(len(data)) {
			end = int64(len(data))
		}
		chunks[i] = data[start:end]
	}
	return chunks, nil
}
