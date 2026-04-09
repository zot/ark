package ark

// CRC: crc-Indexer.md

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"github.com/zot/microfts2"
	"io"
	"log"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"github.com/zot/microvec"
)

var tagRegex = regexp.MustCompile(`@([a-zA-Z][\w.-]*):`)

// chunkAccumulator collects chunk text and extracts tags via callback.
// CRC: crc-Indexer.md | R1116, R1117, R1118, R1120, R1121, R1122
type chunkAccumulator struct {
	chunks    [][]byte
	tagValues []TagValue
	tags      map[string]uint32
	defs      map[string]string
	strategy  string
}

func (a *chunkAccumulator) callback(chunkText string) {
	b := []byte(chunkText)
	a.chunks = append(a.chunks, b)
	tv := ExtractTagValues(b, a.strategy)
	a.tagValues = append(a.tagValues, tv...)
	for k, v := range TagCountsFromValues(tv) {
		if a.tags == nil {
			a.tags = make(map[string]uint32)
		}
		a.tags[k] += v
	}
	for k, v := range ExtractTagDefs(b) {
		if a.defs == nil {
			a.defs = make(map[string]string)
		}
		a.defs[k] = v
	}
}

// Indexer coordinates adding, removing, and refreshing files across
// both microfts2 and microvec. Extracts tags from file content.
type Indexer struct {
	fts       *microfts2.DB
	vec       *microvec.DB
	store     *Store
	pubsub    *PubSub         // nil when running without server
	scheduler *EventScheduler // nil when running without server
	config    *Config         // for schedule tag checks

	pendingSchedule []scheduleItem // accumulated during scan, drained after
}

// scheduleItem is a deferred EnsureUpcoming call.
type scheduleItem struct {
	tag, value, path string
	removes          []DateException // R1035: @remove: from source chunk
	adds             []DateException // R1036: @add: from source chunk
}

// CRC: crc-Indexer.md | Seq: seq-write-actor.md | R1054, R1065
// withFTS returns a shallow copy of the Indexer using a different
// microfts2.DB (typically from Copy()). Used by the write actor to
// run indexing off the main actor.
func (idx *Indexer) withFTS(fts *microfts2.DB) *Indexer {
	return &Indexer{
		fts:       fts,
		vec:       idx.vec,
		store:     idx.store,
		pubsub:    idx.pubsub,
		scheduler: idx.scheduler,
		config:    idx.config,
	}
}

// SetPubSub injects the pubsub registry. Called by the server after
// DB.Open — pubsub is a server-side concern, not a DB concern.
func (idx *Indexer) SetPubSub(ps *PubSub) {
	idx.pubsub = ps
}

// SetScheduler injects the event scheduler. Called by the server.
// CRC: crc-Indexer.md | R866
func (idx *Indexer) SetScheduler(sched *EventScheduler, config *Config) {
	idx.scheduler = sched
	idx.config = config
}

// writeDateIndex checks extracted tags against schedule config and
// ensures upcoming entries exist in the schedule log.
// Uses pre-extracted tag values to avoid re-parsing content.
// R953, R954, R956: skips files outside schedule filter scope.
// CRC: crc-Indexer.md | R866, R869, R870, R872
func (idx *Indexer) writeDateIndex(path string, tagValues []TagValue) {
	if idx.scheduler == nil || idx.config == nil {
		return
	}
	// Schedule log files are output, not input — skip to prevent cascade.
	if strings.HasPrefix(path, idx.config.dbPath+"/schedule/") {
		return
	}
	for _, tv := range tagValues {
		if _, ok := idx.config.IsScheduleTag(tv.Tag); ok && tv.Value != "" {
			if !idx.config.MatchesScheduleFilterForTag(path, tv.Tag) {
				continue
			}
			idx.pendingSchedule = append(idx.pendingSchedule, scheduleItem{
				tag: tv.Tag, value: tv.Value, path: path,
			})
		}
	}
}

// DrainSchedule returns and clears accumulated schedule items.
// Called after scan/refresh completes so EnsureUpcoming I/O
// doesn't block the actor during indexing.
func (idx *Indexer) DrainSchedule() []scheduleItem {
	items := idx.pendingSchedule
	idx.pendingSchedule = nil
	return items
}

