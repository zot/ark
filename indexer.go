package ark

// CRC: crc-Indexer.md

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/bmatsuo/lmdb-go/lmdb"
	"github.com/zot/microfts2"
	"sync"
)

var tagRegex = regexp.MustCompile(`@([a-zA-Z][\w.-]*):`)

// chunkAccumulator collects chunk text + tag-value extractions via two
// microfts2 callbacks running side by side:
//
//   - WithChunkCallback fires per emitted chunk (every chunk, including
//     content-dedup'd ones). Populates chunks (vector path) and tagValues
//     (file-level publish via flattenChunkTags) and defs (D records).
//   - WithIndexedChunkCallback fires only for newly-inserted chunkids.
//     Populates chunkTags ([]ChunkTagValues) for chunkid-keyed F/V/T
//     writes — chunkid arrives in-line with each fire, so no
//     FileInfoByID + zip dance is needed.
//
// CRC: crc-Indexer.md | R1890, R1892
type chunkAccumulator struct {
	chunks    [][]byte
	tagValues [][]TagValue
	chunkTags []ChunkTagValues
	defs      map[string]string
	strategy  string
}

func (a *chunkAccumulator) callback(chunkText string) {
	b := []byte(chunkText)
	a.chunks = append(a.chunks, b)
	a.tagValues = append(a.tagValues, ExtractTagValues(b, a.strategy))
	for k, v := range ExtractTagDefs(b) {
		if a.defs == nil {
			a.defs = make(map[string]string)
		}
		a.defs[k] = v
	}
}

// indexedCallback fires only for newly-inserted chunkids. Extracts tags
// from the chunk content and emits a ChunkTagValues entry keyed by the
// freshly-allocated chunkid. Content-dedup'd chunks (refcount-bumped C
// records) do not fire — their F/V/T records already exist.
//
// Reads only ic.Chunk.Content and ic.CRecord.ChunkID. Overlay-fired
// CRecord has no LMDB transaction context (Txn() and DB() return nil),
// so the callback must never traverse the CRecord into LMDB. This
// makes the same callback shape work for both persistent and tmp://
// indexing without branching. (R1949)
// CRC: crc-Indexer.md | R1891, R1949
func (a *chunkAccumulator) indexedCallback(ic microfts2.IndexedChunk) {
	values := ExtractTagValues(ic.Chunk.Content, a.strategy)
	a.chunkTags = append(a.chunkTags, ChunkTagValues{
		ChunkID: ic.CRecord.ChunkID,
		Values:  values,
	})
}

// flattenChunkTags collapses per-chunk tag-value slices into a single
// flat slice. Used at file-level boundaries (writeDateIndex, pubsub).
// CRC: crc-Indexer.md | R1893
func flattenChunkTags(chunkTags [][]TagValue) []TagValue {
	n := 0
	for _, c := range chunkTags {
		n += len(c)
	}
	out := make([]TagValue, 0, n)
	for _, c := range chunkTags {
		out = append(out, c...)
	}
	return out
}

