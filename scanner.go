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
	// TrackedLinks lists the directory symlinks this scan recorded as
	// tracked, in walk order. They are entities the index knows about, not
	// files: the alias lives on the link rather than on each file beneath
	// it, so the cost is one entry per link. R3352, R3373
	TrackedLinks []*LinkEntry
	// PendingLinks lists every symlink path the walk met, in walk order, when the
	// scanner ran with deferLinks (the off-actor reconcile walk). Classification —
	// linksToRegularFile, Observe, and the resulting TrackedLinks / NewUnresolved
	// entries — is done later on the DB actor, which owns the LinkTable. R3433
	PendingLinks []string
}

// Scanner walks configured source directories and classifies files.
// R1644
type Scanner struct {
	config     *Config
	matcher    *Matcher
	fts        *microfts2.DB
	emptyFiles *EmptyFiles
	// links classifies each directory symlink the walk meets and follows
	// its chain with the cycle guard. R3352, R3353
	links *LinkTable
	// deferLinks is set on the throwaway scanner the reconcile walk runs off the
	// DB actor. The LinkTable is actor-owned, so instead of Observing links inline
	// the off-actor walk collects their paths (walk order) into ScanResults.PendingLinks
	// and the actor runs Observe on them afterward. Zero (inline Observe) for the
	// shared scanner every synchronous caller uses. R3433
	deferLinks bool
}

// NewScanner assembles a Scanner with every collaborator it needs.
//
// It exists so production and tests cannot wire different Scanners. A
// hand-wired fixture that omits a field looks identical to a correct one
// and keeps the suite green while the omitted collaborator's code is dead
// — that is exactly how `withFTS` dropped extmap and db, killing all @ext
// routing for three months with no failing test.
// CRC: crc-Scanner.md | R1644
func NewScanner(config *Config, matcher *Matcher, fts *microfts2.DB, emptyFiles *EmptyFiles) *Scanner {
	return &Scanner{
		config:     config,
		matcher:    matcher,
		fts:        fts,
		emptyFiles: emptyFiles,
		links:      NewLinkTable(config),
	}
}

// Links exposes the link table this scanner maintains, so callers that
// resolve addresses share the one the walk populates rather than deriving
// a second, divergent view of the address space. R3359
// CRC: crc-Scanner.md | R3359
func (sc *Scanner) Links() *LinkTable { return sc.links }

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

			// A symlink is recorded as an entity and the walk stops there
			// — it never descends. WalkDir does not follow links, so
			// d.IsDir() is false for a directory symlink and it would
			// otherwise fall through to the file branch below, be matched
			// against *file* patterns, and be silently dropped when its
			// name fails them. That is the originating defect: its
			// contents unreachable, with no error and no log line.
			//
			// Stat follows the link to learn whether it names a directory.
			// A dangling link cannot answer that, and is recorded anyway:
			// readlink still succeeds, so recording turns an invisible
			// omission into a statable fact.
			// CRC: crc-Scanner.md | R3352, R3353, R3373, R3433
			if d.Type()&os.ModeSymlink != 0 {
				if sc.deferLinks {
					// Off-actor reconcile walk: the LinkTable is actor-owned, so
					// defer classification. Collect the path in walk order and let
					// the actor run linksToRegularFile + Observe. R3433
					results.PendingLinks = append(results.PendingLinks, path)
					return nil
				}
				if linksToRegularFile(path) {
					return nil // a link to a regular file keeps today's handling
				}
				entry, status := sc.links.Observe(path)
				switch status {
				case LinkTracked:
					results.TrackedLinks = append(results.TrackedLinks, entry)
				case LinkProposed:
					// The link proposes its target; it never admits it.
					// R3355, R3357
					results.NewUnresolved = append(results.NewUnresolved, UnresolvedRecord{
						Path:    entry.Resolved,
						Dir:     dir,
						Reason:  UnresolvedReasonSymlink,
						Symlink: entry.Path,
					})
				}
				return nil
			}

			isDir := d.IsDir()

			// Classify directories: if excluded, skip entirely
			if isDir {
				// CRC: crc-Scanner.md | R2133
				cls := sc.matcher.Classify(includes, excludes, path, dir, true)
				if cls == Excluded {
					return filepath.SkipDir
				}
				return nil
			}

			// Classify files
			// CRC: crc-Scanner.md | R2133
			cls := sc.matcher.Classify(includes, excludes, path, dir, false)
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
				// Seen during the walk and matched no pattern. R3356
				results.NewUnresolved = append(results.NewUnresolved, UnresolvedRecord{
					Path:   path,
					Dir:    dir,
					Reason: UnresolvedReasonUncovered,
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
