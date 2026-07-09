package ark

// CRC: crc-ExtMap.md
//
// In-memory routing state for @ext. Six core maps plus overlay-only
// state maintained alongside DB X-record writes; canonical
// re-resolution flow runs from the reindex callback; source-side
// cleanup runs from the orphan callback. Persistent state rebuilt
// at startup by scanning X records; overlay state starts empty
// every session.

import (
	"path/filepath"
	"sync"
	"time"

	"go.etcd.io/bbolt"
)

// OverlayError captures a session-scoped diagnostic for an overlay
// (tmp://) ext routing. Surfaced via `ark errors` CLI.
// CRC: crc-ExtMap.md | R2029
type OverlayError struct {
	Time          time.Time
	SourceChunkID uint64 // zero if externally added
	SourceFileID  uint64 // zero if externally added
	Severity      string // "info" or "warn"
	Message       string
}

// ExtMap holds the in-memory state that supports @ext routing.
// CRC: crc-ExtMap.md | R1992, R2013, R2014, R2029
type ExtMap struct {
	mu                  sync.RWMutex
	targetToChunk       map[uint64][]uint64   // tvid_ext → chunkids
	chunkToTargets      map[uint64][]uint64   // chunkid → tvid_exts
	fileidToTvids       map[uint64][]uint64   // fileid → tvid_exts (target file)
	extByAnchor         map[string][]uint64   // anchor spec text → tvid_exts
	unresolvedTargets   map[uint64]bool       // tvid_exts whose target spec resolves to nothing
	virtualTagCount     map[string]int        // ext-routed contributions per tag (persistent + overlay)
	extSource           map[uint64]uint64     // tvid_ext → source chunkID (R2024, R2026)
	routedTagsByTvidExt map[uint64][]TagValue // tvid_ext → routed (tag, value) pairs (R2121)
	// Derived-record reverse lookups (state B). candidateSourcesByChunk
	// replaces the RC prefix scan; rejectByChunk answers the reject
	// filter's "is tag T net-rejected on chunk C" in one hit (R3065, R3066).
	candidateSourcesByChunk map[uint64][]uint64            // target_chunkid → source tvids (RC)
	rejectByChunk           map[uint64]map[string]int64    // target_chunkid → tagname → judgment score (RJ)
	overlayRoutings         map[uint64]map[uint64][]uint64 // tvid_ext → target_chunkid → routed_tvids (R2013)
	overlayValues           map[string]map[string][]uint64 // tag → value → target_chunkids (R2014)
	overlayErrors           []OverlayError                 // session diagnostics (R2029)
}

// NewExtMap constructs an empty ExtMap.
// CRC: crc-ExtMap.md | R1992, R2013, R2014
func NewExtMap() *ExtMap {
	return &ExtMap{
		targetToChunk:           make(map[uint64][]uint64),
		chunkToTargets:          make(map[uint64][]uint64),
		fileidToTvids:           make(map[uint64][]uint64),
		extByAnchor:             make(map[string][]uint64),
		unresolvedTargets:       make(map[uint64]bool),
		virtualTagCount:         make(map[string]int),
		extSource:               make(map[uint64]uint64),
		routedTagsByTvidExt:     make(map[uint64][]TagValue),
		candidateSourcesByChunk: make(map[uint64][]uint64),
		rejectByChunk:           make(map[uint64]map[string]int64),
		overlayRoutings:         make(map[uint64]map[uint64][]uint64),
		overlayValues:           make(map[string]map[string][]uint64),
	}
}

// VirtualTagCount returns the ext-routed contribution count for a
// tag. Used by Store.TagCounts for T-total augmentation.
// CRC: crc-ExtMap.md | R2010, R2021
func (m *ExtMap) VirtualTagCount(tag string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.virtualTagCount[tag]
}

// VirtualTagCounts returns the ext-routed contribution counts for a
// list of tags under a single RLock. Used by Store.TagCounts to
// avoid per-tag lock acquisitions on the query hot path.
// CRC: crc-ExtMap.md | R2010, R2021
func (m *ExtMap) VirtualTagCounts(tags []string) map[string]int {
	out := make(map[string]int, len(tags))
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, tag := range tags {
		if c := m.virtualTagCount[tag]; c > 0 {
			out[tag] = c
		}
	}
	return out
}

// VirtualTagNames returns the set of tag names with at least one
// ext-routed contribution (persistent X records and overlay routings
// alike, since virtualTagCount merges both). Used by tag-source
// parity in Store.ListTags / Store.MatchTagNames.
// CRC: crc-ExtMap.md | R2344, R2352
func (m *ExtMap) VirtualTagNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.virtualTagCount))
	for name, count := range m.virtualTagCount {
		if count > 0 {
			out = append(out, name)
		}
	}
	return out
}

// SourceChunkID returns the source chunkID that authored the
// @ext routing for tvidExt, or (0, false) if unknown. Used by
// the indexer's re-resolution path to recover sourceDir for
// relative-path narrower resolution.
// CRC: crc-ExtMap.md | R2374
func (m *ExtMap) SourceChunkID(tvidExt uint64) (uint64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cid, ok := m.extSource[tvidExt]
	return cid, ok
}

// CandidateSourcesForChunk returns a copy of the @ext-candidate source
// tvids whose TARGET resolved to chunkID — the RC reverse lookup that
// replaces the old `"RC" + chunkid` prefix scan. Store.DerivedProposals
// resolves each tvid back to its (tagname, value) and RC tally. (R3065)
// CRC: crc-ExtMap.md | R3065
func (m *ExtMap) CandidateSourcesForChunk(chunkID uint64) []uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	src := m.candidateSourcesByChunk[chunkID]
	if len(src) == 0 {
		return nil
	}
	return append([]uint64(nil), src...)
}

// RejectScore returns the signed judgment score for the (chunkID,
// tagname) edge from the in-memory reject map, or 0 when neutral. A
// negative score means net-rejected with magnitude -score; the reject
// filter and DerivedProposals read it in one map hit instead of an
// RJ key lookup. (R3066, R3070)
// CRC: crc-ExtMap.md | R3066, R3070
func (m *ExtMap) RejectScore(chunkID uint64, tagname string) int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if inner := m.rejectByChunk[chunkID]; inner != nil {
		return inner[tagname]
	}
	return 0
}