// AddFile adds a file to both engines and extracts tags. Uses
// WithChunkCallback to accumulate clean chunk text for microvec
// and tag extraction, eliminating the splitChunks double-read.
// CRC: crc-Indexer.md | R1113, R1123
func (idx *Indexer) AddFile(path, strategy string) (uint64, error) {
	acc := chunkAccumulator{strategy: strategy}
	fileid, _, err := idx.fts.AddFileWithContent(path, strategy,
		microfts2.WithChunkCallback(acc.callback))
	if err != nil {
		return 0, fmt.Errorf("fts add %s: %w", path, err)
	}

	if err := idx.vec.AddFile(fileid, acc.chunks); err != nil {
		return fileid, fmt.Errorf("vec add %s: %w", path, err)
	}

	if idx.store != nil {
		if err := idx.store.UpdateTags(fileid, acc.tags); err != nil {
			return fileid, fmt.Errorf("update tags %s: %w", path, err)
		}
		if err := idx.store.UpdateTagDefs(fileid, acc.defs); err != nil {
			return fileid, fmt.Errorf("update tag defs %s: %w", path, err)
		}
		// CRC: crc-Indexer.md | Seq: seq-tag-value-index.md | R1103, R1106
		if err := idx.store.UpdateTagValues(fileid, acc.tagValues); err != nil {
			return fileid, fmt.Errorf("update tag values %s: %w", path, err)
		}
		// R795, R796: publish tag events to subscribers
		if idx.pubsub != nil {
			idx.pubsub.PublishAndWatch("", path, acc.tagValues)
		}
		// R866: write schedule log entries
		idx.writeDateIndex(path, acc.tagValues)
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
	// Vec removal is best-effort: file may never have been vectorized
	idx.vec.RemoveFile(fileid)
	if idx.store != nil {
		if err := idx.store.RemoveTags(fileid); err != nil {
			return fmt.Errorf("remove tags %s: %w", path, err)
		}
		idx.store.RemoveTagDefs(fileid)
		// CRC: crc-Indexer.md | Seq: seq-tag-value-index.md | R1105
		idx.store.RemoveTagValues(fileid)
	}
	return nil
}

// RemoveByID removes a file from both engines and tags by fileid.
func (idx *Indexer) RemoveByID(fileid uint64) error {
	info, err := idx.fts.FileInfoByID(fileid)
	if err != nil {
		return fmt.Errorf("file info %d: %w", fileid, err)
	}
	if err := idx.fts.RemoveFile(info.Names[0]); err != nil {
		return fmt.Errorf("fts remove %d: %w", fileid, err)
	}
	// Vec removal is best-effort: file may never have been vectorized
	idx.vec.RemoveFile(fileid)
	if idx.store != nil {
		if err := idx.store.RemoveTags(fileid); err != nil {
			return fmt.Errorf("remove tags %d: %w", fileid, err)
		}
		idx.store.RemoveTagDefs(fileid)
		idx.store.RemoveTagValues(fileid)
	}
	return nil
}

// refreshPrep holds data prepared by a worker for the ChanSvc to execute.
// Workers populate this via prepareRefresh (file I/O + tag extraction).
// The ChanSvc executes writes via executeRefresh (LMDB mutations).
type refreshPrep struct {
	path      string
	strategy  string
	oldID     uint64
	isAppend  bool
	data      []byte            // full file content
	tags      map[string]uint32 // pre-extracted tags
	defs      map[string]string // pre-extracted tag defs
	tagValues []TagValue        // pre-extracted tag values for pubsub
	// Append-specific fields
	newBytes []byte
	baseLine int
	fullHash string
	fileSize int64
	modTime  int64
}

// prepareRefresh reads a file and extracts tags — safe to call from
// multiple goroutines. LMDB reads (DetectAppend, FileInfoByID) are
// concurrent-safe. Returns a prep struct for executeRefresh.
func (idx *Indexer) prepareRefresh(path, strategy string, fileID uint64) (*refreshPrep, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	prep := &refreshPrep{
		path:     path,
		strategy: strategy,
		oldID:    fileID,
		data:     data,
	}

	// Try append detection (LMDB reads are concurrent-safe)
	if ok, _ := idx.DetectAppend(path, fileID); ok {
		info, err := idx.fts.FileInfoByID(fileID)
		if err == nil && info.FileLength > 0 && int64(len(data)) > info.FileLength {
			prep.isAppend = true
			prep.newBytes = data[info.FileLength:]
			if n := len(info.Chunks); n > 0 {
				_, endLine := parseRange(info.Chunks[n-1].Location)
				prep.baseLine = endLine
			}
			fullHash := sha256.Sum256(data)
			prep.fullHash = fmt.Sprintf("%x", fullHash)
			fi, _ := os.Stat(path)
			prep.fileSize = fi.Size()
			prep.modTime = fi.ModTime().UnixNano()
			tagBytes := tagWindowForAppend(data, info.FileLength)
			prep.tagValues = ExtractTagValues(tagBytes, prep.strategy)
			prep.tags = TagCountsFromValues(prep.tagValues)
			prep.defs = ExtractTagDefs(tagBytes)
			return prep, nil
		}
	}

	// Full refresh path: tags extracted in executeFullRefresh via callback (R1126)
	return prep, nil
}

// executeRefresh runs all LMDB writes for a prepared file.
// Must be called from the ChanSvc goroutine (single writer).
func (idx *Indexer) executeRefresh(prep *refreshPrep) error {
	if prep.isAppend {
		err := idx.fts.AppendChunks(prep.oldID, prep.newBytes, prep.strategy,
			microfts2.WithBaseLine(prep.baseLine),
			microfts2.WithContentHash(prep.fullHash),
			microfts2.WithModTime(prep.modTime),
			microfts2.WithFileLength(prep.fileSize),
		)
		if err != nil {
			// Append failed — fall through to full reindex
			return idx.executeFullRefresh(prep)
		}
		// Vectors: full refresh
		idx.vec.RemoveFile(prep.oldID)
		_, chunks, err := splitChunks(prep.data, prep.oldID, idx.fts)
		if err != nil {
			return fmt.Errorf("read chunks %s: %w", prep.path, err)
		}
		if err := idx.vec.AddFile(prep.oldID, chunks); err != nil {
			return fmt.Errorf("vec add %s: %w", prep.path, err)
		}
		// Tags: incremental
		if idx.store != nil {
			if err := idx.store.AppendTags(prep.oldID, prep.tags); err != nil {
				return fmt.Errorf("append tags %s: %w", prep.path, err)
			}
			if err := idx.store.AppendTagDefs(prep.oldID, prep.defs); err != nil {
				return fmt.Errorf("append tag defs %s: %w", prep.path, err)
			}
			// CRC: crc-Indexer.md | Seq: seq-tag-value-index.md | R1104
			if err := idx.store.AppendTagValues(prep.oldID, prep.tagValues); err != nil {
				return fmt.Errorf("append tag values %s: %w", prep.path, err)
			}
			// R795, R796: publish tag events from appended content
			if idx.pubsub != nil {
				idx.pubsub.PublishAndWatch("", prep.path, prep.tagValues)
			}
			idx.writeDateIndex(prep.path, prep.tagValues)
		}
		return nil
	}
	return idx.executeFullRefresh(prep)
}

// executeFullRefresh does a complete reindex. For full refresh, tags
// are extracted from clean chunk text via callback (R1114, R1124, R1126).
// For append prep with pre-extracted tags, those are used instead.
func (idx *Indexer) executeFullRefresh(prep *refreshPrep) error {
	acc := chunkAccumulator{strategy: prep.strategy}
	fileid, _, err := idx.fts.ReindexWithContent(prep.path, prep.strategy,
		microfts2.WithChunkCallback(acc.callback))
	if err != nil {
		return fmt.Errorf("fts reindex %s: %w", prep.path, err)
	}

	idx.vec.RemoveFile(prep.oldID)

	if err := idx.vec.AddFile(fileid, acc.chunks); err != nil {
		return fmt.Errorf("vec add %s: %w", prep.path, err)
	}

	if idx.store != nil {
		if fileid != prep.oldID {
			idx.store.RemoveTags(prep.oldID)
			idx.store.RemoveTagDefs(prep.oldID)
			idx.store.RemoveTagValues(prep.oldID)
		}
		// Use pre-extracted values (append prep) or callback-extracted values
		tagValues := prep.tagValues
		if tagValues == nil {
			tagValues = acc.tagValues
		}
		tags := prep.tags
		if tags == nil {
			tags = acc.tags
		}
		if err := idx.store.UpdateTags(fileid, tags); err != nil {
			return fmt.Errorf("update tags %s: %w", prep.path, err)
		}
		defs := prep.defs
		if defs == nil {
			defs = acc.defs
		}
		if err := idx.store.UpdateTagDefs(fileid, defs); err != nil {
			return fmt.Errorf("update tag defs %s: %w", prep.path, err)
		}
		// CRC: crc-Indexer.md | Seq: seq-tag-value-index.md | R1103
		if err := idx.store.UpdateTagValues(fileid, tagValues); err != nil {
			return fmt.Errorf("update tag values %s: %w", prep.path, err)
		}
		// R795, R796: publish tag events from refreshed content
		if idx.pubsub != nil {
			idx.pubsub.PublishAndWatch("", prep.path, tagValues)
		}
		// R866: write schedule log entries
		idx.writeDateIndex(prep.path, tagValues)
	}

	return nil
}

// RefreshFile re-indexes a single file. Uses prepareRefresh + executeRefresh
// sequentially (no parallelism for single-file operations).
func (idx *Indexer) RefreshFile(path, strategy string) error {
	status, err := idx.fts.CheckFile(path)
	if err != nil {
		return fmt.Errorf("check file %s: %w", path, err)
	}
	prep, err := idx.prepareRefresh(path, strategy, status.FileID)
	if err != nil {
		return err
	}
	return idx.executeRefresh(prep)
}

// DetectAppend checks whether a file change is append-only by hashing
// the first FileLength bytes and comparing to the stored ContentHash.
// Returns true if the prefix is unchanged and the file grew.
func (idx *Indexer) DetectAppend(path string, fileid uint64) (bool, error) {
	info, err := idx.fts.FileInfoByID(fileid)
	if err != nil {
		return false, err
	}
	var zeroHash [32]byte
	if info.FileLength <= 0 || info.ContentHash == zeroHash {
		return false, nil
	}

	fi, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if fi.Size() <= info.FileLength {
		return false, nil // didn't grow
	}

	// Hash the first FileLength bytes
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.CopyN(h, f, info.FileLength); err != nil {
		return false, err
	}
	var hash [32]byte
	copy(hash[:], h.Sum(nil))

	return hash == info.ContentHash, nil
}

// AppendFile indexes only the new content appended to a file.
// FTS uses AppendChunks; vectors get a full refresh; tags are incremental.
func (idx *Indexer) AppendFile(path string, fileid uint64, strategy string) error {
	info, err := idx.fts.FileInfoByID(fileid)
	if err != nil {
		return fmt.Errorf("file info %d: %w", fileid, err)
	}

	// Read only the new bytes
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	newBytes := data[info.FileLength:]

	// Parse last chunk range for base line
	baseLine := 0
	if n := len(info.Chunks); n > 0 {
		_, endLine := parseRange(info.Chunks[n-1].Location)
		baseLine = endLine
	}

	// Compute new file metadata
	fullHash := sha256.Sum256(data)
	fi, _ := os.Stat(path)

	// Append to FTS index
	err = idx.fts.AppendChunks(fileid, newBytes, strategy,
		microfts2.WithBaseLine(baseLine),
		microfts2.WithContentHash(fmt.Sprintf("%x", fullHash)),
		microfts2.WithModTime(fi.ModTime().UnixNano()),
		microfts2.WithFileLength(fi.Size()),
	)
	if err != nil {
		return fmt.Errorf("fts append %s: %w", path, err)
	}

	// Vectors: full refresh (remove old, re-add all chunks)
	idx.vec.RemoveFile(fileid)
	_, allChunks, err := splitChunks(data, fileid, idx.fts)
	if err != nil {
		return fmt.Errorf("read chunks %s: %w", path, err)
	}
	if err := idx.vec.AddFile(fileid, allChunks); err != nil {
		return fmt.Errorf("vec add %s: %w", path, err)
	}

	// Tags and defs: incremental — scan from previous newline to catch boundary-split tags
	if idx.store != nil {
		tagBytes := tagWindowForAppend(data, info.FileLength)
		tagValues := ExtractTagValues(tagBytes, strategy)
		if err := idx.store.AppendTags(fileid, TagCountsFromValues(tagValues)); err != nil {
			return fmt.Errorf("append tags %s: %w", path, err)
		}
		defs := ExtractTagDefs(tagBytes)
		if err := idx.store.AppendTagDefs(fileid, defs); err != nil {
			return fmt.Errorf("append tag defs %s: %w", path, err)
		}
		// CRC: crc-Indexer.md | Seq: seq-tag-value-index.md | R1104
		if err := idx.store.AppendTagValues(fileid, tagValues); err != nil {
			return fmt.Errorf("append tag values %s: %w", path, err)
		}
		// R795, R796: publish tag events from appended content
		if idx.pubsub != nil {
			idx.pubsub.PublishAndWatch("", path, tagValues)
		}
		// R866: write schedule log entries
		idx.writeDateIndex(path, tagValues)
	}

	return nil
}

// CRC: crc-Indexer.md | Seq: seq-parallel-refresh.md
// RefreshStale re-indexes all stale files in parallel. Workers read files
// and extract tags concurrently. A ChanSvc actor serializes LMDB writes.
// Returns the list of missing files found during the check.
func (idx *Indexer) RefreshStale(patterns []string, matcher *Matcher) ([]microfts2.FileStatus, error) {
	statuses, err := idx.fts.StaleFiles()
	if err != nil {
		return nil, fmt.Errorf("stale files: %w", err)
	}

	// Partition into missing and stale (with optional pattern filter)
	var missing []microfts2.FileStatus
	var stale []microfts2.FileStatus
	for _, s := range statuses {
		if s.Status == "missing" {
			missing = append(missing, s)
			continue
		}
		if s.Status != "stale" {
			continue
		}
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
		stale = append(stale, s)
	}

	if len(stale) == 0 {
		return missing, nil
	}

	// Single file: skip goroutine overhead
	if len(stale) == 1 {
		s := stale[0]
		prep, err := idx.prepareRefresh(s.Path, s.Strategy, s.FileID)
		if err != nil {
			log.Printf("refresh: skip %s: %v", s.Path, err)
			return missing, nil
		}
		if err := idx.executeRefresh(prep); err != nil {
			return missing, fmt.Errorf("refresh %s: %w", s.Path, err)
		}
		return missing, nil
	}

	// Parallel path: workers prepare, ChanSvc writes
	numWorkers := runtime.NumCPU()
	if numWorkers > len(stale) {
		numWorkers = len(stale)
	}

	jobCh := make(chan microfts2.FileStatus, len(stale))
	writeCh := make(chan func() error, numWorkers)
	var errCount int
	var errMu sync.Mutex

	// ChanSvc: serialize all LMDB writes
	var writeWg sync.WaitGroup
	writeWg.Add(1)
	go func() {
		defer writeWg.Done()
		for fn := range writeCh {
			if err := fn(); err != nil {
				log.Printf("refresh: %v", err)
				errMu.Lock()
				errCount++
				errMu.Unlock()
			}
		}
	}()

	// Workers: read files + extract tags in parallel
	var workerWg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
			for s := range jobCh {
				prep, err := idx.prepareRefresh(s.Path, s.Strategy, s.FileID)
				if err != nil {
					log.Printf("refresh: skip %s: %v", s.Path, err)
					continue
				}
				writeCh <- func() error {
					return idx.executeRefresh(prep)
				}
			}
		}()
	}

	// Feed jobs
	for _, s := range stale {
		jobCh <- s
	}
	close(jobCh)

	// Workers finish → close write channel → ChanSvc drains
	workerWg.Wait()
	close(writeCh)
	writeWg.Wait()

	if errCount > 0 {
		log.Printf("refresh: %d file(s) had errors", errCount)
	}
	return missing, nil
}

