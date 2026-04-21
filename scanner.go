package ark

// CRC: crc-Scanner.md

import (
	"os"
	"path/filepath"

	"github.com/zot/microfts2"
)

// FileEntry is a file that should be indexed, with its source strategy.
type FileEntry struct {
	Path     string
	Strategy string
}

// ScanResults holds the output of a directory scan.
// EmptyFiles lists size-zero paths newly observed (or observed with
// a changed mtime) in this scan. R1647
type ScanResults struct {
	NewFiles      []FileEntry
	NewUnresolved []UnresolvedRecord
	EmptyFiles    []string
}

// Scanner walks configured source directories and classifies files.
// R1644
type Scanner struct {
	config     *Config
	matcher    *Matcher
	fts        *microfts2.DB
	emptyFiles *EmptyFiles
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
				// R1645, R1646, R1647: detect empty files (size == 0) up front —
				// any chunker would yield nothing, so we record and report the
				// path separately. The caller evicts prior index entries.
				if sc.emptyFiles != nil {
					info, infoErr := d.Info()
					if infoErr == nil && info.Size() == 0 {
						mtime := info.ModTime()
						if sc.emptyFiles.Has(path, mtime) {
							return nil
						}
						sc.emptyFiles.Record(path, mtime)
						results.EmptyFiles = append(results.EmptyFiles, path)
						return nil
					}
					// Non-empty file: forget any prior empty record so a later
					// truncation is re-detected.
					sc.emptyFiles.Forget(path)
				}
				// Check if already indexed (fresh or stale — both mean it's known;
				// stale files are handled by Refresh, not re-added as new)
				status, err := sc.fts.CheckFile(path)
				if err == nil && (status.Status == "fresh" || status.Status == "stale") {
					return nil
				}
				results.NewFiles = append(results.NewFiles, FileEntry{
					Path:     path,
					Strategy: sc.config.StrategyForFile(relPath, src.Strategies),
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
