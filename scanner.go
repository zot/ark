package ark

// CRC: crc-Scanner.md

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

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
	deferLinks  bool
	walkWorkers int // 0 => runtime.NumCPU(); walk pool size (test seam). R3437
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

// Scan enumerates every configured source directory (in parallel, via
// walkSources) and then applies the stateful classification serially. The
// parallel phase reads only the filesystem and the mutation-free Matcher; the
// serial merge is the only place that touches the actor-confined emptyFiles, the
// shared fts, and the LinkTable, so a symlink's classification and its position
// in the reverse index never depend on which worker reached it first. R3437
// CRC: crc-Scanner.md | R3437
func (sc *Scanner) Scan() (*ScanResults, error) {
	results := &ScanResults{}

	leaves, links, err := walkSources(sc.config, sc.matcher, sc.walkWorkers)
	if err != nil {
		return results, err
	}

	// Serial merge, in deterministic walk order. R3437
	for _, lf := range leaves {
		switch lf.class {
		case Included:
			// R1645, R1646, R1647: an empty file yields no chunks, so record and
			// report it separately; the caller evicts prior index entries.
			if sc.emptyFiles != nil && lf.size == 0 {
				if sc.emptyFiles.Has(lf.path, lf.mtime) {
					continue
				}
				sc.emptyFiles.Record(lf.path, lf.mtime)
				results.EmptyFiles = append(results.EmptyFiles, lf.path)
				continue
			}
			if sc.emptyFiles != nil {
				// Non-empty: forget any prior empty record so a later truncation
				// is re-detected.
				sc.emptyFiles.Forget(lf.path)
			}
			// Already indexed (fresh or stale) means known — stale is Refresh's job.
			status, cerr := sc.fts.CheckFile(lf.path)
			if cerr == nil && (status.Status == "fresh" || status.Status == "stale") {
				continue
			}
			results.NewFiles = append(results.NewFiles, FileEntry{Path: lf.path, Strategy: lf.strategy})
		case Unresolved:
			// Seen during the walk and matched no pattern. R3356
			results.NewUnresolved = append(results.NewUnresolved, UnresolvedRecord{
				Path:   lf.path,
				Dir:    lf.dir,
				Reason: UnresolvedReasonUncovered,
			})
		}
	}

	// Directory symlinks, Observed in deterministic walk order. R3352, R3373, R3433
	for _, lc := range links {
		if sc.deferLinks {
			// Off-actor reconcile walk: the LinkTable is actor-owned, so defer
			// classification — collect the path in walk order and let the actor
			// run linksToRegularFile + Observe afterward. R3433
			results.PendingLinks = append(results.PendingLinks, lc.path)
			continue
		}
		if linksToRegularFile(lc.path) {
			continue // a link to a regular file keeps today's handling
		}
		entry, status := sc.links.Observe(lc.path)
		switch status {
		case LinkTracked:
			results.TrackedLinks = append(results.TrackedLinks, entry)
		case LinkProposed:
			// The link proposes its target; it never admits it. R3355, R3357
			results.NewUnresolved = append(results.NewUnresolved, UnresolvedRecord{
				Path:    entry.Resolved,
				Dir:     lc.srcDir,
				Reason:  UnresolvedReasonSymlink,
				Symlink: entry.Path,
			})
		}
	}
	return results, nil
}

// scanLeaf is one classified file the parallel walk found, carrying everything
// the serial merge needs so the walk itself touches no shared Scanner state.
type scanLeaf struct {
	path     string
	dir      string // the source dir this leaf was found under
	class    Classification
	size     int64
	mtime    time.Time
	strategy string // precomputed for an Included leaf
}

// linkCand is one directory-symlink path the walk met, plus the source dir it
// lives in — needed to attribute a proposed link (see observeScannedLink). R3352
type linkCand struct {
	path   string
	srcDir string
}