// CRC: crc-Indexer.md | Seq: seq-write-actor.md | R1053, R1055
// refreshBatch re-indexes a list of stale files using parallel workers
// for prep and sequential writes. Designed to run in the write goroutine
// (off the main actor).
func (idx *Indexer) refreshBatch(stale []microfts2.FileStatus) {
	if len(stale) == 0 {
		return
	}

	// Single file: skip goroutine overhead
	if len(stale) == 1 {
		s := stale[0]
		prep, err := idx.prepareRefresh(s.Path, s.Strategy, s.FileID)
		if err != nil {
			log.Printf("refresh: skip %s: %v", s.Path, err)
			return
		}
		if err := idx.executeRefresh(prep); err != nil {
			log.Printf("refresh: %s: %v", s.Path, err)
		}
		return
	}

	// Parallel prep, sequential write (same pattern as RefreshStale)
	numWorkers := runtime.NumCPU()
	if numWorkers > len(stale) {
		numWorkers = len(stale)
	}

	jobCh := make(chan microfts2.FileStatus, len(stale))
	writeCh := make(chan func() error, numWorkers)

	// Sequential writer
	var writeWg sync.WaitGroup
	writeWg.Add(1)
	go func() {
		defer writeWg.Done()
		for fn := range writeCh {
			if err := fn(); err != nil {
				log.Printf("refresh: %v", err)
			}
		}
	}()

	// Parallel workers
	var workerWg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
			for s := range jobCh {
				prep, err := idx.prepareRefresh(s.Path, s.Strategy, s.FileID)
				if err != nil {
					log.Printf("refresh: skip %s: %v", s.Path, err)
					continue
				}
				writeCh <- func() error {
					return idx.executeRefresh(prep)
				}
			}
		}()
	}

	for _, s := range stale {
		jobCh <- s
	}
	close(jobCh)
	workerWg.Wait()
	close(writeCh)
	writeWg.Wait()
}

