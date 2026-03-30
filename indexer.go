package ark

// CRC: crc-Indexer.md

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/zot/microfts2"

	"github.com/zot/microvec"
)

var tagRegex = regexp.MustCompile(`@([a-zA-Z][\w.-]*):`)

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

// WriteDayBucketsForFile parses schedule tags and @ack: entries from
// content, discretizes into day buckets with ack status, and writes.
// Called unconditionally — clears stale buckets when no schedule tags remain.
// R953, R954: skips files outside schedule filter scope (except schedule log
// files which are always eligible — they contain materialized events).
// CRC: crc-Indexer.md | R933, R934, R935, R866
func (idx *Indexer) WriteDayBucketsForFile(fileid uint64, path string, content []byte) {
	if idx.config == nil || idx.store == nil {
		return
	}
	isScheduleLog := strings.HasPrefix(path, idx.config.dbPath+"/schedule/")
	if !isScheduleLog && !idx.config.MatchesScheduleFilter(path) {
		// Quick check: does any per-tag filter match this file?
		anyTagMatch := false
		for tag := range idx.config.Schedule.TagConfig {
			if idx.config.MatchesScheduleFilterForTag(path, tag) {
				anyTagMatch = true
				break
			}
		}
		if !anyTagMatch {
			return
		}
	}
	entryIndex := make(map[bucketMapKey]int)
	var allEntries []DayBucketEntry
	var allAcks []AckEntry
	loc := time.Now().Location()

	if isScheduleLog {
		// R869, R870: schedule log files — parse @ark-event-upcoming: and @ark-event-fired:
		idx.dayBucketsFromLogFile(fileid, path, content, loc, entryIndex, &allEntries)
	} else {
		// Source files — parse @tag: lines for schedule tags
		for tag, defaultDur := range idx.config.ScheduleTags() {
			if !idx.config.MatchesScheduleFilterForTag(path, tag) {
				continue
			}
			acks := ParseAcks(content, tag)
			allAcks = append(allAcks, acks...)

			prefix := "@" + tag + ":"
			for _, line := range strings.Split(string(content), "\n") {
				trimmed := strings.TrimSpace(line)
				pos := strings.Index(trimmed, prefix)
				if pos < 0 {
					continue
				}
				value := strings.TrimSpace(trimmed[pos+len(prefix):])
				if value == "" {
					continue
				}

				dr, err := ParseDateValue(value, defaultDur, loc)
				if err != nil {
					continue
				}

				start := dr.Start
				end := dr.End
				if end.Before(start) {
					end = start
				}
				ev := DayBucketEvent{
					Start:   dr.Start,
					End:     dr.End,
					Summary: dr.Description,
					AllDay:  dr.AllDay,
				}
				for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
					dateStr := d.Format("20060102")
					key := bucketMapKey{dateStr, tag}
					if i, ok := entryIndex[key]; ok {
						allEntries[i].Events = append(allEntries[i].Events, ev)
					} else {
						entryIndex[key] = len(allEntries)
						allEntries = append(allEntries, DayBucketEntry{
							Date:   dateStr,
							Tag:    tag,
							Path:   path,
							FileID: fileid,
							Events: []DayBucketEvent{ev},
						})
					}
				}
			}
		}
	}

	// Write unconditionally — WriteDayBuckets clears stale entries when empty
	if err := idx.store.WriteDayBucketsWithAcks(fileid, allEntries, allAcks); err != nil {
		log.Printf("schedule: WriteDayBuckets error for %s: %v", path, err)
	}

	// R970, R971: resolve check-gaps when acks cover fired dates
	if idx.scheduler != nil && !isScheduleLog && len(allAcks) > 0 {
		idx.scheduler.ResolveCheckGapsFromAcks(path, allAcks)
	}
}

// dayBucketsFromLogFile parses a schedule log file and creates day bucket entries
// from @ark-event-upcoming: and @ark-event-fired: tags. The event's tag name comes
// from @ark-event: in each chunk.
// CRC: crc-Indexer.md | R869, R870
type bucketMapKey struct{ date, tag string }