// Indexer coordinates adding, removing, and refreshing files. Drives
// microfts2 indexing and orphan-EC cleanup callbacks. Extracts tags
// from file content. Embeddings are written by Librarian.BatchEmbedChunks
// post-reconcile — Indexer no longer writes vectors itself.
// CRC: crc-Indexer.md | R1923, R1926
type Indexer struct {
	fts           *microfts2.DB
	store         *Store
	pubsub        *PubSub         // nil when running without server
	scheduler     *EventScheduler // nil when running without server
	config        *Config         // for schedule tag checks
	pdfChunker    *PDFChunker     // R1720: flushes per-page blobs after microfts2 assigns fileids
	extmap        *ExtMap         // CRC: crc-ExtMap.md | R1996, R2000-R2008
	db            *DB             // back-pointer for ExtMap routing — uses ResolveExtTarget + chunkFileID
	recallWatcher *RecallWatcher  // nil when [recall].enabled=false; CRC: crc-Indexer.md | R2696, R2697

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
		fts:           fts,
		store:         idx.store,
		pubsub:        idx.pubsub,
		scheduler:     idx.scheduler,
		config:        idx.config,
		pdfChunker:    idx.pdfChunker,
		recallWatcher: idx.recallWatcher,
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

// SetRecallWatcher injects the simple-recall watcher. Called by the
// server when [recall].enabled is true. Indexer's append path
// enqueues newly-added chunks for ambient recall.
// CRC: crc-Indexer.md | R2697
func (idx *Indexer) SetRecallWatcher(w *RecallWatcher) {
	idx.recallWatcher = w
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

// AddFile adds a file to both engines and extracts tags. Uses two
// microfts2 callbacks: WithChunkCallback for vector path and file-level
// publish, WithIndexedChunkCallback for chunkid-keyed F/V/T writes
// (only fires for genuinely-new chunkids — dedup'd chunks cost zero).
// CRC: crc-Indexer.md | R1113, R1123, R1891
func (idx *Indexer) AddFile(path, strategy string) (uint64, error) {
	acc := chunkAccumulator{strategy: strategy}
	fileid, _, err := idx.fts.AddFileWithContent(path, strategy,
		microfts2.WithChunkCallback(acc.callback),
		microfts2.WithIndexedChunkCallback(acc.indexedCallback))
	if err != nil {
		return 0, fmt.Errorf("fts add %s: %w", path, err)
	}

	if idx.pdfChunker != nil {
		if err := idx.pdfChunker.FlushBlobs(path, fileid); err != nil {
			log.Printf("pdf: flush blobs %s: %v", path, err)
		}
	}

	// Embeddings (EC/EF) are written by Librarian.BatchEmbedChunks
	// post-reconcile — no synchronous vector write here. (R1923, R1926)

	if idx.store != nil {
		// CRC: crc-Indexer.md | Seq: seq-tag-value-index.md | R1883, R1891
		if err := idx.store.UpdateTagValues(acc.chunkTags); err != nil {
			return fileid, fmt.Errorf("update tag values %s: %w", path, err)
		}
		if err := idx.store.UpdateTagDefs(fileid, acc.defs); err != nil {
			return fileid, fmt.Errorf("update tag defs %s: %w", path, err)
		}
		// CRC: crc-Indexer.md | Seq: seq-ext-routing.md | R1996, R2000-R2007
		added := chunkIDsOf(acc.chunkTags)
		if err := idx.runExtRouting(fileid, added, nil, acc.chunkTags); err != nil {
			return fileid, fmt.Errorf("ext routing %s: %w", path, err)
		}
		flat := flattenChunkTags(acc.tagValues)
		// R795, R796: publish tag events to subscribers
		if idx.pubsub != nil {
			idx.pubsub.PublishAndWatch("", path, flat)
		}
		// R866: write schedule log entries
		idx.writeDateIndex(path, flat)
	}

	return fileid, nil
}

// chunkIDsOf returns the chunkids from a ChunkTagValues slice,
// skipping overlay (tmp://) ids. Used to feed ExtMap with the
// addedChunkIDs vector for re-resolution.
// CRC: crc-Indexer.md | R2000
func chunkIDsOf(cts []ChunkTagValues) []uint64 {
	out := make([]uint64, 0, len(cts))
	for _, ct := range cts {
		if IsOverlayID(ct.ChunkID) {
			continue
		}
		out = append(out, ct.ChunkID)
	}
	return out
}

// CRC: crc-Indexer.md | R1850, R1852, R1853
func (idx *Indexer) RemoveFile(path string) error {
	status, err := idx.fts.CheckFile(path)
	if err != nil {
		return fmt.Errorf("check file %s: %w", path, err)
	}
	fileid := status.FileID

	if err := idx.store.WithTvidTxn(func(tt *TvidTxn) error {
		return idx.fts.RemoveFileWithCallback(path, idx.removeCallback(fileid, tt))
	}); err != nil {
		return fmt.Errorf("fts remove %s: %w", path, err)
	}
	// EC/EF cleanup runs inside the removeCallback above (R1850, R1852, R1853, R1923).
	if idx.store != nil {
		// V/F/T cleanup is driven by orphan-chunkid callbacks (R1899)
		// installed via idx.removeCallback above.
		idx.store.RemoveTagDefs(fileid)
		idx.store.RemovePageContents(fileid)
	}
	return nil
}

// CRC: crc-Indexer.md | R1851, R1852, R1853
func (idx *Indexer) RemoveByID(fileid uint64) error {
	info, err := idx.fts.FileInfoByID(fileid)
	if err != nil {
		return fmt.Errorf("file info %d: %w", fileid, err)
	}
	if err := idx.store.WithTvidTxn(func(tt *TvidTxn) error {
		return idx.fts.RemoveFileWithCallback(info.Names[0], idx.removeCallback(fileid, tt))
	}); err != nil {
		return fmt.Errorf("fts remove %d: %w", fileid, err)
	}
	// EC/EF cleanup is driven by the removeCallback above (R1923).
	if idx.store != nil {
		// V/F/T cleanup driven by orphan-chunkid callbacks (R1899).
		idx.store.RemoveTagDefs(fileid)
		idx.store.RemovePageContents(fileid)
	}
	return nil
}

// cleanupOrphans drops EC records and inline F/V/T contributions for
// orphaned chunks, capturing @ext source tvids first so ExtMap can
// strike their X records and routed-tag V entries afterward. Order
// matters: the @ext tvids must be read before RemoveTagValuesInTxn
// wipes F[orphan][ext], and CleanupSource must run before tt.Commit
// drops tvid_exts whose source V records emptied. (R1899, R2008,
// R2009, R2022, R2024)
func (idx *Indexer) cleanupOrphans(txn *lmdb.Txn, tt *TvidTxn, orphanedChunkIDs []uint64) error {
	type extPair struct {
		sourceChunkID uint64
		tvidExt       uint64
	}
	var extPairs []extPair
	if idx.extmap != nil {
		for _, id := range orphanedChunkIDs {
			ts, err := idx.store.ReadExtTvidsForChunk(txn, id)
			if err != nil {
				return err
			}
			for _, te := range ts {
				extPairs = append(extPairs, extPair{sourceChunkID: id, tvidExt: te})
			}
		}
	}
	for _, id := range orphanedChunkIDs {
		if err := idx.store.DeleteChunkEmbeddingInTxn(txn, id); err != nil {
			return err
		}
		if err := idx.store.RemoveTagValuesInTxn(txn, tt, id); err != nil {
			return err
		}
	}
	if idx.extmap != nil {
		for _, p := range extPairs {
			if err := idx.extmap.CleanupSource(txn, tt, idx.db, p.sourceChunkID, p.tvidExt); err != nil {
				return err
			}
		}
	}
	return nil
}

// removeCallback returns a RemoveCallback that cleans up orphaned EC records
// and the EF centroid inside the same LMDB transaction. The TvidTxn
// receives F/V/T removals; its caller commits or aborts based on the
// surrounding RemoveFileWithCallback result. R1852, R1853, R1963
func (idx *Indexer) removeCallback(fileID uint64, tt *TvidTxn) microfts2.RemoveCallback {
	if idx.store == nil {
		return nil
	}
	return func(txn *lmdb.Txn, orphanedChunkIDs []uint64) error {
		if err := idx.cleanupOrphans(txn, tt, orphanedChunkIDs); err != nil {
			return err
		}
		return idx.store.DeleteFileCentroidInTxn(txn, fileID)
	}
}

// reindexCallback returns a ReindexCallback that cleans up orphaned EC records
// and the EF centroid inside the same LMDB transaction. The TvidTxn
// receives F/V/T removals; its caller commits or aborts based on the
// surrounding ReindexWithCallback result. R1849, R1852, R1899, R1963
//
// addedChunkIDs is intentionally ignored here — ReresolveOnReindex
// runs post-UpdateTagValues (see runExtRouting), where the new
// chunks' @id V records are already visible.
func (idx *Indexer) reindexCallback(fileID uint64, tt *TvidTxn) microfts2.ReindexCallback {
	if idx.store == nil {
		return nil
	}
	return func(txn *lmdb.Txn, orphanedChunkIDs, _ []uint64) error {
		if err := idx.cleanupOrphans(txn, tt, orphanedChunkIDs); err != nil {
			return err
		}
		if len(orphanedChunkIDs) > 0 {
			Logv(1, "reindex: fileID=%d orphaned %d EC records", fileID, len(orphanedChunkIDs))
		}
		return idx.store.DeleteFileCentroidInTxn(txn, fileID)
	}
}

// runExtRouting handles the post-UpdateTagValues @ext flow for one
// file: IndexExt for each new chunk's @ext entries, then
// ReresolveOnReindex to absorb target-side changes (including the
// "appearing UUID" case).
//
// Resolution runs OUTSIDE the LMDB write txn so ResolveExtTarget's
// internal View doesn't nest. The write txn opens once for record
// I/O only.
//
// fileID is the source-side file just indexed; addedChunkIDs and
// orphanedChunkIDs are the chunk diffs reported by microfts2;
// chunkTags are the per-chunk tag values just persisted by Store.
// CRC: crc-Indexer.md | R1996, R2000, R2001, R2002, R2003, R2004, R2005, R2006, R2007, R2011
func (idx *Indexer) runExtRouting(fileID uint64, addedChunkIDs, orphanedChunkIDs []uint64, chunkTags []ChunkTagValues) error {
	if idx.extmap == nil || idx.db == nil {
		return nil
	}
	idxPlans := idx.collectIndexExtPlans(fileID, chunkTags)
	rrPlans := idx.collectReresolvePlans(fileID, addedChunkIDs, orphanedChunkIDs)
	if len(idxPlans) == 0 && len(rrPlans) == 0 {
		return nil
	}
	return idx.store.WithTvidTxn(func(tt *TvidTxn) error {
		return idx.store.env.Update(func(txn *lmdb.Txn) error {
			for _, p := range idxPlans {
				if err := idx.extmap.applyIndexExt(txn, tt, idx.db, p); err != nil {
					return err
				}
			}
			for _, p := range rrPlans {
				if err := idx.extmap.applyReresolve(txn, tt, idx.db, fileID, p); err != nil {
					return err
				}
			}
			return nil
		})
	})
}

// collectIndexExtPlans walks freshly-written chunkTags, parses each
// @ext value, and resolves the target. Resolution happens here
// (outside the write txn) so the txn-aware applyIndexExt only does
// record I/O. Overlay (tmp://) source chunkids are accepted —
// applyIndexExt branches per-target on bothPersistent.
//
// `sourceDir` (derived once from fileID) threads through to
// ResolveExtTarget so relative-path narrower bases absolutize
// against the source file's directory. (R2374)
// CRC: crc-Indexer.md | R1996, R2012, R2016, R2374
func (idx *Indexer) collectIndexExtPlans(fileID uint64, chunkTags []ChunkTagValues) []extIndexPlan {
	sourceDir := idx.sourceDirFor(fileID)
	var out []extIndexPlan
	for _, ct := range chunkTags {
		for _, tv := range ct.Values {
			if tv.Tag != tagExt || tv.Value == "" {
				continue
			}
			tvidExt, ok := idx.store.tvids.Lookup(tagExt, tv.Value)
			if !ok {
				continue
			}
			target, routed, parseOK := ParseExtTarget(tv.Value)
			if !parseOK {
				continue
			}
			parts, _ := ParseExtTargetParts(target, sourceDir)
			out = append(out, extIndexPlan{
				sourceChunkID: ct.ChunkID,
				sourceFileID:  fileID,
				tvidExt:       tvidExt,
				target:        target,
				targetBase:    parts.BaseValue,
				targets:       idx.db.ResolveExtTarget(target, sourceDir),
				routedTags:    routed,
			})
		}
	}
	return out
}

// sourceDirFor returns the absolute directory of fileID's
// canonical path, or "" if the file is unknown. Used to thread
// source-dir context into ResolveExtTarget for relative-path
// narrower resolution.
// CRC: crc-Indexer.md | R2374
func (idx *Indexer) sourceDirFor(fileID uint64) string {
	if idx.db == nil {
		return ""
	}
	path, ok := idx.db.fileIDPath(fileID)
	if !ok {
		return ""
	}
	return filepath.Dir(path)
}

// runOverlayExtRouting handles @ext routing for overlay (tmp://)
// source content. Mirrors runExtRouting but skips the LMDB
// transaction since every routing for an overlay source has
// bothPersistent=false — applyIndexExt accepts nil txn/tt and writes
// only to in-memory ExtMap state.
// CRC: crc-Indexer.md | R2012, R2016, R2018
func (idx *Indexer) runOverlayExtRouting(fileID uint64, chunkTags []ChunkTagValues) error {
	if idx.extmap == nil || idx.db == nil {
		return nil
	}
	plans := idx.collectIndexExtPlans(fileID, chunkTags)
	if len(plans) == 0 {
		return nil
	}
	for _, p := range plans {
		if err := idx.extmap.applyIndexExt(nil, nil, idx.db, p); err != nil {
			return err
		}
	}
	return nil
}

// collectReresolvePlans determines which tvid_exts need re-resolution
// for this file change and pre-resolves each. Each candidate's
// source file may differ (the changed file F is the target of the
// routings; the sources live elsewhere), so sourceDir is recovered
// per-tvid_ext via ExtMap.SourceChunkID → chunkFileID → fileIDPath.
// CRC: crc-Indexer.md | R2000, R2001, R2374
func (idx *Indexer) collectReresolvePlans(fileID uint64, addedChunkIDs, orphanedChunkIDs []uint64) []extReresolvePlan {
	candidates := idx.extmap.candidatesForFileChange(idx.db, fileID, addedChunkIDs, orphanedChunkIDs)
	if len(candidates) == 0 {
		return nil
	}
	// Cache sourceDir per source-fileID so a many-tvid file pays the
	// path lookup once.
	sourceDirCache := make(map[uint64]string)
	out := make([]extReresolvePlan, 0, len(candidates))
	for tvidExt := range candidates {
		_, value, ok := idx.store.tvids.Resolve(tvidExt)
		if !ok {
			continue
		}
		target, routed, parseOK := ParseExtTarget(value)
		if !parseOK {
			continue
		}
		sourceDir := idx.reresolveSourceDir(tvidExt, sourceDirCache)
		parts, _ := ParseExtTargetParts(target, sourceDir)
		out = append(out, extReresolvePlan{
			tvidExt:    tvidExt,
			target:     target,
			targetBase: parts.BaseValue,
			routedTags: routed,
			newTargets: idx.db.ResolveExtTarget(target, sourceDir),
		})
	}
	return out
}

// reresolveSourceDir returns the source directory for tvidExt's
// authoring chunk, memoized by source-fileID across the
// candidate loop.
// CRC: crc-Indexer.md | R2374
func (idx *Indexer) reresolveSourceDir(tvidExt uint64, cache map[uint64]string) string {
	if idx.extmap == nil || idx.db == nil {
		return ""
	}
	srcChunk, ok := idx.extmap.SourceChunkID(tvidExt)
	if !ok {
		return ""
	}
	var srcFileID uint64
	var fileOK bool
	_ = idx.db.fts.Env().View(func(txn *lmdb.Txn) error {
		srcFileID, fileOK = idx.db.chunkFileID(txn, srcChunk)
		return nil
	})
	if !fileOK {
		return ""
	}
	if dir, hit := cache[srcFileID]; hit {
		return dir
	}
	dir := idx.sourceDirFor(srcFileID)
	cache[srcFileID] = dir
	return dir
}

type extIndexPlan struct {
	sourceChunkID uint64
	sourceFileID  uint64
	tvidExt       uint64
	target        string // TARGET text as authored (verbatim for V record / debugging)
	targetBase    string // BASE of the TARGET — absolutized path or UUID value — for extByAnchor keying (R2380)
	targets       []uint64
	routedTags    []TagValue
}

type extReresolvePlan struct {
	tvidExt    uint64
	target     string // TARGET text as authored
	targetBase string // BASE for extByAnchor keying (R2380)
	routedTags []TagValue
	newTargets []uint64
}

// refreshPrep holds data prepared by a worker for the ChanSvc to execute.
// Workers populate this via prepareRefresh (file I/O + append detection).
// The ChanSvc executes writes via executeRefresh (LMDB mutations).
// Tag extraction happens on-actor via chunk callbacks (R1896).
type refreshPrep struct {
	path     string
	strategy string
	oldID    uint64
	isAppend bool
	data     []byte // full file content
	// Append-specific fields
	newBytes []byte
	baseLine int
	fullHash string
	fileSize int64
	modTime  int64
}

// prepareRefresh reads a file and detects append-vs-full — safe to call
// from multiple goroutines. LMDB reads (DetectAppend, FileInfoByID) are
// concurrent-safe. Returns a prep struct for executeRefresh.
// Tag extraction itself moves on-actor (R1896).
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
	ok, _ := idx.DetectAppend(path, fileID)
	Logv(2, "prepare-refresh: %s detect=%v", path, ok)
	if ok {
		info, err := idx.fts.FileInfoByID(fileID)
		Logv(2, "prepare-refresh: %s info-err=%v FL=%d data-len=%d",
			path, err, info.FileLength, len(data))
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
			return prep, nil
		}
	}

	return prep, nil
}