// tagWindowForAppend returns a slice of data suitable for tag extraction
// during an append. It backs up from the split point to the previous
// newline to catch tags that straddle the boundary.
func tagWindowForAppend(data []byte, splitPos int64) []byte {
	start := splitPos
	for start > 0 && data[start-1] != '\n' {
		start--
	}
	return data[start:]
}

// TagCountsFromValues derives tag counts from extracted tag values.
// This avoids running a second regex pass over the same content.
func TagCountsFromValues(values []TagValue) map[string]uint32 {
	if len(values) == 0 {
		return nil
	}
	tags := make(map[string]uint32)
	for _, tv := range values {
		tags[tv.Tag]++
	}
	return tags
}

// ExtractTags scans content for @tag: patterns and returns tag counts.
// Tag names are stored lowercase. The colon is required (disambiguates
// from emails and mentions). Skips mentioned tags per R1317-R1325.
func ExtractTags(content []byte) map[string]uint32 {
	locs := tagRegex.FindAllSubmatchIndex(content, -1)
	if len(locs) == 0 {
		return nil
	}
	markdown := false // ExtractTags doesn't know strategy; callers use ExtractTagValues
	tags := make(map[string]uint32)
	for _, loc := range locs {
		atPos := loc[0] // byte offset of '@'
		if isMention(content, atPos, markdown) {
			continue
		}
		name := strings.ToLower(string(content[loc[2]:loc[3]]))
		tags[name]++
	}
	if len(tags) == 0 {
		return nil
	}
	return tags
}

