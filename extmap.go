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
	"sync"
	"time"

	"github.com/bmatsuo/lmdb-go/lmdb"
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
	mu                sync.RWMutex
	targetToChunk     map[uint64][]uint64            // tvid_ext → chunkids
	chunkToTargets    map[uint64][]uint64            // chunkid → tvid_exts
	fileidToTvids     map[uint64][]uint64            // fileid → tvid_exts (target file)
	extByAnchor       map[string][]uint64            // anchor spec text → tvid_exts
	unresolvedTargets map[uint64]bool                // tvid_exts whose target spec resolves to nothing
	virtualTagCount   map[string]int                 // ext-routed contributions per tag (persistent + overlay)
	extSource         map[uint64]uint64              // tvid_ext → source chunkID (R2024, R2026)
	overlayRoutings   map[uint64]map[uint64][]uint64 // tvid_ext → target_chunkid → routed_tvids (R2013)
	overlayValues     map[string]map[string][]uint64 // tag → value → target_chunkids (R2014)
	overlayErrors     []OverlayError                 // session diagnostics (R2029)
}

// NewExtMap constructs an empty ExtMap.
// CRC: crc-ExtMap.md | R1992, R2013, R2014
func NewExtMap() *ExtMap {
	return &ExtMap{
		targetToChunk:     make(map[uint64][]uint64),
		chunkToTargets:    make(map[uint64][]uint64),
		fileidToTvids:     make(map[uint64][]uint64),
		extByAnchor:       make(map[string][]uint64),
		unresolvedTargets: make(map[uint64]bool),
		virtualTagCount:   make(map[string]int),
		extSource:         make(map[uint64]uint64),
		overlayRoutings:   make(map[uint64]map[uint64][]uint64),
		overlayValues:     make(map[string]map[string][]uint64),
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

// OverlayTagValueFiles returns chunkids carrying (tag, value) via
// overlay-touched @ext routings. Unioned with persistent V record
// scan and TmpTagStore.TagValueFiles results by Store.TagValueFiles.
// CRC: crc-ExtMap.md | R2019, R2020
func (m *ExtMap) OverlayTagValueFiles(tag, value string) []uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	src := m.overlayValues[tag][value]
	if len(src) == 0 {
		return nil
	}
	out := make([]uint64, len(src))
	copy(out, src)
	return out
}

// OverlayTagFiles emits per-(chunkid, tag) records for overlay-routed
// targets matching the requested tag names. Unioned by Store.TagFiles
// alongside persistent and TmpTagStore results.
// CRC: crc-ExtMap.md | R2019, R2020
func (m *ExtMap) OverlayTagFiles(tags []string) []TagFileRecord {
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
	for tag, byVal := range m.overlayValues {
		if !tagSet[tag] {
			continue
		}
		for _, chunkIDs := range byVal {
			counts := make(map[uint64]uint32, len(chunkIDs))
			for _, cid := range chunkIDs {
				counts[cid]++
			}
			for cid, count := range counts {
				out = append(out, TagFileRecord{
					ChunkID: cid,
					Tag:     tag,
					Count:   count,
				})
			}
		}
	}
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
	m.overlayRoutings = make(map[uint64]map[uint64][]uint64)
	m.overlayValues = make(map[string]map[string][]uint64)
	m.overlayErrors = nil

	return db.store.env.View(func(txn *lmdb.Txn) error {
		return db.store.ScanAllExtRecords(txn, func(tvidExt, targetChunk uint64, routedTvids []uint64) error {
			m.targetToChunk[tvidExt] = append(m.targetToChunk[tvidExt], targetChunk)
			m.chunkToTargets[targetChunk] = appendUnique(m.chunkToTargets[targetChunk], tvidExt)
			if fileID, ok := db.chunkFileID(txn, targetChunk); ok {
				m.fileidToTvids[fileID] = appendUnique(m.fileidToTvids[fileID], tvidExt)
			}
			if _, value, ok := db.store.tvids.Resolve(tvidExt); ok {
				if spec, _, parseOk := ParseExtTarget(value); parseOk {
					m.extByAnchor[spec] = appendUnique(m.extByAnchor[spec], tvidExt)
				}
			}
			for _, rt := range routedTvids {
				if tag, _, ok := db.store.tvids.Resolve(rt); ok {
					m.virtualTagCount[tag]++
				}
			}
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
		_ = db.fts.Env().View(func(txn *lmdb.Txn) error {
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
func (m *ExtMap) applyIndexExt(txn *lmdb.Txn, tt *TvidTxn, db *DB, p extIndexPlan) error {
	sourceOverlay := IsOverlayID(p.sourceChunkID)
	if len(p.targets) == 0 {
		m.mu.Lock()
		m.extByAnchor[p.target] = appendUnique(m.extByAnchor[p.target], p.tvidExt)
		m.unresolvedTargets[p.tvidExt] = true
		m.extSource[p.tvidExt] = p.sourceChunkID
		m.mu.Unlock()
		return nil
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
		m.extByAnchor[p.target] = appendUnique(m.extByAnchor[p.target], p.tvidExt)
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
	m.extByAnchor[p.target] = appendUnique(m.extByAnchor[p.target], p.tvidExt)
	m.extSource[p.tvidExt] = p.sourceChunkID
	delete(m.unresolvedTargets, p.tvidExt)
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
func (m *ExtMap) allocRoutedTvid(txn *lmdb.Txn, tt *TvidTxn, db *DB, tag, value string, targetChunk uint64, bothPersistent bool) (uint64, error) {
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
func (m *ExtMap) applyReresolve(txn *lmdb.Txn, tt *TvidTxn, db *DB, fileID uint64, p extReresolvePlan) error {
	tvidExt := p.tvidExt
	newTargets := p.newTargets
	routed := p.routedTags
	target := p.target

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
	if len(newTargets) == 0 {
		m.unresolvedTargets[tvidExt] = true
		m.extByAnchor[target] = appendUnique(m.extByAnchor[target], tvidExt)
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
func (m *ExtMap) CleanupSource(txn *lmdb.Txn, tt *TvidTxn, db *DB, sourceChunkID, tvidExt uint64) error {
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
	if _, val, ok := resolveTvid(tt, db, tvidExt); ok {
		if spec, _, parseOK := ParseExtTarget(val); parseOK {
			m.extByAnchor[spec] = removeUint64(m.extByAnchor[spec], tvidExt)
			if len(m.extByAnchor[spec]) == 0 {
				delete(m.extByAnchor, spec)
			}
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
func filterByFileWithIDs(txn *lmdb.Txn, db *DB, chunks []uint64, fileID uint64) ([]uint64, map[uint64]uint64) {
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
