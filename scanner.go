package ark

// CRC: crc-Scanner.md

import (
	"os"
	"path/filepath"

	"microfts2"
)

// FileEntry is a file that should be indexed, with its source strategy.
type FileEntry struct {
	Path     string
	Strategy string
}

// ScanResults holds the output of a directory scan.
type ScanResults struct {
	NewFiles      []FileEntry
	NewUnresolved []UnresolvedRecord
}

// Scanner walks configured source directories and classifies files.
type Scanner struct {
	config  *Config
	matcher *Matcher
	fts     *microfts2.DB
}

// Scan walks all configured source directories and returns new files
// to index and new unresolved files.
func (sc *Scanner) Scan() (*ScanResults, error) {
	results := &ScanResults{}

	for _, src := range sc.config.Sources {
		if IsGlob(src.Dir) {
			continue // glob sources are directives, not walkable dirs
		}
		includes, excludes := sc.config.EffectivePatterns(src)
		dir := src.Dir

		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // skip inaccessible paths
			}

			// Get path relative to the source directory for pattern matching
			relPath, err := filepath.Rel(dir, path)
			if err != nil {
				return nil
			}
			if relPath == "." {
				return nil
			}

			isDir := d.IsDir()

			// Classify directories: if excluded, skip entirely
			if isDir {
				cls := sc.matcher.Classify(includes, excludes, relPath, true)
				if cls == Excluded {
					return filepath.SkipDir
				}
				return nil
			}

			// Classify files
			cls := sc.matcher.Classify(includes, excludes, relPath, false)
			switch cls {
			case Included:
				// Check if already indexed
				status, err := sc.fts.CheckFile(path)
				if err == nil && status.Status == "fresh" {
					return nil
				}
				results.NewFiles = append(results.NewFiles, FileEntry{
					Path:     path,
					Strategy: sc.config.StrategyForFile(relPath, src.Strategy),
				})
			case Unresolved:
				results.NewUnresolved = append(results.NewUnresolved, UnresolvedRecord{
					Path: path,
					Dir:  dir,
				})
			}
			// Excluded files: do nothing
			return nil
		})
		if err != nil {
			return results, err
		}
	}
	return results, nil
}