// tagValueRegex matches @tag: followed by the value to end of line.
// For compound tags (@ref: path @topic: body), the greedy [^\n]* captures
// everything — the outer tag's value includes subsequent tags as text.
// The loop in ExtractTagValues peels embedded tags from the value,
// so all tags fire for pubsub. Outer values are preserved intact.
var tagValueRegex = regexp.MustCompile(`@([a-zA-Z][\w.-]*):\s*([^\n]*)`)

// ExtractTagValues scans content for @tag: patterns and returns name+value pairs.
// Used by both tag counting and pubsub delivery. Skips mentioned tags
// per R1317-R1325. Strategy controls whether markdown-specific heuristics
// (fenced/indented code) apply.
// Compound tags on a single line produce entries for each embedded tag,
// with outer tags keeping their full value. (Resolves O26.)
func ExtractTagValues(content []byte, strategy string) []TagValue {
	markdown := strategy == "markdown"
	locs := tagValueRegex.FindAllSubmatchIndex(content, -1)
	if len(locs) == 0 {
		return nil
	}
	values := make([]TagValue, 0, len(locs))
	for _, loc := range locs {
		atPos := loc[0]
		if isMention(content, atPos, markdown) {
			continue
		}
		tag := strings.ToLower(string(content[loc[2]:loc[3]]))
		val := content[loc[4]:loc[5]]
		values = append(values, TagValue{
			Tag:   tag,
			Value: strings.TrimSpace(string(val)),
		})
		// Peel compound tags from the value portion
		for sub := tagValueRegex.FindSubmatch(val); sub != nil; sub = tagValueRegex.FindSubmatch(val) {
			val = sub[2]
			values = append(values, TagValue{
				Tag:   strings.ToLower(string(sub[1])),
				Value: strings.TrimSpace(string(val)),
			})
		}
	}
	return values
}