// walkSources enumerates every configured source directory concurrently with a
// bounded worker pool (workers <= 0 means runtime.NumCPU()) and classifies each
// leaf with the Matcher. It reads only the filesystem and the mutation-free
// Matcher — no fts, no emptyFiles, no LinkTable — so it is safe across the pool
// and testable without a *DB. Directory symlinks are recorded and never
// descended (matching filepath.WalkDir). It returns the classified leaves and
// the link candidates in deterministic component-wise DFS order, so the serial
// merge's Observe order equals a sequential walk's. R3437
func walkSources(cfg *Config, matcher *Matcher, workers int) ([]scanLeaf, []linkCand, error) {
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var leaves []scanLeaf
	var links []linkCand

	// spawn runs fn in a pool goroutine, or inline when the pool is full — the
	// select's default is what keeps a bounded recursive walk from deadlocking on
	// the semaphore. Each fn is a walkDir that calls wg.Done itself. R3437
	spawn := func(fn func()) {
		wg.Add(1)
		select {
		case sem <- struct{}{}:
			go func() {
				defer func() { <-sem }()
				fn()
			}()
		default:
			fn()
		}
	}

	var walkDir func(dir string, src Source, includes, excludes []string)
	walkDir = func(dir string, src Source, includes, excludes []string) {
		defer wg.Done()
		entries, rderr := os.ReadDir(dir)
		if rderr != nil {
			return // inaccessible: skip, as the sequential walk did
		}
		for _, e := range entries {
			path := filepath.Join(dir, e.Name())

			// A symlink is recorded as an entity and the walk stops there — it is
			// never descended (WalkDir does not follow links). R3352, R3373
			if e.Type()&os.ModeSymlink != 0 {
				mu.Lock()
				links = append(links, linkCand{path: path, srcDir: src.Dir})
				mu.Unlock()
				continue
			}

			relPath, relErr := filepath.Rel(src.Dir, path)
			if relErr != nil || relPath == "." {
				continue
			}

			if e.IsDir() {
				// Excluded directory → do not descend (filepath.SkipDir). R2133
				if matcher.Classify(includes, excludes, path, src.Dir, true) == Excluded {
					continue
				}
				spawn(func() { walkDir(path, src, includes, excludes) })
				continue
			}

			// A regular file: classify; keep only Included / Unresolved. R2133
			cls := matcher.Classify(includes, excludes, path, src.Dir, false)
			if cls != Included && cls != Unresolved {
				continue // Excluded files: do nothing
			}
			leaf := scanLeaf{path: path, dir: src.Dir, class: cls}
			if info, infoErr := e.Info(); infoErr == nil {
				leaf.size = info.Size()
				leaf.mtime = info.ModTime()
			}
			if cls == Included {
				leaf.strategy = cfg.StrategyForFile(relPath, src.Strategies)
			}
			mu.Lock()
			leaves = append(leaves, leaf)
			mu.Unlock()
		}
	}

	for _, src := range cfg.Sources {
		if IsGlob(src.Dir) {
			continue // glob sources are directives, not walkable dirs
		}
		includes, excludes := cfg.EffectivePatterns(src)
		spawn(func() { walkDir(src.Dir, src, includes, excludes) })
	}
	wg.Wait()

	// Deterministic DFS order (component-wise, matching filepath.WalkDir) so a
	// parallel walk's Observe order equals a sequential walk's — files are a set,
	// but the link reverse-index order must be preserved. R3437
	sort.Slice(links, func(i, j int) bool { return dfsLess(links[i].path, links[j].path) })
	sort.Slice(leaves, func(i, j int) bool { return dfsLess(leaves[i].path, leaves[j].path) })
	return leaves, links, nil
}

// dfsLess orders two paths the way filepath.WalkDir visits them: component by
// component, so a directory's descendants sort between it and its siblings and
// each directory's entries sort by name. R3437
func dfsLess(a, b string) bool {
	as := strings.Split(a, string(os.PathSeparator))
	bs := strings.Split(b, string(os.PathSeparator))
	for i := 0; i < len(as) && i < len(bs); i++ {
		if as[i] != bs[i] {
			return as[i] < bs[i]
		}
	}
	return len(as) < len(bs)
}