// sourceDirLocked returns the source file's directory for
// tvidExt's @ext authoring chunk, or "" when unknown. Used by
// extByAnchor BASE keying for relative-path absolutization.
// Caller must hold m.mu (Lock or RLock).
// CRC: crc-ExtMap.md | R2374, R2380
func (m *ExtMap) sourceDirLocked(txn *bbolt.Tx, db *DB, tvidExt uint64) string {
	srcChunk, ok := m.extSource[tvidExt]
	if !ok {
		return ""
	}
	fileID, ok := db.chunkFileID(txn, srcChunk)
	if !ok {
		return ""
	}
	path, ok := db.fileIDPath(fileID)
	if !ok {
		return ""
	}
	return filepath.Dir(path)
}

// VirtualTagValues returns the distinct values routed for the given
// tag name across all @ext routings (persistent + overlay). Used by
// tag-source parity in Store.QueryTagValues / Store.MatchTagValues.
// CRC: crc-ExtMap.md | R2344, R2352
func (m *ExtMap) VirtualTagValues(tag string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	seen := make(map[string]struct{})
	for _, routed := range m.routedTagsByTvidExt {
		for _, tv := range routed {
			if tv.Tag == tag {
				seen[tv.Value] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	return out
}

// RoutedTagsForChunk returns the (tag, value) pairs that route TO
// targetChunkID via any @ext routing — persistent or overlay. Walks
// chunkToTargets[targetChunkID] and unions routedTagsByTvidExt for
// each tvid_ext. Used by Store.AllTagsForChunk for tag-source
// parity. Lighter-weight than ExtRoutingsForTargetChunk: no source
// path resolution, no DB transaction.
// CRC: crc-ExtMap.md | R2344, R2351
func (m *ExtMap) RoutedTagsForChunk(targetChunkID uint64) []TagValue {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tvidExts := m.chunkToTargets[targetChunkID]
	if len(tvidExts) == 0 {
		return nil
	}
	var out []TagValue
	for _, te := range tvidExts {
		out = append(out, m.routedTagsByTvidExt[te]...)
	}
	return out
}

// ExtTagValueChunks returns target chunkids carrying (tag, value) via
// any @ext routing — persistent or overlay. Walks
// routedTagsByTvidExt (the cache populated by Rebuild and maintained
// alongside writes) and emits the target chunkids for each tvid_ext
// whose routed pairs include (tag, value). Unioned by
// Store.TagValueChunks with the persistent V record scan and
// TmpTagStore.TagValueChunks results.
// CRC: crc-ExtMap.md | R2120, R2124
func (m *ExtMap) ExtTagValueChunks(tag, value string) []uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []uint64
	for tvidExt, routed := range m.routedTagsByTvidExt {
		matched := false
		for _, tv := range routed {
			if tv.Tag == tag && tv.Value == value {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		for _, chunkID := range m.targetToChunk[tvidExt] {
			out = append(out, chunkID)
		}
	}
	return out
}

// ExtTagFiles emits per-(chunkid, tag) records for ext-routed targets
// — persistent or overlay — that carry any of the requested tag
// names. Walks routedTagsByTvidExt + targetToChunk in one pass under
// a single RLock. Unioned by Store.TagFiles alongside the F-record
// scan and TmpTagStore results.
// CRC: crc-ExtMap.md | R2120, R2124
func (m *ExtMap) ExtTagFiles(tags []string) []TagFileRecord {
	if len(tags) == 0 {
		return nil
	}
	tagSet := make(map[string]bool, len(tags))
	for _, tag := range tags {
		tagSet[tag] = true
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []TagFileRecord
	for tvidExt, routed := range m.routedTagsByTvidExt {
		chunks := m.targetToChunk[tvidExt]
		if len(chunks) == 0 {
			continue
		}
		for _, tv := range routed {
			if !tagSet[tv.Tag] {
				continue
			}
			for _, chunkID := range chunks {
				out = append(out, TagFileRecord{
					ChunkID: chunkID,
					Tag:     tv.Tag,
					Count:   1,
				})
			}
		}
	}
	return out
}

// IncomingExtRouting is the per-target render shape — one entry per
// @ext routing landing in a given target chunk. Carries the source
// metadata (file path, chunk id) and the routed (tag, value) pairs
// needed by Server.enrichContent to emit the <ark-ext-tags>
// affordance. Distinct from the storage-shape ExtRouting in store.go,
// which is just (target_chunkid, []routed_tvid).
// CRC: crc-ExtMap.md | R2065, R2073, R2079
type IncomingExtRouting struct {
	TvidExt        uint64
	SourceChunkID  uint64
	SourceFilePath string
	TargetAnchor   string
	Routed         []TagValue
}

// ExtRoutingsForTargetChunk returns every @ext routing whose target is
// targetChunkID, fully resolved for rendering. Branches on
// bothPersistent to read routed tvids from X records (LMDB) or from
// the in-memory overlayRoutings map. Returns nil when no routings
// target this chunk. TargetAnchor is always "" for v1 — anchored
// target forms (path:section, UUID:section) aren't yet resolvable, so
// chunkToTargets only holds bare-target routings.
// CRC: crc-ExtMap.md | R2065, R2073, R2079
func (m *ExtMap) ExtRoutingsForTargetChunk(targetChunkID uint64, db *DB) []IncomingExtRouting {
	type pending struct {
		tvidExt        uint64
		sourceChunkID  uint64
		bothPersistent bool
		overlayRouted  []uint64
	}
	targetOverlay := IsOverlayID(targetChunkID)

	m.mu.RLock()
	tvidExts := m.chunkToTargets[targetChunkID]
	if len(tvidExts) == 0 {
		m.mu.RUnlock()
		return nil
	}
	pendings := make([]pending, 0, len(tvidExts))
	for _, te := range tvidExts {
		srcChunk, ok := m.extSource[te]
		if !ok {
			continue
		}
		bp := !IsOverlayID(srcChunk) && !targetOverlay
		var overlayRouted []uint64
		if !bp {
			if per := m.overlayRoutings[te][targetChunkID]; len(per) > 0 {
				overlayRouted = append([]uint64(nil), per...)
			}
		}
		pendings = append(pendings, pending{
			tvidExt:        te,
			sourceChunkID:  srcChunk,
			bothPersistent: bp,
			overlayRouted:  overlayRouted,
		})
	}
	m.mu.RUnlock()
	if len(pendings) == 0 {
		return nil
	}

	out := make([]IncomingExtRouting, 0, len(pendings))
	_ = db.store.bolt.View(func(txn *bbolt.Tx) error {
		for _, p := range pendings {
			srcPath := ""
			if fid, ok := db.chunkFileID(txn, p.sourceChunkID); ok {
				if path, ok := db.resolveFilePath(fid); ok {
					srcPath = path
				}
			}

			var rtvids []uint64
			if p.bothPersistent {
				rtvids, _ = db.store.ReadExtRecord(txn, p.tvidExt, targetChunkID)
			} else {
				rtvids = p.overlayRouted
			}
			routed := make([]TagValue, 0, len(rtvids))
			for _, t := range rtvids {
				if tag, val, ok := db.store.tvids.Resolve(t); ok {
					routed = append(routed, TagValue{Tag: tag, Value: val})
				}
			}

			out = append(out, IncomingExtRouting{
				TvidExt:        p.tvidExt,
				SourceChunkID:  p.sourceChunkID,
				SourceFilePath: srcPath,
				Routed:         routed,
			})
		}
		return nil
	})
	return out
}

// RecordOverlayError appends an internal diagnostic to the overlay
// error log. Called by applyIndexExt and applyReresolve when they
// take overlay-touched branches.
// CRC: crc-ExtMap.md | R2029, R2030
func (m *ExtMap) RecordOverlayError(severity string, sourceChunkID, sourceFileID uint64, message string) {
	m.mu.Lock()
	m.overlayErrors = append(m.overlayErrors, OverlayError{
		Time:          time.Now(),
		SourceChunkID: sourceChunkID,
		SourceFileID:  sourceFileID,
		Severity:      severity,
		Message:       message,
	})
	m.mu.Unlock()
}

// AddOverlayError appends an externally-supplied entry. Used by
// `ark errors --overlay --add`.
// CRC: crc-ExtMap.md | R2030, R2031
func (m *ExtMap) AddOverlayError(severity, message string) {
	m.mu.Lock()
	m.overlayErrors = append(m.overlayErrors, OverlayError{
		Time:     time.Now(),
		Severity: severity,
		Message:  message,
	})
	m.mu.Unlock()
}

// OverlayErrors returns a snapshot of the overlay error log.
// CRC: crc-ExtMap.md | R2030, R2031
func (m *ExtMap) OverlayErrors() []OverlayError {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]OverlayError, len(m.overlayErrors))
	copy(out, m.overlayErrors)
	return out
}

// ClearOverlayErrors resets the log.
// CRC: crc-ExtMap.md | R2030, R2031
func (m *ExtMap) ClearOverlayErrors() {
	m.mu.Lock()
	m.overlayErrors = nil
	m.mu.Unlock()
}

// Rebuild repopulates the six core maps by scanning X records. Called
// from DB.Open after the TvidMap is loaded. Overlay state is zeroed —
// it has no on-disk source.
// CRC: crc-ExtMap.md | R1993, R2015
func (m *ExtMap) Rebuild(db *DB) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.targetToChunk = make(map[uint64][]uint64)
	m.chunkToTargets = make(map[uint64][]uint64)
	m.fileidToTvids = make(map[uint64][]uint64)
	m.extByAnchor = make(map[string][]uint64)
	m.unresolvedTargets = make(map[uint64]bool)
	m.virtualTagCount = make(map[string]int)
	m.extSource = make(map[uint64]uint64)
	m.routedTagsByTvidExt = make(map[uint64][]TagValue)
	m.candidateSourcesByChunk = make(map[uint64][]uint64)
	m.rejectByChunk = make(map[uint64]map[string]int64)
	m.overlayRoutings = make(map[uint64]map[uint64][]uint64)
	m.overlayValues = make(map[string]map[string][]uint64)
	m.overlayErrors = nil

	return db.store.bolt.View(func(txn *bbolt.Tx) error {
		if err := db.store.ScanAllExtRecords(txn, func(tvidExt, targetChunk uint64, routedTvids []uint64) error {
			m.targetToChunk[tvidExt] = append(m.targetToChunk[tvidExt], targetChunk)
			m.chunkToTargets[targetChunk] = appendUnique(m.chunkToTargets[targetChunk], tvidExt)
			if fileID, ok := db.chunkFileID(txn, targetChunk); ok {
				m.fileidToTvids[fileID] = appendUnique(m.fileidToTvids[fileID], tvidExt)
			}
			if tag, value, ok := db.store.tvids.Resolve(tvidExt); ok {
				// Resolve source chunkID first so extByAnchor BASE
				// keying can use the source's directory for relative-
				// path absolutization (R2374, R2380).
				// CRC: crc-ExtMap.md | R2108, R2109
				if _, have := m.extSource[tvidExt]; !have {
					if vbytes, gerr := bGet(txn, tagValueFullKey(tag, value, tvidExt)); gerr == nil {
						if srcs := decodeVarints(vbytes); len(srcs) > 0 {
							m.extSource[tvidExt] = srcs[0]
						}
					}
				}
				if target, _, parseOk := ParseExtTarget(value); parseOk {
					sourceDir := m.sourceDirLocked(txn, db, tvidExt)
					parts, _ := ParseExtTargetParts(target, sourceDir)
					m.extByAnchor[parts.BaseValue] = appendUnique(m.extByAnchor[parts.BaseValue], tvidExt)
				}
			}
			// CRC: crc-ExtMap.md | R2121, R2122
			// routedTagsByTvidExt is keyed per tvid_ext; the same tvid
			// appears once per X record but the routed list is identical
			// across them, so write once on the first sighting.
			needRouted := false
			if _, have := m.routedTagsByTvidExt[tvidExt]; !have {
				needRouted = true
			}
			pairs := make([]TagValue, 0, len(routedTvids))
			for _, rt := range routedTvids {
				if tag, val, ok := db.store.tvids.Resolve(rt); ok {
					m.virtualTagCount[tag]++
					if needRouted {
						pairs = append(pairs, TagValue{Tag: tag, Value: val})
					}
				}
			}
			if needRouted {
				m.routedTagsByTvidExt[tvidExt] = pairs
			}
			return nil
		}); err != nil {
			return err
		}
		// Populate the derived-record reverse lookups from RC/RJ. Empty
		// until the wiring flip authors the first candidate/judgment.
		// CRC: crc-ExtMap.md | R3064, R3065
		if err := db.store.ScanAllDerivedCandidates(txn, func(srcTvid, targetChunk, tally uint64) error {
			m.candidateSourcesByChunk[targetChunk] = appendUnique(m.candidateSourcesByChunk[targetChunk], srcTvid)
			return nil
		}); err != nil {
			return err
		}
		// CRC: crc-ExtMap.md | R3064, R3066
		return db.store.ScanAllDerivedJudgments(txn, func(srcTvid, targetChunk uint64, score, nanos int64) error {
			_, value, ok := db.store.tvids.Resolve(srcTvid)
			if !ok {
				return nil
			}
			_, routed, parseOk := ParseExtTarget(value)
			if !parseOk || len(routed) == 0 {
				return nil
			}
			if m.rejectByChunk[targetChunk] == nil {
				m.rejectByChunk[targetChunk] = make(map[string]int64)
			}
			m.rejectByChunk[targetChunk][routed[0].Tag] = score
			return nil
		})
	})
}

// candidatesForFileChange gathers tvid_exts from
// fileidToTvids[fileID], extByAnchor[F.path], and chunkToTargets for
// each orphaned chunk. Overlay routings appear in the same maps and
// are returned alongside persistent ones.
// CRC: crc-ExtMap.md | R2000, R2027
func (m *ExtMap) candidatesForFileChange(db *DB, fileID uint64, addedChunkIDs, orphanedChunkIDs []uint64) map[uint64]bool {
	path, _ := db.fileIDPath(fileID)
	var addedUUIDs []string
	if len(addedChunkIDs) > 0 {
		_ = db.fts.DB().View(func(txn *bbolt.Tx) error {
			for _, cid := range addedChunkIDs {
				addedUUIDs = append(addedUUIDs, db.chunkIDValues(txn, cid)...)
			}
			return nil
		})
	}
	out := make(map[uint64]bool)
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, tv := range m.fileidToTvids[fileID] {
		out[tv] = true
	}
	for _, tv := range m.extByAnchor[path] {
		out[tv] = true
	}
	for _, cid := range orphanedChunkIDs {
		for _, tv := range m.chunkToTargets[cid] {
			out[tv] = true
		}
	}
	for _, uuid := range addedUUIDs {
		for _, tv := range m.extByAnchor[uuid] {
			out[tv] = true
		}
	}
	return out
}

// applyIndexExt writes records for one pre-resolved index plan.
// Resolution and parsing already happened outside the txn; this
// runs only LMDB writes (when bothPersistent) plus in-memory state
// updates. txn and tt may be nil for fully-overlay-source flows
// where no LMDB writes can fire.
// CRC: crc-ExtMap.md | R1995, R1996, R1997, R1998, R1999, R2012, R2016, R2017, R2018, R2030
func (m *ExtMap) applyIndexExt(txn *bbolt.Tx, tt *TvidTxn, db *DB, p extIndexPlan) error {
	sourceOverlay := IsOverlayID(p.sourceChunkID)
	if len(p.targets) == 0 {
		m.mu.Lock()
		m.extByAnchor[p.targetBase] = appendUnique(m.extByAnchor[p.targetBase], p.tvidExt)
		m.unresolvedTargets[p.tvidExt] = true
		m.extSource[p.tvidExt] = p.sourceChunkID
		m.mu.Unlock()
		return nil
	}
	// R3062: candidate/judgment take the lighter derived path — an RC/RJ
	// record instead of the live V edge, no virtualTagCount bump.
	if p.class != extClassCommitted {
		return m.applyDerivedIndexExt(txn, db, p, sourceOverlay)
	}
	type accepted struct {
		chunkID        uint64
		fileID         uint64
		bothPersistent bool
	}
	var taken []accepted
	for _, t := range p.targets {
		fid, ok := db.chunkFileID(txn, t)
		if !ok {
			continue
		}
		if fid == p.sourceFileID {
			Logv(0, "ext: self-reference rejected (source fileid=%d, target chunk=%d)", p.sourceFileID, t)
			if sourceOverlay || IsOverlayID(t) {
				m.RecordOverlayError("warn", p.sourceChunkID, p.sourceFileID,
					"self-reference in overlay routing rejected")
			}
			continue
		}
		taken = append(taken, accepted{
			chunkID:        t,
			fileID:         fid,
			bothPersistent: !sourceOverlay && !IsOverlayID(t),
		})
	}
	if len(taken) == 0 {
		m.mu.Lock()
		m.extByAnchor[p.targetBase] = appendUnique(m.extByAnchor[p.targetBase], p.tvidExt)
		m.extSource[p.tvidExt] = p.sourceChunkID
		m.mu.Unlock()
		return nil
	}
	// Allocate routed tvids per target. For persistent both-ends, also
	// write X + V records inside the txn.
	type targetWrite struct {
		chunkID        uint64
		fileID         uint64
		bothPersistent bool
		tvids          []uint64
	}
	writes := make([]targetWrite, 0, len(taken))
	for _, a := range taken {
		tvids := make([]uint64, 0, len(p.routedTags))
		for _, rt := range p.routedTags {
			tvid, err := m.allocRoutedTvid(txn, tt, db, rt.Tag, rt.Value, a.chunkID, a.bothPersistent)
			if err != nil {
				return err
			}
			tvids = append(tvids, tvid)
		}
		if a.bothPersistent {
			if err := db.store.WriteExtRecord(txn, p.tvidExt, a.chunkID, tvids); err != nil {
				return err
			}
		}
		writes = append(writes, targetWrite{
			chunkID:        a.chunkID,
			fileID:         a.fileID,
			bothPersistent: a.bothPersistent,
			tvids:          tvids,
		})
	}
	hasOverlayWrite := false
	m.mu.Lock()
	m.extByAnchor[p.targetBase] = appendUnique(m.extByAnchor[p.targetBase], p.tvidExt)
	m.extSource[p.tvidExt] = p.sourceChunkID
	delete(m.unresolvedTargets, p.tvidExt)
	// CRC: crc-ExtMap.md | R2121, R2123 — routed tags are tvid_ext-scoped.
	if _, have := m.routedTagsByTvidExt[p.tvidExt]; !have {
		dup := make([]TagValue, len(p.routedTags))
		copy(dup, p.routedTags)
		m.routedTagsByTvidExt[p.tvidExt] = dup
	}
	for _, w := range writes {
		m.targetToChunk[p.tvidExt] = append(m.targetToChunk[p.tvidExt], w.chunkID)
		m.chunkToTargets[w.chunkID] = appendUnique(m.chunkToTargets[w.chunkID], p.tvidExt)
		m.fileidToTvids[w.fileID] = appendUnique(m.fileidToTvids[w.fileID], p.tvidExt)
		for _, rt := range p.routedTags {
			m.virtualTagCount[rt.Tag]++
			if !w.bothPersistent {
				m.putOverlayValueLocked(rt.Tag, rt.Value, w.chunkID)
			}
		}
		if !w.bothPersistent {
			m.putOverlayRoutingLocked(p.tvidExt, w.chunkID, w.tvids)
			hasOverlayWrite = true
		}
	}
	m.mu.Unlock()
	if hasOverlayWrite {
		m.RecordOverlayError("info", p.sourceChunkID, p.sourceFileID,
			"overlay @ext routing — session-scoped, dies with server")
	}
	return nil
}

// allocRoutedTvid allocates (or reuses) a tvid for a routed tag. For
// persistent routings it goes through the standard
// addChunkIDToVRecord path (which writes V multi-set + adjusts T).
// For overlay-touched routings it allocates via TvidMap (Lookup →
// AllocOverlay) without LMDB writes — the caller handles
// overlayValues bookkeeping.
// CRC: crc-ExtMap.md | R2017
func (m *ExtMap) allocRoutedTvid(txn *bbolt.Tx, tt *TvidTxn, db *DB, tag, value string, targetChunk uint64, bothPersistent bool) (uint64, error) {
	if bothPersistent {
		return db.store.addChunkIDToVRecord(txn, tt, tag, value, targetChunk)
	}
	if tvid, ok := db.store.tvids.Lookup(tag, value); ok {
		return tvid, nil
	}
	return db.store.tvids.AllocOverlay(tag, value), nil
}

// putOverlayRoutingLocked records an overlay routing's routed tvids.
// Caller holds m.mu.
func (m *ExtMap) putOverlayRoutingLocked(tvidExt, targetChunk uint64, routedTvids []uint64) {
	per := m.overlayRoutings[tvidExt]
	if per == nil {
		per = make(map[uint64][]uint64)
		m.overlayRoutings[tvidExt] = per
	}
	dup := make([]uint64, len(routedTvids))
	copy(dup, routedTvids)
	per[targetChunk] = dup
}

// putOverlayValueLocked appends a chunkid to overlayValues[tag][value]
// (multi-set, no dedup). Caller holds m.mu.
func (m *ExtMap) putOverlayValueLocked(tag, value string, chunkID uint64) {
	byVal := m.overlayValues[tag]
	if byVal == nil {
		byVal = make(map[string][]uint64)
		m.overlayValues[tag] = byVal
	}
	byVal[value] = append(byVal[value], chunkID)
}

// strikeOverlayValueLocked removes one occurrence of chunkID from
// overlayValues[tag][value]. Caller holds m.mu.
func (m *ExtMap) strikeOverlayValueLocked(tag, value string, chunkID uint64) {
	byVal := m.overlayValues[tag]
	if byVal == nil {
		return
	}
	xs := byVal[value]
	for i, x := range xs {
		if x == chunkID {
			xs = append(xs[:i], xs[i+1:]...)
			break
		}
	}
	if len(xs) == 0 {
		delete(byVal, value)
		if len(byVal) == 0 {
			delete(m.overlayValues, tag)
		}
	} else {
		byVal[value] = xs
	}
}

// applyReresolve runs steps 3-7 of the canonical re-resolution flow
// for one pre-resolved tvid_ext. Per-target branching on
// bothPersistent dispatches Adds and Removes between LMDB and overlay
// state.
// CRC: crc-ExtMap.md | R1994, R2002, R2003, R2004, R2005, R2006, R2026
func (m *ExtMap) applyReresolve(txn *bbolt.Tx, tt *TvidTxn, db *DB, fileID uint64, p extReresolvePlan) error {
	// R3062: candidate/judgment re-resolve through the derived path.
	if p.class != extClassCommitted {
		return m.applyDerivedReresolve(txn, db, fileID, p)
	}
	tvidExt := p.tvidExt
	newTargets := p.newTargets
	routed := p.routedTags
	targetBase := p.targetBase

	m.mu.RLock()
	oldAll := append([]uint64(nil), m.targetToChunk[tvidExt]...)
	sourceChunkID := m.extSource[tvidExt]
	m.mu.RUnlock()
	sourceOverlay := IsOverlayID(sourceChunkID)

	oldScoped, removeFids := filterByFileWithIDs(txn, db, oldAll, fileID)
	newScoped, addFids := filterByFileWithIDs(txn, db, newTargets, fileID)

	adds := setDiff(newScoped, oldScoped)
	removes := setDiff(oldScoped, newScoped)

	type remOp struct {
		chunk          uint64
		fileID         uint64
		tags           []string
		vals           []string // parallel to tags, for batched overlay strikes
		bothPersistent bool
	}
	remOps := make([]remOp, 0, len(removes))
	for _, removed := range removes {
		bothPersistent := !sourceOverlay && !IsOverlayID(removed)
		op := remOp{chunk: removed, fileID: removeFids[removed], bothPersistent: bothPersistent}
		var routings []uint64
		if bothPersistent {
			routedRecords, err := db.store.ReadExtRecord(txn, tvidExt, removed)
			if err != nil {
				return err
			}
			routings = routedRecords
		} else {
			m.mu.RLock()
			if per, ok := m.overlayRoutings[tvidExt]; ok {
				rt := per[removed]
				routings = make([]uint64, len(rt))
				copy(routings, rt)
			}
			m.mu.RUnlock()
		}
		for _, rt := range routings {
			tag, val, ok := resolveTvid(tt, db, rt)
			if !ok {
				continue
			}
			if bothPersistent {
				if _, err := db.store.removeOneChunkIDFromVRecord(txn, tt, tag, val, rt, removed); err != nil {
					return err
				}
			}
			op.tags = append(op.tags, tag)
			op.vals = append(op.vals, val)
		}
		if bothPersistent {
			if err := db.store.DeleteExtRecord(txn, tvidExt, removed); err != nil {
				return err
			}
		}
		remOps = append(remOps, op)
	}
	type addOp struct {
		chunk          uint64
		fileID         uint64
		bothPersistent bool
		tvids          []uint64
	}
	addOps := make([]addOp, 0, len(adds))
	for _, added := range adds {
		bothPersistent := !sourceOverlay && !IsOverlayID(added)
		op := addOp{chunk: added, fileID: addFids[added], bothPersistent: bothPersistent}
		tvids := make([]uint64, 0, len(routed))
		for _, rt := range routed {
			tvid, err := m.allocRoutedTvid(txn, tt, db, rt.Tag, rt.Value, added, bothPersistent)
			if err != nil {
				return err
			}
			tvids = append(tvids, tvid)
		}
		op.tvids = tvids
		if bothPersistent {
			if err := db.store.WriteExtRecord(txn, tvidExt, added, tvids); err != nil {
				return err
			}
		}
		addOps = append(addOps, op)
	}

	hasOverlayWrite := false
	m.mu.Lock()
	for _, op := range remOps {
		m.targetToChunk[tvidExt] = removeUint64(m.targetToChunk[tvidExt], op.chunk)
		m.chunkToTargets[op.chunk] = removeUint64(m.chunkToTargets[op.chunk], tvidExt)
		if op.fileID != 0 {
			m.fileidToTvids[op.fileID] = removeUint64(m.fileidToTvids[op.fileID], tvidExt)
		}
		for i, tag := range op.tags {
			m.virtualTagCount[tag]--
			if m.virtualTagCount[tag] <= 0 {
				delete(m.virtualTagCount, tag)
			}
			if !op.bothPersistent {
				m.strikeOverlayValueLocked(tag, op.vals[i], op.chunk)
			}
		}
		if !op.bothPersistent {
			if per, ok := m.overlayRoutings[tvidExt]; ok {
				delete(per, op.chunk)
				if len(per) == 0 {
					delete(m.overlayRoutings, tvidExt)
				}
			}
		}
	}
	for _, op := range addOps {
		m.targetToChunk[tvidExt] = append(m.targetToChunk[tvidExt], op.chunk)
		m.chunkToTargets[op.chunk] = appendUnique(m.chunkToTargets[op.chunk], tvidExt)
		if op.fileID != 0 {
			m.fileidToTvids[op.fileID] = appendUnique(m.fileidToTvids[op.fileID], tvidExt)
		}
		for _, rt := range routed {
			m.virtualTagCount[rt.Tag]++
			if !op.bothPersistent {
				m.putOverlayValueLocked(rt.Tag, rt.Value, op.chunk)
			}
		}
		if !op.bothPersistent {
			m.putOverlayRoutingLocked(tvidExt, op.chunk, op.tvids)
			hasOverlayWrite = true
		}
		delete(m.unresolvedTargets, tvidExt)
	}
	// CRC: crc-ExtMap.md | R2121, R2123 — set cache on Adds; drop on empty.
	if len(addOps) > 0 {
		if _, have := m.routedTagsByTvidExt[tvidExt]; !have {
			dup := make([]TagValue, len(routed))
			copy(dup, routed)
			m.routedTagsByTvidExt[tvidExt] = dup
		}
	}
	if len(m.targetToChunk[tvidExt]) == 0 {
		delete(m.routedTagsByTvidExt, tvidExt)
	}
	if len(newTargets) == 0 {
		m.unresolvedTargets[tvidExt] = true
		m.extByAnchor[targetBase] = appendUnique(m.extByAnchor[targetBase], tvidExt)
	}
	m.mu.Unlock()
	if hasOverlayWrite {
		m.RecordOverlayError("info", sourceChunkID, 0,
			"overlay @ext re-resolution added session-scoped target")
	}
	return nil
}

// CleanupSource runs source-side cleanup when a source chunk is
// orphaned. Walks targetToChunk[tvidExt] in-memory; per target,
// branches on bothPersistent to dispatch LMDB ops vs overlay-state
// ops. Drops tvidExt from all maps after the walk.
//
// MUST run before tt.Commit drops tvidExt — the V record empties may
// trigger tt.Remove(tvid_routed) and the spec recovery contract for
// re-resolution paths sharing the txn.
//
// txn and tt may be nil for overlay sources: every routing has
// bothPersistent=false so no LMDB writes fire.
// CRC: crc-ExtMap.md | R2008, R2009, R2022, R2023, R2024, R2025
func (m *ExtMap) CleanupSource(txn *bbolt.Tx, tt *TvidTxn, db *DB, sourceChunkID, tvidExt uint64) error {
	// R3064: candidate/judgment sources strike RC/RJ, not X + routed V.
	if tag, value, ok := resolveTvid(tt, db, tvidExt); ok {
		if class := extClassForTag(tag); class != extClassCommitted {
			var routedTag string
			if _, routed, pok := ParseExtTarget(value); pok {
				if routed, _, _ = extractCountField(routed); len(routed) > 0 {
					routedTag = routed[0].Tag
				}
			}
			return m.cleanupDerivedSource(txn, db, tvidExt, class, routedTag)
		}
	}
	sourceOverlay := IsOverlayID(sourceChunkID)
	m.mu.RLock()
	targets := append([]uint64(nil), m.targetToChunk[tvidExt]...)
	overlayPer := m.overlayRoutings[tvidExt]
	overlayCopy := make(map[uint64][]uint64, len(overlayPer))
	for k, v := range overlayPer {
		dup := make([]uint64, len(v))
		copy(dup, v)
		overlayCopy[k] = dup
	}
	m.mu.RUnlock()

	type cleanedRouting struct {
		chunkID        uint64
		fileID         uint64
		tags           []string
		vals           []string // parallel to tags, for batched overlay strikes
		bothPersistent bool
	}
	cleaned := make([]cleanedRouting, 0, len(targets))
	for _, targetChunk := range targets {
		bothPersistent := !sourceOverlay && !IsOverlayID(targetChunk)
		c := cleanedRouting{chunkID: targetChunk, bothPersistent: bothPersistent}
		if txn != nil {
			if id, ok := db.chunkFileID(txn, targetChunk); ok {
				c.fileID = id
			}
		}

		var routings []uint64
		if bothPersistent {
			r, err := db.store.ReadExtRecord(txn, tvidExt, targetChunk)
			if err != nil {
				return err
			}
			routings = r
		} else {
			routings = overlayCopy[targetChunk]
		}

		for _, rt := range routings {
			tag, val, ok := resolveTvid(tt, db, rt)
			if !ok {
				continue
			}
			if bothPersistent {
				if _, err := db.store.removeOneChunkIDFromVRecord(txn, tt, tag, val, rt, targetChunk); err != nil {
					return err
				}
			}
			c.tags = append(c.tags, tag)
			c.vals = append(c.vals, val)
		}
		if bothPersistent {
			if err := db.store.DeleteExtRecord(txn, tvidExt, targetChunk); err != nil {
				return err
			}
		}
		cleaned = append(cleaned, c)
	}

	m.mu.Lock()
	for _, c := range cleaned {
		m.chunkToTargets[c.chunkID] = removeUint64(m.chunkToTargets[c.chunkID], tvidExt)
		if c.fileID != 0 {
			m.fileidToTvids[c.fileID] = removeUint64(m.fileidToTvids[c.fileID], tvidExt)
		}
		for i, tag := range c.tags {
			m.virtualTagCount[tag]--
			if m.virtualTagCount[tag] <= 0 {
				delete(m.virtualTagCount, tag)
			}
			if !c.bothPersistent {
				m.strikeOverlayValueLocked(tag, c.vals[i], c.chunkID)
			}
		}
	}
	delete(m.targetToChunk, tvidExt)
	delete(m.unresolvedTargets, tvidExt)
	delete(m.overlayRoutings, tvidExt)
	delete(m.extSource, tvidExt)
	delete(m.routedTagsByTvidExt, tvidExt) // CRC: crc-ExtMap.md | R2123
	if _, val, ok := resolveTvid(tt, db, tvidExt); ok {
		if target, _, parseOK := ParseExtTarget(val); parseOK {
			sourceDir := m.sourceDirLocked(txn, db, tvidExt)
			parts, _ := ParseExtTargetParts(target, sourceDir)
			key := parts.BaseValue
			m.extByAnchor[key] = removeUint64(m.extByAnchor[key], tvidExt)
			if len(m.extByAnchor[key]) == 0 {
				delete(m.extByAnchor, key)
			}
		}
	}
	m.mu.Unlock()
	return nil
}

// candidateTally clamps a candidate's @count into an RC tally (≥1). A count
// of 0 or 1 (or a stray negative) yields 1; larger counts pass through.
// CRC: crc-ExtMap.md | R3074
func candidateTally(count int64) uint64 {
	if count > 1 {
		return uint64(count)
	}
	return 1
}

// setRejectLocked sets (or, when score==0, clears) the reject-filter entry
// for (targetChunk, tagname). Caller holds m.mu.
// CRC: crc-ExtMap.md | R3066
func (m *ExtMap) setRejectLocked(targetChunk uint64, tagname string, score int64) {
	if score == 0 {
		if inner := m.rejectByChunk[targetChunk]; inner != nil {
			delete(inner, tagname)
			if len(inner) == 0 {
				delete(m.rejectByChunk, targetChunk)
			}
		}
		return
	}
	inner := m.rejectByChunk[targetChunk]
	if inner == nil {
		inner = make(map[string]int64)
		m.rejectByChunk[targetChunk] = inner
	}
	inner[tagname] = score
}

// applyDerivedIndexExt handles index-time derivation for the candidate and
// judgment classes. For each persistent target it writes the RC/RJ record
// (materializing @count into the tally / signed score) and maintains the
// reverse-lookup maps — no V edge, no virtualTagCount, which is the entire
// proposed/judged-vs-committed distinction. Persistent-only: an overlay
// source or target writes no derived record (R3063).
// CRC: crc-ExtMap.md | R3062, R3063, R3064, R3074
func (m *ExtMap) applyDerivedIndexExt(txn *bbolt.Tx, db *DB, p extIndexPlan, sourceOverlay bool) error {
	var routedTag string
	if len(p.routedTags) > 0 {
		routedTag = p.routedTags[0].Tag
	}
	type derivedWrite struct {
		chunk  uint64
		fileID uint64
	}
	writes := make([]derivedWrite, 0, len(p.targets))
	for _, t := range p.targets {
		fid, ok := db.chunkFileID(txn, t)
		if !ok {
			continue
		}
		if fid == p.sourceFileID {
			continue // self-reference rejected (mirror committed path)
		}
		if sourceOverlay || IsOverlayID(t) {
			continue // R3063: persistent-only
		}
		switch p.class {
		case extClassCandidate:
			if err := db.store.WriteDerivedCandidate(txn, p.tvidExt, t, candidateTally(p.count)); err != nil {
				return err
			}
		case extClassJudgment:
			if p.count == 0 {
				if err := db.store.DeleteDerivedJudgment(txn, p.tvidExt, t); err != nil {
					return err
				}
			} else if err := db.store.WriteDerivedJudgment(txn, p.tvidExt, t, p.count, time.Now().UnixNano()); err != nil {
				return err
			}
		}
		writes = append(writes, derivedWrite{chunk: t, fileID: fid})
	}
	m.mu.Lock()
	m.extByAnchor[p.targetBase] = appendUnique(m.extByAnchor[p.targetBase], p.tvidExt)
	m.extSource[p.tvidExt] = p.sourceChunkID
	delete(m.unresolvedTargets, p.tvidExt)
	for _, w := range writes {
		m.targetToChunk[p.tvidExt] = append(m.targetToChunk[p.tvidExt], w.chunk)
		m.chunkToTargets[w.chunk] = appendUnique(m.chunkToTargets[w.chunk], p.tvidExt)
		m.fileidToTvids[w.fileID] = appendUnique(m.fileidToTvids[w.fileID], p.tvidExt)
		switch p.class {
		case extClassCandidate:
			m.candidateSourcesByChunk[w.chunk] = appendUnique(m.candidateSourcesByChunk[w.chunk], p.tvidExt)
		case extClassJudgment:
			m.setRejectLocked(w.chunk, routedTag, p.count)
		}
	}
	m.mu.Unlock()
	return nil
}

// applyDerivedReresolve re-resolves a candidate/judgment source when its
// target file reindexes: it writes RC/RJ for newly-resolved targets and
// deletes them for vanished ones, maintaining the reverse-lookup maps.
// Persistent-only (R3063), so there is no overlay branch. Mirrors the
// committed applyReresolve, minus V edges and virtualTagCount.
// CRC: crc-ExtMap.md | R3062, R3063, R3064, R3074
func (m *ExtMap) applyDerivedReresolve(txn *bbolt.Tx, db *DB, fileID uint64, p extReresolvePlan) error {
	tvidExt := p.tvidExt
	var routedTag string
	if len(p.routedTags) > 0 {
		routedTag = p.routedTags[0].Tag
	}
	m.mu.RLock()
	oldAll := append([]uint64(nil), m.targetToChunk[tvidExt]...)
	sourceChunkID := m.extSource[tvidExt]
	m.mu.RUnlock()
	if IsOverlayID(sourceChunkID) {
		return nil // R3063: overlay source never derives
	}
	oldScoped, removeFids := filterByFileWithIDs(txn, db, oldAll, fileID)
	newScoped, addFids := filterByFileWithIDs(txn, db, p.newTargets, fileID)
	adds := setDiff(newScoped, oldScoped)
	removes := setDiff(oldScoped, newScoped)

	for _, removed := range removes {
		if IsOverlayID(removed) {
			continue
		}
		switch p.class {
		case extClassCandidate:
			if err := db.store.DeleteDerivedCandidate(txn, tvidExt, removed); err != nil {
				return err
			}
		case extClassJudgment:
			if err := db.store.DeleteDerivedJudgment(txn, tvidExt, removed); err != nil {
				return err
			}
		}
	}
	for _, added := range adds {
		if IsOverlayID(added) {
			continue
		}
		switch p.class {
		case extClassCandidate:
			if err := db.store.WriteDerivedCandidate(txn, tvidExt, added, candidateTally(p.count)); err != nil {
				return err
			}
		case extClassJudgment:
			if p.count == 0 {
				if err := db.store.DeleteDerivedJudgment(txn, tvidExt, added); err != nil {
					return err
				}
			} else if err := db.store.WriteDerivedJudgment(txn, tvidExt, added, p.count, time.Now().UnixNano()); err != nil {
				return err
			}
		}
	}

	m.mu.Lock()
	for _, removed := range removes {
		if IsOverlayID(removed) {
			continue
		}
		m.targetToChunk[tvidExt] = removeUint64(m.targetToChunk[tvidExt], removed)
		m.chunkToTargets[removed] = removeUint64(m.chunkToTargets[removed], tvidExt)
		if fid := removeFids[removed]; fid != 0 {
			m.fileidToTvids[fid] = removeUint64(m.fileidToTvids[fid], tvidExt)
		}
		switch p.class {
		case extClassCandidate:
			m.candidateSourcesByChunk[removed] = removeUint64(m.candidateSourcesByChunk[removed], tvidExt)
			if len(m.candidateSourcesByChunk[removed]) == 0 {
				delete(m.candidateSourcesByChunk, removed)
			}
		case extClassJudgment:
			m.setRejectLocked(removed, routedTag, 0)
		}
	}
	for _, added := range adds {
		if IsOverlayID(added) {
			continue
		}
		m.targetToChunk[tvidExt] = append(m.targetToChunk[tvidExt], added)
		m.chunkToTargets[added] = appendUnique(m.chunkToTargets[added], tvidExt)
		if fid := addFids[added]; fid != 0 {
			m.fileidToTvids[fid] = appendUnique(m.fileidToTvids[fid], tvidExt)
		}
		switch p.class {
		case extClassCandidate:
			m.candidateSourcesByChunk[added] = appendUnique(m.candidateSourcesByChunk[added], tvidExt)
		case extClassJudgment:
			if p.count != 0 {
				m.setRejectLocked(added, routedTag, p.count)
			}
		}
	}
	if len(p.newTargets) == 0 {
		m.unresolvedTargets[tvidExt] = true
		m.extByAnchor[p.targetBase] = appendUnique(m.extByAnchor[p.targetBase], tvidExt)
	}
	m.mu.Unlock()
	return nil
}

// cleanupDerivedSource strikes the RC/RJ records and reverse-lookup entries
// for an orphaned candidate/judgment source, then drops the source tvid from
// the shared maps. The committed sibling lives inline in CleanupSource.
// CRC: crc-ExtMap.md | R3064
func (m *ExtMap) cleanupDerivedSource(txn *bbolt.Tx, db *DB, tvidExt uint64, class extClass, routedTag string) error {
	m.mu.RLock()
	targets := append([]uint64(nil), m.targetToChunk[tvidExt]...)
	m.mu.RUnlock()
	type cleaned struct {
		chunk  uint64
		fileID uint64
	}
	done := make([]cleaned, 0, len(targets))
	for _, targetChunk := range targets {
		c := cleaned{chunk: targetChunk}
		if txn != nil {
			if id, ok := db.chunkFileID(txn, targetChunk); ok {
				c.fileID = id
			}
			switch class {
			case extClassCandidate:
				if err := db.store.DeleteDerivedCandidate(txn, tvidExt, targetChunk); err != nil {
					return err
				}
			case extClassJudgment:
				if err := db.store.DeleteDerivedJudgment(txn, tvidExt, targetChunk); err != nil {
					return err
				}
			}
		}
		done = append(done, c)
	}
	m.mu.Lock()
	for _, c := range done {
		m.chunkToTargets[c.chunk] = removeUint64(m.chunkToTargets[c.chunk], tvidExt)
		if c.fileID != 0 {
			m.fileidToTvids[c.fileID] = removeUint64(m.fileidToTvids[c.fileID], tvidExt)
		}
		switch class {
		case extClassCandidate:
			m.candidateSourcesByChunk[c.chunk] = removeUint64(m.candidateSourcesByChunk[c.chunk], tvidExt)
			if len(m.candidateSourcesByChunk[c.chunk]) == 0 {
				delete(m.candidateSourcesByChunk, c.chunk)
			}
		case extClassJudgment:
			m.setRejectLocked(c.chunk, routedTag, 0)
		}
	}
	delete(m.targetToChunk, tvidExt)
	delete(m.unresolvedTargets, tvidExt)
	delete(m.extSource, tvidExt)
	for base, ids := range m.extByAnchor {
		nids := removeUint64(ids, tvidExt)
		if len(nids) == 0 {
			delete(m.extByAnchor, base)
		} else if len(nids) != len(ids) {
			m.extByAnchor[base] = nids
		}
	}
	m.mu.Unlock()
	return nil
}

// resolveTvid prefers the active TvidTxn (snapshot of in-flight
// allocations) and falls back to the persistent TvidMap. Used by
// CleanupSource paths that may be invoked with tt=nil from overlay
// removal.
func resolveTvid(tt *TvidTxn, db *DB, tvid uint64) (string, string, bool) {
	if tt != nil {
		if tag, val, ok := tt.Resolve(tvid); ok {
			return tag, val, true
		}
	}
	if db != nil && db.store != nil && db.store.tvids != nil {
		return db.store.tvids.Resolve(tvid)
	}
	return "", "", false
}

// appendUnique appends v to xs only if it is not already present.
func appendUnique(xs []uint64, v uint64) []uint64 {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}

// removeUint64 removes the first occurrence of v from xs.
func removeUint64(xs []uint64, v uint64) []uint64 {
	for i, x := range xs {
		if x == v {
			return append(xs[:i], xs[i+1:]...)
		}
	}
	return xs
}

// setDiff returns elements of a not in b.
func setDiff(a, b []uint64) []uint64 {
	if len(b) == 0 {
		return append([]uint64(nil), a...)
	}
	bSet := make(map[uint64]bool, len(b))
	for _, x := range b {
		bSet[x] = true
	}
	out := a[:0:0]
	for _, x := range a {
		if !bSet[x] {
			out = append(out, x)
		}
	}
	return out
}

// filterByFileWithIDs keeps only chunkids that live in fileID and
// returns the chunkid→fileid map for reuse downstream (the map covers
// every chunk in the input, not only matches, so callers can index
// fileids without repeating the C-record read).
func filterByFileWithIDs(txn *bbolt.Tx, db *DB, chunks []uint64, fileID uint64) ([]uint64, map[uint64]uint64) {
	if len(chunks) == 0 {
		return nil, nil
	}
	fids := make(map[uint64]uint64, len(chunks))
	out := chunks[:0:0]
	for _, c := range chunks {
		fid, ok := db.chunkFileID(txn, c)
		if !ok {
			continue
		}
		fids[c] = fid
		if fid == fileID {
			out = append(out, c)
		}
	}
	return out, fids
}