// isMention checks whether a tag at the given byte offset is a mention
// (not a real annotation). Four heuristics applied in order.
// CRC: crc-Indexer.md | R1317-R1325
func isMention(content []byte, atPos int, markdown bool) bool {
	// R1320: no preceding whitespace and not at line start → embedded in token
	if atPos > 0 {
		prev := content[atPos-1]
		if prev != ' ' && prev != '\t' && prev != '\n' {
			return true
		}
	}

	// R1321: odd quote count before @ on same line → inside quotes
	quotes := 0
	for i := atPos - 1; i >= 0 && content[i] != '\n'; i-- {
		if content[i] == '`' || content[i] == '"' {
			quotes++
		}
	}
	if quotes%2 != 0 {
		return true
	}

	if !markdown {
		return false
	}

	// R1322: inside fenced code block → mention
	// Count fence delimiters (``` or ~~~) in lines above atPos
	fences := 0
	i := 0
	for i < atPos {
		// Find start of next line
		lineStart := i
		lineEnd := bytes.IndexByte(content[i:], '\n')
		if lineEnd < 0 {
			lineEnd = len(content)
		} else {
			lineEnd += i
		}
		if lineStart >= atPos {
			break
		}
		line := content[lineStart:lineEnd]
		trimmed := bytes.TrimLeft(line, " \t")
		if bytes.HasPrefix(trimmed, []byte("```")) || bytes.HasPrefix(trimmed, []byte("~~~")) {
			fences++
		}
		i = lineEnd + 1
	}
	if fences%2 != 0 {
		return true
	}

	// R1323: indented code block (4+ spaces or tab at line start)
	lineStart := atPos
	for lineStart > 0 && content[lineStart-1] != '\n' {
		lineStart--
	}
	if atPos-lineStart >= 4 {
		prefix := content[lineStart : lineStart+4]
		if prefix[0] == '\t' || (prefix[0] == ' ' && prefix[1] == ' ' && prefix[2] == ' ' && prefix[3] == ' ') {
			return true
		}
	} else if lineStart < len(content) && content[lineStart] == '\t' {
		return true
	}

	return false
}

// tagDefRegex matches @tag: definitions at line start. First word after
// "@tag:" is the tag name, rest is description.
var tagDefRegex = regexp.MustCompile(`(?:^|\n)@tag:\s+(\S+)\s+(.+)`)

// ExtractTagDefs scans content for @tag: name description lines.
// Returns map of tagname → description.
func ExtractTagDefs(content []byte) map[string]string {
	matches := tagDefRegex.FindAllSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	defs := make(map[string]string)
	for _, m := range matches {
		name := strings.ToLower(string(m[1]))
		desc := strings.TrimSpace(string(m[2]))
		defs[name] = desc
	}
	return defs
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
	chunks := make([][]byte, len(info.Chunks))
	for i, r := range info.Chunks {
		chunks[i] = []byte(extractByRange(lines, r.Location))
	}
	return data, chunks, nil
}