// executeRefresh runs all LMDB writes for a prepared file.
// Must be called from the ChanSvc goroutine (single writer).
// CRC: crc-Indexer.md | R1894, R1896, R1898
func (idx *Indexer) executeRefresh(prep *refreshPrep) error {
	Logv(2, "execute-refresh: %s isAppend=%v newBytes=%d", prep.path, prep.isAppend, len(prep.newBytes))
	if prep.isAppend {
		acc := chunkAccumulator{strategy: prep.strategy}
		err := idx.fts.AppendChunks(prep.oldID, prep.newBytes, prep.strategy,
			microfts2.WithBaseLine(prep.baseLine),
			microfts2.WithContentHash(prep.fullHash),
			microfts2.WithModTime(prep.modTime),
			microfts2.WithFileLength(prep.fileSize),
			microfts2.WithAppendChunkCallback(acc.callback),
			microfts2.WithIndexedChunkCallback(acc.indexedCallback),
		)
		Logv(2, "execute-refresh: %s AppendChunks err=%v newChunks=%d", prep.path, err, len(acc.chunkTags))
		if err != nil {
			// Append failed — fall through to full reindex
			Logv(2, "execute-refresh: %s falling through to full refresh due to err: %v", prep.path, err)
			return idx.executeFullRefresh(prep)
		}
		// Embeddings: orphan EC/EF cleanup ran inside the reindex callback;
		// new chunks will be embedded by Librarian.BatchEmbedChunks
		// post-reconcile (R1923, R1926).
		// Tags: chunkid-keyed; chunkids arrive via the indexed callback.
		if idx.store != nil {
			// CRC: crc-Indexer.md | Seq: seq-tag-value-index.md | R1884, R1894
			if err := idx.store.AppendTagValues(acc.chunkTags); err != nil {
				return fmt.Errorf("append tag values %s: %w", prep.path, err)
			}
			if err := idx.store.AppendTagDefs(prep.oldID, acc.defs); err != nil {
				return fmt.Errorf("append tag defs %s: %w", prep.path, err)
			}
			// CRC: crc-Indexer.md | Seq: seq-ext-routing.md | R2007
			added := chunkIDsOf(acc.chunkTags)
			if err := idx.runExtRouting(prep.oldID, added, nil, acc.chunkTags); err != nil {
				return fmt.Errorf("ext routing %s: %w", prep.path, err)
			}
			flat := flattenChunkTags(acc.tagValues)
			// R795, R796: publish tag events from appended content
			if idx.pubsub != nil {
				idx.pubsub.PublishAndWatch("", prep.path, flat)
			}
			idx.writeDateIndex(prep.path, flat)
			// CRC: crc-Indexer.md | Seq: seq-recall-watcher.md#1 | R2696, R2729
			// Simple-recall watcher hook: hand the freshly-appended
			// bytes and the chunkIDs the chunker emitted to the
			// watcher's turn-boundary state machine. The watcher
			// applies its own enable + source-qualification gates
			// internally. Live-append is the only path that drives
			// the watcher — full reindex (executeFullRefresh) and
			// initial add (AddFile) would amount to backfill, which
			// R2698 disallows.
			if idx.recallWatcher != nil {
				idx.recallWatcher.OnAppend(prep.path, prep.strategy, prep.newBytes, added)
			}
		}
		return nil
	}
	return idx.executeFullRefresh(prep)
}

