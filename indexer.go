package ark

// CRC: crc-Indexer.md

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/zot/microfts2"
	"go.etcd.io/bbolt"
	"sync"
)

// R751: tagRegex matches `@tag:` anywhere in content — no line-start anchor.
// CRC: crc-Indexer.md | R118, R751
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
	strategy  string // chunker strategy, for tag re-extraction (R2913)
	chunkTags []ChunkTagValues
}

// indexedCallback fires only for newly-inserted chunkids. microfts2 indexes
// chunk content verbatim (the trigram index is full-text — tags kept, R2913),
// so this re-extracts the chunk's ark tags from its original content,
// keyed by the freshly-allocated chunkid. Content-dedup'd chunks
// (refcount-bumped C records) do not fire — they share the chunkid and its
// F/V/T records, so the single fire that lands suffices. Runs for every
// strategy including pdf: a pdf chunk's Content is extracted text, where
// ark tags are real (unlike the raw bytes fileLevelTags sees, which it
// skips).
//
// Reads only ic.Chunk.Content and ic.CRecord.ChunkID. Overlay-fired CRecord
// has no LMDB transaction context (Txn() and DB() return nil), so the
// callback must never traverse the CRecord into LMDB. This makes the same
// callback shape work for both persistent and tmp:// indexing without
// branching. (R1949)
// CRC: crc-Indexer.md | R1891, R1904, R1949, R2913, R121
func (a *chunkAccumulator) indexedCallback(ic microfts2.IndexedChunk) {
	a.chunkTags = append(a.chunkTags, ChunkTagValues{
		ChunkID: ic.CRecord.ChunkID,
		Values:  ExtractTagValues(ic.Chunk.Content, a.strategy),
	})
}