func (idx *Indexer) dayBucketsFromLogFile(fileid uint64, path string, content []byte, loc *time.Location, entryIndex map[bucketMapKey]int, allEntries *[]DayBucketEntry) {
	var curTag string
	for _, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "@ark-event:") {
			curTag = strings.TrimSpace(trimmed[len("@ark-event:"):])
			continue
		}
		if curTag == "" {
			continue
		}
		var dateStr string
		if strings.HasPrefix(trimmed, "@ark-event-upcoming:") {
			dateStr = strings.TrimSpace(trimmed[len("@ark-event-upcoming:"):])
		} else if strings.HasPrefix(trimmed, "@ark-event-fired:") {
			dateStr = strings.TrimSpace(trimmed[len("@ark-event-fired:"):])
		} else {
			continue
		}
		t, err := ParseDate(dateStr)
		if err != nil {
			continue
		}
		dayStr := t.Format("20060102")
		ev := DayBucketEvent{
			Start: t,
			End:   t,
		}
		// Apply default duration if known
		if defaultDur, ok := idx.config.IsScheduleTag(curTag); ok && defaultDur != "" {
			if defaultDur == "all-day" {
				ev.AllDay = true
				ev.End = time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, loc)
			} else if d, derr := time.ParseDuration(defaultDur); derr == nil {
				ev.End = t.Add(d)
			}
		}
		key := bucketMapKey{dayStr, curTag}
		if i, ok := entryIndex[key]; ok {
			(*allEntries)[i].Events = append((*allEntries)[i].Events, ev)
		} else {
			entryIndex[key] = len(*allEntries)
			*allEntries = append(*allEntries, DayBucketEntry{
				Date:   dayStr,
				Tag:    curTag,
				Path:   path,
				FileID: fileid,
				Events: []DayBucketEvent{ev},
			})
		}
	}
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
		defs := ExtractTagDefs(data)
		if err := idx.store.UpdateTagDefs(fileid, defs); err != nil {
			return fileid, fmt.Errorf("update tag defs %s: %w", path, err)
		}
		// R795, R796: publish tag events to subscribers
		if idx.pubsub != nil {
			idx.pubsub.PublishAndWatch("", path, ExtractTagValues(data))
		}
		// R866: write schedule log entries + day buckets
		idx.writeDateIndex(path, ExtractTagValues(data))
		idx.WriteDayBucketsForFile(fileid, path, data)
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
		// Clean up day bucket entries (TD/TF records)
		if err := idx.store.ClearDayBuckets(fileid); err != nil {
			return fmt.Errorf("clear day buckets %s: %w", path, err)
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
		// Clean up day bucket entries (TD/TF records)
		if err := idx.store.ClearDayBuckets(fileid); err != nil {
			return fmt.Errorf("clear day buckets %d: %w", fileid, err)
		}
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
			prep.tags = ExtractTags(tagBytes)
			prep.defs = ExtractTagDefs(tagBytes)
			prep.tagValues = ExtractTagValues(tagBytes)
			return prep, nil
		}
	}

	// Full refresh path
	prep.tags = ExtractTags(data)
	prep.defs = ExtractTagDefs(data)
	prep.tagValues = ExtractTagValues(data)
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
			// R795, R796: publish tag events from appended content
			if idx.pubsub != nil {
				idx.pubsub.PublishAndWatch("", prep.path, prep.tagValues)
			}
			idx.writeDateIndex(prep.path, prep.tagValues)
			idx.WriteDayBucketsForFile(prep.oldID, prep.path, prep.data)
		}
		return nil
	}
	return idx.executeFullRefresh(prep)
}

// executeFullRefresh does a complete reindex using pre-extracted tags.
func (idx *Indexer) executeFullRefresh(prep *refreshPrep) error {
	fileid, content, err := idx.fts.ReindexWithContent(prep.path, prep.strategy)
	if err != nil {
		return fmt.Errorf("fts reindex %s: %w", prep.path, err)
	}

	idx.vec.RemoveFile(prep.oldID)

	data, chunks, err := splitChunks(content, fileid, idx.fts)
	if err != nil {
		return fmt.Errorf("read chunks %s: %w", prep.path, err)
	}
	if err := idx.vec.AddFile(fileid, chunks); err != nil {
		return fmt.Errorf("vec add %s: %w", prep.path, err)
	}

	if idx.store != nil {
		if fileid != prep.oldID {
			idx.store.RemoveTags(prep.oldID)
			idx.store.RemoveTagDefs(prep.oldID)
		}
		// Use pre-extracted tags if available, otherwise extract from content
		tags := prep.tags
		if tags == nil {
			tags = ExtractTags(data)
		}
		if err := idx.store.UpdateTags(fileid, tags); err != nil {
			return fmt.Errorf("update tags %s: %w", prep.path, err)
		}
		defs := prep.defs
		if defs == nil {
			defs = ExtractTagDefs(data)
		}
		if err := idx.store.UpdateTagDefs(fileid, defs); err != nil {
			return fmt.Errorf("update tag defs %s: %w", prep.path, err)
		}
		// R795, R796: publish tag events from refreshed content
		if idx.pubsub != nil {
			idx.pubsub.PublishAndWatch("", prep.path, prep.tagValues)
		}
		// R866: write schedule log entries + day buckets
		idx.writeDateIndex(prep.path, prep.tagValues)
		idx.WriteDayBucketsForFile(fileid, prep.path, data)
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
		tags := ExtractTags(tagBytes)
		if err := idx.store.AppendTags(fileid, tags); err != nil {
			return fmt.Errorf("append tags %s: %w", path, err)
		}
		defs := ExtractTagDefs(tagBytes)
		if err := idx.store.AppendTagDefs(fileid, defs); err != nil {
			return fmt.Errorf("append tag defs %s: %w", path, err)
		}
		// R795, R796: publish tag events from appended content
		if idx.pubsub != nil {
			idx.pubsub.PublishAndWatch("", path, ExtractTagValues(tagBytes))
		}
		// R866: write schedule log entries + day buckets
		idx.writeDateIndex(path, ExtractTagValues(tagBytes))
		idx.WriteDayBucketsForFile(fileid, path, data)
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

// tagValueRegex matches @tag: followed by the value to end of line.
// For compound tags (@ref: path @topic: body), the greedy [^\n]* captures
// everything — the outer tag's value includes subsequent tags as text.
// The loop in ExtractTagValues peels embedded tags from the value,
// so all tags fire for pubsub. Outer values are preserved intact.
var tagValueRegex = regexp.MustCompile(`@([a-zA-Z][\w.-]*):\s*([^\n]*)`)

// ExtractTagValues scans content for @tag: patterns and returns name+value pairs.
// Used by both tag counting (ExtractTags) and pubsub delivery.
// Compound tags on a single line produce entries for each embedded tag,
// with outer tags keeping their full value. (Resolves O26.)
func ExtractTagValues(content []byte) []TagValue {
	matches := tagValueRegex.FindAllSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	values := make([]TagValue, 0, len(matches))
	for _, m := range matches {
		val := m[2]
		values = append(values, TagValue{
			Tag:   strings.ToLower(string(m[1])),
			Value: strings.TrimSpace(string(val)),
		})
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