// executeFullRefresh does a complete reindex. Tags are extracted from
// clean chunk text via callback. Orphaned chunkids (delivered by the
// reindex callback) drop their F/V/T contributions in the same txn.
// CRC: crc-Indexer.md | R1849, R1852, R1854, R1891, R1899
func (idx *Indexer) executeFullRefresh(prep *refreshPrep) error {
	Logv(1, "full refresh: %s (fileID=%d)", prep.path, prep.oldID)
	acc := chunkAccumulator{strategy: prep.strategy}
	var fileid uint64
	if err := idx.store.WithTvidTxn(func(tt *TvidTxn) error {
		var err error
		fileid, err = idx.fts.ReindexWithCallback(prep.path, prep.strategy, idx.reindexCallback(prep.oldID, tt),
			microfts2.WithChunkCallback(acc.callback),
			microfts2.WithIndexedChunkCallback(acc.indexedCallback))
		return err
	}); err != nil {
		return fmt.Errorf("fts reindex %s: %w", prep.path, err)
	}

	if idx.pdfChunker != nil {
		if err := idx.pdfChunker.FlushBlobs(prep.path, fileid); err != nil {
			log.Printf("pdf: flush blobs %s: %v", prep.path, err)
		}
	}

	// EC/EF cleanup ran inside reindexCb; new EC records arrive on the
	// next BatchEmbedChunks pass. (R1923, R1926)

	if idx.store != nil {
		if fileid != prep.oldID {
			// File-keyed records that survived the reindex callback
			// belong to oldID and need explicit removal.
			idx.store.RemoveTagDefs(prep.oldID)
			idx.store.RemovePageContents(prep.oldID)
		}
		// CRC: crc-Indexer.md | Seq: seq-tag-value-index.md | R1883, R1891
		if err := idx.store.UpdateTagValues(acc.chunkTags); err != nil {
			return fmt.Errorf("update tag values %s: %w", prep.path, err)
		}
		if err := idx.store.UpdateTagDefs(fileid, acc.defs); err != nil {
			return fmt.Errorf("update tag defs %s: %w", prep.path, err)
		}
		// CRC: crc-Indexer.md | Seq: seq-ext-routing.md | R1996, R2000-R2007
		added := chunkIDsOf(acc.chunkTags)
		if err := idx.runExtRouting(fileid, added, nil, acc.chunkTags); err != nil {
			return fmt.Errorf("ext routing %s: %w", prep.path, err)
		}
		flat := flattenChunkTags(acc.tagValues)
		// R795, R796: publish tag events from refreshed content
		if idx.pubsub != nil {
			idx.pubsub.PublishAndWatch("", prep.path, flat)
		}
		// R866: write schedule log entries
		idx.writeDateIndex(prep.path, flat)
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
		Logv(2, "detect-append: %s FileInfoByID err: %v", path, err)
		return false, err
	}
	var zeroHash [32]byte
	if info.FileLength <= 0 || info.ContentHash == zeroHash {
		Logv(2, "detect-append: %s no stored hash/length (FL=%d hash-zero=%v)",
			path, info.FileLength, info.ContentHash == zeroHash)
		return false, nil
	}

	fi, err := os.Stat(path)
	if err != nil {
		Logv(2, "detect-append: %s stat err: %v", path, err)
		return false, err
	}
	if fi.Size() <= info.FileLength {
		Logv(2, "detect-append: %s didn't grow (size=%d FL=%d)",
			path, fi.Size(), info.FileLength)
		return false, nil // didn't grow
	}

	// Hash the first FileLength bytes
	f, err := os.Open(path)
	if err != nil {
		Logv(2, "detect-append: %s open err: %v", path, err)
		return false, err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.CopyN(h, f, info.FileLength); err != nil {
		Logv(2, "detect-append: %s hash copy err: %v", path, err)
		return false, err
	}
	var hash [32]byte
	copy(hash[:], h.Sum(nil))

	match := hash == info.ContentHash
	Logv(2, "detect-append: %s hash match=%v (FL=%d size=%d)",
		path, match, info.FileLength, fi.Size())
	return match, nil
}

// AppendFile indexes only the new content appended to a file.
// FTS uses AppendChunks with a chunk callback; vectors get a full
// refresh; tags are chunkid-keyed via the callback's per-chunk slices.
// Checkpoint advances the indexer's stored FileLength for `path` to
// the file's current on-disk size, without re-chunking or otherwise
// touching the file's content. After Checkpoint, the next refresh
// pass treats only future-appended bytes as new — historical content
// is "capped off." Returns the new FileLength on success. R2745
func (idx *Indexer) Checkpoint(path string) (int64, error) {
	status, err := idx.fts.CheckFile(path)
	if err != nil {
		return 0, fmt.Errorf("check file %s: %w", path, err)
	}
	if status.FileID == 0 {
		return 0, fmt.Errorf("file not indexed: %s", path)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", path, err)
	}
	if err := idx.fts.SetFileLength(status.FileID, fi.Size()); err != nil {
		return 0, fmt.Errorf("set file length %s: %w", path, err)
	}
	return fi.Size(), nil
}

// CRC: crc-Indexer.md | R1894, R1895
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

	// Append to FTS index, accumulating per-chunk tag values via the
	// chunkid-aware indexed callback (newly-inserted chunks only) and
	// the text-only callback (every chunk, for vector + pubsub paths).
	acc := chunkAccumulator{strategy: strategy}
	err = idx.fts.AppendChunks(fileid, newBytes, strategy,
		microfts2.WithBaseLine(baseLine),
		microfts2.WithContentHash(fmt.Sprintf("%x", fullHash)),
		microfts2.WithModTime(fi.ModTime().UnixNano()),
		microfts2.WithFileLength(fi.Size()),
		microfts2.WithAppendChunkCallback(acc.callback),
		microfts2.WithIndexedChunkCallback(acc.indexedCallback),
	)
	if err != nil {
		return fmt.Errorf("fts append %s: %w", path, err)
	}

	// Embeddings: Librarian.BatchEmbedChunks reconciles EC/EF on the next
	// pass. No synchronous vector write here. (R1923, R1926)

	// Tags and defs: chunkid-keyed via indexed callback.
	if idx.store != nil {
		// CRC: crc-Indexer.md | Seq: seq-tag-value-index.md | R1884, R1894
		if err := idx.store.AppendTagValues(acc.chunkTags); err != nil {
			return fmt.Errorf("append tag values %s: %w", path, err)
		}
		if err := idx.store.AppendTagDefs(fileid, acc.defs); err != nil {
			return fmt.Errorf("append tag defs %s: %w", path, err)
		}
		// CRC: crc-Indexer.md | Seq: seq-ext-routing.md | R2007
		added := chunkIDsOf(acc.chunkTags)
		if err := idx.runExtRouting(fileid, added, nil, acc.chunkTags); err != nil {
			return fmt.Errorf("ext routing %s: %w", path, err)
		}
		flat := flattenChunkTags(acc.tagValues)
		// R795, R796: publish tag events from appended content
		if idx.pubsub != nil {
			idx.pubsub.PublishAndWatch("", path, flat)
		}
		// R866: write schedule log entries
		idx.writeDateIndex(path, flat)
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
				if matcher.Match(p, s.Path, "", false) {
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

// (tagWindowForAppend removed per R1895 — append-boundary handling is
// microfts2's responsibility via the chunker append protocol; tags split
// across the seam are re-emitted by the callback as part of the merged
// chunk.)

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
// The greedy [^\n]* captures everything after the colon — for a compound
// line like `@ext: TARGET @t1: v1`, the outer tag's value is the full
// remainder. Embedded tag handling is each outer tag's own job (e.g.
// ParseExtTarget for @ext), not this regex's. R2110, R2111
// The post-colon gap is `[ \t]*` (NOT `\s*`) so an empty-value tag like
// `@e: ` doesn't swallow the newline and glue the next line's content
// onto its own value. R2427
// CRC: crc-Indexer.md | R2110, R2111, R2427
var tagValueRegex = regexp.MustCompile(`@([a-zA-Z][\w.-]*):[ \t]*([^\n]*)`)

// ExtractTagValues returns one (Tag, Value) per `@x:` line — the outer
// tag of each line, with Value spanning from after `@x:` to end of line.
// Embedded `@y: z` segments inside that value are NOT peeled here;
// compound semantics are per-outer-tag and live with each tag's owner
// (ParseExtTarget for @ext; future tags register their own handlers).
// Skips mentions per R1317-R1325. Strategy controls whether
// markdown-specific heuristics (fenced/indented code) apply.
// CRC: crc-Indexer.md | R1317-R1325, R2110, R2111
func ExtractTagValues(content []byte, strategy string) []TagValue {
	markdown := strategy == "markdown"
	locs := tagValueRegex.FindAllSubmatchIndex(content, -1)
	if len(locs) == 0 {
		return nil
	}
	values := make([]TagValue, 0, len(locs))
	for _, loc := range locs {
		if isMention(content, loc[0], markdown) {
			continue
		}
		values = append(values, TagValue{
			Tag:   strings.ToLower(string(content[loc[2]:loc[3]])),
			Value: strings.TrimSpace(string(content[loc[4]:loc[5]])),
		})
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