// fileLevelTags extracts file-level tag-value instances and tag
// definitions directly from source content — the whole file on add/full
// refresh, the appended bytes on append. Sourcing them here, rather than
// from the per-chunk indexed callback, keeps them faithful to every
// occurrence (including content-dedup'd chunks the indexed callback skips)
// and keeps the map-typed defs out of the per-chunk Attrs. pdf is skipped
// *here* only: this file-level path sees the raw PDF byte stream, where the
// tag regex would invent only spurious matches. A pdf's real tags live in
// its extracted chunk text and are picked up per-chunk (indexedCallback),
// not at file level. R757: on a full refresh this scans the entire file
// (the whole `content`), unaffected by any append-only handling.
// A pattern file additionally contributes a *derived* tag definition, computed
// from its frontmatter rather than read from an @tag: line (R3274). It is
// merged into the same defs map, so a pattern file carrying a literal @tag:
// line keeps both. The derivation is content-gated, which is what makes the
// append path contribute nothing without either path knowing about the other
// (R3275) — see seq-patterns-derive.md.
// CRC: crc-Indexer.md | Seq: seq-patterns-derive.md#1.1 | R757, R2913, R3274, R3275
func (idx *Indexer) fileLevelTags(path string, content []byte, strategy string) ([]TagValue, map[string]string) {
	if strategy == "pdf" {
		return nil, nil
	}
	vals, defs := ExtractTagValues(content, strategy), ExtractTagDefs(content)
	if name, desc, ok := NewPatterns(idx.config.PatternsRoot()).DerivedDef(path, content); ok {
		if defs == nil {
			defs = map[string]string{}
		}
		defs[name] = desc
	}
	return vals, defs
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
	bibleChunker  *bibleChunker   // R3214: flushes staged book-index records after a file commits
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
		fts:       fts,
		store:     idx.store,
		pubsub:    idx.pubsub,
		scheduler: idx.scheduler,
		config:    idx.config,
		// extmap + db are the pair runExtRouting gates on. Every caller of
		// withFTS is inside an enqueueWrite closure — the write actor, which
		// is exactly where @ext routing belongs — so dropping them here does
		// not make routing safer, it silently disables it: the gate returns
		// nil and no @ext or @ext-candidate ever resolves on the watcher
		// path. They were added to Indexer a month after this copier was
		// written and never added here, so live routing was dead from then
		// until the ledger read (#67) made the absence visible.
		extmap:        idx.extmap,
		db:            idx.db,
		pdfChunker:    idx.pdfChunker,
		bibleChunker:  idx.bibleChunker,
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
// CRC: crc-Indexer.md | R866, R869, R870, R872, R1009
func (idx *Indexer) writeDateIndex(path string, tagValues []TagValue) {
	if idx.scheduler == nil || idx.config == nil {
		return
	}
	// Schedule log files are output, not input — skip to prevent cascade.
	if strings.HasPrefix(path, idx.config.dbPath+"/schedule/") {
		return
	}
	for _, tv := range tagValues {
		if idx.config.IsScheduleTag(tv.Tag) && tv.Value != "" {
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

// AddFile adds a file to both engines and extracts tags. Per-chunk tags are
// re-extracted from each new chunk's original content in
// WithIndexedChunkCallback for F/V/T writes (dedup'd chunks cost zero).
// File-level tags + defs are re-extracted from the returned source content.
// Files go to microfts2 first via AddFileWithContent, which returns the
// fileid and the source content the chunk callback reads at microfts2's
// offsets (R37, R38); microfts2 skips re-chunking content it already holds
// fresh (R36).
// CRC: crc-Indexer.md | R36, R37, R38, R1113, R1123, R1891, R2913
func (idx *Indexer) AddFile(path, strategy string) (uint64, error) {
	acc := chunkAccumulator{strategy: strategy}
	// CRC: crc-Indexer.md | R209 — an unregistered strategy name is rejected
	// here by microfts2, surfacing as a scan-time error.
	fileid, content, err := idx.fts.AddFileWithContent(path, strategy,
		microfts2.WithIndexedChunkCallback(acc.indexedCallback))
	if err != nil {
		return 0, fmt.Errorf("fts add %s: %w", path, err)
	}
	return fileid, idx.afterAddStore(fileid, path, strategy, content, &acc)
}

// afterAddStore runs the post-store bookkeeping for a fresh add: pdf/bible
// flushes, then chunkid-keyed tag values, file-level tag defs, @ext routing, tag
// pubsub, and the schedule date index. Shared by the fused AddFile and the
// prepared-store path (storeScan → StorePrepared), so the bookkeeping has one
// home and cannot drift between them. (R3426)
// CRC: crc-Indexer.md | Seq: seq-tag-value-index.md, seq-ext-routing.md | R1883, R1891, R1996, R2000-R2007, R2913, R3426
func (idx *Indexer) afterAddStore(fileid uint64, path, strategy string, content []byte, acc *chunkAccumulator) error {
	if idx.pdfChunker != nil {
		if err := idx.pdfChunker.FlushBlobs(path, fileid); err != nil {
			log.Printf("pdf: flush blobs %s: %v", path, err)
		}
	}
	// R3214: the book index records which file holds each of a book's
	// chapters — staged during the chunk walk, persisted once the file is in.
	if idx.bibleChunker != nil {
		if err := idx.bibleChunker.FlushBookIndex(path); err != nil {
			log.Printf("bible: flush book index %s: %v", path, err)
		}
	}

	// Embeddings (EC/EF) are written by Librarian.BatchEmbedChunks
	// post-reconcile — no synchronous vector write here. (R1923, R1926)

	if idx.store != nil {
		// File-level tags + defs come straight from the source content
		// (faithful to dedup'd chunks; keeps map-typed defs out of Attrs).
		fileVals, fileDefs := idx.fileLevelTags(path, content, strategy)
		if err := idx.store.UpdateTagValues(acc.chunkTags); err != nil {
			return fmt.Errorf("update tag values %s: %w", path, err)
		}
		if err := idx.store.UpdateTagDefs(fileid, fileDefs); err != nil {
			return fmt.Errorf("update tag defs %s: %w", path, err)
		}
		added := chunkIDsOf(acc.chunkTags)
		if err := idx.runExtRouting(fileid, added, nil, acc.chunkTags); err != nil {
			return fmt.Errorf("ext routing %s: %w", path, err)
		}
		// R795, R796: publish tag events to subscribers
		if idx.pubsub != nil {
			idx.pubsub.PublishAndWatch("", path, fileVals)
			idx.publishRouted(acc.chunkTags) // R3594
		}
		// R866: write schedule log entries
		idx.writeDateIndex(path, fileVals)
	}

	return nil
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
// orphaned chunks, capturing @ext-family source tvids first so ExtMap can
// strike their derived records (X for @ext, RC/RJ for candidate/judgment)
// and routed-tag V entries afterward. Order matters: the family tvids must
// be read before RemoveTagValuesInTxn wipes F[orphan][ext*], and
// CleanupSource must run before tt.Commit drops tvid_exts whose source V
// records emptied. (R1899, R2008, R2009, R2022, R2024, R3064)
func (idx *Indexer) cleanupOrphans(txn *bbolt.Tx, tt *TvidTxn, orphanedChunkIDs []uint64) error {
	for _, id := range orphanedChunkIDs {
		if err := idx.cleanupOrphanChunk(txn, tt, id); err != nil {
			return err
		}
	}
	return nil
}

// cleanupOrphanChunk drops the EC record and inline F/V/T contributions for one
// orphaned chunk, striking its @ext-family derived records first. The @ext
// family tvids must be read before RemoveTagValuesInTxn wipes F[orphan][ext*],
// and CleanupSource must run before the surrounding TvidTxn commits — both hold
// per chunk, since the records are keyed by this chunk and removing another
// orphan's F records cannot change this one's family read. Shared by the batched
// reindex/remove cleanup (cleanupOrphans) and the per-fire WithRemovedChunkCallback
// on the prepared-store path. (R1899, R2008, R2009, R2022, R2024, R3064)
func (idx *Indexer) cleanupOrphanChunk(txn *bbolt.Tx, tt *TvidTxn, chunkID uint64) error {
	var extTvids []uint64
	if idx.extmap != nil {
		ts, err := idx.store.ReadExtFamilyTvidsForChunk(txn, chunkID)
		if err != nil {
			return err
		}
		extTvids = ts
	}
	if err := idx.store.DeleteChunkEmbeddingInTxn(txn, chunkID); err != nil {
		return err
	}
	if err := idx.store.RemoveTagValuesInTxn(txn, tt, chunkID); err != nil {
		return err
	}
	if idx.extmap != nil {
		for _, te := range extTvids {
			if err := idx.extmap.CleanupSource(txn, tt, idx.db, chunkID, te); err != nil {
				return err
			}
		}
	}
	return nil
}

// removedChunkCleanup returns a microfts2 RemovedChunkCallback that runs
// cleanupOrphanChunk for each chunk orphaned during a prepared store
// (ReindexPrepared / AppendPrepared). The callback carries the store txn on its
// CRecord (crec.Tx()); the shipped seam has no error return, so a cleanup
// failure is captured in *cleanupErr for the caller to surface as a re-scan
// (R3425) rather than being silently dropped. (R1899)
// CRC: crc-FileScanActor.md | R3426
func (idx *Indexer) removedChunkCleanup(tt *TvidTxn, cleanupErr *error) microfts2.RemovedChunkCallback {
	return func(crec microfts2.CRecord) {
		if idx.store == nil {
			return
		}
		if err := idx.cleanupOrphanChunk(crec.Tx(), tt, crec.ChunkID); err != nil && *cleanupErr == nil {
			*cleanupErr = err
			log.Printf("index: orphan cleanup chunk %d: %v", crec.ChunkID, err)
		}
	}
}

// removeCallback returns a RemoveCallback that cleans up orphaned EC records
// and the EF centroid inside the same LMDB transaction. The TvidTxn
// receives F/V/T removals; its caller commits or aborts based on the
// surrounding RemoveFileWithCallback result. R1852, R1853, R1963
func (idx *Indexer) removeCallback(fileID uint64, tt *TvidTxn) microfts2.RemoveCallback {
	if idx.store == nil {
		return nil
	}
	return func(txn *bbolt.Tx, orphanedChunkIDs []uint64) error {
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
	return func(txn *bbolt.Tx, orphanedChunkIDs, _ []uint64) error {
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
// publishRouted publishes the tag values this pass's @ext chunks route, each
// under its target chunk's file path, after the mirror's own publish. Only
// chunks carrying an @ext tag can be sources, so a file with none costs one
// scan of its tags (R3594).
// CRC: crc-Indexer.md | Seq: seq-ext-routing.md | R3594
func (idx *Indexer) publishRouted(chunkTags []ChunkTagValues) {
	if idx.pubsub == nil || idx.extmap == nil || idx.db == nil {
		return
	}
	var sources []uint64
	for _, ct := range chunkTags {
		for _, tv := range ct.Values {
			if tv.Tag == tagExt {
				sources = append(sources, ct.ChunkID)
				break
			}
		}
	}
	byTarget := idx.extmap.RoutedFromSources(sources)
	if len(byTarget) == 0 {
		return
	}
	byPath := make(map[string][]TagValue)
	for target, vals := range byTarget {
		fileID, ok := idx.db.ChunkFileID(target)
		if !ok {
			continue
		}
		path, ok := idx.db.resolveFilePath(fileID)
		if !ok {
			continue
		}
		byPath[path] = append(byPath[path], vals...)
	}
	for path, vals := range byPath {
		idx.pubsub.PublishRouted("", path, vals)
	}
}

func (idx *Indexer) runExtRouting(fileID uint64, addedChunkIDs, orphanedChunkIDs []uint64, chunkTags []ChunkTagValues) error {
	if idx.extmap == nil || idx.db == nil {
		return nil
	}
	// R3384: any file change dirties every internal-multi-hop premise. It goes
	// here, ahead of the plan collection and its early return, because the
	// trigger is *this file changed* rather than *this file carries @ext tags*
	// — an intermediate directory link's own file moving is precisely the
	// event no premise on it would otherwise hear about.
	idx.extmap.MarkInternalMultiHopDirty()
	// R3264: only a mirror tree may author @ext-family directives. An @ext
	// tag is an instruction, not a note, so honoring one from any indexed
	// file means *discussing* a routing authors it — a transcript quoting an
	// @ext-candidate files a real proposal into the human's review queue.
	// Re-resolution is not gated: it is driven by the target side and only
	// ever re-resolves routings a mirror already authored.
	var idxPlans []extIndexPlan
	if path, ok := idx.db.fileIDPath(fileID); ok && idx.db.IsExtMirrorPath(path) {
		idxPlans = idx.collectIndexExtPlans(fileID, chunkTags)
	} else if n := countExtFamilyTags(chunkTags); n > 0 {
		// Loud, because a silent skip is indistinguishable from a broken
		// router — the failure mode that let @ext routing sit dead on this
		// very path for three months without a symptom. (R3265)
		Logv(1, "ext routing: %s is not in a mirror tree — %d @ext-family tag(s) indexed as text, not routed", path, n)
	}
	rrPlans := idx.collectReresolvePlans(fileID, addedChunkIDs, orphanedChunkIDs)
	if len(idxPlans) == 0 && len(rrPlans) == 0 {
		return nil
	}
	return idx.store.WithTvidTxn(func(tt *TvidTxn) error {
		return idx.store.bolt.Update(func(txn *bbolt.Tx) error {
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
// CRC: crc-Indexer.md | R1996, R2012, R2016, R2374, R3061, R3074
// countExtFamilyTags counts the @ext-family tags in a file's chunk tags —
// what the authority gate declined to route, so the log can say how much
// was passed over rather than merely that something was.
// CRC: crc-Indexer.md | R3265
func countExtFamilyTags(chunkTags []ChunkTagValues) int {
	n := 0
	for _, ct := range chunkTags {
		for _, tv := range ct.Values {
			if isExtFamilyTag(tv.Tag) && tv.Value != "" {
				n++
			}
		}
	}
	return n
}

func (idx *Indexer) collectIndexExtPlans(fileID uint64, chunkTags []ChunkTagValues) []extIndexPlan {
	sourceDir := idx.sourceDirFor(fileID)
	var out []extIndexPlan
	for _, ct := range chunkTags {
		for _, tv := range ct.Values {
			// R3061: derive all three @ext-family classes, not @ext alone.
			if !isExtFamilyTag(tv.Tag) || tv.Value == "" {
				continue
			}
			tvidExt, ok := idx.store.tvids.Lookup(tv.Tag, tv.Value)
			if !ok {
				continue
			}
			target, routed, parseOK := ParseExtTarget(tv.Value)
			if !parseOK {
				continue
			}
			// R3074: split the reserved @count out of the routed tags; it
			// materializes into the RC tally / signed RJ score downstream.
			routed, count, _ := extractCountField(routed)
			parts, _ := ParseExtTargetParts(target, sourceDir)
			res := idx.db.ResolveExtTargetVia(target, sourceDir)
			out = append(out, extIndexPlan{
				sourceChunkID: ct.ChunkID,
				sourceFileID:  fileID,
				tvidExt:       tvidExt,
				class:         extClassForTag(tv.Tag),
				count:         count,
				target:        target,
				targetBase:    parts.BaseValue,
				targets:       res.Chunks,
				targetFileID:  res.FileID,
				targetHops:    res.Hops,
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
// source content. An overlay source writes no LMDB records
// (bothPersistent is always false), but it can route to a *persistent*
// target whose fileid must be resolved via chunkFileID → ReadCRecord —
// an LMDB read that needs a live txn. So it opens a read-only env.View
// and threads that txn into applyIndexExt; the read-only txn (and a nil
// TvidTxn) suffice because no writes can fire. Mirrors the
// self-contained read in ExtMap.ExtRoutingsForTargetChunk. The txn is
// born inside the actor closure that drove AddTmpFile (R986), so it
// never nests inside another env.Update.
// CRC: crc-Indexer.md | R2012, R2016, R2018, R2915
func (idx *Indexer) runOverlayExtRouting(fileID uint64, chunkTags []ChunkTagValues) error {
	if idx.extmap == nil || idx.db == nil {
		return nil
	}
	plans := idx.collectIndexExtPlans(fileID, chunkTags)
	if len(plans) == 0 {
		return nil
	}
	return idx.db.store.bolt.View(func(txn *bbolt.Tx) error {
		for _, p := range plans {
			if err := idx.extmap.applyIndexExt(txn, nil, idx.db, p); err != nil {
				return err
			}
		}
		return nil
	})
}

// collectReresolvePlans determines which tvid_exts need re-resolution
// for this file change and pre-resolves each. Each candidate's
// source file may differ (the changed file F is the target of the
// routings; the sources live elsewhere), so sourceDir is recovered
// per-tvid_ext via ExtMap.SourceChunkID → chunkFileID → fileIDPath.
// CRC: crc-Indexer.md | R2000, R2001, R2374, R3061, R3074
func (idx *Indexer) collectReresolvePlans(fileID uint64, addedChunkIDs, orphanedChunkIDs []uint64) []extReresolvePlan {
	return idx.reresolvePlansFor(idx.extmap.candidatesForFileChange(idx.db, fileID, addedChunkIDs, orphanedChunkIDs))
}

// reresolvePlansFor builds re-resolution plans for an explicit set of
// routings. Split out of collectReresolvePlans so the two things that can
// invalidate a routing — a file changing, and a link moving under a premise —
// share one plan builder instead of growing a second. R3388's rule applied to
// the plan step: a parallel implementation would drift, and the drift shows up
// as tags routing correctly through one path and not the other.
// CRC: crc-Indexer.md | R2000, R2001, R3388
func (idx *Indexer) reresolvePlansFor(candidates map[uint64]bool) []extReresolvePlan {
	if len(candidates) == 0 {
		return nil
	}
	// Cache sourceDir per source-fileID so a many-tvid file pays the
	// path lookup once.
	sourceDirCache := make(map[uint64]string)
	out := make([]extReresolvePlan, 0, len(candidates))
	for tvidExt := range candidates {
		tag, value, ok := idx.store.tvids.Resolve(tvidExt)
		if !ok {
			continue
		}
		target, routed, parseOK := ParseExtTarget(value)
		if !parseOK {
			continue
		}
		// R3061/R3074: recover the class from the source tag and split @count.
		routed, count, _ := extractCountField(routed)
		sourceDir := idx.reresolveSourceDir(tvidExt, sourceDirCache)
		parts, _ := ParseExtTargetParts(target, sourceDir)
		out = append(out, extReresolvePlan{
			tvidExt:    tvidExt,
			class:      extClassForTag(tag),
			count:      count,
			target:     target,
			targetBase: parts.BaseValue,
			routedTags: routed,
			newTargets: idx.db.ResolveExtTarget(target, sourceDir),
		})
	}
	return out
}

// reresolveMovedPremises rewrites the routings whose link premise stopped
// holding — a link repointed, replaced, or deleted under an @ext target.
//
// R3388: reuses applyReresolve rather than a parallel updater, so X records,
// targetToChunk, chunkToTargets, fileidToTvids, extByAnchor, virtualTagCount
// and routedTagsByTvidExt all move together as one unit.
//
// It runs applyReresolve **once per file the routing touches**, because that
// function is deliberately file-scoped (filterByFileWithIDs keeps only chunks
// whose fileid matches) and a moved premise spans two files: the one the
// routing is leaving and the one it is arriving at. A file change never needs
// both, since each file's own change event supplies its own scope — but a link
// moving changes *neither* file, which is exactly the hole the single-hop tier
// exists to plug, and why this is the one caller that must supply both. Both
// calls share one transaction, so the routing is never half-moved.
// Deliberately does NOT claim R3387. This runs on the actor and posts nothing
// forward, because its caller (SyncLinkPath) is a link *event* rather than a
// retrieval. R3387 is the sewn retrieval — update-and-retrieve on the actor
// with the tags travelling forward as a continuation — and that half is still
// open as gap O209. Naming it here would turn its coverage green while the
// work is missing.
// CRC: crc-Indexer.md | Seq: seq-ext-tag-retrieval.md#1.4.2 | R3388
func (idx *Indexer) reresolveMovedPremises(changed []extPremiseChange) error {
	if idx.extmap == nil || idx.db == nil || len(changed) == 0 {
		return nil
	}
	candidates := make(map[uint64]bool, len(changed))
	scopes := make(map[uint64]bool, 2*len(changed))
	for _, c := range changed {
		candidates[c.TvidExt] = true
		// Zero is not a file; it is what an unresolvable premise records
		// (R3386), and filterByFileWithIDs would match nothing for it anyway.
		if c.Was != 0 {
			scopes[c.Was] = true
		}
		if c.Now != 0 {
			scopes[c.Now] = true
		}
	}
	plans := idx.reresolvePlansFor(candidates)
	if len(plans) == 0 || len(scopes) == 0 {
		return nil
	}
	return idx.store.WithTvidTxn(func(tt *TvidTxn) error {
		return idx.store.bolt.Update(func(txn *bbolt.Tx) error {
			for fileID := range scopes {
				for _, p := range plans {
					if err := idx.extmap.applyReresolve(txn, tt, idx.db, fileID, p); err != nil {
						return err
					}
				}
			}
			return nil
		})
	})
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
	_ = idx.db.fts.DB().View(func(txn *bbolt.Tx) error {
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
	class         extClass // source tag class: committed / candidate / judgment (R3061)
	count         int64    // materialized @count → RC tally / signed RJ score (R3074)
	target        string   // TARGET text as authored (verbatim for V record / debugging)
	targetBase    string   // BASE of the TARGET — absolutized path or UUID value — for extByAnchor keying (R2380)
	targets       []uint64
	// R3382: the premise `targets` rests on — the fileid the TARGET resolved
	// to, and the link chain crossed getting there. Both come from the same
	// walk that produced `targets`, so the premise can never describe a
	// resolution other than the one recorded. targetHops is nil when the
	// TARGET crossed no tracked link, which is exactly the case needing no
	// premise at all (R3372).
	targetFileID uint64
	targetHops   []string
	routedTags   []TagValue
}

type extReresolvePlan struct {
	tvidExt    uint64
	class      extClass // source tag class recovered from the resolved tag name (R3061)
	count      int64    // materialized @count → RC tally / signed RJ score (R3074)
	target     string   // TARGET text as authored
	targetBase string   // BASE for extByAnchor keying (R2380)
	routedTags []TagValue
	newTargets []uint64
}

// refreshPrep holds data prepared by a worker for the ChanSvc to execute.
// Workers populate this via prepareRefresh (file I/O + append detection).
// The ChanSvc executes writes via executeRefresh (LMDB mutations).
// Tag extraction happens on-actor via chunk callbacks (R1896).
type refreshPrep struct {
	path      string
	strategy  string
	oldID     uint64
	isAppend  bool
	unchanged bool   // R2864: byte-identical to last index — skip the full refresh
	data      []byte // full file content
	// Append-specific fields
	newBytes []byte
	baseLine int
	fullHash string
	fileSize int64
	modTime  int64
}

// chunkedBytes is how much this refresh chunked: nothing when skipped as
// unchanged, the new bytes for an append, the whole file otherwise. R3603
func (p *refreshPrep) chunkedBytes() int64 {
	switch {
	case p.unchanged:
		return 0
	case p.isAppend:
		return int64(len(p.newBytes))
	}
	return int64(len(p.data))
}

// prepareRefresh reads a file and detects append-vs-full — safe to call
// from multiple goroutines. LMDB reads (DetectAppend, FileInfoByID) are
// concurrent-safe. Returns a prep struct for executeRefresh.
// Tag extraction itself moves on-actor (R1896).
// CRC: crc-Indexer.md | Seq: seq-file-change.md | R364, R368, R3008
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
	// R732: level-2 (fine) verbosity tier — per-file refresh/append detail
	Logv(2, "prepare-refresh: %s detect=%v", path, ok)
	info, infoErr := idx.fts.FileInfoByID(fileID)
	if ok {
		Logv(2, "prepare-refresh: %s info-err=%v FL=%d data-len=%d",
			path, infoErr, info.FileLength, len(data))
		if infoErr == nil && info.FileLength > 0 && int64(len(data)) > info.FileLength {
			prep.isAppend = true
			prep.newBytes = data[info.FileLength:]
			if n := len(info.Chunks); n > 0 {
				_, endLine := parseRange(info.Chunks[n-1].Location)
				prep.baseLine = endLine
			}
			fullHash := sha256.Sum256(data)
			prep.fullHash = fmt.Sprintf("%x", fullHash)
			// R3008: the stored watermark must come from the same snapshot we
			// hashed. fileSize is len(data) — the exact bytes covered by
			// fullHash — never os.Stat's size, which can race ahead of `data`
			// under a concurrent append and desync the (FileLength, ContentHash)
			// pair (spurious full reindex) or drop the interleaved bytes.
			// modTime stays best-effort from stat.
			fi, _ := os.Stat(path)
			prep.fileSize = int64(len(data))
			prep.modTime = fi.ModTime().UnixNano()
			return prep, nil
		}
	}

	// Not an append. If the file is byte-identical to what we last
	// indexed — same length AND same content hash — there is nothing to
	// do, so skip the full re-chunk (R2864). This catches redundant
	// re-process events (throttle batches, spurious fsnotify) on large
	// append-only files like chat JSONLs, where a full refresh would be
	// an expensive no-op.
	if infoErr == nil && info.FileLength > 0 && int64(len(data)) == info.FileLength &&
		sha256.Sum256(data) == info.ContentHash {
		prep.unchanged = true
		Logv(2, "prepare-refresh: %s unchanged (size==FL, hash match) — skipping refresh", path)
	}

	return prep, nil
}

// executeRefresh runs all LMDB writes for a prepared file.
// Must be called from the ChanSvc goroutine (single writer).
// CRC: crc-Indexer.md | Seq: seq-file-change.md | R365, R367, R1894, R1896, R1898, R2864
func (idx *Indexer) executeRefresh(prep *refreshPrep) error {
	Logv(2, "execute-refresh: %s isAppend=%v newBytes=%d", prep.path, prep.isAppend, len(prep.newBytes))
	if prep.unchanged {
		// R2864: byte-identical to the last index — a full refresh would
		// re-chunk the whole file to produce the same index. Skip it.
		Logv(1, "index: %s UNCHANGED (skipped — same length and hash)", prep.path)
		Logv(2, "execute-refresh: %s unchanged — skipped full refresh", prep.path)
		return nil
	}
	if prep.isAppend {
		acc := chunkAccumulator{strategy: prep.strategy}
		err := idx.fts.AppendChunks(prep.oldID, prep.newBytes, prep.strategy,
			microfts2.WithBaseLine(prep.baseLine),
			microfts2.WithContentHash(prep.fullHash),
			microfts2.WithModTime(prep.modTime),
			microfts2.WithFileLength(prep.fileSize),
			microfts2.WithIndexedChunkCallback(acc.indexedCallback),
		)
		Logv(2, "execute-refresh: %s AppendChunks err=%v newChunks=%d", prep.path, err, len(acc.chunkTags))
		if err != nil {
			// R3598: append failed — fall through to full reindex. This includes
			// ErrChunkerVersionStale: the file's chunks follow other rules.
			Logv(1, "index: %s APPEND FAILED (%v) — falling back to REINDEX", prep.path, err)
			Logv(2, "execute-refresh: %s falling through to full refresh due to err: %v", prep.path, err)
			return idx.executeFullRefresh(prep)
		}
		// R3266: name the outcome plainly, at default verbosity. Which
		// branch ran decides whether a change was re-derived at all, and
		// until now it could only be inferred from `isAppend=` and
		// `unchanged` buried mid-line across four -vv lines. An append that
		// yields no chunks is the case worth seeing: the file grew and
		// nothing downstream re-ran.
		Logv(1, "index: %s APPENDED %d bytes → %d new chunk(s)", prep.path, len(prep.newBytes), len(acc.chunkTags))
		return idx.afterAppendStore(prep.oldID, prep.path, prep.strategy, prep.newBytes, &acc)
	}
	return idx.executeFullRefresh(prep)
}

// afterAppendStore runs the post-store bookkeeping for an append: incremental
// file-level tags + defs from the appended bytes only, @ext routing, tag pubsub,
// the schedule date index, and the recall-watcher hook. Shared by the fused
// executeRefresh append branch and the prepared-store path (storeScan →
// AppendPrepared). (R3426)
// CRC: crc-Indexer.md | Seq: seq-tag-value-index.md, seq-recall-watcher.md#1 | R1884, R1894, R2007, R2696, R2729, R2913, R3426
func (idx *Indexer) afterAppendStore(oldID uint64, path, strategy string, newBytes []byte, acc *chunkAccumulator) error {
	// Embeddings: orphan EC/EF cleanup ran inside the append callback; new
	// chunks will be embedded by Librarian.BatchEmbedChunks post-reconcile
	// (R1923, R1926). Tags: chunkid-keyed; chunkids arrive via the indexed callback.
	if idx.store == nil {
		return nil
	}
	// Append file-level tags + defs from the appended bytes only
	// (incremental, matching the prior callback semantics).
	fileVals, fileDefs := idx.fileLevelTags(path, newBytes, strategy)
	if err := idx.store.AppendTagValues(acc.chunkTags); err != nil {
		return fmt.Errorf("append tag values %s: %w", path, err)
	}
	if err := idx.store.AppendTagDefs(oldID, fileDefs); err != nil {
		return fmt.Errorf("append tag defs %s: %w", path, err)
	}
	added := chunkIDsOf(acc.chunkTags)
	if err := idx.runExtRouting(oldID, added, nil, acc.chunkTags); err != nil {
		return fmt.Errorf("ext routing %s: %w", path, err)
	}
	// R795, R796: publish tag events from appended content
	if idx.pubsub != nil {
		idx.pubsub.PublishAndWatch("", path, fileVals)
		idx.publishRouted(acc.chunkTags) // R3594
	}
	idx.writeDateIndex(path, fileVals)
	// Simple-recall watcher hook: hand the freshly-appended bytes and the
	// chunkIDs the chunker emitted to the watcher's turn-boundary state
	// machine. Live-append is the only path that drives the watcher — full
	// reindex and initial add would amount to backfill (R2698 disallows).
	if idx.recallWatcher != nil {
		idx.recallWatcher.OnAppend(path, strategy, newBytes, added)
	}
	return nil
}

// executeFullRefresh does a complete reindex. Per-chunk tags arrive via
// the indexed callback (re-extracted from each chunk's original content);
// file-level tags + defs are re-extracted from prep.data. Orphaned chunkids (delivered
// by the reindex callback) drop their F/V/T contributions in the same txn.
// CRC: crc-Indexer.md | R1849, R1852, R1854, R1891, R1899, R2913
func (idx *Indexer) executeFullRefresh(prep *refreshPrep) error {
	// R731: level-1 (coarsest) verbosity tier — high-level refresh milestone.
	// R3266: say REINDEXED in the same vocabulary the append and unchanged
	// branches use, so one grep separates the three outcomes.
	Logv(1, "index: %s REINDEXED in full (fileID=%d)", prep.path, prep.oldID)
	acc := chunkAccumulator{strategy: prep.strategy}
	var fileid uint64
	if err := idx.store.WithTvidTxn(func(tt *TvidTxn) error {
		var err error
		fileid, err = idx.fts.ReindexWithCallback(prep.path, prep.strategy, idx.reindexCallback(prep.oldID, tt),
			microfts2.WithIndexedChunkCallback(acc.indexedCallback))
		return err
	}); err != nil {
		return fmt.Errorf("fts reindex %s: %w", prep.path, err)
	}
	return idx.afterReindexStore(fileid, prep.oldID, prep.path, prep.strategy, prep.data, &acc)
}

// afterReindexStore runs the post-store bookkeeping for a content-diff reindex:
// pdf/bible flushes, removal of file-keyed records still under the old fileid,
// then chunkid-keyed tag values, file-level defs, @ext routing, tag pubsub, and
// the schedule date index. Shared by the fused executeFullRefresh and the
// prepared-store path (storeScan → ReindexPrepared). (R3426)
// CRC: crc-Indexer.md | Seq: seq-tag-value-index.md, seq-ext-routing.md | R1883, R1891, R1996, R2000-R2007, R2913, R3426
func (idx *Indexer) afterReindexStore(fileid, oldID uint64, path, strategy string, data []byte, acc *chunkAccumulator) error {
	if idx.pdfChunker != nil {
		if err := idx.pdfChunker.FlushBlobs(path, fileid); err != nil {
			log.Printf("pdf: flush blobs %s: %v", path, err)
		}
	}
	// R3214: same book-index flush as the add path — a reindex re-walks the
	// file, so its chapter set is staged again and overwrites in place.
	if idx.bibleChunker != nil {
		if err := idx.bibleChunker.FlushBookIndex(path); err != nil {
			log.Printf("bible: flush book index %s: %v", path, err)
		}
	}

	// EC/EF cleanup ran inside the reindex/removed callback; new EC records
	// arrive on the next BatchEmbedChunks pass. (R1923, R1926)

	if idx.store != nil {
		if fileid != oldID {
			// File-keyed records that survived the reindex callback
			// belong to oldID and need explicit removal.
			idx.store.RemoveTagDefs(oldID)
			idx.store.RemovePageContents(oldID)
		}
		// File-level tags + defs re-extracted from the full file content.
		fileVals, fileDefs := idx.fileLevelTags(path, data, strategy)
		if err := idx.store.UpdateTagValues(acc.chunkTags); err != nil {
			return fmt.Errorf("update tag values %s: %w", path, err)
		}
		if err := idx.store.UpdateTagDefs(fileid, fileDefs); err != nil {
			return fmt.Errorf("update tag defs %s: %w", path, err)
		}
		added := chunkIDsOf(acc.chunkTags)
		if err := idx.runExtRouting(fileid, added, nil, acc.chunkTags); err != nil {
			return fmt.Errorf("ext routing %s: %w", path, err)
		}
		// R795, R796: publish tag events from refreshed content
		if idx.pubsub != nil {
			idx.pubsub.PublishAndWatch("", path, fileVals)
			idx.publishRouted(acc.chunkTags) // R3594
		}
		// R866: write schedule log entries
		idx.writeDateIndex(path, fileVals)
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

// scanPrep is the off-slot compute result for one per-file scan: a microfts2
// prepared handle plus ark's own content snapshot and its length+hash, used for
// the store-boundary content-staleness escape (R3423). Produced by prepareScan
// off the write slot; consumed by storeScan on it.
// CRC: crc-FileScanActor.md | R3426
type scanPrep struct {
	path, strategy string
	oldID          uint64 // 0 for a fresh add
	force          bool   // R3568: full in-place reindex even if unchanged or only appended
	isAppend       bool
	prepared       *microfts2.PreparedFile   // full add / reindex
	appendPrep     *microfts2.PreparedAppend // append
	data           []byte                    // full file content, for fileLevelTags + the escape
	newBytes       []byte                    // append: the bytes past the committed length
	fullHash       string
	fileSize       int64
	modTime        int64
}

// chunkedBytes is how much this scan chunked: the new bytes for an append, the
// whole file otherwise. R3602
func (p *scanPrep) chunkedBytes() int64 {
	if p.isAppend {
		return int64(len(p.newBytes))
	}
	return p.fileSize
}

// errScanSuperseded is returned by the per-chunk compute when a newer event
// bumped the path's changeNum: the scan abandons between chunks and stores
// nothing (its finished grams stay cached for the newer scan). Not a real error
// — prepareFileScan drops it without logging, and retire is a no-op since the
// entry is no longer this scan's. R3430
var errScanSuperseded = errors.New("scan superseded by a newer change")

// scanGate mediates per-chunk grams reuse and changeNum preemption for an
// off-slot full-file scan. Its methods run on the DB actor, where the per-path
// reuse map lives (no mutex), so buildEntries never touches shared state itself.
// Realized by dbScanGate in production and a fake in tests.
// CRC: crc-FileScanActor.md | R3429, R3430
type scanGate interface {
	// reuse consults the reuse map: superseded reports a newer scan won (abandon),
	// cached is reusable grams for hash (nil on a miss).
	reuse(hash [32]byte) (superseded bool, cached *microfts2.PreparedChunk)
	// keep caches freshly computed grams under hash for the newer scan to reuse.
	keep(hash [32]byte, pc *microfts2.PreparedChunk)
}

// buildEntries is the per-chunk compute loop: for each split chunk consult the
// gate, preempt on supersession (between chunks, never inside a PrepareChunk),
// reuse cached grams on a hash hit, else compute them once and cache. Returns the
// ordered PreparedEntry list AssembleFile takes, or errScanSuperseded. prepareChunk
// is injected (idx.fts.PrepareChunk in production) so the loop is testable with a
// fake gate and a counting compute — no DB, no fts, no fixtures.
// CRC: crc-FileScanActor.md | Seq: seq-file-scan-actor.md#1.5.3.2 | R3428, R3429, R3430, R3431
func buildEntries(splits []microfts2.SplitChunk, gate scanGate, prepareChunk func([]byte) *microfts2.PreparedChunk) ([]microfts2.PreparedEntry, error) {
	entries := make([]microfts2.PreparedEntry, 0, len(splits))
	for _, sc := range splits {
		superseded, cached := gate.reuse(sc.Hash) // R3430: preemption checkpoint
		if superseded {
			return nil, errScanSuperseded
		}
		pc := cached
		if pc == nil {
			pc = prepareChunk(sc.Chunk.Content) // R3428: grams only on a reuse miss
			gate.keep(sc.Hash, pc)              // R3431: deposit for the newer scan
		}
		entries = append(entries, microfts2.PreparedEntry{Chunk: sc.Chunk, Hash: sc.Hash, Grams: pc})
	}
	return entries, nil
}

// prepareScan runs one file's index compute off the write slot: read the file,
// compute its length+hash, detect append-vs-full against the committed F record,
// and compute grams holding no write transaction. A full add/reindex runs the
// per-chunk compute (SplitFile → buildEntries → AssembleFile) with the reuse gate;
// an append keeps the single PrepareAppend call. Safe to call from a plain
// goroutine — the LMDB read (FileInfoByID) is MVCC-safe and runs on a private fts
// copy. Returns a scanPrep for storeScan.
// CRC: crc-FileScanActor.md | Seq: seq-file-scan-actor.md#1.5 | R3419, R3426, R3428
func (idx *Indexer) prepareScan(path, strategy string, oldID uint64, known, stale, force bool, gate scanGate) (*scanPrep, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	prep := &scanPrep{
		path: path, strategy: strategy, oldID: oldID, force: force, data: data,
		fullHash: fmt.Sprintf("%x", sha256.Sum256(data)),
		fileSize: int64(len(data)),
	}
	if fi, statErr := os.Stat(path); statErr == nil {
		prep.modTime = fi.ModTime().UnixNano()
	}
	// R3600: judged stale yet byte-identical to its record means stale only by
	// chunker version. Force the scan, or append detection and the store-time
	// unchanged escape (R3423) would both pass it by and leave it stale.
	if stale && known && !force {
		if info, err := idx.fts.FileInfoByID(oldID); err == nil &&
			info.FileLength == prep.fileSize && sha256.Sum256(data) == info.ContentHash {
			force = true
			prep.force = true
		}
	}

	// Append detection off the same snapshot we hashed: the current file's first
	// FileLength bytes match the committed content hash AND the file grew. Using
	// `data` rather than a second read keeps the (length, hash) pair coherent
	// under a concurrent append. (R360-R362, R3008) A forced scan never appends:
	// it re-chunks the whole file under the current rules (R3568).
	if known && !force {
		info, infoErr := idx.fts.FileInfoByID(oldID)
		if infoErr == nil && info.FileLength > 0 && int64(len(data)) > info.FileLength &&
			sha256.Sum256(data[:info.FileLength]) == info.ContentHash {
			newBytes := data[info.FileLength:]
			baseLine := 0
			var lastLocator []byte
			if n := len(info.Chunks); n > 0 {
				_, endLine := parseRange(info.Chunks[n-1].Location)
				baseLine = endLine
				lastLocator = info.Chunks[n-1].Locator
			}
			ap, apErr := idx.fts.PrepareAppend(path, lastLocator, newBytes, strategy,
				microfts2.WithBaseLine(baseLine))
			if apErr != nil {
				return nil, fmt.Errorf("prepare append %s: %w", path, apErr)
			}
			prep.isAppend = true
			prep.newBytes = newBytes
			prep.appendPrep = ap
			return prep, nil
		}
	}

	// Full add (oldID == 0) or full reindex. Per-chunk compute (R3428): split the
	// file once (cheap boundaries + hashes, no grams), compute grams per chunk
	// through the reuse gate, then assemble the same opaque handle StorePrepared/
	// ReindexPrepared take — equivalent to the fused PrepareFile, plus reuse and
	// per-chunk preemption.
	splits, meta, splitErr := idx.fts.SplitFile(path, strategy)
	if splitErr != nil {
		return nil, fmt.Errorf("split file %s: %w", path, splitErr)
	}
	entries, buildErr := buildEntries(splits, gate, idx.fts.PrepareChunk)
	if buildErr != nil {
		return nil, buildErr // errScanSuperseded: the newer scan owns the store
	}
	pf, asmErr := idx.fts.AssembleFile(path, strategy, entries, meta)
	if asmErr != nil {
		return nil, fmt.Errorf("assemble file %s: %w", path, asmErr)
	}
	prep.prepared = pf
	return prep, nil
}

// contentUnchangedForStore is the pure store-boundary staleness decision: a full
// store may be skipped only when the committed index already holds this exact
// content — same length AND same hash. An append is never skipped (its new bytes
// are past the committed length by construction), which is the load-bearing
// exemption. R3423
// CRC: crc-FileScanActor.md | Seq: seq-file-scan-actor.md#1.9.1 | R3423
func contentUnchangedForStore(isAppend bool, committedLen int64, committedHash [32]byte, prepSize int64, prepHash string) bool {
	if isAppend {
		return false
	}
	if committedLen <= 0 {
		return false
	}
	return committedLen == prepSize && fmt.Sprintf("%x", committedHash) == prepHash
}

// storeContentUnchanged fetches the committed F record and applies the pure
// content-staleness decision. R3423
func (idx *Indexer) storeContentUnchanged(prep *scanPrep) bool {
	info, err := idx.fts.FileInfoByID(prep.oldID)
	if err != nil {
		return false
	}
	return contentUnchangedForStore(prep.isAppend, info.FileLength, info.ContentHash, prep.fileSize, prep.fullHash)
}

// storeScan applies a prepared scan on the write slot: the length+hash
// content-staleness escape (full only), then StorePrepared / ReindexPrepared /
// AppendPrepared, then the shared post-store bookkeeping. Runs on the write
// goroutine (idx bound to the write copy). The compute already happened
// off-slot, so only this short store + record work holds the slot. R3423, R3426
// CRC: crc-FileScanActor.md | Seq: seq-file-scan-actor.md#1.9 | R3423, R3426
func (idx *Indexer) storeScan(prep *scanPrep) error {
	if prep.isAppend {
		return idx.storeAppendScan(prep)
	}
	// R3423: the file changed on disk between prepare and store to nothing new —
	// or an interleaving scan already stored this exact content. Skip the
	// redundant re-chunk.
	if prep.oldID != 0 && !prep.force && idx.storeContentUnchanged(prep) { // R3568: forced, store anyway
		Logv(1, "index: %s UNCHANGED (skipped — same length and hash)", prep.path)
		return nil
	}
	acc := chunkAccumulator{strategy: prep.strategy}
	if prep.oldID == 0 {
		fileid, err := idx.fts.StorePrepared(prep.prepared,
			microfts2.WithIndexedChunkCallback(acc.indexedCallback))
		if err != nil {
			return fmt.Errorf("store %s: %w", prep.path, err)
		}
		Logv(1, "index: %s ADDED (fileID=%d)", prep.path, fileid)
		return idx.afterAddStore(fileid, prep.path, prep.strategy, prep.data, &acc)
	}

	Logv(1, "index: %s REINDEXED in full (fileID=%d)", prep.path, prep.oldID)
	var fileid uint64
	var cleanupErr error
	if err := idx.store.WithTvidTxn(func(tt *TvidTxn) error {
		var e error
		fileid, e = idx.fts.ReindexPrepared(prep.prepared,
			microfts2.WithIndexedChunkCallback(acc.indexedCallback),
			microfts2.WithRemovedChunkCallback(idx.removedChunkCleanup(tt, &cleanupErr)))
		return e
	}); err != nil {
		return fmt.Errorf("reindex %s: %w", prep.path, err)
	}
	if cleanupErr != nil {
		return fmt.Errorf("reindex orphan cleanup %s: %w", prep.path, cleanupErr)
	}
	// A reindex allocates a fresh fileid, so the old fileid's EF centroid is now
	// stale. The fused path drops it inside the reindex txn (reindexCallback →
	// DeleteFileCentroidInTxn); the prepared seam exposes no positive reindex
	// callback (only WithRemovedChunkCallback, which never fires when nothing is
	// orphaned), so drop it here on the serialized write goroutine. A crash in
	// the tiny gap leaves only a stale centroid the next embed pass recomputes.
	if idx.store != nil {
		if err := idx.store.DeleteFileCentroid(prep.oldID); err != nil {
			log.Printf("index: drop stale centroid %d: %v", prep.oldID, err)
		}
	}
	return idx.afterReindexStore(fileid, prep.oldID, prep.path, prep.strategy, prep.data, &acc)
}

// storeAppendScan stores a prepared append on the write slot. Appends skip the
// content-staleness escape (R3423). On ErrAppendTailMoved — or any store error —
// the caller re-emits one change event (R3425) rather than falling through to a
// full reindex on the slot. R3426, R3427
// CRC: crc-FileScanActor.md | Seq: seq-file-scan-actor.md#1.9.4 | R3426, R3427
func (idx *Indexer) storeAppendScan(prep *scanPrep) error {
	acc := chunkAccumulator{strategy: prep.strategy}
	var cleanupErr error
	if err := idx.store.WithTvidTxn(func(tt *TvidTxn) error {
		return idx.fts.AppendPrepared(prep.oldID, prep.appendPrep,
			microfts2.WithContentHash(prep.fullHash),
			microfts2.WithModTime(prep.modTime),
			microfts2.WithFileLength(prep.fileSize),
			microfts2.WithIndexedChunkCallback(acc.indexedCallback),
			microfts2.WithRemovedChunkCallback(idx.removedChunkCleanup(tt, &cleanupErr)))
	}); err != nil {
		return fmt.Errorf("append %s: %w", prep.path, err)
	}
	if cleanupErr != nil {
		return fmt.Errorf("append orphan cleanup %s: %w", prep.path, cleanupErr)
	}
	Logv(1, "index: %s APPENDED %d bytes → %d new chunk(s)", prep.path, len(prep.newBytes), len(acc.chunkTags))
	return idx.afterAppendStore(prep.oldID, prep.path, prep.strategy, prep.newBytes, &acc)
}

// DetectAppend checks whether a file change is append-only by hashing
// the first FileLength bytes and comparing to the stored ContentHash.
// Returns true if the prefix is unchanged and the file grew.
// CRC: crc-Indexer.md | Seq: seq-file-change.md | R360, R361, R362
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

	// Append to FTS index. Per-chunk tags arrive via the chunkid-aware
	// indexed callback (newly-inserted chunks only, re-extracted from each
	// chunk's original content); file-level tags + defs are re-extracted
	// from the appended bytes.
	acc := chunkAccumulator{strategy: strategy}
	err = idx.fts.AppendChunks(fileid, newBytes, strategy,
		microfts2.WithBaseLine(baseLine),
		microfts2.WithContentHash(fmt.Sprintf("%x", fullHash)),
		microfts2.WithModTime(fi.ModTime().UnixNano()),
		microfts2.WithFileLength(fi.Size()),
		microfts2.WithIndexedChunkCallback(acc.indexedCallback),
	)
	if err != nil {
		return fmt.Errorf("fts append %s: %w", path, err)
	}

	// Embeddings: Librarian.BatchEmbedChunks reconciles EC/EF on the next
	// pass. No synchronous vector write here. (R1923, R1926)

	// Tags and defs: chunkid-keyed via indexed callback.
	if idx.store != nil {
		// File-level tags + defs from the appended bytes (incremental).
		// CRC: crc-Indexer.md | R2913
		fileVals, fileDefs := idx.fileLevelTags(path, newBytes, strategy)
		// CRC: crc-Indexer.md | Seq: seq-tag-value-index.md | R1884, R1894
		if err := idx.store.AppendTagValues(acc.chunkTags); err != nil {
			return fmt.Errorf("append tag values %s: %w", path, err)
		}
		if err := idx.store.AppendTagDefs(fileid, fileDefs); err != nil {
			return fmt.Errorf("append tag defs %s: %w", path, err)
		}
		// CRC: crc-Indexer.md | Seq: seq-ext-routing.md | R2007
		added := chunkIDsOf(acc.chunkTags)
		if err := idx.runExtRouting(fileid, added, nil, acc.chunkTags); err != nil {
			return fmt.Errorf("ext routing %s: %w", path, err)
		}
		// R795, R796: publish tag events from appended content
		if idx.pubsub != nil {
			idx.pubsub.PublishAndWatch("", path, fileVals)
			idx.publishRouted(acc.chunkTags) // R3594
		}
		// R866: write schedule log entries
		idx.writeDateIndex(path, fileVals)
	}

	return nil
}

// prepareStaleRefresh prepares a file microfts2 has judged stale. The
// byte-identical skip (R2864) absorbs redundant change events and never applies
// here: a stale file whose bytes match its record is stale by chunker version,
// and skipping it would leave it stale forever.
// CRC: crc-Indexer.md | R3597
func (idx *Indexer) prepareStaleRefresh(s microfts2.FileStatus) (*refreshPrep, error) {
	prep, err := idx.prepareRefresh(s.Path, s.Strategy, s.FileID)
	if err != nil {
		return nil, err
	}
	prep.unchanged = false
	return prep, nil
}

// CRC: crc-Indexer.md | Seq: seq-parallel-refresh.md | R517-R522
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
		prep, err := idx.prepareStaleRefresh(s)
		if err != nil {
			log.Printf("refresh: skip %s: %v", s.Path, err)
			return missing, nil
		}
		if err := idx.executeRefresh(prep); err != nil {
			return missing, fmt.Errorf("refresh %s: %w", s.Path, err)
		}
		releaseAfterRefresh(prep.chunkedBytes())
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
	var chunked int64 // R3603: written only by the write goroutine, read after writeWg.Wait

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
				prep, err := idx.prepareStaleRefresh(s)
				if err != nil {
					log.Printf("refresh: skip %s: %v", s.Path, err)
					continue
				}
				writeCh <- func() error {
					if err := idx.executeRefresh(prep); err != nil {
						return err
					}
					chunked += prep.chunkedBytes()
					return nil
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
	releaseAfterRefresh(chunked)
	return missing, nil
}

// releaseAfterRefresh releases memory when a stale refresh chunked at least the
// scan threshold. R3603
// CRC: crc-Indexer.md | R3603
func releaseAfterRefresh(chunked int64) {
	if chunked >= releaseAfterScanBytes {
		releaseMemory("stale refresh")
	}
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
// CRC: crc-Indexer.md | R1317-R1325, R2110, R2111, R117
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

// stripArkTags removes real ark-tag spans from chunk content so the text
// that gets EC-embedded and trigram-indexed is tag-free (tag semantics
// live in V/EV/ED records, not the content embedding). It reuses the
// exact tag-recognition of ExtractTagValues — same regex, same
// isMention filter — so strip and extraction agree on what a tag is.
//
// A full-line tag (only whitespace before the `@`) takes its whole line
// including the trailing newline; an inline tag (content precedes it on
// the line) takes only the `@tag: …EOL` span, keeping the surrounding
// line. Mentions are left untouched. Because a value runs to end of line,
// there is at most one real tag per line. CRC: crc-Indexer.md | R2913
func stripArkTags(content []byte, strategy string) []byte {
	markdown := strategy == "markdown"
	locs := tagValueRegex.FindAllSubmatchIndex(content, -1)
	if len(locs) == 0 {
		return content
	}
	out := make([]byte, 0, len(content))
	prev := 0
	for _, loc := range locs {
		at, end := loc[0], loc[1] // '@' … end of value (no newline)
		if isMention(content, at, markdown) {
			continue
		}
		lineStart := at
		for lineStart > 0 && content[lineStart-1] != '\n' {
			lineStart--
		}
		fullLine := true
		for i := lineStart; i < at; i++ {
			if content[i] != ' ' && content[i] != '\t' {
				fullLine = false
				break
			}
		}
		removeStart, removeEnd := at, end
		if fullLine {
			removeStart = lineStart // eat the leading whitespace too
			if removeEnd < len(content) && content[removeEnd] == '\n' {
				removeEnd++ // and the trailing newline
			}
		}
		out = append(out, content[prev:removeStart]...)
		prev = removeEnd
	}
	out = append(out, content[prev:]...)
	return out
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
// R752: tagDefRegex keeps its line-start anchor (`(?:^|\n)`) — definitions
// are a structured, line-anchored format.
// CRC: crc-Indexer.md | R502, R752
var tagDefRegex = regexp.MustCompile(`(?:^|\n)@tag:\s+(\S+)\s+(.+)`)

// ExtractTagDefs scans content for @tag: name description lines.
// Returns map of tagname → description.
// CRC: crc-Indexer.md | R503
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
